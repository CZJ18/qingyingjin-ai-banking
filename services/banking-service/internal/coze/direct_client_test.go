package coze

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectRunnerUsesGatewayTaskCredentialAndRejectsUnsafePlans(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("g", 32) || r.Header.Get("X-Banking-Authorization") != "Bearer task" {
				t.Error("wrong gateway routing/auth")
			}
			mode := "read_only"
			if unsafe {
				mode = "intent"
			}
			content, _ := json.Marshal(map[string]any{"tasks": []any{map[string]any{"key": "billing", "agent_type": "billing", "tool": "generate_billing_report", "skill_id": "billing_analysis", "skill_version": 1, "execution_mode": mode, "parameters": map[string]any{"account_id": "3"}, "dependencies": []string{}, "priority": 1, "use_coze": false}}})
			json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-response", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
		}))
		d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 128, outbound.TestPolicy())
		if e != nil {
			t.Fatal(e)
		}
		in := RunRequest{WorkflowID: "direct_provider", Parameters: map[string]any{"input_string": `{"instruction":"analyze","bindings":{"account_id":"3"}}`}}
		if _, e = d.RunSyncWorkflow(context.Background(), in); e == nil || calls != 0 {
			t.Fatal("missing delegation sent a request")
		}
		result, e := d.RunSyncWorkflow(WithDelegation(context.Background(), "task"), in)
		if unsafe && e == nil || !unsafe && (e != nil || !strings.HasPrefix(result.ExecuteID, "direct_provider:")) {
			t.Fatal(result, e)
		}
		s.Close()
	}
	if _, e := NewDirectRunner("https://gateway.invalid", strings.Repeat("g", 32), "model", 1, outbound.Policy{Environment: "production"}); e == nil {
		t.Fatal("direct mode escaped isolation")
	}
}
