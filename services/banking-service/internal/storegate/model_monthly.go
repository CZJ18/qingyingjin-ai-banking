package storegate

import (
	"fmt"
	"strings"
)

// Additive 021 fences; historical migration checksums are unchanged.
func ModelMonthlyFences(dialect string) []string {
	var out []string
	for _, table := range []string{"banking_model_user_limits", "banking_model_user_windows", "banking_model_attempt_owners"} {
		immutable := table != "banking_model_user_windows"
		if dialect == "postgres" {
			fn := "qyj_storage_fence"
			if immutable {
				fn = "qyj_model_spend_events_fence"
			}
			out = append(out, fmt.Sprintf(`CREATE TRIGGER qyj_storage_fence BEFORE INSERT OR UPDATE OR DELETE ON public.%s FOR EACH ROW EXECUTE FUNCTION public.%s()`, table, fn))
			if immutable {
				out = append(out, fmt.Sprintf(`CREATE TRIGGER qyj_monthly_no_truncate BEFORE TRUNCATE ON public.%s FOR EACH STATEMENT EXECUTE FUNCTION public.qyj_model_spend_events_fence()`, table))
			}
		} else {
			for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
				name := "qyj_fence_" + strings.TrimPrefix(table, "banking_") + "_" + strings.ToLower(event)
				statement := mysqlTrigger(name, table, event)
				if immutable && event != "INSERT" {
					statement = fmt.Sprintf("CREATE TRIGGER `%s` BEFORE %s ON %s FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='BANKING_MODEL_MONTHLY_APPEND_ONLY'", name, event, table)
				}
				out = append(out, "DROP TRIGGER IF EXISTS `"+name+"`", statement)
			}
		}
	}
	if dialect == "postgres" {
		// Reuse the reviewed permission revocation logic with only the new tables.
		permissions := strings.NewReplacer("banking_model_spend_pools", "banking_model_user_limits", "banking_model_spend_attempts", "banking_model_user_windows", "banking_model_spend_events", "banking_model_attempt_owners").Replace(modelSpendPostgresPermissions)
		out = append(out, permissions)
	}
	return out
}
