package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
)

func spec(key, agent, tool string, deps ...string) TaskSpec {
	return TaskSpec{Key: key, AgentType: agent, Tool: tool, Parameters: json.RawMessage(`{}`), Dependencies: deps}
}
func TestSkillGraphBinding(t *testing.T) {
	base := spec("b", "billing", "get_transactions")
	got, err := ValidateTasks([]TaskSpec{base})
	if err != nil || got[0].SkillID != "billing_analysis" || got[0].SkillVersion != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, version := range []int64{0, 99} {
		bad := base
		bad.SkillID = "billing_analysis"
		bad.SkillVersion = version
		if _, err := ValidateTasks([]TaskSpec{bad}); err == nil {
			t.Fatal("invalid version accepted")
		}
	}
	bad := base
	bad.SkillID = "subscription_analysis"
	bad.SkillVersion = 1
	if _, err := ValidateTasks([]TaskSpec{bad}); err == nil {
		t.Fatal("cross-skill tool accepted")
	}
}

func TestTaskKeysMatchLangGraphContract(t *testing.T) {
	for _, key := range []string{"read.bill", "read:bill", "read/bill"} {
		if _, err := ValidateTasks([]TaskSpec{spec(key, "billing", "get_transactions")}); err == nil {
			t.Fatalf("key %q cannot cross the LangGraph task boundary", key)
		}
	}
	if _, err := ValidateTasks([]TaskSpec{spec("read_bill-1", "billing", "get_transactions")}); err != nil {
		t.Fatal(err)
	}
}
func TestOldWaveCannotRemoveResumedCancellation(t *testing.T) {
	s := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.active["job"] = activeWave{token: "new", cancel: cancel}
	s.releaseWave("job", "old")
	s.interrupt("job")
	if ctx.Err() == nil {
		t.Fatal("old completion removed new wave cancellation")
	}
	s.releaseWave("job", "new")
	if len(s.active) != 0 {
		t.Fatal("current wave not released")
	}
}
func TestRejectUnsafeGraphs(t *testing.T) {
	for _, tasks := range [][]TaskSpec{
		{spec("a", "billing", "execute_transfer")}, {spec("a", "billing", "detect_subscriptions")}, {spec("a", "billing", "analyze_spending", "missing")}, {spec("a", "billing", "analyze_spending", "b"), spec("b", "billing", "compare_budget", "a")}, {spec("a", "billing", "analyze_spending"), spec("a", "billing", "compare_budget")},
	} {
		if _, e := ValidateTasks(tasks); e == nil {
			t.Fatal("invalid graph accepted", tasks)
		}
	}
}
func TestReadyDependencyAndPriority(t *testing.T) {
	tasks, e := ValidateTasks([]TaskSpec{spec("b", "billing", "analyze_spending"), spec("s", "savings", "get_financial_plan", "b"), spec("a", "subscription", "analyze_subscription_cost")})
	if e != nil || len(tasks) != 3 {
		t.Fatal(e)
	}
	if tasks[0].ExecutionMode != "read_only" {
		t.Fatal(tasks)
	}
}
func TestSubscriptionDetectionUsesBusinessDefaultWindow(t *testing.T) {
	if err := ValidateParameters("detect_subscriptions", json.RawMessage(`{}`), true); err != nil {
		t.Fatalf("the subscription API supports its bounded default three-month window: %v", err)
	}
	if err := ValidateParameters("detect_subscriptions", json.RawMessage(`{"timezone":"UTC"}`), true); err == nil {
		t.Fatal("unsupported fields must still be rejected")
	}
}
