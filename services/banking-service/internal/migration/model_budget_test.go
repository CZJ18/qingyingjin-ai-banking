package migration

import (
	"strings"
	"testing"
)

func TestModelBudgetMigrationIsAdditiveAndOwnerBound(t *testing.T) {
	if CurrentVersion < 18 {
		t.Fatal("model quota needs schema 18")
	}
	for _, ddl := range []string{modelBudgetMySQLSQL, modelBudgetPostgresSQL} {
		for _, required := range []string{"banking_model_budget_windows", "banking_model_calls", "user_id", "tokens_reserved", "request_hash", "response_json", "UNIQUE"} {
			if !strings.Contains(ddl, required) {
				t.Fatal("missing quota persistence constraint", required)
			}
		}
		if strings.Contains(strings.ToUpper(ddl), "DROP TABLE") || strings.Contains(strings.ToUpper(ddl), "ALTER TABLE BANKING_USERS") {
			t.Fatal("model budget migration rewrites prior financial data")
		}
	}
}
