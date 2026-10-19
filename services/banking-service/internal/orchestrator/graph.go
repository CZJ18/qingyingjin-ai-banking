package orchestrator

import (
	"bytes"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/skills"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
	"regexp"
	"strconv"
)

type ToolPolicy struct {
	Agent string
	Mode  string
}

func Lookup(tool string) (ToolPolicy, bool) {
	for _, d := range skills.Catalog() {
		for _, p := range d.AllowedPairs {
			if p.Tool == tool {
				return ToolPolicy{p.Agent, p.Mode}, true
			}
		}
	}
	return ToolPolicy{}, false
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func ValidateTasks(tasks []TaskSpec) ([]TaskSpec, error) {
	if len(tasks) == 0 || len(tasks) > 32 {
		return nil, entity.Fail("INVALID_TASK_PLAN", "1..32 tasks required")
	}
	seen := map[string]bool{}
	out := append([]TaskSpec{}, tasks...)
	for i := range out {
		t := &out[i]
		binding, err := skills.Resolve(skills.Request{SkillID: t.SkillID, SkillVersion: t.SkillVersion, Agent: t.AgentType, Tool: t.Tool, Mode: t.ExecutionMode})
		if err != nil {
			return nil, err
		}
		if !keyPattern.MatchString(t.Key) || seen[t.Key] || t.Priority < 0 || t.Priority > 1000 {
			return nil, entity.Fail("INVALID_TASK_PLAN", "invalid key or priority")
		}
		seen[t.Key] = true
		t.SkillID, t.SkillVersion, t.ExecutionMode = binding.SkillID, binding.SkillVersion, binding.Mode
		if t.Dependencies == nil {
			t.Dependencies = []string{}
		}
		if len(t.Parameters) == 0 {
			t.Parameters = json.RawMessage(`{}`)
		}
		if e := ValidateParameters(t.Tool, t.Parameters, false); e != nil {
			return nil, e
		}
	}
	indegree := map[string]int{}
	children := map[string][]string{}
	for _, t := range out {
		deps := map[string]bool{}
		for _, dep := range t.Dependencies {
			if !seen[dep] || dep == t.Key || deps[dep] {
				return nil, entity.Fail("INVALID_TASK_PLAN", "missing or repeated dependency")
			}
			deps[dep] = true
			indegree[t.Key]++
			children[dep] = append(children[dep], t.Key)
		}
	}
	q := []string{}
	for _, t := range out {
		if indegree[t.Key] == 0 {
			q = append(q, t.Key)
		}
	}
	count := 0
	for len(q) > 0 {
		k := q[0]
		q = q[1:]
		count++
		for _, child := range children[k] {
			indegree[child]--
			if indegree[child] == 0 {
				q = append(q, child)
			}
		}
	}
	if count != len(out) {
		return nil, entity.Fail("INVALID_TASK_PLAN", "dependency cycle rejected")
	}
	return out, nil
}
func strictJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return entity.Fail("INVALID_ARGUMENT", "invalid structured tool parameters")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return entity.Fail("INVALID_ARGUMENT", "one JSON value required")
	}
	return nil
}
func ValidateParameters(tool string, raw json.RawMessage, complete bool) error {
	if len(raw) > 65536 || len(raw) == 0 || raw[0] != '{' {
		return entity.Fail("INVALID_ARGUMENT", "bounded object parameters required")
	}
	p, ok := Lookup(tool)
	if !ok {
		return entity.Fail("INVALID_TASK_PLAN", "unknown tool")
	}
	switch p.Agent {
	case "lifestyle":
		if tool == "compile_lifestyle_plan" {
			var in lifestyle.CompileToolInput
			if e := strictJSON(raw, &in); e != nil {
				return e
			}
			if in.GoalID > 0 && in.ExpectedVersion > 0 && in.BudgetID >= 0 && in.FinancialPlanID >= 0 && len(in.BillingReportID) <= 64 {
				return nil
			}
			if !complete && in.GoalID == 0 && in.ExpectedVersion == 0 {
				return nil
			}
			return entity.Fail("INPUT_REQUIRED", "owned goal, version and bounded source bindings required")
		}
		var in lifestyle.IDInput
		if e := strictJSON(raw, &in); e != nil {
			return e
		}
		if tool == "get_lifestyle_goal" && in.GoalID > 0 && in.PlanID == 0 || tool == "get_lifestyle_plan" && in.PlanID > 0 && in.GoalID == 0 {
			return nil
		}
		if !complete && in.GoalID == 0 && in.PlanID == 0 {
			return nil
		}
		return entity.Fail("INPUT_REQUIRED", "bind exactly one owned lifestyle goal or plan")
	case "billing":
		var in billing.Input
		if e := strictJSON(raw, &in); e != nil {
			return e
		}
		if complete {
			_, e := billing.Normalize(in)
			if e != nil {
				return entity.Fail("INPUT_REQUIRED", "billing period/account parameters required")
			}
		}
	case "subscription":
		if tool == "create_cancel_intent" {
			var in subscription.IntentInput
			if e := strictJSON(raw, &in); e != nil {
				return e
			}
			if in.SubscriptionID != "" {
				if _, e := planning.ID(in.SubscriptionID); e != nil {
					return e
				}
			}
			if complete && (in.SubscriptionID == "" || in.ExpectedVersion <= 0) {
				return entity.Fail("INPUT_REQUIRED", "subscription ID and adopted version required")
			}
			return nil
		}
		if tool == "get_subscription_details" || tool == "get_subscription_status" {
			var in subscription.IDInput
			if e := strictJSON(raw, &in); e != nil {
				return e
			}
			if in.SubscriptionID == "" {
				if complete {
					return entity.Fail("INPUT_REQUIRED", "subscription ID required")
				}
			} else {
				_, e := planning.ID(in.SubscriptionID)
				return e
			}
			return nil
		}
		if tool == "detect_subscriptions" {
			var in subscription.DetectInput
			if e := strictJSON(raw, &in); e != nil {
				return e
			}
			// The subscription service owns the bounded default three-month
			// window and validates any supplied dates and account ownership.
			// Keep the same DTO contract for user and delegated read calls.
		} else {
			var in struct{}
			return strictJSON(raw, &in)
		}
	case "savings":
		var in planning.IDInput
		if e := strictJSON(raw, &in); e != nil {
			return e
		}
		if in.PlanID == "" {
			if complete {
				return entity.Fail("INPUT_REQUIRED", "financial plan binding required")
			}
		} else {
			_, e := planning.ID(in.PlanID)
			return e
		}
	case "supervisor":
		if tool == "plan_instruction" {
			var in struct {
				Instruction string   `json:"instruction"`
				Bindings    Bindings `json:"bindings"`
			}
			if e := strictJSON(raw, &in); e != nil {
				return e
			}
			if len(in.Instruction) == 0 || len(in.Instruction) > 8000 {
				return entity.Fail("INVALID_ARGUMENT", "bounded instruction required")
			}
			return nil
		}
		var in struct{}
		return strictJSON(raw, &in)
	}
	return nil
}
func ValidateOutput(call Call, out AgentOutput) error {
	if out.TaskID != call.TaskID || out.AgentType != call.AgentType || out.Status != "succeeded" || out.FinancialExecutionEnabled || len(out.Result) == 0 || !json.Valid(out.Result) || out.Evidence == nil || out.Errors == nil || out.NextActions == nil || len(out.Errors) != 0 || len(out.NextActions) > 8 {
		return entity.Fail("TOOL_RESULT_INVALID", "structured agent output violates contract")
	}
	for _, e := range out.Evidence {
		if e.ID == "" || len(e.ID) > 128 {
			return entity.Fail("TOOL_RESULT_INVALID", "invalid evidence identity")
		}
	}
	for _, a := range out.NextActions {
		if a.Type == "confirm_cancel_intent" && call.Tool == "create_cancel_intent" && call.Confirmed && a.RequiresConfirmation {
			continue
		}
		if a.Type != "verify_subscriptions" && a.Type != "review_required" {
			return entity.Fail("TOOL_RESULT_INVALID", "agent action cannot escalate privileges")
		}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(out.Result, &object) != nil || object == nil {
		return entity.Fail("TOOL_RESULT_INVALID", "object result required")
	}
	if v := object["user_id"]; len(v) > 0 {
		var user string
		if json.Unmarshal(v, &user) != nil || user != strconv.FormatInt(call.Meta.UserID, 10) {
			return entity.Fail("FORBIDDEN", "tool result owner mismatch")
		}
	}
	return nil
}
