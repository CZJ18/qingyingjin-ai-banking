//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
)

// Only the parent runner provisions/migrates the new isolated database. This
// harness never discovers an application DSN, auto-migrates, or removes data.
func spendSchemaFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := os.Getenv("BANKING_SPEND_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_SPEND_TEST_ENV requires a newly provisioned isolated SQL fixture")
	}
	if os.Getenv("BANKING_TEST_ENVIRONMENT") != "isolated" {
		t.Fatal("isolated test environment acknowledgement required")
	}
	values, err := godotenv.Read(path)
	if err != nil {
		t.Fatal("cannot read isolated schema fixture")
	}
	cfg, err := config.Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal("invalid isolated schema fixture")
	}
	name, host := cfg.MySQLDatabase, cfg.MySQLHost
	if cfg.DatabaseDriver == "postgres" {
		name, host = cfg.PostgresDatabase, cfg.PostgresHost
	}
	if name != os.Getenv("BANKING_SPEND_TEST_EXPECT_DATABASE") || !strings.HasSuffix(name, "_test") || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		t.Fatal("schema fixture is outside exact loopback test database allowlist")
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal("cannot open isolated schema fixture")
	}
	t.Cleanup(conn.Close)
	var version int
	if err = conn.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil || version != 20 {
		t.Fatal("runner must migrate the isolated fixture to schema 020 first")
	}
	return conn.SQL, cfg.DatabaseDriver
}

func spendBind(query, dialect string) string {
	if dialect != "postgres" {
		return query
	}
	for n := 1; strings.Contains(query, "?"); n++ {
		query = strings.Replace(query, "?", fmt.Sprintf("$%d", n), 1)
	}
	return query
}

func spendSchemaTransaction(t *testing.T, db *sql.DB, dialect string) (*sql.Tx, string, string, string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	project, attempt, event := "schema-"+uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(spendBind(query, dialect), args...); err != nil {
			t.Fatal("cannot create rollback-only schema fixture:", err)
		}
	}
	exec("UPDATE banking_storage_control SET mode='active' WHERE id=1")
	// An overrun must be representable: a limit CHECK must not hide debt.
	exec("INSERT INTO banking_model_spend_pools(project_id,currency,policy_hash,limit_units,used_units,held_units,frozen,created_at,updated_at) VALUES(?,'CNY',?,9,11,3,?,1,1)", project, strings.Repeat("a", 64), true)
	exec("INSERT INTO banking_model_spend_attempts(id,project_id,request_key,request_hash,policy_hash,status,reserved_units,response_body,created_at,updated_at) VALUES(?,?,?,?,?,'reported',3,?,1,1)", attempt, project, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("a", 64), "data: {\"usage\":{}}\n\ndata: [DONE]\n\n")
	// Signed audit amounts permit future adjustments without weakening holds.
	exec("INSERT INTO banking_model_spend_events(id,project_id,attempt_id,kind,dedupe_key,request_hash,amount_units,snapshot_json,created_at) VALUES(?,?,?,'fixture',?,?,-1,'{}',1)", event, project, attempt, strings.Repeat("d", 64), strings.Repeat("c", 64))
	return tx, project, attempt, event
}

func TestSpend020SchemaReplayAndPlainSSE(t *testing.T) {
	db, dialect := spendSchemaFixture(t)
	var before, after string
	if err := db.QueryRow("SELECT checksum FROM banking_schema_migrations WHERE version=20").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := applyModelSpend(context.Background(), db, dialect); err != nil {
		t.Fatal("020 rerun failed:", err)
	}
	if err := db.QueryRow("SELECT checksum FROM banking_schema_migrations WHERE version=20").Scan(&after); err != nil || before != after {
		t.Fatal("020 rerun changed checksum")
	}
	tx, _, attempt, _ := spendSchemaTransaction(t, db, dialect)
	var response string
	if err := tx.QueryRow(spendBind("SELECT response_body FROM banking_model_spend_attempts WHERE id=?", dialect), attempt).Scan(&response); err != nil || response != "data: {\"usage\":{}}\n\ndata: [DONE]\n\n" {
		t.Fatal("SSE response text changed or was parsed as JSON")
	}
}

func TestSpend020SchemaFreezeAndAppendOnly(t *testing.T) {
	db, dialect := spendSchemaFixture(t)
	for _, operation := range []string{"pool update", "attempt update", "event insert frozen", "event update", "event delete", "event truncate"} {
		if operation == "event truncate" && dialect != "postgres" {
			continue
		} // MySQL TRUNCATE is DDL; runtime must have no DROP privilege.
		t.Run(operation, func(t *testing.T) {
			tx, project, attempt, event := spendSchemaTransaction(t, db, dialect)
			var query, want string
			var args []any
			switch operation {
			case "pool update":
				query = "UPDATE banking_model_spend_pools SET held_units=4 WHERE project_id=?"
				args = []any{project}
				want = "BANKING_STORAGE_FROZEN"
			case "attempt update":
				query = "UPDATE banking_model_spend_attempts SET status='unknown' WHERE id=?"
				args = []any{attempt}
				want = "BANKING_STORAGE_FROZEN"
			case "event insert frozen":
				query = "INSERT INTO banking_model_spend_events(id,project_id,attempt_id,kind,dedupe_key,request_hash,amount_units,snapshot_json,created_at) VALUES(?,?,?,'fixture',?,?,1,'{}',1)"
				args = []any{uuid.NewString(), project, attempt, strings.Repeat("e", 64), strings.Repeat("c", 64)}
				want = "BANKING_STORAGE_FROZEN"
			case "event update":
				query = "UPDATE banking_model_spend_events SET amount_units=1 WHERE id=?"
				args = []any{event}
				want = "BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY"
			case "event delete":
				query = "DELETE FROM banking_model_spend_events WHERE id=?"
				args = []any{event}
				want = "BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY"
			case "event truncate":
				query = "TRUNCATE banking_model_spend_events"
				want = "BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY"
			}
			if strings.Contains(operation, "frozen") || operation == "pool update" || operation == "attempt update" {
				if _, err := tx.Exec("UPDATE banking_storage_control SET mode='frozen' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Exec(spendBind(query, dialect), args...); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s bypassed expected %s fence: %v", operation, want, err)
			}
		})
	}
}

func TestSpend020SchemaConstraints(t *testing.T) {
	db, dialect := spendSchemaFixture(t)
	for _, operation := range []string{"negative pool", "negative reserve", "negative actual", "negative estimate", "invalid status", "large response", "duplicate request", "duplicate event", "missing attempt"} {
		t.Run(operation, func(t *testing.T) {
			tx, project, attempt, _ := spendSchemaTransaction(t, db, dialect)
			var query string
			var args []any
			switch operation {
			case "negative pool":
				query = "UPDATE banking_model_spend_pools SET held_units=-1 WHERE project_id=?"
				args = []any{project}
			case "negative reserve":
				query = "UPDATE banking_model_spend_attempts SET reserved_units=-1 WHERE id=?"
				args = []any{attempt}
			case "negative actual":
				query = "UPDATE banking_model_spend_attempts SET actual_units=-1 WHERE id=?"
				args = []any{attempt}
			case "negative estimate":
				query = "UPDATE banking_model_spend_attempts SET estimated_units=-1 WHERE id=?"
				args = []any{attempt}
			case "invalid status":
				query = "UPDATE banking_model_spend_attempts SET status='released' WHERE id=?"
				args = []any{attempt}
			case "large response":
				query = "UPDATE banking_model_spend_attempts SET response_body=? WHERE id=?"
				args = []any{strings.Repeat("x", 2097153), attempt}
			case "duplicate request":
				query = "INSERT INTO banking_model_spend_attempts(id,project_id,request_key,request_hash,policy_hash,status,reserved_units,created_at,updated_at) VALUES(?,?,?,?,?,'reserved',1,1,1)"
				args = []any{uuid.NewString(), project, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("a", 64)}
			case "duplicate event", "missing attempt":
				query = "INSERT INTO banking_model_spend_events(id,project_id,attempt_id,kind,dedupe_key,request_hash,amount_units,snapshot_json,created_at) VALUES(?,?,?,'fixture',?,?,1,'{}',1)"
				key := strings.Repeat("d", 64)
				if operation == "missing attempt" {
					attempt = uuid.NewString()
					key = strings.Repeat("f", 64)
				}
				args = []any{uuid.NewString(), project, attempt, key, strings.Repeat("c", 64)}
			}
			if _, err := tx.Exec(spendBind(query, dialect), args...); err == nil {
				t.Fatal("database accepted invalid monetary state:", operation)
			}
		})
	}
}
