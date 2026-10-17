package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"strings"
	"testing"
)

func TestLifestyleConclusionsAreDerivedFromActualVerifiedPlan(t *testing.T) {
	snap := lifestyle.Snapshot{Goal: lifestyle.Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 100000, AdjustableCategories: []string{"shopping"}, ProtectedCategories: []string{}, ProhibitedOperations: []string{}}, BudgetMinor: 200000, CompleteMonth: true, Categories: []lifestyle.Category{{Name: "dining", AllocatedMinor: 80000, EssentialReserveMinor: 80000, ObservedMinor: 70000}, {Name: "shopping", AllocatedMinor: 120000, ObservedMinor: 60000}}, EvidenceTransactionIDs: []string{"actual-tx"}}
	result, e := lifestyle.Calculate(snap)
	if e != nil {
		t.Fatal(e)
	}
	sr, _ := json.Marshal(snap)
	rr, _ := json.Marshal(result)
	raw, _ := json.Marshal(lifestyle.PlanView{Plan: lifestyle.Plan{Base: entity.Base{ID: 7, UserID: 42}, SnapshotJSON: sr, ResultJSON: rr}, Recommendations: []lifestyle.Recommendation{}})
	out := AgentOutput{Result: raw, Evidence: []Evidence{{Type: "plan", ID: "7"}, {Type: "transaction", ID: "actual-tx"}}}
	c := Call{AgentType: "lifestyle", TaskID: "task", Tool: "get_lifestyle_plan"}
	findings, e := lifestyleFindings(c, &out)
	if e != nil {
		t.Fatal(e)
	}
	if e = renderLifestyleFindings([]string{"plan.conflict", "plan.necessary_expenses", "plan.historical_evidence"}, findings, &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.Explanation, "实际历史交易") || strings.Contains(out.Explanation, "没有交易") || len(out.ExplanationEvidenceIDs) != 2 {
		t.Fatal(out)
	}
	for _, ids := range [][]string{{"plan.no_transactions"}, {"plan.conflict", "plan.conflict"}, {}} {
		if e = renderLifestyleFindings(ids, findings, &out); entity.Code(e) != "MODEL_OUTPUT_INVALID" {
			t.Fatal(ids, e)
		}
	}
	withoutSource := AgentOutput{Result: raw, Evidence: []Evidence{{ID: "7"}}}
	if e = renderLifestyleFindings([]string{"plan.historical_evidence"}, findings, &withoutSource); entity.Code(e) != "TOOL_OUTPUT_INVALID" {
		t.Fatal(e)
	}
	for _, malicious := range []bool{false, true} {
		runner := workflowFixture(func(_ context.Context, r coze.RunRequest) (coze.RunResponse, error) {
			if strings.Contains(r.Parameters["input_string"].(string), "Bearer") {
				t.Fatal("credentials exposed")
			}
			value := map[string]any{"task_id": "task", "agent_type": "lifestyle", "finding_ids": []string{"plan.conflict", "plan.necessary_expenses"}}
			if malicious {
				value["explanation"] = "没有交易记录"
			}
			return workflowResult(value), nil
		})
		x, _ := NewHTTPExecutor("http://127.0.0.1:8090", executorAuth(), runner, map[string]string{"lifestyle": "123"}, nil)
		actual := AgentOutput{Result: raw, Evidence: out.Evidence}
		e = x.explain(context.Background(), c, &actual)
		if malicious {
			if entity.Code(e) != "MODEL_OUTPUT_INVALID" {
				t.Fatal(e)
			}
		} else if e != nil || actual.WorkflowExecuteID != "fixture-execute" || len(actual.ExplanationFindingIDs) != 2 {
			t.Fatal(actual, e)
		}
	}
}
