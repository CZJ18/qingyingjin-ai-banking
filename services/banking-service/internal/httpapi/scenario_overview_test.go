package httpapi

import (
	"testing"
)

func TestLangGraphScenarioOverviewExactReadOnlySupervisorRoute(t *testing.T) {
	f := &langGraphRunnerFixture{}
	h, a, _ := langGraphTestHandler(t, f, langGraphActive)
	token := langGraphToken(t, a, "supervisor", langGraphTestTask)
	body := `{"skill_id":"task_planning","skill_version":1,"tool":"get_scenario_overview","execution_mode":"read_only","parameters":{"scenario":"cards","account_id":"1"},"use_coze":false}`
	w, out := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", token, langGraphTestKey, body, nil)
	langGraphWantStatus(t, w, out, 200, "OK")
	if len(f.calls) != 1 || f.calls[0].AgentType != "supervisor" || f.calls[0].Tool != "get_scenario_overview" || f.calls[0].UseCoze || f.calls[0].Mode != "read_only" {
		t.Fatal(f.calls)
	}
	for _, bad := range []string{`{"skill_id":"task_planning","skill_version":1,"tool":"get_scenario_overview","execution_mode":"read_only","parameters":{"scenario":"cards","account_id":"1"},"use_coze":true}`, `{"skill_id":"task_planning","skill_version":1,"tool":"get_scenario_overview","execution_mode":"read_only","parameters":{"scenario":"cards","account_id":"1","user_id":"43"},"use_coze":false}`} {
		w, _ := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", token, langGraphTestKey, bad, nil)
		if w.Code == 200 {
			t.Fatal("unsafe input reached runner")
		}
	}
	if len(f.calls) != 1 {
		t.Fatal("rejected request reached runner")
	}
	other := langGraphToken(t, a, "billing", langGraphTestTask)
	w, out = langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", other, langGraphTestKey, body, nil)
	langGraphWantStatus(t, w, out, 403, "FORBIDDEN")
}
