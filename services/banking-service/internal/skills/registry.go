package skills

import "github.com/qingyingjin/banking-service/internal/entity"

func definition(id, owner, description, entry string, pairs ...Pair) Definition {
	return Definition{ID: id, Version: 1, Owner: owner, Description: description, InternalEntry: entry, AllowedPairs: pairs, RequiredEvidence: []string{"task_id", "audit_id"}, RuleVersion: "1"}
}
func reads(agent string, tools ...string) []Pair {
	out := make([]Pair, 0, len(tools))
	for _, t := range tools {
		out = append(out, Pair{agent, t, "read_only"})
	}
	return out
}

var definitions = []Definition{
	definition("goal_compilation", "supervisor", "Compile structured goals using the configured Coze parser", "goal.parse"),
	definition("task_planning", "supervisor", "Plan a bounded supervisory dependency graph", "", Pair{"supervisor", "plan_instruction", "read_only"}, Pair{"supervisor", "request_confirmation", "user_confirmation"}),
	definition("billing_analysis", "billing", "Deterministic spending analysis with transaction evidence", "", reads("billing", "get_transactions", "get_account_summary", "analyze_spending", "compare_budget", "detect_anomalies", "generate_billing_report")...),
	definition("subscription_analysis", "subscription", "Evidence-backed recurring payment analysis", "", reads("subscription", "detect_subscriptions", "get_subscription_details", "analyze_subscription_cost", "suggest_subscription_actions", "get_subscription_status")...),
	definition("savings_planning", "savings", "Deterministic cash-flow planning, without execution", "", Pair{"savings", "get_financial_plan", "read_only"}, Pair{"savings", "compare_financial_plans", "read_only"}, Pair{"savings", "replan_financial_plan", "advice"}),
	definition("operation_preparation", "gateway", "Prepare proposals; authorization and execution remain separate", "execution.prepare", Pair{"subscription", "create_cancel_intent", "intent"}, Pair{"supervisor", "require_gateway", "intent"}),
	definition("lifestyle_planning", "lifestyle", "Evidence-backed normal-living budgets, without financial execution", "", Pair{"lifestyle", "get_lifestyle_goal", "read_only"}, Pair{"lifestyle", "get_lifestyle_plan", "read_only"}, Pair{"lifestyle", "compile_lifestyle_plan", "advice"}),
}

// Resolve only falls back for requests omitting BOTH version fields.
func Resolve(r Request) (Binding, error) {
	legacy := r.SkillID == "" && r.SkillVersion == 0
	for _, d := range definitions {
		if !legacy && (r.SkillID != d.ID || r.SkillVersion != d.Version) {
			continue
		}
		for _, p := range d.AllowedPairs {
			if r.Agent == p.Agent && r.Tool == p.Tool && (r.Mode == "" || r.Mode == p.Mode) {
				return Binding{d.ID, d.Version, p.Agent, p.Tool, p.Mode}, nil
			}
		}
	}
	return Binding{}, entity.Fail("INVALID_TASK_PLAN", "unknown or unauthorized skill, version, role, tool or execution mode")
}
func Catalog() []Definition {
	out := make([]Definition, len(definitions))
	for i, d := range definitions {
		out[i] = d
		out[i].AllowedPairs = append([]Pair{}, d.AllowedPairs...)
		out[i].RequiredEvidence = append([]string{}, d.RequiredEvidence...)
	}
	return out
}

// Internal is for service-owned entrypoints, never externally submitted tools.
func Internal(entry string) Definition {
	for _, d := range Catalog() {
		if d.InternalEntry == entry {
			return d
		}
	}
	panic("unregistered internal skill entry")
}
