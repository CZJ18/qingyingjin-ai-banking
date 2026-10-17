//go:build integration

package migration

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func smsHistoricalChecksums(t *testing.T, db *sql.DB, ctx context.Context, dialect string) string {
	t.Helper()
	query := "SELECT GROUP_CONCAT(CONCAT(version,':',checksum) ORDER BY version) FROM banking_schema_migrations WHERE version<=12"
	if dialect == "postgres" {
		query = "SELECT string_agg(version::text || ':' || checksum,',' ORDER BY version) FROM banking_schema_migrations WHERE version<=12"
	}
	var result string
	if err := db.QueryRowContext(ctx, query).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertSMSAuthDefaults(t *testing.T, db *sql.DB, ctx context.Context, dialect string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO banking_users(id,display_name,status,created_at,updated_at) VALUES(6013001,'SMS default fixture','active',1,1)"); err != nil {
		t.Fatal(err)
	}
	var verified, changed int64
	var method string
	if err = tx.QueryRowContext(ctx, "SELECT phone_verified_at,password_changed_at,phone_verification_method FROM banking_users WHERE id=6013001").Scan(&verified, &changed, &method); err != nil {
		t.Fatal(err)
	}
	if verified != 0 || changed != 0 || method != "" {
		t.Fatal("new or historical users must not be marked phone-verified or password-changed without proof")
	}
	// Unix milliseconds must not truncate; development proof must remain labeled.
	if _, err = tx.ExecContext(ctx, "UPDATE banking_users SET phone_verified_at=1792112345678,password_changed_at=1792112345689,phone_verification_method='development' WHERE id=6013001"); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT phone_verified_at,password_changed_at,phone_verification_method FROM banking_users WHERE id=6013001").Scan(&verified, &changed, &method); err != nil {
		t.Fatal(err)
	}
	if verified != 1792112345678 || changed != 1792112345689 || method != "development" {
		t.Fatalf("%s: verification metadata lost precision or provider identity", dialect)
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT null_verification"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE banking_users SET phone_verification_method=NULL WHERE id=6013001"); err == nil {
		t.Fatal("NULL verification method bypassed the explicit unverified default")
	}
	if _, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT null_verification"); err != nil {
		t.Fatal(err)
	}
}

func TestSMSAuthPostgresLive(t *testing.T) {
	t.Run("concurrent_additive_migration_preserves_credentials_and_checksums", func(t *testing.T) {
		db, ctx := phoneAuthTestDB(t, "postgres")
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := applyPhoneAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
		before := smsHistoricalChecksums(t, db, ctx, "postgres")
		if _, err := db.ExecContext(ctx, "INSERT INTO banking_users(id,display_name,status,created_at,updated_at,phone_number,password_hash,auth_version,refresh_hash,refresh_expires_at) VALUES(6013002,'Existing credentials','active',1,2,'+8619900001300','hash-fixture',7,'refresh-fixture',1792112345678)"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); errors <- applySMSAuth(ctx, db, "postgres") }()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := applySMSAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
		if smsHistoricalChecksums(t, db, ctx, "postgres") != before {
			t.Fatal("SMS migration changed historical 001–012 checksums")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_users WHERE id=6013002 AND display_name='Existing credentials' AND updated_at=2 AND phone_number='+8619900001300' AND password_hash='hash-fixture' AND auth_version=7 AND refresh_hash='refresh-fixture' AND refresh_expires_at=1792112345678 AND phone_verified_at=0 AND password_changed_at=0 AND phone_verification_method=''"); n != 1 {
			t.Fatal("SMS migration changed existing user credentials or claimed phone verification")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations WHERE version=13"); n != 1 {
			t.Fatal("schema 13 was not recorded exactly once")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"); n != 47 {
			t.Fatal("SMS migration changed 46 business tables plus migration metadata")
		}
		assertSMSAuthDefaults(t, db, ctx, "postgres")
	})
	t.Run("late_failure_rolls_back_all_verification_columns", func(t *testing.T) {
		db, ctx := phoneAuthTestDB(t, "postgres")
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := applyPhoneAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
		original := smsAuthPostgresSQL
		smsAuthPostgresSQL += "\nSELECT 1/0;\n"
		err := applySMSAuth(ctx, db, "postgres")
		smsAuthPostgresSQL = original
		if err == nil {
			t.Fatal("injected late failure did not stop SMS migration")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_users' AND column_name IN ('phone_verified_at','password_changed_at','phone_verification_method')"); n != 0 {
			t.Fatal("transactional migration left partially committed SMS metadata")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations WHERE version=13"); n != 0 {
			t.Fatal("failed migration recorded schema 13")
		}
		if err = applySMSAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("changed_checksum_is_rejected", func(t *testing.T) {
		db, ctx := phoneAuthTestDB(t, "postgres")
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := applyPhoneAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
		if err := applySMSAuth(ctx, db, "postgres"); err != nil {
			t.Fatal(err)
		}
		original := smsAuthPostgresSQL
		smsAuthPostgresSQL += "\n"
		err := applySMSAuth(ctx, db, "postgres")
		smsAuthPostgresSQL = original
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("changed applied migration was accepted: %v", err)
		}
	})
}

func TestSMSAuthMySQLLive(t *testing.T) {
	db, ctx := phoneAuthTestDB(t, "mysql")
	t.Run("full_apply_is_additive_and_reentrant", func(t *testing.T) {
		before := smsHistoricalChecksums(t, db, ctx, "mysql")
		for range 2 {
			if err := Apply(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); errors <- applySMSAuth(ctx, db, "mysql") }()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if smsHistoricalChecksums(t, db, ctx, "mysql") != before {
			t.Fatal("SMS migration changed historical 001–012 checksums")
		}
		assertSMSAuthDefaults(t, db, ctx, "mysql")
	})
	t.Run("interrupted_ddl_retries_and_checks_exact_column_definitions", func(t *testing.T) {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		name := "sms013_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err = c.ExecContext(ctx, "CREATE TABLE "+name+" (id BIGINT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
			t.Fatal(err)
		}
		defer c.ExecContext(context.Background(), "DROP TABLE "+name)
		statements, _ := smsAuthStatements("mysql")
		for i := range statements {
			statements[i] = strings.ReplaceAll(statements[i], "banking_users", name)
		}
		if _, err = c.ExecContext(ctx, statements[0]); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []string{"BIGINT NULL DEFAULT 0", "BIGINT UNSIGNED NOT NULL DEFAULT 0", "BIGINT NOT NULL DEFAULT 1", "INT NOT NULL DEFAULT 0"} {
			if _, err = c.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_verified_at "+invalid); err != nil {
				t.Fatal(err)
			}
			if err = applySMSAuthMySQLDDL(ctx, c, name, statements); err == nil || !strings.Contains(err.Error(), "incompatible definition") {
				t.Fatalf("incompatible timestamp column was adopted: %v", err)
			}
		}
		if _, err = c.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_verified_at BIGINT NOT NULL DEFAULT 0"); err != nil {
			t.Fatal(err)
		}
		broken := append([]string(nil), statements...)
		broken[2] = "INVALID SMS AUTHENTICATION DDL"
		if err = applySMSAuthMySQLDDL(ctx, c, name, broken); err == nil {
			t.Fatal("injected MySQL DDL failure was ignored")
		}
		for range 2 {
			if err = applySMSAuthMySQLDDL(ctx, c, name, statements); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = c.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_verification_method VARCHAR(17) NOT NULL DEFAULT ''"); err != nil {
			t.Fatal(err)
		}
		if err = applySMSAuthMySQLDDL(ctx, c, name, statements); err == nil || !strings.Contains(err.Error(), "incompatible definition") {
			t.Fatalf("incompatible method column was adopted: %v", err)
		}
		if _, err = c.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_verification_method VARCHAR(16) NOT NULL DEFAULT ''"); err != nil {
			t.Fatal(err)
		}
		for i := range statements {
			if present, err := mysqlSMSAuthColumnPresent(ctx, c, name, i); err != nil || !present {
				t.Fatalf("interrupted migration did not restore column %d: %v", i, err)
			}
		}
		if _, err := mysqlSMSAuthColumnPresent(ctx, c, name, 3); err == nil {
			t.Fatal("unexpected migration column accepted")
		}
	})
	t.Run("changed_checksum_is_rejected", func(t *testing.T) {
		original := smsAuthMySQLSQL
		smsAuthMySQLSQL += "\n"
		err := applySMSAuth(ctx, db, "mysql")
		smsAuthMySQLSQL = original
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("changed applied migration was accepted: %v", err)
		}
	})
}
