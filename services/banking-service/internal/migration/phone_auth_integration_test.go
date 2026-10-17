//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
)

func phoneAuthTestDB(t *testing.T, dialect string) (*sql.DB, context.Context) {
	t.Helper()
	key := "BANKING_AUTH_MIGRATION_" + strings.ToUpper(dialect) + "_ENV"
	path := os.Getenv(key)
	if path == "" {
		t.Skip(key + " is not configured; no live migration test executed")
	}
	values, err := godotenv.Read(path)
	if err != nil {
		t.Fatal("cannot read isolated migration test configuration")
	}
	values["BANKING_CHECK_TIMEOUT"] = "15s"
	cfg, err := config.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	name := cfg.MySQLDatabase
	if dialect == "postgres" {
		name = cfg.PostgresDatabase
	}
	if cfg.DatabaseDriver != dialect || !strings.HasSuffix(name, "_test") {
		t.Fatal("phone authentication migration tests require an explicitly isolated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	if dialect == "mysql" {
		connections, err := platform.Open(cfg)
		if err != nil {
			t.Fatal("cannot open isolated migration database")
		}
		t.Cleanup(connections.Close)
		if err = connections.SQL.PingContext(ctx); err != nil {
			t.Fatal("isolated MySQL migration database is unavailable")
		}
		return connections.SQL, ctx
	}
	connectionURL := url.URL{Scheme: "postgres", User: url.UserPassword(cfg.PostgresUser, cfg.PostgresPassword), Host: net.JoinHostPort(cfg.PostgresHost, strconv.Itoa(cfg.PostgresPort)), Path: "/" + cfg.PostgresDatabase}
	connectionURL.RawQuery = url.Values{"sslmode": {cfg.PostgresSSLMode}, "connect_timeout": {"15"}}.Encode()
	pgcfg, err := pgx.ParseConfig(connectionURL.String())
	if err != nil {
		t.Fatal("cannot parse isolated migration connection")
	}
	admin := stdlib.OpenDB(*pgcfg)
	t.Cleanup(func() { admin.Close() })
	schema := "auth012_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("cannot create isolated migration schema")
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("cannot remove isolated migration schema: %v", err)
		}
	})
	pgcfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*pgcfg)
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func assertPhoneAuthConstraints(t *testing.T, db *sql.DB, ctx context.Context, dialect string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	placeholder := func(n int) string {
		if dialect == "postgres" {
			return fmt.Sprintf("$%d", n)
		}
		return "?"
	}
	seed := time.Now().UnixNano() / 1000
	insert := "INSERT INTO banking_users(id,display_name,status,created_at,updated_at,phone_number,refresh_hash) VALUES(" + placeholder(1) + ",'Auth migration fixture','active',1,1," + placeholder(2) + "," + placeholder(3) + ")"
	// Multiple legacy users with NULL phone/refresh credentials must remain valid.
	for i := int64(0); i < 2; i++ {
		if _, err = tx.ExecContext(ctx, insert, seed+i, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	var password string
	var version, expiry int64
	if err = tx.QueryRowContext(ctx, "SELECT password_hash,auth_version,refresh_expires_at FROM banking_users WHERE id="+placeholder(1), seed).Scan(&password, &version, &expiry); err != nil || password != "" || version != 0 || expiry != 0 {
		t.Fatalf("legacy credential defaults are not preserved: %v", err)
	}
	phone := "+86199" + strconv.FormatInt(seed%100000000, 10)
	refresh := fmt.Sprintf("%064x", seed)
	if _, err = tx.ExecContext(ctx, insert, seed+2, phone, refresh); err != nil {
		t.Fatal(err)
	}
	for i, args := range [][]any{{seed + 3, phone, nil}, {seed + 4, nil, refresh}} {
		if _, err = tx.ExecContext(ctx, "SAVEPOINT duplicate_credential"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, insert, args...); err == nil {
			t.Fatalf("credential uniqueness check %d accepted duplicate", i)
		}
		if _, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT duplicate_credential"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhoneAuthPostgresLive(t *testing.T) {
	t.Run("concurrent_additive_migration_and_constraints", func(t *testing.T) {
		db, ctx := phoneAuthTestDB(t, "postgres")
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO banking_users(id,display_name,status,created_at,updated_at) VALUES(6012001,'Existing user','active',1,1)"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); errors <- applyPhoneAuth(ctx, db, "postgres") }()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations WHERE version=12"); n != 1 {
			t.Fatal("concurrent migration recorded version 12 more than once")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_users WHERE id=6012001 AND phone_number IS NULL AND password_hash='' AND auth_version=0 AND refresh_hash IS NULL AND refresh_expires_at=0"); n != 1 {
			t.Fatal("existing user changed during additive migration")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"); n != 47 {
			t.Fatal("authentication migration changed the 46 business table contract")
		}
		assertPhoneAuthConstraints(t, db, ctx, "postgres")
	})
	t.Run("late_failure_rolls_back_all_columns", func(t *testing.T) {
		db, ctx := phoneAuthTestDB(t, "postgres")
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
		original := phoneAuthPostgresSQL
		phoneAuthPostgresSQL += "\nSELECT 1/0;\n"
		err := applyPhoneAuth(ctx, db, "postgres")
		phoneAuthPostgresSQL = original
		if err == nil {
			t.Fatal("injected late failure did not stop migration")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_users' AND column_name IN ('phone_number','password_hash','auth_version','refresh_hash','refresh_expires_at')"); n != 0 {
			t.Fatal("transactional migration left partially committed credential columns")
		}
		if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations WHERE version=12"); n != 0 {
			t.Fatal("failed migration recorded schema 12")
		}
		if err = applyPhoneAuth(ctx, db, "postgres"); err != nil {
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
		original := phoneAuthPostgresSQL
		phoneAuthPostgresSQL += "\n"
		err := applyPhoneAuth(ctx, db, "postgres")
		phoneAuthPostgresSQL = original
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("changed applied migration was accepted: %v", err)
		}
	})
}

func TestPhoneAuthMySQLLive(t *testing.T) {
	db, ctx := phoneAuthTestDB(t, "mysql")
	t.Run("full_apply_reentrant_and_constraints", func(t *testing.T) {
		var before string
		if err := db.QueryRowContext(ctx, "SELECT GROUP_CONCAT(CONCAT(version,':',checksum) ORDER BY version) FROM banking_schema_migrations WHERE version<=11").Scan(&before); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := Apply(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); errors <- applyPhoneAuth(ctx, db, "mysql") }()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		var after string
		if err := db.QueryRowContext(ctx, "SELECT GROUP_CONCAT(CONCAT(version,':',checksum) ORDER BY version) FROM banking_schema_migrations WHERE version<=11").Scan(&after); err != nil || after != before {
			t.Fatal("historical migration checksums changed", err)
		}
		assertPhoneAuthConstraints(t, db, ctx, "mysql")
	})
	t.Run("interrupted_auto_commit_retries_and_rejects_wrong_columns", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		name := "auth012_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err = conn.ExecContext(ctx, "CREATE TABLE "+name+" (id BIGINT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(context.Background(), "DROP TABLE "+name)
		statements, _ := phoneAuthStatements("mysql")
		for i := range statements {
			statements[i] = strings.ReplaceAll(statements[i], "banking_users", name)
		}
		// Keep reviewed index names; MySQL index names are table-local.
		for i := 5; i < len(statements); i++ {
			statements[i] = strings.ReplaceAll(statements[i], name+"_", "banking_users_")
		}
		if _, err = conn.ExecContext(ctx, statements[0]); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_number VARCHAR(21) NULL"); err != nil {
			t.Fatal(err)
		}
		if err = applyPhoneAuthMySQLDDL(ctx, conn, name, statements); err == nil || !strings.Contains(err.Error(), "incompatible definition") {
			t.Fatalf("incompatible preexisting column was adopted: %v", err)
		}
		if _, err = conn.ExecContext(ctx, "ALTER TABLE "+name+" MODIFY phone_number VARCHAR(20) NULL"); err != nil {
			t.Fatal(err)
		}
		broken := append([]string(nil), statements...)
		broken[6] = "INVALID AUTHENTICATION DDL"
		if err = applyPhoneAuthMySQLDDL(ctx, conn, name, broken); err == nil {
			t.Fatal("injected MySQL DDL failure was ignored")
		}
		for range 2 {
			if err = applyPhoneAuthMySQLDDL(ctx, conn, name, statements); err != nil {
				t.Fatal(err)
			}
		}
		for i := range statements {
			if present, err := mysqlPhoneAuthObjectPresent(ctx, conn, name, i); err != nil || !present {
				t.Fatalf("MySQL interrupted migration did not restore object %d: %v", i, err)
			}
		}
	})
	t.Run("changed_checksum_is_rejected", func(t *testing.T) {
		original := phoneAuthMySQLSQL
		phoneAuthMySQLSQL += "\n"
		err := applyPhoneAuth(ctx, db, "mysql")
		phoneAuthMySQLSQL = original
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("changed applied migration was accepted: %v", err)
		}
	})
}
