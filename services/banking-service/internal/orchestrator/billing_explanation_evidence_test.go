package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

// A complete synthetic month must not disclose every row/audit ID to the
// qualitative model. The persisted report remains the source for all rows.
func TestBillingExplanationMonthEvidenceStaysInGoReport(t *testing.T) {
	const reportID = "synthetic-owned-billing-report"
	raw := json.RawMessage(`{"currency":"CNY","total_income":500000,"total_expense":390000,"transaction_count":61,"category_breakdown":[{"category":"shopping","expense_minor":300000},{"category":"dining","expense_minor":90000}],"snapshot":{"merchant":"PRIVATE-SOURCE-ONLY"}}`)
	evidence := []Evidence{{Type: "billing_report", ID: reportID, AuditID: "PRIVATE-REPORT-AUDIT"}}
	for i := 0; i < 61; i++ {
		evidence = append(evidence, Evidence{Type: "transaction", ID: fmt.Sprintf("PRIVATE-ROW-%d", i), AuditID: "PRIVATE-ROW-AUDIT-REFERENCE"})
	}
	before := append([]Evidence(nil), evidence...)
	out := AgentOutput{Result: raw, Evidence: evidence}
	calls := 0
	runner := workflowFixture(func(_ context.Context, request coze.RunRequest) (coze.RunResponse, error) {
		calls++
		input := request.Parameters["input_string"].(string)
		if strings.Contains(input, "PRIVATE-") || len(input) > 2048 {
			t.Errorf("model input repeats source rows/audits or grows with the month: %d bytes", len(input))
		}
		var payload struct {
			Result   json.RawMessage `json:"authoritative_result"`
			Evidence []Evidence      `json:"evidence"`
		}
		if json.Unmarshal([]byte(input), &payload) != nil || len(payload.Evidence) != 1 || payload.Evidence[0] != (Evidence{Type: "billing_report", ID: reportID}) {
			t.Error("model must cite the owned report without audit metadata")
		}
		if !bytes.Contains(payload.Result, []byte(`"total_expense":390000`)) || !bytes.Contains(payload.Result, []byte(`"transaction_count":61`)) {
			t.Error("deterministic summary was lost")
		}
		return workflowResult(map[string]any{"task_id": "synthetic-month-task", "agent_type": "billing", "explanation": "建议先核对购物必要性，复盘实际支出变化。", "evidence_ids": []string{reportID}}), nil
	})
	x := &HTTPExecutor{coze: runner, workflows: map[string]string{"billing": "123"}}
	if err := x.explain(context.Background(), Call{TaskID: "synthetic-month-task", AgentType: "billing"}, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !bytes.Equal(out.Result, raw) || !reflect.DeepEqual(out.Evidence, before) || len(out.ExplanationEvidenceIDs) != 1 || out.ExplanationEvidenceIDs[0] != reportID {
		t.Fatal("model compaction changed persisted facts/evidence or lost the citation")
	}
}

func TestBillingExplanationRequiresOneUnambiguousReportCitation(t *testing.T) {
	for _, evidence := range [][]Evidence{
		{{Type: "transaction", ID: "synthetic-row"}},
		{{Type: "billing_report", ID: ""}},
		{{Type: "billing_report", ID: "synthetic-a"}, {Type: "billing_report", ID: "synthetic-b"}},
	} {
		calls := 0
		runner := workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
			calls++
			return workflowResult(nil), nil
		})
		x := &HTTPExecutor{coze: runner, workflows: map[string]string{"billing": "123"}}
		out := AgentOutput{Result: json.RawMessage(`{"currency":"CNY","total_income":0,"total_expense":0,"transaction_count":0,"category_breakdown":[]}`), Evidence: evidence}
		if err := x.explain(context.Background(), Call{TaskID: "synthetic-task", AgentType: "billing"}, &out); err == nil || calls != 0 {
			t.Fatal("missing/ambiguous report evidence reached a model")
		}
	}
}

func TestBillingExplanationCannotCiteRowsNotSentToModel(t *testing.T) {
	runner := workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
		return workflowResult(map[string]any{"task_id": "synthetic-task", "agent_type": "billing", "explanation": "已记录账单仅供核对。", "evidence_ids": []string{"synthetic-hidden-row"}}), nil
	})
	x := &HTTPExecutor{coze: runner, workflows: map[string]string{"billing": "123"}}
	out := AgentOutput{Result: json.RawMessage(`{"currency":"CNY","total_income":0,"total_expense":100,"transaction_count":1,"category_breakdown":[{"category":"shopping","expense_minor":100}]}`), Evidence: []Evidence{{Type: "billing_report", ID: "synthetic-report"}, {Type: "transaction", ID: "synthetic-hidden-row"}}}
	if err := x.explain(context.Background(), Call{TaskID: "synthetic-task", AgentType: "billing"}, &out); err == nil {
		t.Fatal("model cited a row it was not given")
	}
}

type monthEvidenceBudget struct{ reserves, unexpected int }

func (b *monthEvidenceBudget) Reserve(context.Context, string, string) (modelspend.Attempt, error) {
	b.reserves++
	return modelspend.Attempt{}, modelspend.ErrQuota // Stop the isolated fixture before any supplier transport.
}
func (b *monthEvidenceBudget) Observe(context.Context, string, modelspend.Report) error {
	b.unexpected++
	return errors.New("no synthetic supplier call was allowed")
}
func (b *monthEvidenceBudget) Unknown(context.Context, string) error {
	b.unexpected++
	return errors.New("no synthetic supplier call was allowed")
}

func TestBillingExplanationMonthFitsGatewayBeforeReservation(t *testing.T) {
	budget := &monthEvidenceBudget{}
	const token = "synthetic-month-gateway-token-only-0000"
	p := modelspend.Policy{ProjectID: "synthetic-month-context", Currency: "USD", Model: "synthetic-context-model", PriceVersion: "synthetic-only", PriceSource: "synthetic:offline", PriceEvidenceSHA256: strings.Repeat("a", 64), ValidFrom: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Limit: "1", InputPrice: "1", CacheHitPrice: "0", OutputPrice: "1", ContextTokens: 4096, MaxOutputTokens: 512}
	gate, err := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: token, SupplierKey: "synthetic-never-dispatched-key", BaseURL: "http://127.0.0.1:1/v1", Outbound: outbound.TestPolicy()}, budget)
	if err != nil {
		t.Fatal(err)
	}
	out := AgentOutput{Result: json.RawMessage(`{"currency":"CNY","total_income":500000,"total_expense":390000,"transaction_count":61,"category_breakdown":[{"category":"shopping","expense_minor":300000},{"category":"dining","expense_minor":90000}]}`), Evidence: []Evidence{{Type: "billing_report", ID: "synthetic-owned-report"}}}
	for i := 0; i < 61; i++ {
		out.Evidence = append(out.Evidence, Evidence{Type: "transaction", ID: fmt.Sprintf("synthetic-row-%d", i), AuditID: "synthetic-long-audit-identifier-0000000"})
	}
	runner := workflowFixture(func(_ context.Context, request coze.RunRequest) (coze.RunResponse, error) {
		// More system-prompt headroom than the deployed Billingv0.0.8 prompt.
		body, _ := json.Marshal(map[string]any{"model": p.Model, "messages": []map[string]string{{"role": "system", "content": strings.Repeat("s", 2048)}, {"role": "user", "content": request.Parameters["input_string"].(string)}}, "max_tokens": 512})
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, req)
		if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "BUDGET_EXCEEDED") {
			t.Errorf("whole-month summary must pass the real context parser before the synthetic budget refuses dispatch: status=%d body=%s", response.Code, response.Body.String())
		}
		return workflowResult(map[string]any{"task_id": "synthetic-task", "agent_type": "billing", "explanation": "仅根据已记录账单提出核对建议。", "evidence_ids": []string{"synthetic-owned-report"}}), nil
	})
	x := &HTTPExecutor{coze: runner, workflows: map[string]string{"billing": "123"}}
	if err := x.explain(context.Background(), Call{TaskID: "synthetic-task", AgentType: "billing"}, &out); err != nil {
		t.Fatal(err)
	}
	if budget.reserves != 1 || budget.unexpected != 0 {
		t.Fatal("context rejected the compact summary or a supplier transport was attempted")
	}
}
