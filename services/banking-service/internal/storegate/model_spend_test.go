package storegate

import (
	"strings"
	"testing"
)

func TestModelSpendFencesCoverAllTablesAndKeepEventsAppendOnly(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		sql := strings.Join(ModelSpendFences(dialect), ";\n")
		for _, table := range []string{"banking_model_spend_pools", "banking_model_spend_attempts", "banking_model_spend_events"} {
			if !strings.Contains(sql, table) {
				t.Fatal("missing monetary freeze boundary", dialect, table)
			}
		}
		if !strings.Contains(sql, "BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY") {
			t.Fatal("event mutations have no immutable fence", dialect)
		}
		if dialect == "postgres" {
			for _, required := range []string{"BEFORE TRUNCATE", "SECURITY DEFINER", "search_path TO pg_catalog", "FOR SHARE", "session_user", "REVOKE ALL"} {
				if !strings.Contains(sql, required) {
					t.Fatal("missing PostgreSQL protection", required)
				}
			}
		} else if strings.Count(sql, "BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY") != 2 {
			t.Fatal("MySQL events need unconditional UPDATE and DELETE denial")
		}
	}
}
