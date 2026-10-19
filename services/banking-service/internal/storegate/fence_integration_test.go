//go:build integration

package storegate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
)

func fenceTestConfig(t *testing.T, name string) config.Config {
	t.Helper()
	if os.Getenv("BANKING_STOREGATE_TEST") != "1" {
		t.Skip("BANKING_STOREGATE_TEST=1 enables isolated storage-authority integration tests")
	}
	path := filepath.Join("..", "..", "..", "..", "deploy", "banking-postgres", name)
	values, err := godotenv.Read(path)
	if err != nil {
		t.Fatal("required private storegate test configuration is unavailable")
	}
	cfg, err := config.Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal("invalid private storegate test configuration")
	}
	return cfg
}

func fenceTestDB(t *testing.T, cfg config.Config) *sql.DB {
	t.Helper()
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if !strings.Contains(name, "test") {
		t.Fatal("storage authority tests require an explicitly named test database")
	}
	connections, err := platform.Open(cfg)
	if err != nil {
		t.Fatal("failed to initialize private test connection")
	}
	t.Cleanup(connections.Close)
	if err := connections.SQL.PingContext(context.Background()); err != nil {
		t.Fatal("private storegate test database is unavailable")
	}
	return connections.SQL
}

func fenceMode(t *testing.T, db *sql.DB, dialect, mode string) {
	t.Helper()
	query := "UPDATE banking_storage_control SET mode=? WHERE id=1"
	if dialect == "postgres" {
		query = "UPDATE banking_storage_control SET mode=$1 WHERE id=1"
	}
	if _, err := db.ExecContext(context.Background(), query, mode); err != nil {
		t.Fatal(err)
	}
}

const fenceUserInsert = "INSERT INTO banking_users(id,display_name,status,created_at,updated_at) VALUES(1960077007001,'P7 rollback fixture','active',1,1)"

func assertFrozenWrite(t *testing.T, db *sql.DB, statement string, importFlag string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if importFlag != "" {
		if _, err = tx.ExecContext(context.Background(), importFlag); err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.ExecContext(context.Background(), statement)
	if err == nil || !strings.Contains(err.Error(), "BANKING_STORAGE_FROZEN") {
		t.Fatalf("write must be denied by the database storage fence: %v", err)
	}
}

func verifyFrozenCRUD(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	fenceMode(t, db, dialect, "frozen")
	for _, tc := range []struct{ name, sql string }{
		{"insert", fenceUserInsert},
		{"update", "UPDATE banking_risk_rules SET description=description WHERE name='simulation_only'"},
		{"delete", "DELETE FROM banking_risk_rules WHERE name='simulation_only'"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertFrozenWrite(t, db, tc.sql, "") })
	}
}

func verifyFreezeWaits(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	fenceMode(t, db, dialect, "active")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.ExecContext(ctx, fenceUserInsert); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := Change(ctx, db, dialect, "active", "frozen", "")
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("freeze overtook an in-flight writer holding the SHARE lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatalf("freeze failed after writer released its lock: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("freeze did not resume after in-flight writer finished")
	}
	state, err := Status(ctx, db)
	if err != nil || state.Mode != "frozen" {
		t.Fatalf("freeze state was not committed: %v / %s", err, state.Mode)
	}
}

func verifyNoFixtureRows(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM banking_users WHERE id=1960077007001").Scan(&count); err != nil || count != 0 {
		t.Fatalf("storegate tests must leave no committed business fixtures: count=%d error=%v", count, err)
	}
}

func verifyFenceCoverage(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	query := "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name LIKE 'qyj_fence_%'"
	want := 156 // 011's 46 + 018's 2 + 019's 1 + 020's 3, with 3 operations each.
	if dialect == "postgres" {
		query = "SELECT COUNT(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname='qyj_storage_fence' AND tgrelid IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace)"
		want = 52
	}
	var actual int
	if err := db.QueryRowContext(context.Background(), query).Scan(&actual); err != nil || actual != want {
		t.Fatalf("every business table must have INSERT/UPDATE/DELETE fencing: count=%d want=%d error=%v", actual, want, err)
	}
}

func TestStoregatePostgres(t *testing.T) {
	ownerConfig := fenceTestConfig(t, "finance.test.env.local")
	if ownerConfig.DatabaseDriver != "postgres" {
		t.Fatal("PostgreSQL owner test configuration is required")
	}
	owner := fenceTestDB(t, ownerConfig)
	verifyFenceCoverage(t, owner, "postgres")
	verifyNoFixtureRows(t, owner)
	runtimeConfig := fenceTestConfig(t, "finance.app.env.local")
	runtimeConfig.PostgresDatabase = ownerConfig.PostgresDatabase
	runtime := fenceTestDB(t, runtimeConfig)
	t.Cleanup(func() {
		fenceMode(t, owner, "postgres", "standby")
		verifyNoFixtureRows(t, owner)
	})
	t.Run("frozen_insert_update_delete", func(t *testing.T) { verifyFrozenCRUD(t, owner, "postgres") })
	t.Run("freeze_waits_for_inflight_writer", func(t *testing.T) { verifyFreezeWaits(t, owner, "postgres") })
	t.Run("runtime_cannot_change_control", func(t *testing.T) {
		for _, statement := range []string{
			"UPDATE banking_storage_control SET mode='active' WHERE id=1",
			"DELETE FROM banking_storage_control WHERE id=1",
			"DELETE FROM banking_storage_transitions",
		} {
			tx, err := runtime.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.ExecContext(context.Background(), statement)
			tx.Rollback()
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("runtime control mutation must be denied by grants: %v", err)
			}
		}
	})
	t.Run("runtime_active_write_remains_available", func(t *testing.T) {
		fenceMode(t, owner, "postgres", "active")
		tx, err := runtime.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(context.Background(), fenceUserInsert); err != nil {
			t.Fatalf("runtime financial writes must work while storage is active: %v", err)
		}
	})
	t.Run("runtime_cannot_forge_import_authority", func(t *testing.T) {
		for _, mode := range []string{"standby", "loading"} {
			fenceMode(t, owner, "postgres", mode)
			assertFrozenWrite(t, runtime, fenceUserInsert, "SET LOCAL banking.migration_import='on'")
		}
	})
	t.Run("owner_loading_import_requires_flag", func(t *testing.T) {
		fenceMode(t, owner, "postgres", "loading")
		assertFrozenWrite(t, owner, fenceUserInsert, "")
		tx, err := owner.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(context.Background(), "SET LOCAL banking.migration_import='on'"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(context.Background(), fenceUserInsert); err != nil {
			t.Fatalf("explicit operator import must be allowed in loading state: %v", err)
		}
	})
}

func TestStoregateMySQL(t *testing.T) {
	cfg := fenceTestConfig(t, "finance.mysql-core-test.env.local")
	if cfg.DatabaseDriver != "mysql" {
		t.Fatal("MySQL test configuration is required")
	}
	db := fenceTestDB(t, cfg)
	verifyFenceCoverage(t, db, "mysql")
	verifyNoFixtureRows(t, db)
	t.Cleanup(func() {
		fenceMode(t, db, "mysql", "active")
		verifyNoFixtureRows(t, db)
	})
	t.Run("frozen_insert_update_delete", func(t *testing.T) { verifyFrozenCRUD(t, db, "mysql") })
	t.Run("freeze_waits_for_inflight_writer", func(t *testing.T) { verifyFreezeWaits(t, db, "mysql") })
	t.Run("loading_import_requires_flag", func(t *testing.T) {
		fenceMode(t, db, "mysql", "loading")
		assertFrozenWrite(t, db, fenceUserInsert, "")
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(context.Background(), "SET @banking_migration_import=0")
		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(context.Background(), "SET @banking_migration_import=1"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(context.Background(), fenceUserInsert); err != nil {
			t.Fatalf("explicit operator import must be allowed in loading state: %v", err)
		}
	})
}

func TestStoregatePostgresGenerationCAS(t *testing.T) {
	cfg := fenceTestConfig(t, "finance.test.env.local")
	if cfg.DatabaseDriver != "postgres" {
		t.Fatal("PostgreSQL owner test configuration is required")
	}
	db := fenceTestDB(t, cfg)
	ctx := context.Background()
	original, err := Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "UPDATE banking_storage_control SET mode=$1,snapshot_digest=$2 WHERE id=1", original.Mode, original.SnapshotDigest); err != nil {
			t.Fatal(err)
		}
	})
	fenceMode(t, db, "postgres", "standby")
	verified, err := Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var transitions int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_storage_transitions").Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	// A completed import or another operator changes the target after its
	// snapshot was verified, while leaving it in the same standby mode.
	if _, err = db.ExecContext(ctx, "UPDATE banking_storage_control SET generation=generation+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	digest := verified.SnapshotDigest
	if len(digest) != 64 {
		digest = strings.Repeat("c", 64)
	}
	if _, err = Change(ctx, db, "postgres", "standby", "active", digest, verified.Generation); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("a stale snapshot generation must not activate a changed target: %v", err)
	}
	current, err := Status(ctx, db)
	if err != nil || current.Mode != "standby" || current.Generation != verified.Generation+1 || current.SnapshotDigest != verified.SnapshotDigest {
		t.Fatalf("rejected activation changed target state: %+v / %v", current, err)
	}
	var after int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_storage_transitions").Scan(&after); err != nil || after != transitions {
		t.Fatalf("rejected activation must not append an authority transition: before=%d after=%d error=%v", transitions, after, err)
	}
	activated, err := Change(ctx, db, "postgres", "standby", "active", digest, current.Generation)
	if err != nil || activated.Mode != "active" || activated.Generation != current.Generation+1 {
		t.Fatalf("matching generation must activate the verified target: %+v / %v", activated, err)
	}
	if _, err = Change(ctx, db, "postgres", "standby", "active", digest, current.Generation); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("idempotent activation must still reject stale verification generation: %v", err)
	}
	replayed, err := Change(ctx, db, "postgres", "standby", "active", digest, activated.Generation)
	if err != nil || replayed != activated {
		t.Fatalf("current-generation activation replay must preserve state: %+v / %v", replayed, err)
	}
}
