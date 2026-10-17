package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/service"
)

const langGraphTestTask = "lg-01234567-89ab-cdef-8123-0123456789ab.billing_1"

var langGraphTestKey = strings.Repeat("l", 32)

type langGraphRunnerFixture struct {
	calls []orchestrator.Call
	plans []service.Meta
	run   func(context.Context, orchestrator.Call) (orchestrator.AgentOutput, error)
	plan  func(context.Context, service.Meta, string, orchestrator.Bindings) ([]orchestrator.TaskSpec, string, error)
}

func (f *langGraphRunnerFixture) Run(ctx context.Context, call orchestrator.Call) (orchestrator.AgentOutput, error) {
	f.calls = append(f.calls, call)
	if f.run != nil {
		return f.run(ctx, call)
	}
	return orchestrator.AgentOutput{TaskID: call.TaskID, AgentType: call.AgentType, Status: "succeeded", Result: json.RawMessage(`{}`)}, nil
}

func (f *langGraphRunnerFixture) PlanWithEvidence(ctx context.Context, meta service.Meta, instruction string, bindings orchestrator.Bindings) ([]orchestrator.TaskSpec, string, error) {
	f.plans = append(f.plans, meta)
	if f.plan != nil {
		return f.plan(ctx, meta, instruction, bindings)
	}
	return []orchestrator.TaskSpec{{Key: "billing_1", AgentType: "billing", SkillID: "billing_analysis", SkillVersion: 1, Tool: "detect_anomalies", ExecutionMode: "read_only", Parameters: json.RawMessage(`{}`)}}, "test-workflow-execute", nil
}

func langGraphTestHandler(t *testing.T, runner LangGraphExecutor, active func(context.Context, int64) error) (http.Handler, *identity.Auth, *bytes.Buffer) {
	t.Helper()
	a := identity.New(strings.Repeat("u", 32), "qingyingjin", "banking-service", strings.Repeat("t", 32), "42", "7")
	var logs bytes.Buffer
	b := NewLangGraphBridge(langGraphTestKey, runner)
	b.ActiveUser = active
	h := NewWithLifestyle(a, slog.New(slog.NewJSONHandler(&logs, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, nil, nil, b)
	return h, a, &logs
}

func langGraphActive(context.Context, int64) error { return nil }

func langGraphToken(t *testing.T, auth *identity.Auth, role, task string) string {
	t.Helper()
	var token string
	var err error
	if role == "user" {
		token, err = auth.IssueUser("42", "7", time.Minute)
	} else {
		token, err = auth.IssueAgent("42", role, task, time.Minute)
	}
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func langGraphJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type langGraphResponse struct {
	Code   string          `json:"code"`
	TaskID string          `json:"task_id"`
	Data   json.RawMessage `json:"data"`
}

func langGraphRequest(t *testing.T, h http.Handler, method, path, token, key, body string, headers map[string]string) (*httptest.ResponseRecorder, langGraphResponse) {
	t.Helper()
	q := httptest.NewRequest(method, path, strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	if token != "" {
		q.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		q.Header.Set("X-Banking-Agent-Key", key)
	}
	for name, value := range headers {
		q.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, q)
	var envelope langGraphResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid response JSON on %s: %v", path, err)
	}
	return w, envelope
}

func langGraphWantStatus(t *testing.T, w *httptest.ResponseRecorder, response langGraphResponse, status int, code string) {
	t.Helper()
	if w.Code != status || response.Code != code {
		t.Fatalf("got HTTP %d code %s, want HTTP %d code %s", w.Code, response.Code, status, code)
	}
}

func TestLangGraphRoutesRequireConfiguration(t *testing.T) {
	h, auth, _ := testHandler(t, false)
	token, err := auth.IssueUser("42", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/langgraph/delegations", "/api/v1/langgraph/run", "/api/v1/langgraph/plan"} {
		q := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		q.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, q)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"DEPENDENCY_UNAVAILABLE"`) {
			t.Fatalf("%s: got %d %s, want disabled bridge 503", path, w.Code, w.Body.String())
		}
	}
}

func TestLangGraphIncompleteConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string
		runner LangGraphExecutor
		active func(context.Context, int64) error
	}{
		{"missing service key", "", &langGraphRunnerFixture{}, langGraphActive},
		{"short service key", strings.Repeat("l", 31), &langGraphRunnerFixture{}, langGraphActive},
		{"missing executor", langGraphTestKey, nil, langGraphActive},
		{"missing persisted user verifier", langGraphTestKey, &langGraphRunnerFixture{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, auth, logs := testHandler(t, false)
			bridge := NewLangGraphBridge(tc.key, tc.runner)
			bridge.ActiveUser = tc.active
			h := NewWithLifestyle(auth, slog.New(slog.NewJSONHandler(logs, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, nil, nil, bridge)
			token := langGraphToken(t, auth, "user", "")
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", token, langGraphTestKey, `{}`, nil)
			langGraphWantStatus(t, w, response, 503, "DEPENDENCY_UNAVAILABLE")
		})
	}
}

func TestLangGraphRequiresSignedJWTAndIndependentServiceKey(t *testing.T) {
	for _, endpoint := range []struct{ path, role string }{{"delegations", "user"}, {"run", "billing"}, {"plan", "supervisor"}} {
		t.Run(endpoint.path, func(t *testing.T) {
			activeCalls := 0
			runner := &langGraphRunnerFixture{}
			h, auth, logs := langGraphTestHandler(t, runner, func(context.Context, int64) error { activeCalls++; return nil })
			token := langGraphToken(t, auth, endpoint.role, langGraphTestTask)
			for _, tc := range []struct{ name, token, key string }{
				{"missing JWT", "", langGraphTestKey},
				{"tool key as JWT", strings.Repeat("t", 32), langGraphTestKey},
				{"tampered signed JWT", token + "x", langGraphTestKey},
				{"missing service key", token, ""},
				{"incorrect service key", token, strings.Repeat("z", 32)},
				{"tool key as service key", token, strings.Repeat("t", 32)},
				{"JWT signing secret as service key", token, strings.Repeat("u", 32)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/"+endpoint.path, tc.token, tc.key, `{}`, nil)
					langGraphWantStatus(t, w, response, 401, "UNAUTHENTICATED")
				})
			}
			if activeCalls != 0 || len(runner.calls) != 0 || len(runner.plans) != 0 {
				t.Fatal("invalid credentials reached persisted users or executor")
			}
			for _, secret := range []string{token, langGraphTestKey, strings.Repeat("t", 32), strings.Repeat("u", 32)} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("request log contains credentials")
				}
			}
		})
	}
}

func TestLangGraphDelegationBindsFiveRolesToOwnerTaskAnd120Seconds(t *testing.T) {
	h, auth, logs := langGraphTestHandler(t, &langGraphRunnerFixture{}, langGraphActive)
	userToken := langGraphToken(t, auth, "user", "")
	for _, role := range []string{"supervisor", "billing", "savings", "subscription", "lifestyle"} {
		t.Run(role, func(t *testing.T) {
			body := langGraphJSON(t, map[string]any{"task_id": langGraphTestTask, "agent_type": role})
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations?user_id=999", userToken, langGraphTestKey, body, nil)
			langGraphWantStatus(t, w, response, 200, "OK")
			var delegation struct {
				Token   string `json:"token"`
				Expires int    `json:"expires_in_seconds"`
				Role    string `json:"agent_type"`
				Task    string `json:"task_id"`
			}
			if err := json.Unmarshal(response.Data, &delegation); err != nil {
				t.Fatal(err)
			}
			actor, err := auth.User(delegation.Token)
			if err != nil || actor.Kind != "agent" || actor.UserID != "42" || actor.AgentType != role || actor.TaskID != langGraphTestTask || actor.TokenID == "" {
				t.Fatal("delegation is not signed and bound to its owner, role and task")
			}
			var claims identity.Claims
			if _, err := jwt.ParseWithClaims(delegation.Token, &claims, func(*jwt.Token) (any, error) { return []byte(strings.Repeat("u", 32)), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("qingyingjin"), jwt.WithAudience("banking-service")); err != nil {
				t.Fatal(err)
			}
			if delegation.Expires != 120 || claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 120*time.Second || delegation.Role != role || delegation.Task != langGraphTestTask {
				t.Fatal("delegation has incorrect task, role or lifetime")
			}
			for _, scope := range actor.Scopes {
				if scope == "execution:authorize" || scope == "execution:execute" || scope == "transfer:approve" {
					t.Fatal("agent delegation grants financial execution")
				}
			}
			if strings.Contains(logs.String(), delegation.Token) {
				t.Fatal("delegated JWT was logged")
			}
		})
	}
	if strings.Contains(logs.String(), userToken) || strings.Contains(logs.String(), langGraphTestKey) {
		t.Fatal("delegation request credentials were logged")
	}
}

func TestLangGraphDelegationRejectsInvalidNamespaceRoleAndIdentityOverrides(t *testing.T) {
	runner := &langGraphRunnerFixture{}
	h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
	token := langGraphToken(t, auth, "user", "")
	for _, task := range []string{
		"old-go-job.billing", "01234567-89ab-cdef-8123-0123456789ab.billing", "lg-01234567-89AB-cdef-8123-0123456789ab.billing",
		"lg-01234567-89ab-cdef-8123-0123456789az.billing", "lg-01234567-89ab-cdef-8123-0123456789ab.",
		"lg-01234567-89ab-cdef-8123-0123456789ab._billing", "lg-01234567-89ab-cdef-8123-0123456789ab.billing.extra",
		"lg-01234567-89ab-cdef-8123-0123456789ab." + strings.Repeat("b", 65),
	} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", token, langGraphTestKey, langGraphJSON(t, map[string]any{"task_id": task, "agent_type": "billing"}), nil)
		langGraphWantStatus(t, w, response, 400, "INVALID_ARGUMENT")
	}
	for _, role := range []string{"", "gateway", "executor", "Billing", "billing "} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", token, langGraphTestKey, langGraphJSON(t, map[string]any{"task_id": langGraphTestTask, "agent_type": role}), nil)
		langGraphWantStatus(t, w, response, 400, "INVALID_ARGUMENT")
	}
	for _, extra := range []string{"user_id", "token", "scopes", "expires_in_seconds"} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", token, langGraphTestKey, langGraphJSON(t, map[string]any{"task_id": langGraphTestTask, "agent_type": "billing", extra: "999"}), nil)
		langGraphWantStatus(t, w, response, 400, "INVALID_ARGUMENT")
	}
	agent := langGraphToken(t, auth, "billing", langGraphTestTask)
	w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", agent, langGraphTestKey, `{}`, nil)
	langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
	if len(runner.calls) != 0 || len(runner.plans) != 0 {
		t.Fatal("delegation used business executor")
	}
}

func TestLangGraphRequiresActivePersistedJWTUser(t *testing.T) {
	for _, endpoint := range []struct{ path, role string }{{"delegations", "user"}, {"run", "billing"}, {"plan", "supervisor"}} {
		t.Run(endpoint.path, func(t *testing.T) {
			runner := &langGraphRunnerFixture{}
			var checked int64
			h, auth, _ := langGraphTestHandler(t, runner, func(_ context.Context, id int64) error {
				checked = id
				return entity.Fail("FORBIDDEN", "active simulation user required")
			})
			token := langGraphToken(t, auth, endpoint.role, langGraphTestTask)
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/"+endpoint.path+"?user_id=999", token, langGraphTestKey, `{}`, nil)
			langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
			if checked != 42 || len(runner.calls) != 0 || len(runner.plans) != 0 {
				t.Fatal("inactive JWT owner reached executor, or caller changed checked owner")
			}
		})
	}
}

func TestLangGraphRunUsesSignedOwnerRoleTaskAndRegisteredReadPlanSkills(t *testing.T) {
	for _, tc := range []struct{ role, skill, tool, mode string }{
		{"billing", "billing_analysis", "detect_anomalies", "read_only"},
		{"subscription", "subscription_analysis", "suggest_subscription_actions", "read_only"},
		{"savings", "savings_planning", "get_financial_plan", "read_only"},
		{"savings", "savings_planning", "replan_financial_plan", "advice"},
		{"lifestyle", "lifestyle_planning", "get_lifestyle_plan", "read_only"},
		{"lifestyle", "lifestyle_planning", "compile_lifestyle_plan", "advice"},
	} {
		t.Run(tc.role+"/"+tc.tool, func(t *testing.T) {
			runner := &langGraphRunnerFixture{run: func(ctx context.Context, call orchestrator.Call) (orchestrator.AgentOutput, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 60*time.Second {
					t.Error("specialist call is not bounded by 60 second deadline")
				}
				return orchestrator.AgentOutput{TaskID: call.TaskID, AgentType: call.AgentType, Status: "succeeded", SkillID: "untrusted-output-skill", SkillVersion: 99, Result: json.RawMessage(`{}`)}, nil
			}}
			h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
			token := langGraphToken(t, auth, tc.role, langGraphTestTask)
			body := langGraphJSON(t, map[string]any{"skill_id": tc.skill, "skill_version": 1, "tool": tc.tool, "execution_mode": tc.mode, "parameters": map[string]any{}, "use_coze": true})
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run?user_id=999&task_id=old-job&agent_type=supervisor", token, langGraphTestKey, body, map[string]string{"X-Task-ID": "spoofed-task", "X-Trace-ID": "bridge-test"})
			langGraphWantStatus(t, w, response, 200, "OK")
			if len(runner.calls) != 1 {
				t.Fatal("registered skill did not call executor once")
			}
			call := runner.calls[0]
			if call.Meta.UserID != 42 || call.TaskID != langGraphTestTask || call.Meta.TaskID != langGraphTestTask || call.AgentType != tc.role || call.SkillID != tc.skill || call.SkillVersion != 1 || call.Mode != tc.mode || call.Meta.TraceID != "bridge-test" || !call.UseCoze || call.JobID != "" || call.Confirmed || call.InvocationKey != "langgraph:"+langGraphTestTask || response.TaskID != langGraphTestTask {
				t.Fatal("executor call lost signed identity, skill binding or LangGraph ownership")
			}
			var out orchestrator.AgentOutput
			if err := json.Unmarshal(response.Data, &out); err != nil {
				t.Fatal(err)
			}
			if out.SkillID != tc.skill || out.SkillVersion != 1 || out.RuleVersion != "1" || out.TaskID != langGraphTestTask || out.AgentType != tc.role || out.Status != "succeeded" || out.FinancialExecutionEnabled {
				t.Fatal("result does not expose authoritative identity and skill with execution disabled")
			}
			serialized := langGraphJSON(t, call)
			if strings.Contains(serialized, token) || strings.Contains(serialized, langGraphTestKey) {
				t.Fatal("executor call includes HTTP credentials")
			}
		})
	}
}

func TestLangGraphRunRejectsRoleVersionModeAndPrepareExecuteTools(t *testing.T) {
	for _, tc := range []struct {
		name, role, skill, tool, mode string
		version                       int64
	}{
		{"wrong role", "subscription", "billing_analysis", "detect_anomalies", "read_only", 1},
		{"supervisor cannot run", "supervisor", "task_planning", "plan_instruction", "read_only", 1},
		{"missing skill", "billing", "", "detect_anomalies", "read_only", 1},
		{"missing version", "billing", "billing_analysis", "detect_anomalies", "read_only", 0},
		{"unknown version", "billing", "billing_analysis", "detect_anomalies", "read_only", 2},
		{"missing explicit binding", "billing", "", "detect_anomalies", "read_only", 0},
		{"unknown skill", "billing", "made_up", "detect_anomalies", "read_only", 1},
		{"execute mode", "billing", "billing_analysis", "detect_anomalies", "execute", 1},
		{"prepare mode", "billing", "billing_analysis", "detect_anomalies", "prepare", 1},
		{"prepare cancellation", "subscription", "operation_preparation", "create_cancel_intent", "intent", 1},
		{"prepare gateway", "supervisor", "operation_preparation", "require_gateway", "intent", 1},
		{"financial execute tool", "billing", "billing_analysis", "execute_transfer", "read_only", 1},
		{"confirmation", "supervisor", "task_planning", "request_confirmation", "user_confirmation", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &langGraphRunnerFixture{}
			h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
			body := langGraphJSON(t, map[string]any{"skill_id": tc.skill, "skill_version": tc.version, "tool": tc.tool, "execution_mode": tc.mode, "parameters": map[string]any{}})
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", langGraphToken(t, auth, tc.role, langGraphTestTask), langGraphTestKey, body, nil)
			langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
			if len(runner.calls) != 0 {
				t.Fatal("forbidden skill reached executor")
			}
		})
	}
}

func TestLangGraphRunRequiresExplicitMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		includeMode bool
	}{
		{"missing execution mode", false},
		{"empty execution mode", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &langGraphRunnerFixture{}
			h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
			body := map[string]any{"skill_id": "billing_analysis", "skill_version": 1, "tool": "detect_anomalies", "parameters": map[string]any{}}
			if tc.includeMode {
				body["execution_mode"] = ""
			}
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", langGraphToken(t, auth, "billing", langGraphTestTask), langGraphTestKey, langGraphJSON(t, body), nil)
			langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
			if len(runner.calls) != 0 {
				t.Fatal("inferred execution mode reached executor")
			}
		})
	}
}

func TestLangGraphRunRejectsUserOldTaskAndBodyIdentityOverrides(t *testing.T) {
	runner := &langGraphRunnerFixture{}
	h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
	body := map[string]any{"skill_id": "billing_analysis", "skill_version": 1, "tool": "detect_anomalies", "parameters": map[string]any{}}
	for _, token := range []string{langGraphToken(t, auth, "user", ""), langGraphToken(t, auth, "billing", "old-go-task"), langGraphToken(t, auth, "billing", "lg-01234567-89AB-cdef-8123-0123456789ab.billing")} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", token, langGraphTestKey, langGraphJSON(t, body), nil)
		langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
	}
	for _, field := range []string{"user_id", "agent_type", "task_id", "confirmed", "token", "job_id"} {
		in := map[string]any{}
		for key, value := range body {
			in[key] = value
		}
		in[field] = "999"
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", langGraphToken(t, auth, "billing", langGraphTestTask), langGraphTestKey, langGraphJSON(t, in), nil)
		langGraphWantStatus(t, w, response, 400, "INVALID_ARGUMENT")
	}
	if len(runner.calls) != 0 {
		t.Fatal("caller-supplied identity reached executor")
	}
}

func TestLangGraphRunRejectsMismatchedOrExecutingOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*orchestrator.AgentOutput)
	}{
		{"task", func(out *orchestrator.AgentOutput) { out.TaskID = "old-task" }},
		{"role", func(out *orchestrator.AgentOutput) { out.AgentType = "supervisor" }},
		{"status", func(out *orchestrator.AgentOutput) { out.Status = "failed" }},
		{"financial execution", func(out *orchestrator.AgentOutput) { out.FinancialExecutionEnabled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &langGraphRunnerFixture{run: func(_ context.Context, call orchestrator.Call) (orchestrator.AgentOutput, error) {
				out := orchestrator.AgentOutput{TaskID: call.TaskID, AgentType: call.AgentType, Status: "succeeded", Result: json.RawMessage(`{}`)}
				tc.alter(&out)
				return out, nil
			}}
			h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", langGraphToken(t, auth, "billing", langGraphTestTask), langGraphTestKey, `{"skill_id":"billing_analysis","skill_version":1,"tool":"detect_anomalies","execution_mode":"read_only","parameters":{}}`, nil)
			langGraphWantStatus(t, w, response, 422, "TOOL_OUTPUT_INVALID")
			if len(response.Data) != 0 {
				t.Fatal("invalid output was returned to worker")
			}
		})
	}
}

func TestLangGraphPlanRequiresSupervisorAndKeepsLangGraphOwnership(t *testing.T) {
	runner := &langGraphRunnerFixture{plan: func(ctx context.Context, meta service.Meta, instruction string, bindings orchestrator.Bindings) ([]orchestrator.TaskSpec, string, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 60*time.Second {
			t.Error("Supervisor call has no bounded deadline")
		}
		if instruction != "analyze monthly spending" || bindings.AccountID != "77" || meta.UserID != 42 || meta.TaskID != langGraphTestTask {
			t.Error("Supervisor input or signed owner/task was replaced")
		}
		return []orchestrator.TaskSpec{{Key: "billing_1", AgentType: "billing", Tool: "detect_anomalies", SkillID: "billing_analysis", SkillVersion: 1}}, "coze-execute-1", nil
	}}
	h, auth, _ := langGraphTestHandler(t, runner, langGraphActive)
	body := `{"instruction":"analyze monthly spending","bindings":{"account_id":"77"}}`
	for _, role := range []string{"user", "billing", "subscription", "savings", "lifestyle"} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/plan", langGraphToken(t, auth, role, langGraphTestTask), langGraphTestKey, body, nil)
		langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
	}
	token := langGraphToken(t, auth, "supervisor", langGraphTestTask)
	w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/plan?user_id=999", token, langGraphTestKey, body, map[string]string{"X-Task-ID": "spoofed-task"})
	langGraphWantStatus(t, w, response, 200, "OK")
	var out struct {
		Tasks     []orchestrator.TaskSpec `json:"tasks"`
		ExecuteID string                  `json:"workflow_execute_id"`
		Owner     string                  `json:"owner_engine"`
		Financial bool                    `json:"financial_execution_enabled"`
	}
	if err := json.Unmarshal(response.Data, &out); err != nil {
		t.Fatal(err)
	}
	if len(runner.plans) != 1 || len(runner.calls) != 0 || len(out.Tasks) != 1 || out.ExecuteID != "coze-execute-1" || out.Owner != "langgraph" || out.Financial || response.TaskID != langGraphTestTask || bytes.Contains(response.Data, []byte(`"job_id"`)) {
		t.Fatal("Supervisor result lost Coze evidence or created a legacy Go job")
	}
	for _, invalid := range []string{`{"instruction":" "}`, langGraphJSON(t, map[string]any{"instruction": strings.Repeat("x", 8193)}), `{"instruction":"valid","user_id":"999"}`, `{"instruction":"valid","task_id":"old-task"}`} {
		w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/plan", token, langGraphTestKey, invalid, nil)
		langGraphWantStatus(t, w, response, 400, "INVALID_ARGUMENT")
	}
	if len(runner.plans) != 1 {
		t.Fatal("invalid Supervisor request reached Coze planner")
	}
}

func TestLangGraphBridgePreservesExistingRoutesAndAgentMutationDenials(t *testing.T) {
	h, auth, _ := langGraphTestHandler(t, &langGraphRunnerFixture{}, langGraphActive)
	user := langGraphToken(t, auth, "user", "")
	for _, tc := range []struct {
		method, path, token, tool string
		status                    int
		code                      string
	}{
		{"GET", "/healthz", "", "", 200, "OK"},
		{"GET", "/api/v1/context", user, "", 200, "OK"},
		{"GET", "/api/v1/skills", user, "", 200, "OK"},
		{"GET", "/api/v1/accounts", user, "", 503, "DEPENDENCY_UNAVAILABLE"},
		{"POST", "/api/v1/billing/detect_anomalies", user, "", 503, "DEPENDENCY_UNAVAILABLE"},
		{"GET", "/api/v1/financial-plans/77", user, "", 503, "DEPENDENCY_UNAVAILABLE"},
		{"POST", "/api/v1/orchestrations", user, "", 503, "DEPENDENCY_UNAVAILABLE"},
		{"GET", "/api/v2/lifestyle-goals/77", user, "", 503, "DEPENDENCY_UNAVAILABLE"},
		{"GET", "/tools/v1/integration/context", "", strings.Repeat("t", 32), 200, "OK"},
		{"POST", "/api/v1/langgraph/execute", user, "", 404, "NOT_FOUND"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w, response := langGraphRequest(t, h, tc.method, tc.path, tc.token, "", `{}`, map[string]string{"X-Banking-Tool-Key": tc.tool})
			langGraphWantStatus(t, w, response, tc.status, tc.code)
		})
	}
	agent := langGraphToken(t, auth, "billing", langGraphTestTask)
	for _, path := range []string{"/api/v1/transfers", "/api/v1/simulation/accounts", "/api/v1/users/me", "/api/v1/subscriptions/create_cancel_intent"} {
		w, response := langGraphRequest(t, h, "POST", path, agent, langGraphTestKey, `{}`, nil)
		langGraphWantStatus(t, w, response, 403, "FORBIDDEN")
	}
}

type langGraphWorkflowFixture func(context.Context, coze.RunRequest) (coze.RunResponse, error)

func (f langGraphWorkflowFixture) RunSyncWorkflow(ctx context.Context, request coze.RunRequest) (coze.RunResponse, error) {
	return f(ctx, request)
}

func TestLangGraphRealExecutorPreservesBankingValidationAndCozeEvidence(t *testing.T) {
	period, err := billing.Normalize(billing.Input{From: "2026-10-01T00:00:00Z", To: "2026-11-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := billing.Analyze(42, "detect_anomalies", period, billing.Snapshot{At: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	report.AuditID = "bridge-test-audit"
	for _, tc := range []struct {
		name, mutation, code  string
		status, banks, models int
	}{
		{"valid evidence and explanation", "", "OK", 200, 1, 1},
		{"parameter owner injection", "parameter", "INVALID_ARGUMENT", 400, 0, 0},
		{"Banking returned another owner", "owner", "TOOL_OUTPUT_INVALID", 422, 1, 0},
		{"Banking enabled financial execution", "financial", "TOOL_OUTPUT_INVALID", 422, 1, 0},
		{"Coze invented evidence", "evidence", "MODEL_OUTPUT_INVALID", 422, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, auth, _ := testHandler(t, false)
			bankCalls, modelCalls := 0, 0
			var bankToken string
			bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				bankCalls++
				bankToken = strings.TrimPrefix(q.Header.Get("Authorization"), "Bearer ")
				actor, err := auth.User(bankToken)
				if err != nil || actor.Kind != "agent" || actor.UserID != "42" || actor.AgentType != "billing" || actor.TaskID != langGraphTestTask {
					t.Error("real executor's protected Banking request has invalid signed identity")
				}
				if q.URL.Path != "/api/v1/billing/detect_anomalies" || q.Method != "POST" || q.Header.Get("X-Task-ID") != langGraphTestTask || q.Header.Get("X-Banking-Agent-Key") != "" {
					t.Error("real executor used incorrect protected route/correlation or leaked service key")
				}
				result := report
				if tc.mutation == "owner" {
					result.UserID = 999
				}
				if tc.mutation == "financial" {
					result.FinancialExecutionEnabled = true
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": result}); err != nil {
					t.Error(err)
				}
			}))
			defer bank.Close()
			var callerToken string
			workflow := langGraphWorkflowFixture(func(_ context.Context, request coze.RunRequest) (coze.RunResponse, error) {
				modelCalls++
				payload := langGraphJSON(t, request)
				for _, secret := range []string{callerToken, bankToken, langGraphTestKey, strings.Repeat("u", 32)} {
					if secret != "" && strings.Contains(payload, secret) {
						t.Error("Coze model received Banking credentials")
					}
				}
				if request.WorkflowID != "123" {
					t.Error("wrong specialist workflow")
				}
				evidence := report.ID
				if tc.mutation == "evidence" {
					evidence = "invented-report"
				}
				output := map[string]any{"task_id": langGraphTestTask, "agent_type": "billing", "explanation": "Analysis uses the validated transaction evidence.", "evidence_ids": []string{evidence}}
				zero := int64(0)
				return coze.RunResponse{Code: &zero, ExecuteID: "bridge-coze-execute", Data: langGraphJSON(t, map[string]any{"output": output})}, nil
			})
			executor, err := orchestrator.NewHTTPExecutor(bank.URL, auth, workflow, map[string]string{"billing": "123"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			bridge := NewLangGraphBridge(langGraphTestKey, executor)
			bridge.ActiveUser = langGraphActive
			h := NewWithLifestyle(auth, slog.New(slog.NewJSONHandler(&logs, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, nil, nil, bridge)
			callerToken = langGraphToken(t, auth, "billing", langGraphTestTask)
			parameters := map[string]any{"from": "2026-10-01T00:00:00Z", "to": "2026-11-01T00:00:00Z"}
			if tc.mutation == "parameter" {
				parameters["user_id"] = "999"
			}
			body := langGraphJSON(t, map[string]any{"skill_id": "billing_analysis", "skill_version": 1, "tool": "detect_anomalies", "execution_mode": "read_only", "parameters": parameters, "use_coze": true})
			w, response := langGraphRequest(t, h, "POST", "/api/v1/langgraph/run", callerToken, langGraphTestKey, body, nil)
			langGraphWantStatus(t, w, response, tc.status, tc.code)
			if bankCalls != tc.banks || modelCalls != tc.models {
				t.Fatalf("downstream calls: Banking=%d Coze=%d, want %d/%d", bankCalls, modelCalls, tc.banks, tc.models)
			}
			if tc.status == 200 {
				var out orchestrator.AgentOutput
				if err := json.Unmarshal(response.Data, &out); err != nil {
					t.Fatal(err)
				}
				var actual billing.Report
				if err := json.Unmarshal(out.Result, &actual); err != nil {
					t.Fatal(err)
				}
				if out.WorkflowExecuteID != "bridge-coze-execute" || out.Explanation == "" || len(out.ExplanationEvidenceIDs) != 1 || out.ExplanationEvidenceIDs[0] != report.ID || actual.ID != report.ID || actual.UserID != 42 || actual.TotalExpense != report.TotalExpense || out.FinancialExecutionEnabled {
					t.Fatal("Coze explanation replaced protected Banking facts or evidence")
				}
			}
			for _, secret := range []string{callerToken, bankToken, langGraphTestKey} {
				if secret != "" && strings.Contains(logs.String(), secret) {
					t.Fatal("real executor bridge logged credentials")
				}
			}
		})
	}
}
