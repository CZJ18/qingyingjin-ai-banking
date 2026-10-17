// Package storage contains explicit, offline financial-database migration tools.
// It is never called from an Agent or an HTTP handler.
package storage

// Tables is the foreign-key insertion order of business tables, including
// schema 018's model reservations. Schema history/control metadata are excluded.
// This fixed allowlist prevents accidentally migrating the Coze/product schemas.
var Tables = []string{
	"banking_users", "banking_bank_accounts", "banking_transactions",
	"banking_ledger_entries", "banking_core_audits", "banking_core_idempotency",
	"banking_transfer_intents", "banking_transfer_approvals", "banking_budgets",
	"banking_budget_categories", "banking_budget_rules", "banking_savings_goals",
	"banking_savings_plans", "banking_savings_ledgers", "banking_life_goals",
	"banking_goal_versions", "banking_goal_constraints", "banking_task_plans",
	"banking_task_dependencies", "banking_billing_reports", "banking_subscriptions",
	"banking_subscription_payments", "banking_subscription_analyses",
	"banking_subscription_action_intents", "banking_subscription_approvals",
	"banking_financial_plans", "banking_replan_events", "banking_plan_versions",
	"banking_plan_constraints", "banking_plan_decisions", "banking_agent_jobs",
	"banking_agent_tasks", "banking_agent_dependencies", "banking_agent_executions",
	"banking_tool_invocations", "banking_execution_traces", "banking_operation_intents",
	"banking_user_authorizations", "banking_risk_rules", "banking_risk_events",
	"banking_execution_audits", "banking_lifestyle_goals", "banking_lifestyle_goal_versions",
	"banking_lifestyle_plans", "banking_lifestyle_recommendations", "banking_lifestyle_decisions",
	"banking_model_budget_windows", "banking_model_calls",
}

func primaryKey(table string) []string {
	switch table {
	case "banking_core_idempotency":
		return []string{"user_id", "action", "idem_key"}
	case "banking_transfer_approvals":
		return []string{"transfer_id"}
	case "banking_risk_rules":
		return []string{"name"}
	default:
		return []string{"id"}
	}
}

type TableDigest struct {
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

type Check struct {
	Name       string `json:"name"`
	Violations int64  `json:"violations"`
}

// Report deliberately contains aggregate metadata only. It is safe to archive;
// row contents, account numbers, tokens, authorization nonces and DSNs are absent.
type Report struct {
	Passed         bool          `json:"passed"`
	Replayed       bool          `json:"replayed"`
	SourceDialect  string        `json:"source_dialect"`
	TargetDialect  string        `json:"target_dialect"`
	Source         []TableDigest `json:"source"`
	Target         []TableDigest `json:"target"`
	SourceChecks   []Check       `json:"source_checks"`
	TargetChecks   []Check       `json:"target_checks"`
	SourceSequence int64         `json:"reserved_source_mutation_seq"`
	TargetSequence int64         `json:"reserved_target_mutation_seq"`
	Digest         string        `json:"snapshot_digest"`
}
