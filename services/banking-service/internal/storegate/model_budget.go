package storegate

import (
	"fmt"
	"strings"
)

// ModelBudgetFences is owned by additive migration 018, not historical 011.
// The fixed table names prevent callers from installing arbitrary SQL triggers.
func ModelBudgetFences(dialect string) []string {
	var out []string
	for _, table := range []string{"banking_model_budget_windows", "banking_model_calls"} {
		if dialect == "postgres" {
			out = append(out, fmt.Sprintf(`CREATE OR REPLACE TRIGGER qyj_storage_fence BEFORE INSERT OR UPDATE OR DELETE ON "%s" FOR EACH ROW EXECUTE FUNCTION qyj_storage_fence()`, table))
		} else {
			for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
				name := "qyj_fence_" + strings.TrimPrefix(table, "banking_") + "_" + strings.ToLower(event)
				out = append(out, "DROP TRIGGER IF EXISTS `"+name+"`", mysqlTrigger(name, table, event))
			}
		}
	}
	return out
}
