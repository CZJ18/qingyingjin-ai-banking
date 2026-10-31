package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

// Explicit protocol fixture only. No external provider, SQL, credentials or fees.
func TestDirectLifestyleNativeResultAndRequiredLimitationRoundTrip(t *testing.T) {
	snapshot := lifestyle.Snapshot{Goal: lifestyle.Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 1, ExpenseReductionMonthlyMinor: 50000, AdjustableCategories: []string{"sports"}}, BudgetMinor: 10000, Categories: []lifestyle.Category{{Name: "sports", AllocatedMinor: 10000, ObservedMinor: 6000, EssentialReserveMinor: 1000}}, CompleteMonth: false, EvidenceTransactionIDs: []string{"synthetic-tx"}}
	calculated, err := lifestyle.Calculate(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapJSON, _ := json.Marshal(snapshot)
	resultJSON, _ := json.Marshal(calculated)
	native := lifestyle.PlanView{Plan: lifestyle.Plan{Base: entity.Base{ID: 7, UserID: 42}, GoalID: 1, GoalVersion: 1, SnapshotJSON: snapJSON, ResultJSON: resultJSON}, Recommendations: []lifestyle.Recommendation{}}
	nativeJSON, _ := json.Marshal(native)
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v2/lifestyle-plans/7" {
			t.Error("unexpected native tool", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": native})
	}))
	defer bank.Close()
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"correct", `{"finding_ids":["goal.reduction_target","plan.missing_information"]}`, true},
		{"omitted_limitation", `{"finding_ids":["goal.reduction_target"]}`, false},
		{"invented_action", `{"finding_ids":["plan.missing_information","plan.action.fake"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Banking-Authorization") == "" || strings.Contains(string(body), "synthetic-tx") || strings.Contains(string(body), "task_id") {
					t.Error("direct gateway or privacy binding lost")
				}
				json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-local-only", "model": "synthetic-test", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": tc.content}}}})
			}))
			defer gateway.Close()
			runner, e := coze.NewDirectRunner(gateway.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			x, e := NewHTTPExecutor(bank.URL, executorAuth(), runner, map[string]string{"lifestyle": coze.DirectLifestyleExplanationWorkflow}, nil, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			c := billingCall()
			c.AgentType, c.Tool, c.Parameters, c.UseCoze = "lifestyle", "get_lifestyle_plan", json.RawMessage(`{"plan_id":"7"}`), true
			out, e := x.Run(context.Background(), c)
			if calls != 1 {
				t.Fatal("unexpected mock call count", calls)
			}
			if !tc.valid {
				if e == nil {
					t.Fatal("unsafe explanation accepted")
				}
				return
			}
			if e != nil || out.FinancialExecutionEnabled || out.WorkflowExecuteID != "direct_provider:synthetic-local-only" || !strings.Contains(out.Explanation, "500.00元") || !strings.Contains(out.Explanation, "需先补充信息") {
				t.Fatal(out, e)
			}
			var actual lifestyle.PlanView
			if json.Unmarshal(out.Result, &actual) != nil {
				t.Fatal("invalid native result")
			}
			actualJSON, _ := json.Marshal(actual)
			if string(actualJSON) != string(nativeJSON) {
				t.Fatal("model changed native facts")
			}
		})
	}
}
