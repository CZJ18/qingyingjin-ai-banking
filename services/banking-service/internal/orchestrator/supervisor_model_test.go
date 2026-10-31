package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/service"
)

func TestSupervisorPreservesExplicitSpecialistModelRequest(t *testing.T) {
	bank := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("planning must not run business tools") }))
	defer bank.Close()
	workflow := workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
		return workflowResult(map[string]any{"tasks": []any{map[string]any{
			"key": "billing_1", "agent_type": "billing", "tool": "get_account_summary",
			"skill_id": "billing_analysis", "skill_version": 1, "execution_mode": "read_only",
			"use_coze": true, "parameters": map[string]any{"account_id": "42", "from": "2026-10-01T00:00:00Z", "to": "2026-11-01T00:00:00Z"},
			"dependencies": []string{}, "priority": 100,
		}}}), nil
	})
	x, err := NewHTTPExecutor(bank.URL, executorAuth(), workflow, map[string]string{"supervisor": "42"}, nil, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	tasks, _, err := x.PlanWithEvidence(context.Background(), service.Meta{UserID: 42, TaskID: "lg-fixture.supervisor_plan"}, "分析本月账单并解释结果", Bindings{AccountID: "42"})
	if err != nil {
		t.Fatalf("valid model plan lost specialist model flag: %v", err)
	}
	raw, err := json.Marshal(tasks)
	if err != nil || !strings.Contains(string(raw), `"use_coze":true`) {
		t.Fatalf("explicit model request not preserved: %s %v", raw, err)
	}
}

func TestSupervisorCannotExpandBoundAccountScope(t *testing.T) {
	for _, account := range []string{"99", ""} {
		t.Run("account_"+account, func(t *testing.T) {
			bank := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("planning must not execute tools") }))
			defer bank.Close()
			workflow := workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
				return workflowResult(map[string]any{"tasks": []any{map[string]any{
					"key": "billing", "agent_type": "billing", "tool": "get_account_summary",
					"parameters": map[string]any{"account_id": account},
				}}}), nil
			})
			x, err := NewHTTPExecutor(bank.URL, executorAuth(), workflow, map[string]string{"supervisor": "42"}, nil, outbound.TestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = x.PlanWithEvidence(context.Background(), service.Meta{UserID: 42, TaskID: "bound-scope"}, "分析选中账户", Bindings{AccountID: "42"})
			if err == nil {
				t.Fatal("model expanded the selected account scope")
			}
		})
	}
}
