//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Every case uses a random schema in the explicitly configured test database.
// The operator DSN stays in process memory and is never written to test output.
func postgresFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("BANKING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BANKING_TEST_POSTGRES_DSN is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	if err = admin.PingContext(ctx); err != nil {
		t.Fatal("PostgreSQL test database is unavailable")
	}
	schema := "p7_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove isolated migration schema: %v", err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func pgCount(t *testing.T, ctx context.Context, db *sql.DB, query string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgresBaselineLive(t *testing.T) {
	db, ctx := postgresFixture(t)
	for range 2 {
		if err := ApplyPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"); n != 47 {
		t.Fatalf("want 46 business tables + migration metadata, got %d", n)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations"); n != 10 {
		t.Fatalf("want ten baseline versions, got %d", n)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid IN (SELECT oid FROM pg_class WHERE relnamespace=current_schema()::regnamespace)"); n != 19 {
		t.Fatalf("want 19 replan triggers, got %d", n)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_risk_rules"); n != 6 {
		t.Fatalf("want six deterministic risk seeds, got %d", n)
	}
	if _, err := db.ExecContext(ctx, "UPDATE banking_schema_migrations SET checksum=repeat('0',64) WHERE version=3"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPostgres(ctx, db); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("changed applied checksum must stop startup: %v", err)
	}
}

func TestPostgresRejectsUnrelatedSchema(t *testing.T) {
	db, ctx := postgresFixture(t)
	if _, err := db.ExecContext(ctx, "CREATE TABLE unrelated (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPostgres(ctx, db); err == nil || !strings.Contains(err.Error(), "independent empty") {
		t.Fatalf("must reject unrelated schema: %v", err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='banking_schema_migrations'"); n != 0 {
		t.Fatal("failed baseline must roll back metadata DDL")
	}
}

func TestPostgresFailedDDLLeavesNoPartialBaseline(t *testing.T) {
	db, ctx := postgresFixture(t)
	original := postgresLifestyleSQL
	postgresLifestyleSQL += "\nSELECT 1/0;\n"
	err := ApplyPostgres(ctx, db)
	postgresLifestyleSQL = original
	if err == nil || !strings.Contains(err.Error(), "migration 10") {
		t.Fatalf("injected late DDL failure must stop version 10: %v", err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"); n != 0 {
		t.Fatalf("failed version 10 left %d partially committed tables", n)
	}
	if err := ApplyPostgres(ctx, db); err != nil {
		t.Fatalf("clean retry after transactional rollback: %v", err)
	}
}

func seedPostgresUser(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	if err := ApplyPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO banking_users(id,display_name,status,created_at,updated_at) VALUES(1,'P7 fixture','active',1,1)"); err != nil {
		t.Fatal(err)
	}
}

const pgAccountFixture = "INSERT INTO banking_bank_accounts(user_id,external_account_id,display_name,account_type,masked_account_number,currency,status,created_at,updated_at) VALUES(1,$1,'fixture','checking','****0001','CNY','active',1,1) RETURNING id"

func TestPostgresOutboxAtomicityAndEquality(t *testing.T) {
	db, ctx := postgresFixture(t)
	seedPostgresUser(t, db, ctx)
	var id int64
	if err := db.QueryRowContext(ctx, pgAccountFixture, "CaseÉ").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 1 {
		t.Fatalf("insert event missing: %d", n)
	}
	if _, err := db.ExecContext(ctx, "UPDATE banking_bank_accounts SET version=version+1"); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 1 {
		t.Fatal("unrelated account version change emitted a balance event")
	}
	if _, err := db.ExecContext(ctx, "UPDATE banking_bank_accounts SET status='frozen'"); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 2 {
		t.Fatal("changed account status did not emit an event")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE banking_bank_accounts SET balance_minor=999"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 2 {
		t.Fatal("rolled back update leaked an outbox event")
	}
	if n := pgCount(t, ctx, db, "SELECT balance_minor FROM banking_bank_accounts"); n != 0 {
		t.Fatal("rolled back update changed balance")
	}
	if err := db.QueryRowContext(ctx, pgAccountFixture, "casee").Scan(&id); err == nil {
		t.Fatal("case/accent-insensitive external account uniqueness was lost")
	}
	for _, key := range []string{"Key", "key"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO banking_core_idempotency(user_id,action,idem_key,request_hash,response,created_at) VALUES(1,'test',$1,'hash',$2,1)", key, []byte{0, 255}); err != nil {
			t.Fatal(err)
		}
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_core_idempotency"); n != 2 {
		t.Fatal("binary idempotency keys were incorrectly folded")
	}
	var eventKey string
	var millis int64
	if err := db.QueryRowContext(ctx, "SELECT event_key,created_at FROM banking_replan_events LIMIT 1").Scan(&eventKey, &millis); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(eventKey); err != nil || millis < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatalf("event must carry native UUID and integer milliseconds: %s / %d", eventKey, millis)
	}
}

func TestPostgresConcurrentMigrationRunners(t *testing.T) {
	db, ctx := postgresFixture(t)
	errors := make(chan error, 2)
	for range 2 {
		go func() { errors <- ApplyPostgres(ctx, db) }()
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_schema_migrations"); n != 10 {
		t.Fatal(fmt.Sprintf("concurrent runner produced %d migration records", n))
	}
}

func TestPostgresImportSuppressionRequiresControlOwner(t *testing.T) {
	runtimeDSN := os.Getenv("BANKING_TEST_POSTGRES_RUNTIME_DSN")
	if runtimeDSN == "" {
		t.Skip("BANKING_TEST_POSTGRES_RUNTIME_DSN is not configured")
	}
	runtimeConfig, err := pgx.ParseConfig(runtimeDSN)
	if err != nil {
		t.Fatal("invalid PostgreSQL runtime test DSN")
	}
	db, ctx := postgresFixture(t)
	seedPostgresUser(t, db, ctx)
	var schema string
	if err = db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE banking_storage_control (id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{runtimeConfig.User}.Sanitize()
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT,INSERT,UPDATE ON banking_bank_accounts,banking_users,banking_replan_events TO " + role,
		"GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA " + schema + " TO " + role,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	ownerTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerTx.Rollback()
	if _, err = ownerTx.ExecContext(ctx, "SET LOCAL banking.migration_import='on'"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = ownerTx.QueryRowContext(ctx, pgAccountFixture, "owner import").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = ownerTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 0 {
		t.Fatal("authorized owner import generated derived events")
	}
	runtimeConfig.RuntimeParams["search_path"] = schema
	runtimeDB := stdlib.OpenDB(*runtimeConfig)
	defer runtimeDB.Close()
	runtimeTx, err := runtimeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("runtime role could not connect to isolated test schema")
	}
	defer runtimeTx.Rollback()
	if _, err = runtimeTx.ExecContext(ctx, "SET LOCAL banking.migration_import='on'"); err != nil {
		t.Fatal(err)
	}
	if err = runtimeTx.QueryRowContext(ctx, pgAccountFixture, "runtime cannot suppress").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = runtimeTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, ctx, db, "SELECT COUNT(*) FROM banking_replan_events"); n != 1 {
		t.Fatal("runtime custom GUC bypassed transactional outbox")
	}
}
