package orchestrator

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/scenariooverview"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func stringID(id int64) string { return strconv.FormatInt(id, 10) }

// Financial numbers remain in the verified result, never in model prose. These
// rules deliberately reject rather than attempt to repair untrusted narration.
var chineseMoney = regexp.MustCompile(`[零〇一二两三四五六七八九十百千万亿]+\s*(元|分|块|人民币|成|%)`)

func validateExplanation(text string, ids []string, evidence []Evidence) error {
	invalid := func() error {
		return entity.Fail("MODEL_OUTPUT_INVALID", "qualitative explanation and real evidence references required")
	}
	if strings.TrimSpace(text) == "" || len(text) > 8192 || ids == nil || chineseMoney.MatchString(text) {
		return invalid()
	}
	for _, r := range text {
		if unicode.IsNumber(r) {
			return invalid()
		}
	}
	for _, token := range []string{"Bearer ", "confirmed=true", "api_key", "access_token", "已执行转账", "已经转账", "已取消订阅", "已完成扣款"} {
		if strings.Contains(text, token) {
			return invalid()
		}
	}
	allowed := map[string]bool{}
	for _, ev := range evidence {
		allowed[ev.ID] = true
	}
	if len(allowed) > 0 && len(ids) == 0 {
		return invalid()
	}
	used := map[string]bool{}
	for _, id := range ids {
		if !allowed[id] || used[id] {
			return invalid()
		}
		used[id] = true
	}
	return nil
}

// Explanations do not need raw merchant text, transaction remarks, source
// prompts or stored model narration. Keep these out of the model data boundary.
func explanationData(raw json.RawMessage) (json.RawMessage, error) {
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var clean func(any) any
	clean = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				switch k {
				case "text", "merchant", "merchant_name", "remarks", "remark", "memo", "note", "description", "explanation", "evidence", "user_id", "display_name", "masked_account_number", "external_account_id", "external_transaction_id":
					delete(x, k)
				default:
					x[k] = clean(val)
				}
			}
			return x
		case []any:
			for i := range x {
				x[i] = clean(x[i])
			}
			return x
		default:
			return v
		}
	}
	return json.Marshal(clean(v))
}

// Billing prose needs the deterministic summary, not the report's transaction
// snapshot or account metadata. The full report stays unchanged in persistence
// and in the API response. Citation identity travels separately in Evidence.
func billingExplanationData(raw json.RawMessage) (json.RawMessage, error) {
	var report map[string]json.RawMessage
	if json.Unmarshal(raw, &report) != nil {
		return nil, invalidOutput()
	}
	result := map[string]json.RawMessage{}
	for _, name := range []string{"currency", "total_income", "total_expense", "transaction_count"} {
		if len(report[name]) == 0 || string(report[name]) == "null" {
			return nil, invalidOutput()
		}
		result[name] = report[name]
	}
	var categories []struct {
		Category string `json:"category"`
		Expense  int64  `json:"expense_minor"`
	}
	if len(report["category_breakdown"]) == 0 || json.Unmarshal(report["category_breakdown"], &categories) != nil {
		return nil, invalidOutput()
	}
	result["category_breakdown"], _ = json.Marshal(categories)
	result["analysis_limitations"], _ = json.Marshal(coze.BillingExplanationLimitations())
	return json.Marshal(result)
}

// The qualitative summary is supported by the owned report as a whole. Sending
// every transaction and repeated audit ID exhausts the bounded model context
// for ordinary full months. The full evidence stays in AgentOutput and the
// persisted report; the model may cite only the report reference it receives.
func billingExplanationEvidence(evidence []Evidence) ([]Evidence, error) {
	reportID := ""
	for _, ev := range evidence {
		if ev.Type != "billing_report" {
			continue
		}
		if reportID != "" || ev.ID == "" {
			return nil, invalidOutput()
		}
		reportID = ev.ID
	}
	if reportID == "" {
		return nil, invalidOutput()
	}
	return []Evidence{{Type: "billing_report", ID: reportID}}, nil
}

// The Supervisor may select tools; persisted IDs for lifestyle tasks must come
// from trusted caller bindings, not a number invented by the model.
func validateAccountBindings(tasks []TaskSpec, b Bindings) error {
	for _, t := range tasks {
		if t.AgentType == "savings" {
			var p planning.IDInput
			_, e := planning.ID(b.FinancialPlanID)
			if e != nil || strictJSON(t.Parameters, &p) != nil || p.PlanID != b.FinancialPlanID {
				return entity.Fail("INVALID_TASK_PLAN", "savings parameters must match the selected financial plan binding")
			}
		}
		if t.Tool == "get_scenario_overview" {
			var p scenariooverview.Input
			if strictJSON(t.Parameters, &p) != nil || p.Validate() != nil || b.AccountID == "" || p.AccountID != b.AccountID {
				return entity.Fail("INVALID_TASK_PLAN", "scenario overview must match the selected account binding")
			}
		}
	}
	if b.AccountID == "" && b.From == "" && b.To == "" {
		return nil
	}
	for _, task := range tasks {
		// These registered tools intentionally cover all caller-owned subscriptions
		// and accept no account/period parameters. A selected task scope therefore
		// cannot use them; use scoped detection or a scoped scenario overview.
		if task.AgentType == "subscription" && (task.Tool == "analyze_subscription_cost" || task.Tool == "suggest_subscription_actions") {
			return entity.Fail("INVALID_TASK_PLAN", "global subscription tools cannot satisfy selected account or period bindings")
		}
		if task.AgentType == "supervisor" && task.Tool == "require_gateway" {
			proposal, err := parseOperationSuggestion(task.Parameters)
			if err != nil {
				return err
			}
			var parameters map[string]json.RawMessage
			_ = json.Unmarshal(proposal.Parameters, &parameters)
			if b.AccountID != "" {
				for _, field := range []string{"source_account_id", "account_id", "bank_account_id"} {
					if raw, ok := parameters[field]; ok {
						var id string
						if json.Unmarshal(raw, &id) != nil || id != b.AccountID {
							return entity.Fail("INVALID_TASK_PLAN", "operation source must match the selected account")
						}
					}
				}
			}
			continue
		}
		if task.AgentType != "billing" && task.Tool != "detect_subscriptions" {
			continue
		}
		var parameters map[string]json.RawMessage
		if json.Unmarshal(task.Parameters, &parameters) != nil {
			return entity.Fail("INVALID_TASK_PLAN", "invalid account parameters")
		}
		var account string
		if b.AccountID != "" && (json.Unmarshal(parameters["account_id"], &account) != nil || account != b.AccountID) {
			return entity.Fail("INVALID_TASK_PLAN", "model account must match the selected account binding")
		}
		if b.From != "" || b.To != "" {
			from, ef := time.Parse(time.RFC3339Nano, b.From)
			to, et := time.Parse(time.RFC3339Nano, b.To)
			var rawFrom, rawTo string
			pf, pt := json.Unmarshal(parameters["from"], &rawFrom), json.Unmarshal(parameters["to"], &rawTo)
			actualFrom, af := time.Parse(time.RFC3339Nano, rawFrom)
			actualTo, at := time.Parse(time.RFC3339Nano, rawTo)
			if ef != nil || et != nil || !from.Before(to) || pf != nil || pt != nil || af != nil || at != nil || !actualFrom.Equal(from) || !actualTo.Equal(to) {
				return entity.Fail("INVALID_TASK_PLAN", "model analysis period must match the selected interval binding")
			}
		}
	}
	return nil
}

func validateLifestyleBindings(tasks []TaskSpec, b Bindings) error {
	for _, t := range tasks {
		if t.AgentType != "lifestyle" {
			continue
		}
		invalid := func() error {
			return entity.Fail("INVALID_TASK_PLAN", "Lifestyle parameters must match trusted bindings")
		}
		if t.Tool == "compile_lifestyle_plan" {
			var p lifestyle.CompileToolInput
			if strictJSON(t.Parameters, &p) != nil || b.LifestyleGoalID == "" || b.LifestyleGoalVersion <= 0 || b.BudgetID == "" || b.BillingReportID == "" || b.FinancialPlanID == "" {
				return invalid()
			}
			if stringID(p.GoalID) != b.LifestyleGoalID || p.ExpectedVersion != b.LifestyleGoalVersion || stringID(p.BudgetID) != b.BudgetID || p.BillingReportID != b.BillingReportID || stringID(p.FinancialPlanID) != b.FinancialPlanID {
				return invalid()
			}
		} else {
			var p lifestyle.IDInput
			if strictJSON(t.Parameters, &p) != nil {
				return invalid()
			}
			if t.Tool == "get_lifestyle_goal" && (b.LifestyleGoalID == "" || stringID(p.GoalID) != b.LifestyleGoalID || p.PlanID != 0) {
				return invalid()
			}
			if t.Tool == "get_lifestyle_plan" && (b.LifestylePlanID == "" || stringID(p.PlanID) != b.LifestylePlanID || p.GoalID != 0) {
				return invalid()
			}
		}
	}
	return nil
}
