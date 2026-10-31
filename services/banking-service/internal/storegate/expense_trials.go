package storegate

import "strings"

// Reuse the same storage authority, not an independent execution mechanism.
func ExpenseTrialMySQLFence(event string) string {
	return mysqlTrigger("qyj_fence_expense_trials_"+strings.ToLower(event), "banking_expense_trials", event)
}
