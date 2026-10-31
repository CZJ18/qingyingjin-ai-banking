package orchestrator

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
)

func TestAllBillingToolsDiscloseOnlySummaryForExplanation(t *testing.T) {
	// Synthetic detail must stay in the returned report, outside model input.
	raw := json.RawMessage(`{"currency":"CNY","total_income":500000,"total_expense":12345,"transaction_count":1,"category_breakdown":[{"category":"shopping","expense_minor":12345,"transaction_ids":["private-row"]}],"transactions":[{"transaction_id":"private-row","account_id":"private-account","occurred_at":1790000000000,"amount_minor":12345,"merchant":"private-merchant"}],"accounts":[{"id":"private-account","bank_name":"private-bank","balance_minor":987654}],"analysis_period":{"from":"2026-09-01T00:00:00+08:00","to":"2026-10-01T00:00:00+08:00","timezone":"Asia/Shanghai"},"report_id":"private-report"}`)
	allowed := map[string]bool{"currency": true, "total_income": true, "total_expense": true, "transaction_count": true, "category_breakdown": true, "analysis_limitations": true}
	for _, tool := range billing.Tools {
		t.Run(tool, func(t *testing.T) {
			var captured string
			runner := workflowFixture(func(_ context.Context, request coze.RunRequest) (coze.RunResponse, error) {
				captured = request.Parameters["input_string"].(string)
				return workflowResult(map[string]any{"task_id": "synthetic-task", "agent_type": "billing", "explanation": "已记录消费可供用户观察比较，账单覆盖完整性未经核验。", "evidence_ids": []string{"synthetic-report"}}), nil
			})
			x := HTTPExecutor{coze: runner, workflows: map[string]string{"billing": "123"}}
			out := AgentOutput{Result: append(json.RawMessage{}, raw...), Evidence: []Evidence{{Type: "billing_report", ID: "synthetic-report"}}}
			if err := x.explain(context.Background(), Call{TaskID: "synthetic-task", AgentType: "billing", Tool: tool}, &out); err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Result map[string]json.RawMessage `json:"authoritative_result"`
			}
			if json.Unmarshal([]byte(captured), &payload) != nil || len(payload.Result) != len(allowed) {
				t.Fatalf("billing tool sent detail instead of the approved summary: %s", captured)
			}
			for key := range payload.Result {
				if !allowed[key] {
					t.Fatalf("unexpected model input field %q", key)
				}
			}
			if string(payload.Result["total_expense"]) != "12345" || string(payload.Result["category_breakdown"]) != `[{"category":"shopping","expense_minor":12345}]` {
				t.Fatalf("summary changed deterministic amounts or retained transaction detail: %s", captured)
			}
			if !reflect.DeepEqual(out.Result, raw) {
				t.Fatal("minimizing model input changed the authoritative report")
			}
		})
	}
}

func TestAllBillingToolsRefuseExplanationWithoutSummary(t *testing.T) {
	for _, tool := range billing.Tools {
		t.Run(tool, func(t *testing.T) {
			calls := 0
			x := HTTPExecutor{coze: workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
				calls++
				return workflowResult(map[string]any{"task_id": "synthetic-task", "agent_type": "billing", "explanation": "已记录消费可供观察。", "evidence_ids": []string{"synthetic-report"}}), nil
			}), workflows: map[string]string{"billing": "123"}}
			out := AgentOutput{Result: json.RawMessage(`{"currency":"CNY","transactions":[{"amount_minor":12345}]}`), Evidence: []Evidence{{ID: "synthetic-report"}}}
			err := x.explain(context.Background(), Call{TaskID: "synthetic-task", AgentType: "billing", Tool: tool}, &out)
			if entity.Code(err) != "TOOL_OUTPUT_INVALID" || calls != 0 {
				t.Fatalf("incomplete summary reached model boundary: calls=%d err=%v", calls, err)
			}
		})
	}
}
