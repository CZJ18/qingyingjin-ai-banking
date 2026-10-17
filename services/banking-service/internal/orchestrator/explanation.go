package orchestrator

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"regexp"
	"strconv"
	"strings"
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

// The Supervisor may select tools; persisted IDs for lifestyle tasks must come
// from trusted caller bindings, not a number invented by the model.
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
