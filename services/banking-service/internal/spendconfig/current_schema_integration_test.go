package spendconfig

import (
	"context"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
)

var spendTestEnv = flag.String("spend-test-env", "", "explicit isolated gateway-only configuration; never discovered")
var spendTestPolicy = flag.String("spend-test-policy", "", "explicit synthetic project price policy")
var spendTestDatabase = flag.String("spend-test-database", "", "exact existing isolated test database")
var spendTestRole = flag.String("spend-test-role", "gate_runtime", "explicit non-superuser isolated database role")

// This test only reads an explicitly selected isolated database. It never loads
// supplier credentials, migrates a schema, provisions a quota or reserves money.
func TestCheckCurrentSchemaRetainsFeePolicyBinding(t *testing.T) {
	if *spendTestEnv == "" && *spendTestPolicy == "" && *spendTestDatabase == "" {
		t.Skip("explicit isolated SQL inputs not supplied")
	}
	if *spendTestEnv == "" || *spendTestPolicy == "" || !strings.HasSuffix(*spendTestDatabase, "_test") {
		t.Fatal("all isolated test inputs are required")
	}
	c, err := Load(*spendTestEnv, *spendTestPolicy, *spendTestDatabase, false)
	if err != nil || c.Outbound.Environment != "test" || *spendTestRole == "" || c.Database.PostgresUser != *spendTestRole {
		t.Fatal("explicit minimal gateway configuration required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := platform.Open(c.Database)
	if err != nil {
		t.Fatal("isolated connection initialization failed", err)
	}
	defer conn.Close()
	var superuser, bypassRLS bool
	if conn.SQL.QueryRowContext(ctx, "SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&superuser, &bypassRLS) != nil || superuser || bypassRLS {
		t.Fatal("isolated runtime role must retain non-superuser and RLS restrictions")
	}
	var version int
	if conn.SQL.QueryRowContext(ctx, "SELECT MAX(version) FROM banking_schema_migrations").Scan(&version) != nil || version != migration.CurrentVersion {
		t.Fatal("test must exercise the actual current schema")
	}
	if err := Check(ctx, c, conn); err != nil {
		t.Fatal("read-only spend readiness rejects current additive application schema", err)
	}
	var existingProject string
	if conn.SQL.QueryRowContext(ctx, "SELECT project_id FROM banking_model_spend_pools ORDER BY project_id LIMIT 1").Scan(&existingProject) != nil {
		t.Fatal("isolated copy must retain an existing policy-bound ledger")
	}
	wrong := c
	wrong.Policy.ProjectID = existingProject
	wrong.Policy.PriceVersion = "deliberately-mismatched-isolated-regression"
	if Check(ctx, wrong, conn) == nil {
		t.Fatal("schema compatibility must not bypass persisted pool policy binding")
	}
}
