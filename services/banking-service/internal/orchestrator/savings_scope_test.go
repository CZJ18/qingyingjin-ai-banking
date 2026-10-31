package orchestrator

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestSavingsPlannerCannotInventOrOmitFinancialPlanBinding(t *testing.T) {
	for _, tool := range []string{"get_financial_plan", "compare_financial_plans", "replan_financial_plan"} {
		task := TaskSpec{AgentType: "savings", Tool: tool, Parameters: json.RawMessage(`{"plan_id":"7"}`)}
		if e := validateAccountBindings([]TaskSpec{task}, Bindings{FinancialPlanID: "7"}); e != nil {
			t.Fatal(e)
		}
		for _, b := range []Bindings{{}, {FinancialPlanID: "8"}, {FinancialPlanID: "07"}, {AccountID: "1"}} {
			if e := validateAccountBindings([]TaskSpec{task}, b); entity.Code(e) != "INVALID_TASK_PLAN" {
				t.Errorf("%s accepted missing or changed plan binding: %+v, %v", tool, b, e)
			}
		}
		task.Parameters = json.RawMessage(`{}`)
		if e := validateAccountBindings([]TaskSpec{task}, Bindings{FinancialPlanID: "7"}); entity.Code(e) != "INVALID_TASK_PLAN" {
			t.Errorf("%s accepted omitted plan", tool)
		}
	}
}
