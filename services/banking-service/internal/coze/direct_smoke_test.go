package coze

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestDirectSmokeIsFixedTextAndNeverAPlan(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid body")
		}
		for _, k := range []string{"tools", "tool_choice", "response_format"} {
			if _, ok := body[k]; ok {
				t.Errorf("smoke contains %s", k)
			}
		}
		if string(body["thinking"]) != `{"type":"disabled"}` {
			t.Error("fixed smoke must explicitly disable default supplier thinking")
		}
		var messages []map[string]string
		json.Unmarshal(body["messages"], &messages)
		if len(messages) != 1 || messages[0]["content"] != "Reply with BANKING_SMOKE_OK. No personal financial data; no tools or financial operations." {
			t.Errorf("not the fixed smoke input: %s", body["messages"])
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-smoke", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": "BANKING_SMOKE_OK"}, "finish_reason": "stop"}}})
	}))
	defer s.Close()
	d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	in := RunRequest{WorkflowID: "direct_provider_smoke", Parameters: map[string]any{}}
	if _, e = d.RunSyncWorkflow(context.Background(), in); e == nil || calls != 0 {
		t.Fatal("missing task delegation reached gateway")
	}
	ctx := WithDelegation(context.Background(), "task")
	out, e := d.RunSyncWorkflow(ctx, in)
	if e != nil || out.ExecuteID != "direct_provider:synthetic-smoke" || !strings.Contains(out.Data, "BANKING_SMOKE_OK") {
		t.Fatalf("smoke failed: %+v %v", out, e)
	}
	for _, parameters := range []map[string]any{{"input_string": "custom text"}, {"account_id": "1"}} {
		in.Parameters = parameters
		if _, e = d.RunSyncWorkflow(ctx, in); e == nil {
			t.Fatal("caller changed fixed smoke input")
		}
	}
	if calls != 1 {
		t.Fatal("invalid smoke caused an extra gateway request", calls)
	}
}

func TestDirectSmokeExactApprovalBytesAndTokenCeiling(t *testing.T) {
	raw, e := DirectSmokeRequest("deepseek-flash", 128)
	expected := `{"model":"deepseek-flash","messages":[{"content":"Reply with BANKING_SMOKE_OK. No personal financial data; no tools or financial operations.","role":"user"}],"max_tokens":128,"thinking":{"type":"disabled"}}`
	if e != nil || string(raw) != expected {
		t.Fatal("approval payload bytes changed", e)
	}
	for _, max := range []int64{0, 129, 4096} {
		if _, e := DirectSmokeRequest("deepseek-flash", max); e == nil {
			t.Fatal("smoke output ceiling bypassed", max)
		}
	}
}
