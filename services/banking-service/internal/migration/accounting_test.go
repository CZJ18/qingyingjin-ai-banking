package migration

import (
	"database/sql"
	"strings"
	"testing"
)

func TestAccountingMigrationDoesNotAdoptWrongExistingColumns(t *testing.T) {
	if accountingColumnMatches("VARCHAR(64) NULL", "bigint", sql.NullInt64{}, "YES", sql.NullString{}) {
		t.Fatal("incompatible column adopted")
	}
	if !accountingColumnMatches("VARCHAR(24) NOT NULL DEFAULT 'legacy'", "character varying", sql.NullInt64{Int64: 24, Valid: true}, "NO", sql.NullString{String: "'legacy'::character varying", Valid: true}) {
		t.Fatal("valid PostgreSQL column rejected")
	}
	if CurrentVersion < 19 || !strings.Contains(modelAccountingMySQLSQL, "banking_model_reconciliations") || !strings.Contains(modelAccountingPostgresSQL, "usage_status") {
		t.Fatal("missing additive migration")
	}
}
