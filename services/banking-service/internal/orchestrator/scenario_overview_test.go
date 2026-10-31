package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/service"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/skills"
)

func TestScenarioOverviewRegisteredReadOnlyAndAccountBound(t *testing.T) {
	for _, scenario := range []string{"transfer", "cards", "investment", "subscriptions", "birthday"} {
		params := json.RawMessage(`{"scenario":"` + scenario + `","account_id":"1"}`)
		binding, err := skills.Resolve(skills.Request{SkillID: "task_planning", SkillVersion: 1, Agent: "supervisor", Tool: "get_scenario_overview", Mode: "read_only"})
		if err != nil || binding.Mode != "read_only" {
			t.Errorf("%s overview is not a registered read-only Supervisor tool: %v", scenario, err)
			continue
		}
		if err := ValidateParameters("get_scenario_overview", params, true); err != nil {
			t.Errorf("%s valid request rejected: %v", scenario, err)
		}
		tasks := []TaskSpec{{AgentType: "supervisor", Tool: "get_scenario_overview", Parameters: params}}
		if err := validateAccountBindings(tasks, Bindings{AccountID: "1"}); err != nil {
			t.Errorf("same selected account rejected: %v", err)
		}
		if err := validateAccountBindings(tasks, Bindings{AccountID: "2"}); entity.Code(err) != "INVALID_TASK_PLAN" {
			t.Errorf("%s could change the selected account: %v", scenario, err)
		}
		if err := validateAccountBindings(tasks, Bindings{}); entity.Code(err) != "INVALID_TASK_PLAN" {
			t.Errorf("%s has no trusted selected account: %v", scenario, err)
		}
	}
}

func TestScenarioOverviewCannotBecomeSpecialistModelOrMutation(t *testing.T) {
	_, err := ValidateTasks([]TaskSpec{{Key: "overview", AgentType: "supervisor", Tool: "get_scenario_overview", Parameters: json.RawMessage(`{"scenario":"cards","account_id":"1"}`), UseCoze: true}})
	if err == nil {
		t.Fatal("read-only overview cannot request an additional model call")
	}
	for _, raw := range []string{`{}`, `{"scenario":"cards"}`, `{"scenario":"cards","account_id":""}`, `{"scenario":"cards","account_id":"0"}`, `{"scenario":"cards","account_id":"01"}`, `{"scenario":"cards","account_id":1}`, `{"scenario":"execute","account_id":"1"}`, `{"scenario":"cards","account_id":"1","user_id":"7"}`} {
		if err := ValidateParameters("get_scenario_overview", json.RawMessage(raw), true); err == nil {
			t.Errorf("unsafe or incomplete overview request accepted: %s", raw)
		}
	}
}

func TestScenarioOverviewExecutorUsesOnlyBoundReadOnlyHTTPAndChecksResponse(t *testing.T) {
	requests := 0
	response := `{"scenario":"cards","account_id":"1","user_id":"42","is_simulated":true,"items":[],"limitations":["Synthetic isolated records only."],"truncated":false}`
	auth := executorAuth()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/api/v1/scenarios/get_scenario_overview" {
			t.Error("unexpected operation or tool transport")
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": json.RawMessage(response)})
	}))
	defer server.Close()
	x, e := NewHTTPExecutor(server.URL, auth, nil, nil, nil, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	call := Call{Meta: service.Meta{UserID: 42}, TaskID: "overview-task", AgentType: "supervisor", SkillID: "task_planning", SkillVersion: 1, Mode: "read_only", Tool: "get_scenario_overview", Parameters: json.RawMessage(`{"scenario":"cards","account_id":"1"}`)}
	out, e := x.Run(context.Background(), call)
	if e != nil || out.FinancialExecutionEnabled || len(out.NextActions) != 0 {
		t.Fatal(out, e)
	}
	response = `{"scenario":"cards","account_id":"2","user_id":"42","is_simulated":true,"items":[],"limitations":["Synthetic isolated records only."],"truncated":false}`
	if _, e = x.Run(context.Background(), call); entity.Code(e) != "TOOL_OUTPUT_INVALID" {
		t.Fatal("foreign account output accepted", e)
	}
	call.UseCoze = true
	if _, e = x.Run(context.Background(), call); entity.Code(e) != "INVALID_TASK_PLAN" {
		t.Fatal(e)
	}
	if requests != 2 {
		t.Fatal("forbidden additional model call reached transport", requests)
	}
}
