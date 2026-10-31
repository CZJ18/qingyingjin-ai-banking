package coze

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const syntheticGoalInput = `{"task_id":"synthetic-task-a","agent_type":"lifestyle","findings":[{"finding_id":"goal.reduction_target","text":"目标少花500.00元，不等同于存款。","evidence_ids":["synthetic-plan-a"]},{"finding_id":"plan.missing_information","text":"账单月份尚未结束，不能判断整月方案。","evidence_ids":["synthetic-plan-a"]}]}`

func TestDirectLifestyleExactFactsAndStrictResult(t *testing.T) {
	first, e := DirectLifestyleExplanationRequest("synthetic-test", 512, syntheticGoalInput)
	if e != nil {
		t.Fatal(e)
	}
	second, e := DirectLifestyleExplanationRequest("synthetic-test", 512, strings.ReplaceAll(strings.ReplaceAll(syntheticGoalInput, "synthetic-task-a", "synthetic-task-b"), "synthetic-plan-a", "synthetic-plan-b"))
	if e != nil || !bytes.Equal(first, second) {
		t.Fatal("volatile identity changed provider body", e)
	}
	changed, e := DirectLifestyleExplanationRequest("synthetic-test", 512, strings.ReplaceAll(syntheticGoalInput, "500.00", "600.00"))
	if e != nil || bytes.Equal(first, changed) {
		t.Fatal("changed goal reused approved body", e)
	}
	for _, content := range []string{`{"finding_ids":["foreign"]}`, `{"finding_ids":["goal.reduction_target"]}`, `{"finding_ids":["plan.missing_information","plan.missing_information"]}`, `{"finding_ids":["plan.missing_information"],"explanation":"已转账"}`} {
		if _, e := directLifestyleResponse(syntheticGoalInput, content, "synthetic-response"); e == nil {
			t.Fatal("unbound or unsafe result accepted", content)
		}
	}
	response, e := directLifestyleResponse(syntheticGoalInput, `{"finding_ids":["plan.missing_information","goal.reduction_target"]}`, "synthetic-response")
	if e != nil || !strings.Contains(response.Data, "synthetic-task-a") {
		t.Fatal(response, e)
	}
	if !DirectWorkflowAllowed("lifestyle", DirectLifestyleExplanationWorkflow) || DirectWorkflowAllowed("billing", DirectLifestyleExplanationWorkflow) {
		t.Fatal("role binding lost")
	}
}
func TestDirectLifestyleCannotBypassGatewayOrDelegation(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Banking-Authorization") != "Bearer synthetic-lease" {
			t.Error("gateway binding lost")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-response", "model": "synthetic-test", "choices": []any{map[string]any{"message": map[string]string{"content": `{"finding_ids":["plan.missing_information","goal.reduction_target"]}`}, "finish_reason": "stop"}}})
	}))
	defer s.Close()
	runner, e := NewDirectRunner(s.URL, strings.Repeat("x", 32), "synthetic-test", 512, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	in := RunRequest{WorkflowID: DirectLifestyleExplanationWorkflow, Parameters: map[string]any{"input_string": syntheticGoalInput}}
	if _, e := runner.RunSyncWorkflow(context.Background(), in); e == nil || calls != 0 {
		t.Fatal("missing delegation dispatched")
	}
	if _, e := runner.RunSyncWorkflow(WithDelegation(context.Background(), "synthetic-lease"), in); e != nil || calls != 1 {
		t.Fatal(e, calls)
	}
}
