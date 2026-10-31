package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
)

func TestScopedSubscriptionPlanCannotSelectGlobalTools(t *testing.T) {
	for _, tool := range []string{"analyze_subscription_cost", "suggest_subscription_actions"} {
		for name, bindings := range map[string]Bindings{
			"account":            {AccountID: "23"},
			"period":             {From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z"},
			"account_and_period": {AccountID: "23", From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z"},
		} {
			t.Run(tool+"/"+name, func(t *testing.T) {
				task := TaskSpec{Key: "subscriptions", AgentType: "subscription", SkillID: "subscription_analysis", SkillVersion: 1, ExecutionMode: "read_only", Tool: tool, Parameters: json.RawMessage(`{}`)}
				if _, err := ValidateTasks([]TaskSpec{task}); err != nil {
					t.Fatal(err)
				}
				if err := validateAccountBindings([]TaskSpec{task}, bindings); entity.Code(err) != "INVALID_TASK_PLAN" {
					t.Fatalf("global tool escaped the selected account/period: %v", err)
				}
			})
		}
	}
}

func TestUnboundSubscriptionGlobalToolsRemainAvailable(t *testing.T) {
	for _, tool := range []string{"analyze_subscription_cost", "suggest_subscription_actions"} {
		if err := validateAccountBindings([]TaskSpec{{AgentType: "subscription", Tool: tool, Parameters: json.RawMessage(`{}`)}}, Bindings{}); err != nil {
			t.Fatalf("explicit all-user query changed: %v", err)
		}
	}
}

func TestScopedSubscriptionDetectionRemainsAvailable(t *testing.T) {
	b := Bindings{AccountID: "23", From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z"}
	task := TaskSpec{AgentType: "subscription", Tool: "detect_subscriptions", Parameters: json.RawMessage(`{"account_id":"23","from":"2026-08-31T16:00:00Z","to":"2026-09-30T16:00:00Z"}`)}
	if err := validateAccountBindings([]TaskSpec{task}, b); err != nil {
		t.Fatal(err)
	}
}
