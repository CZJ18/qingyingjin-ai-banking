package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/service"
)

type smokeRunner struct{ calls int }

func (*smokeRunner) DirectProvider() bool { return true }
func (s *smokeRunner) RunSyncWorkflow(ctx context.Context, in coze.RunRequest) (coze.RunResponse, error) {
	s.calls++
	if in.WorkflowID != "direct_provider_smoke" || len(in.Parameters) != 0 || !coze.HasDelegation(ctx) {
		panic("unsafe smoke delegation")
	}
	return workflowResult(map[string]any{"model_response": "BANKING_SMOKE_OK", "model": "synthetic-test", "smoke_only": true}), nil
}
func TestSmokeTaskDoesNotInvokeBankingTools(t *testing.T) {
	bankCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { bankCalls++ }))
	defer server.Close()
	runner := &smokeRunner{}
	x, e := NewHTTPExecutor(server.URL, executorAuth(), runner, map[string]string{"supervisor": "direct_provider"}, nil, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	c := Call{Meta: service.Meta{UserID: 42}, TaskID: "task-smoke", AgentType: "supervisor", SkillID: "task_planning", SkillVersion: 1, Mode: "read_only", Tool: "model_smoke", Parameters: json.RawMessage(`{}`)}
	out, e := x.Run(context.Background(), c)
	if e != nil || out.FinancialExecutionEnabled || ValidateOutput(c, out) != nil {
		t.Fatalf("smoke failed: %+v %v", out, e)
	}
	for _, params := range []string{`{"input":"custom"}`, `{"account_id":"1"}`} {
		c.Parameters = json.RawMessage(params)
		if _, e = x.Run(context.Background(), c); e == nil {
			t.Fatal("custom smoke params accepted")
		}
	}
	if runner.calls != 1 || bankCalls != 0 {
		t.Fatal("smoke invoked a business tool", runner.calls, bankCalls)
	}
}

func TestSmokeCannotBeMixedWithBusinessTasks(t *testing.T) {
	smoke := TaskSpec{Key: "smoke", AgentType: "supervisor", SkillID: "task_planning", SkillVersion: 1, Tool: "model_smoke", ExecutionMode: "read_only", Parameters: json.RawMessage(`{}`)}
	bill := TaskSpec{Key: "bill", AgentType: "billing", Tool: "generate_billing_report", Parameters: json.RawMessage(`{}`)}
	if _, e := ValidateTasks([]TaskSpec{smoke, bill}); e == nil {
		t.Fatal("smoke mixed with financial tools")
	}
	smoke.UseCoze = true
	if _, e := ValidateTasks([]TaskSpec{smoke}); e == nil {
		t.Fatal("Coze smoke accepted")
	}
}
