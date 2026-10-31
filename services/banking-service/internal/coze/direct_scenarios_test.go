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

// The local response is explicitly synthetic; this checks the production
// adapter and monetary bridge routing, not supplier availability or quality.
func TestDirectScenarioProfileUsesExistingGatewayAndBoundedPlan(t *testing.T) {
	for _, kind := range []string{"transfer", "cards", "investment", "subscriptions", "birthday"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("g", 32) || r.Header.Get("X-Banking-Authorization") != "Bearer exact-task" {
					t.Error("scenario mode bypassed existing cost gateway")
				}
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
					Max int64 `json:"max_tokens"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Max != 512 || len(body.Messages) != 2 || body.Messages[0].Content == DirectPlannerPrompt || !strings.Contains(body.Messages[0].Content, "get_scenario_overview") {
					t.Error("missing explicit bounded scenario planner prompt")
				}
				content, _ := json.Marshal(map[string]any{"tasks": []any{map[string]any{"key": "context", "agent_type": "supervisor", "tool": "get_scenario_overview", "skill_id": "task_planning", "skill_version": 1, "execution_mode": "read_only", "parameters": map[string]any{"scenario": kind, "account_id": "3"}, "dependencies": []string{}, "priority": 1, "use_coze": false}}})
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-profile", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
			}))
			defer s.Close()
			d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			in := RunRequest{WorkflowID: "direct_provider_scenarios", Parameters: map[string]any{"input_string": `{"instruction":"review selected scenario","bindings":{"account_id":"3"}}`}}
			if _, e = d.RunSyncWorkflow(context.Background(), in); e == nil || calls != 0 {
				t.Fatal("missing task delegation sent a request")
			}
			out, e := d.RunSyncWorkflow(WithDelegation(context.Background(), "exact-task"), in)
			if e != nil || calls != 1 || out.ExecuteID != "direct_provider:synthetic-profile" {
				t.Fatalf("explicit scenario profile rejected: calls=%d out=%+v err=%v", calls, out, e)
			}
		})
	}
}

func TestDirectScenarioProfileRejectsExecutableOrRecursivePlans(t *testing.T) {
	for _, bad := range []map[string]any{
		{"agent_type": "supervisor", "tool": "execute_operation", "skill_id": "task_planning", "skill_version": 1, "execution_mode": "read_only"},
		{"agent_type": "supervisor", "tool": "plan_instruction", "skill_id": "task_planning", "skill_version": 1, "execution_mode": "read_only"},
		{"agent_type": "supervisor", "tool": "model_smoke", "skill_id": "task_planning", "skill_version": 1, "execution_mode": "read_only"},
		{"agent_type": "supervisor", "tool": "get_scenario_overview", "skill_id": "task_planning", "skill_version": 1, "execution_mode": "read_only", "use_coze": true},
		{"agent_type": "supervisor", "tool": "get_scenario_overview", "skill_id": "task_planning", "skill_version": 2, "execution_mode": "read_only"},
		{"agent_type": "subscription", "tool": "analyze_subscription_cost", "skill_id": "subscription_analysis", "skill_version": 1, "execution_mode": "read_only"},
		{"agent_type": "subscription", "tool": "get_subscription_details", "skill_id": "subscription_analysis", "skill_version": 1, "execution_mode": "read_only"},
		{"agent_type": "supervisor", "tool": "get_scenario_overview", "execution_mode": "read_only"},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			content, _ := json.Marshal(map[string]any{"tasks": []any{bad}})
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-unsafe", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
		}))
		d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
		if e != nil {
			t.Fatal(e)
		}
		_, e = d.RunSyncWorkflow(WithDelegation(context.Background(), "exact-task"), RunRequest{WorkflowID: "direct_provider_scenarios", Parameters: map[string]any{"input_string": `{"instruction":"synthetic","bindings":{"account_id":"3"}}`}})
		s.Close()
		if e == nil {
			t.Fatal("unapproved, recursive or executable plan accepted", bad)
		}
	}
}

func TestDirectScenarioProfileDoesNotExpandLegacyBilling(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := `{"tasks":[{"agent_type":"supervisor","tool":"get_scenario_overview","skill_id":"task_planning","skill_version":1,"execution_mode":"read_only","use_coze":false}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-legacy", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
	}))
	defer s.Close()
	d, e := NewDirectRunner(s.URL, strings.Repeat("g", 32), "synthetic-test", 512, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.RunSyncWorkflow(WithDelegation(context.Background(), "exact-task"), RunRequest{WorkflowID: "direct_provider", Parameters: map[string]any{"input_string": "synthetic"}}); e == nil {
		t.Fatal("legacy billing scope expanded")
	}
}
