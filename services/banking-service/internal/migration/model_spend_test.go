package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/storegate"
)

func TestModelSpend020SchemaContract(t *testing.T) {
	if CurrentVersion != 20 {
		t.Errorf("schema version = %d, want additive 020", CurrentVersion)
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			raw, err := os.ReadFile("model_spend_020_" + dialect + ".sql")
			if err != nil {
				t.Fatal("missing model spend 020 schema:", err)
			}
			ddl := strings.ToLower(string(raw))
			for _, table := range []string{"pools", "attempts", "events"} {
				if !strings.Contains(ddl, "create table banking_model_spend_"+table) {
					t.Error("missing isolated spend table", table)
				}
			}
			for _, required := range []string{
				"project_id varchar(64)", "currency varchar(3)", "policy_hash varchar(64)",
				"limit_units>=0", "used_units>=0", "held_units>=0", "reserved_units>=0",
				"estimated_units>=0", "actual_units>=0", "'reserved','unknown','reported','settled'",
				"unique(project_id,request_key)", "dedupe_key varchar(64) not null unique",
				"response_body", "octet_length(response_body)<=2097152", "amount_units bigint not null",
				"references banking_model_spend_pools(project_id)", "references banking_model_spend_attempts(id)",
			} {
				if !strings.Contains(ddl, required) {
					t.Errorf("missing contract %q", required)
				}
			}
			for _, forbidden := range []string{"if not exists", "used_units+held_units", "used_units + held_units", "amount_units>=0", "response_body json"} {
				if strings.Contains(ddl, forbidden) {
					t.Errorf("unsafe schema contract %q", forbidden)
				}
			}
		})
	}
}

// Record the immutable fence output before 020 is implemented. These fixed
// outputs are checksum inputs of historical migrations, not the new allowlist.
func TestHistoricalModelFenceChecksums(t *testing.T) {
	want := map[string][2]string{
		"mysql":    {"1cb571ed7995a6d8239cfc5f180210feb1315dfdf5405eabe46f67f6fe4a5e38", "e53c4730049ca50e5bcff3455c4db452b2b7367848598bc773079d8ffa38afad"},
		"postgres": {"3c65a7353eb3f8d3379832d8d09bbbd8d2d4abac22bd519ba66ecd4f852b923b", "0a45287f2f35a6c11b9ce2e1048a9a70b54df9324d1a18df4b462ebbd251348b"},
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		budget := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(storegate.ModelBudgetFences(dialect), ";\n"))))
		accounting := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(storegate.ModelReconciliationFences(dialect), ";\n"))))
		t.Logf("%s frozen018fences=%s frozen019fences=%s", dialect, budget, accounting)
		if [2]string{budget, accounting} != want[dialect] {
			t.Fatal("historical migration fence checksum changed")
		}
	}
}

func TestModelSpend020RefusesUnregisteredExistingTablesBeforeDDL(t *testing.T) {
	for _, count := range []int64{1, 2, 3} {
		fixture := &spendMigrationConn{existingTables: count}
		db := sql.OpenDB(spendMigrationConnector{fixture})
		err := applyModelSpendOn(context.Background(), db, "mysql")
		db.Close()
		if err == nil || !strings.Contains(err.Error(), "manual recovery") || len(fixture.executed) != 0 {
			t.Fatalf("unregistered %d-table schema adopted or changed: %v / %v", count, err, fixture.executed)
		}
	}
}

func TestModelSpend020RecordsExactChecksumAndRerunIsReadOnly(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		fixture := &spendMigrationConn{}
		db := sql.OpenDB(spendMigrationConnector{fixture})
		if err := applyModelSpendOn(context.Background(), db, dialect); err != nil {
			t.Fatal(err)
		}
		ddl := modelSpendMySQLSQL
		if dialect == "postgres" {
			ddl = modelSpendPostgresSQL
		}
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl+strings.Join(storegate.ModelSpendFences(dialect), ";\n"))))
		if fixture.previous != want {
			t.Fatal("020 checksum did not cover exact SQL and fences")
		}
		before := len(fixture.executed)
		if err := applyModelSpendOn(context.Background(), db, dialect); err != nil || len(fixture.executed) != before {
			t.Fatal("identical migration rerun was not read-only", err)
		}
		fixture.previous = strings.Repeat("0", 64)
		if err := applyModelSpendOn(context.Background(), db, dialect); err == nil || len(fixture.executed) != before {
			t.Fatal("checksum mismatch accepted or caused mutation", err)
		}
		db.Close()
	}
}

func TestModelSpend020MySQLPartialDDLNeedsManualRecovery(t *testing.T) {
	fixture := &spendMigrationConn{failCreateAt: 2}
	db := sql.OpenDB(spendMigrationConnector{fixture})
	defer db.Close()
	if err := applyModelSpendOn(context.Background(), db, "mysql"); err == nil {
		t.Fatal("DDL error ignored")
	}
	before := len(fixture.executed)
	if fixture.existingTables != 1 || fixture.previous != "" {
		t.Fatal("partial DDL was marked migrated")
	}
	if err := applyModelSpendOn(context.Background(), db, "mysql"); err == nil || !strings.Contains(err.Error(), "manual recovery") || len(fixture.executed) != before {
		t.Fatal("partial MySQL DDL was silently adopted", err)
	}
}

// A narrow database/sql driver exercises migration decisions without opening a
// network connection. DDL validity and transaction behavior use isolated DB tests.
type spendMigrationConnector struct{ c *spendMigrationConn }

func (c spendMigrationConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c spendMigrationConnector) Driver() driver.Driver                        { return spendMigrationDriver{} }

type spendMigrationDriver struct{}

func (spendMigrationDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected open")
}

type spendMigrationConn struct {
	previous              string
	existingTables        int64
	failCreateAt, creates int
	executed              []string
}

func (*spendMigrationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*spendMigrationConn) Close() error              { return nil }
func (*spendMigrationConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected begin") }
func (c *spendMigrationConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(q, "version=20") {
		if c.previous == "" {
			return &spendMigrationRows{}, nil
		}
		return &spendMigrationRows{values: [][]driver.Value{{c.previous}}}, nil
	}
	if strings.Contains(q, "information_schema.tables") {
		return &spendMigrationRows{values: [][]driver.Value{{c.existingTables}}}, nil
	}
	return nil, fmt.Errorf("unexpected query %s", q)
}
func (c *spendMigrationConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.executed = append(c.executed, q)
	if strings.HasPrefix(q, "CREATE TABLE banking_model_spend_") {
		c.creates++
		if c.creates == c.failCreateAt {
			return nil, errors.New("controlled partial DDL failure")
		}
		c.existingTables++
	}
	if strings.HasPrefix(q, "INSERT INTO banking_schema_migrations") {
		c.previous = args[0].Value.(string)
	}
	return driver.RowsAffected(1), nil
}

type spendMigrationRows struct{ values [][]driver.Value }

func (*spendMigrationRows) Columns() []string { return []string{"value"} }
func (*spendMigrationRows) Close() error      { return nil }
func (r *spendMigrationRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
