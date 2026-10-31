package coze

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectContextRejectionRemainsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"MODEL_CONTEXT_LIMIT_EXCEEDED","message":"MODEL_CONTEXT_LIMIT_EXCEEDED","type":"model_gateway_error"}}`))
	}))
	defer server.Close()
	runner, err := NewDirectRunner(server.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.RunSyncWorkflow(WithDelegation(context.Background(), "task"), RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}})
	if BudgetCode(err) != "MODEL_CONTEXT_LIMIT_EXCEEDED" {
		t.Fatal("context error hidden", err)
	}
}

func TestDirectTransportBeforeWriteIsBudgetUnavailable(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	origin := "http://" + listener.Addr().String()
	listener.Close()
	runner, e := NewDirectRunner(origin, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	_, e = runner.RunSyncWorkflow(WithDelegation(context.Background(), "task"), RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}})
	if BudgetCode(e) != "MODEL_BUDGET_UNAVAILABLE" {
		t.Fatalf("unsent request was misreported as uncertain paid outcome: %v", e)
	}
	_, e = runner.AdmissionSnapshot(context.Background(), "Bearer user")
	if BudgetCode(e) != "MODEL_BUDGET_UNAVAILABLE" {
		t.Fatalf("read-only gateway connection failure not explained: %v", e)
	}
}

func TestDirectTransportAfterWriteRemainsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	runner, e := NewDirectRunner(server.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	_, e = runner.RunSyncWorkflow(WithDelegation(context.Background(), "task"), RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}})
	if BudgetCode(e) != "MODEL_OUTCOME_UNKNOWN" {
		t.Fatalf("sent request lost conservative unknown state: %v", e)
	}
}

func TestDirectPlannerPinsExplicitNonThinking(t *testing.T) {
	raw, e := DirectRequest("synthetic-test", 512, `{"instruction":"analyze","bindings":{"account_id":"1"}}`)
	if e != nil {
		t.Fatal(e)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(raw, &body)
	if string(body["thinking"]) != `{"type":"disabled"}` {
		t.Fatal("bounded JSON planner still relies on supplier default thinking mode")
	}
}

func TestDirectAbortedResponseNeverBecomesSuccessfulBusinessResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-aborted", "model": "synthetic-test", "choices": []any{map[string]any{"finish_reason": "aborted", "message": map[string]string{"content": "BANKING_SMOKE_OK"}}}})
	}))
	defer server.Close()
	runner, e := NewDirectRunner(server.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	response, e := runner.RunSyncWorkflow(WithDelegation(context.Background(), "task"), RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}})
	if BudgetCode(e) != "MODEL_OUTCOME_UNKNOWN" || response.ExecuteID != "" {
		t.Fatal("aborted completion incorrectly accepted as a completed business task")
	}
}

func TestDirectRunnerPreservesUnavailableBudgetAndExpiredPolicy(t *testing.T) {
	for _, tc := range []struct{ upstream, want string }{
		{"BUDGET_UNAVAILABLE", "MODEL_BUDGET_UNAVAILABLE"},
		{"POLICY_UNAVAILABLE", "MODEL_APPROVAL_REQUIRED"},
	} {
		t.Run(tc.upstream, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": tc.upstream}})
			}))
			defer server.Close()
			runner, err := NewDirectRunner(server.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			result, err := runner.RunSyncWorkflow(WithDelegation(context.Background(), "synthetic-task"), RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}})
			if BudgetCode(err) != tc.want || result.ExecuteID != "" || calls != 1 {
				t.Fatalf("upstream refusal hidden: %v calls=%d", err, calls)
			}
		})
	}
}
