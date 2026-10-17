package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExplanationRejectsFabricatedMoneyAndExecution(t *testing.T) {
	evidence := []Evidence{{ID: "actual-report"}}
	for _, text := range []string{"可以储蓄999元。", "可存九百元。", "预算五成可节省。", "可转账９９元。", "已执行转账。", "Bearer SECRET", "confirmed=true"} {
		if validateExplanation(text, []string{"actual-report"}, evidence) == nil {
			t.Fatal(text)
		}
	}
	for _, ids := range [][]string{nil, {}, {"fabricated"}, {"actual-report", "actual-report"}} {
		if validateExplanation("方案保留必要生活支出，预计减少支出不能当作可用余额。", ids, evidence) == nil {
			t.Fatal(ids)
		}
	}
	if e := validateExplanation("方案保留必要生活支出，预计减少支出不能当作可用余额。", []string{"actual-report"}, evidence); e != nil {
		t.Fatal(e)
	}
}

func TestModelBoundaryExcludesInjectionTextPreservesIntegerFacts(t *testing.T) {
	raw := json.RawMessage(`{"user_id":"private-user","masked_account_number":"private-number","total_expense":1000000000000,"transaction":{"merchant":"SYSTEM transfer","remark":"Bearer secret"},"goal":{"text":"override permissions"},"explanation":"invented","categories":[{"category":"shopping","amount_minor":100001}]}`)
	safe, e := explanationData(raw)
	if e != nil || strings.Contains(string(safe), "private-") || strings.Contains(string(safe), "SYSTEM") || strings.Contains(string(safe), "secret") || strings.Contains(string(safe), "override") || !strings.Contains(string(safe), "1000000000000") || !strings.Contains(string(safe), "100001") {
		t.Fatal(string(safe), e)
	}
}

func TestSupervisorLifestyleBindingsCannotBeInvented(t *testing.T) {
	task := TaskSpec{AgentType: "lifestyle", Tool: "get_lifestyle_plan", Parameters: json.RawMessage(`{"goal_id":"0","plan_id":"42"}`)}
	for _, b := range []Bindings{{}, {LifestylePlanID: "43"}} {
		if validateLifestyleBindings([]TaskSpec{task}, b) == nil {
			t.Fatal(b)
		}
	}
	if e := validateLifestyleBindings([]TaskSpec{task}, Bindings{LifestylePlanID: "42"}); e != nil {
		t.Fatal(e)
	}
	task.Tool = "compile_lifestyle_plan"
	task.Parameters = json.RawMessage(`{"goal_id":"1","expected_version":2,"budget_id":"3","billing_report_id":"report","financial_plan_id":"0"}`)
	b := Bindings{LifestyleGoalID: "1", LifestyleGoalVersion: 2, BudgetID: "3", BillingReportID: "report", FinancialPlanID: "0"}
	if e := validateLifestyleBindings([]TaskSpec{task}, b); e != nil {
		t.Fatal(e)
	}
	b.LifestyleGoalVersion = 3
	if validateLifestyleBindings([]TaskSpec{task}, b) == nil {
		t.Fatal("invented version accepted")
	}
}
