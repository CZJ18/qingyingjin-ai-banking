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
)

type directExplanationFixture struct{}

func (*directExplanationFixture) DirectProvider() bool { return true }
func (*directExplanationFixture) RunSyncWorkflow(context.Context, coze.RunRequest) (coze.RunResponse, error) {
	panic("configuration must not dispatch")
}

func TestDirectExplanationRetainsDeterministicResultAndEmptyReportEvidence(t *testing.T) {
	report := billingFixture(t)
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": report})
	}))
	defer bank.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-only-explanation", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": `{"explanation":"当前范围内没有交易记录，无法据此判断实际消费情况。","evidence_ids":["billing_report"]}`}, "finish_reason": "stop"}}})
	}))
	defer gateway.Close()
	runner, err := coze.NewDirectRunner(gateway.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewHTTPExecutor(bank.URL, executorAuth(), runner, map[string]string{"billing": coze.DirectBillingExplanationWorkflow}, nil, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	call := billingCall()
	call.UseCoze = true
	out, err := executor.Run(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if out.Explanation == "" || out.FinancialExecutionEnabled || len(out.ExplanationEvidenceIDs) != 1 || out.ExplanationEvidenceIDs[0] != report.ID {
		t.Fatal(out)
	}
	var actual map[string]any
	if json.Unmarshal(out.Result, &actual) != nil || actual["report_id"] != report.ID || actual["total_expense"] != float64(report.TotalExpense) {
		t.Fatal("model replaced deterministic result")
	}
}

func TestDirectExplanationBindingRequiresExactBillingRole(t *testing.T) {
	for _, tc := range []struct {
		role, id string
		valid    bool
	}{
		{"billing", "direct_provider_billing_explanation", true},
		{"subscription", "direct_provider_billing_explanation", false},
		{"billing", "123", false},
		{"billing", "direct_provider", false},
		{"lifestyle", "direct_provider_lifestyle_explanation", true},
		{"billing", "direct_provider_lifestyle_explanation", false},
		{"lifestyle", "123", false},
	} {
		_, err := NewHTTPExecutor("http://127.0.0.1:8090", executorAuth(), &directExplanationFixture{}, map[string]string{tc.role: tc.id}, nil, outbound.TestPolicy())
		if (err == nil) != tc.valid {
			t.Errorf("%s/%s: %v", tc.role, tc.id, err)
		}
	}
}
