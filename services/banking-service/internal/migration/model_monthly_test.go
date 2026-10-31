package migration

import (
	"os"
	"strings"
	"testing"
)

func TestMonthly021SchemaRetainsOwnerAndMoneyConstraints(t *testing.T) {
	if CurrentVersion < 21 {
		t.Fatal("monthly migration not registered")
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		raw, e := os.ReadFile("model_monthly_021_" + dialect + ".sql")
		if e != nil {
			t.Fatal(e)
		}
		ddl := string(raw)
		for _, required := range []string{"CREATE TABLE banking_model_user_limits", "CREATE TABLE banking_model_user_windows", "CREATE TABLE banking_model_attempt_owners", "CHECK(user_id>0)", "CHECK(limit_units>=0)", "CHECK(held_units>=0)", "CHECK(used_units>=0)", "UNIQUE(limit_id,period)", "UNIQUE(project_id,user_id)", "REFERENCES banking_model_spend_attempts(id)", "REFERENCES banking_model_user_windows(id)"} {
			if !strings.Contains(ddl, required) {
				t.Fatal(dialect, required)
			}
		}
	}
}
