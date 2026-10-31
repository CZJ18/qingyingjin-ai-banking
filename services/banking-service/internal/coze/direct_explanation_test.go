package coze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestBillingExplanationPromptRequestsReviewableActions(t *testing.T) {
	for _, required := range []string{"结论：", "依据：", "局限：", "建议：", "行动：", "复核："} {
		if !strings.Contains(DirectBillingExplanationPrompt, required) {
			t.Errorf("billing guidance does not require %q", required)
		}
	}
}

func TestDirectBillingExplanationReviewedSnapshotPayloadContract(t *testing.T) {
	// Synthetic SQL facts from the completed isolated October acceptance report.
	input := `{"task_id":"draft","agent_type":"billing","authoritative_result":{"currency":"CNY","total_income":100000,"total_expense":24000,"transaction_count":3,"category_breakdown":[{"category":"dining","expense_minor":18000},{"category":"sports","expense_minor":6000}]},"evidence":[{"type":"billing_report","id":"draft-report"},{"type":"transaction","id":"2"},{"type":"transaction","id":"3"},{"type":"transaction","id":"4"}]}`
	raw, err := DirectBillingExplanationRequest("deepseek-flash", 512, input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("completeness_not_verified")) {
		t.Fatal("missing mandatory limitations")
	}
	// The optional-purchase, alternative and weekly-review guidance invalidates
	// the earlier draft body 7bb36ff7...4a2c. This pins serialization only: it does
	// not approve this body or renew any historical plan, approval or confirmation.
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "4824ca4ff462bf2e066a52977e96d27f339ad72d6dfa09f191da6ee51ab306df" {
		t.Fatal("exact draft payload contract changed; review before any authorization", got)
	}
}

func TestDirectExplanationExactPayloadStableButFactsCannotChange(t *testing.T) {
	input := `{"task_id":"task-a","agent_type":"billing","authoritative_result":{"currency":"CNY","total_income":100000,"total_expense":24000,"transaction_count":2,"category_breakdown":[{"category":"dining","expense_minor":24000}]},"evidence":[{"type":"billing_report","id":"report-a"},{"type":"transaction","id":"3"}]}`
	first, err := DirectBillingExplanationRequest("synthetic-test", 512, input)
	if err != nil {
		t.Fatal(err)
	}
	changedIDs := strings.ReplaceAll(strings.ReplaceAll(input, "task-a", "task-b"), "report-a", "report-b")
	second, err := DirectBillingExplanationRequest("synthetic-test", 512, changedIDs)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("volatile IDs changed exact payload", err)
	}
	changedFacts := strings.ReplaceAll(input, "24000", "25000")
	third, err := DirectBillingExplanationRequest("synthetic-test", 512, changedFacts)
	if err != nil || bytes.Equal(first, third) {
		t.Fatal("changed facts reused approved payload", err)
	}
	for _, content := range []string{
		`{"explanation":"解释","evidence_ids":["foreign-report"]}`,
		`{"explanation":"解释","evidence_ids":["3","3"]}`,
		`{"explanation":"解释","evidence_ids":["3"],"total_expense":0}`,
		`{"explanation":"解释","explanation":"覆盖","evidence_ids":["3"]}`,
	} {
		if _, err := directExplanationResponse(input, content, "synthetic-response"); err == nil {
			t.Fatal("invalid supplier result accepted", content)
		}
	}
}

func TestDirectBillingExplanationUsesGatewayAndStableMinimalFacts(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Banking-Authorization") != "Bearer synthetic-task" {
			t.Error("gateway task binding lost")
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 2 {
			t.Fatal("bad request")
		}
		input := body.Messages[1].Content
		for _, private := range []string{"private-user", "private-merchant", "volatile-task", "volatile-report", "volatile-audit", "snapshot_at"} {
			if strings.Contains(input, private) {
				t.Error("unnecessary or volatile field sent", private)
			}
		}
		if !strings.Contains(input, `"total_expense":24000`) || !strings.Contains(input, `"3"`) {
			t.Error("missing deterministic facts")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-only", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": `{"explanation":"支出主要集中在餐饮，请结合实际需要核查，必要生活开支应保留。","evidence_ids":["3"]}`}, "finish_reason": "stop"}}})
	}))
	defer s.Close()
	d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	input := `{"task_id":"volatile-task","agent_type":"billing","authoritative_result":{"user_id":"private-user","report_id":"volatile-report","audit_id":"volatile-audit","snapshot_at":123,"total_income":100000,"total_expense":24000,"transaction_count":2,"currency":"CNY","category_breakdown":[{"category":"dining","expense_minor":24000,"transaction_ids":["3"]}],"evidence_transaction_ids":["2","3"],"transactions":[{"merchant":"private-merchant"}]},"evidence":[{"type":"billing_report","id":"volatile-report","audit_id":"volatile-audit"},{"type":"transaction","id":"2"},{"type":"transaction","id":"3"}]}`
	in := RunRequest{WorkflowID: "direct_provider_billing_explanation", Parameters: map[string]any{"input_string": input}}
	if _, e = d.RunSyncWorkflow(context.Background(), in); e == nil || calls != 0 {
		t.Fatal("missing delegation admitted")
	}
	out, e := d.RunSyncWorkflow(WithDelegation(context.Background(), "synthetic-task"), in)
	if e != nil || calls != 1 || !strings.Contains(out.Data, "volatile-task") || !strings.Contains(out.Data, "explanation") {
		t.Fatal(out, e, calls)
	}
}
