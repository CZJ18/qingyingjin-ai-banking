package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func executorAuth() *identity.Auth {
	return identity.New(strings.Repeat("x", 32), "test", "test", "tool", "42", "")
}
func TestSkillRejectedBeforeTransport(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	x, err := NewHTTPExecutor(server.URL, executorAuth(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int64{0, 99} {
		c := billingCall()
		c.SkillID = "billing_analysis"
		c.SkillVersion = version
		if _, err := x.Run(context.Background(), c); entity.Code(err) != "INVALID_TASK_PLAN" {
			t.Fatal(err)
		}
	}
	c := billingCall()
	c.Mode = "execute"
	if _, err := x.Run(context.Background(), c); entity.Code(err) != "INVALID_TASK_PLAN" {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("invalid skill reached banking service")
	}
}

type workflowFixture func(context.Context, coze.RunRequest) (coze.RunResponse, error)

func (f workflowFixture) RunSyncWorkflow(c context.Context, r coze.RunRequest) (coze.RunResponse, error) {
	return f(c, r)
}
func workflowResult(value any) coze.RunResponse {
	b, _ := json.Marshal(value)
	end, _ := json.Marshal(map[string]any{"output": json.RawMessage(b)})
	zero := int64(0)
	return coze.RunResponse{Code: &zero, ExecuteID: "fixture-execute", Data: string(end)}
}
func TestHTTPExecutorCozeCannotReplaceFinancialFacts(t *testing.T) {
	report := billingFixture(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": report})
	}))
	defer s.Close()
	for _, tc := range []struct {
		name   string
		extra  bool
		invent bool
	}{{"explanation", false, false}, {"invented evidence", false, true}, {"extra money", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			runner := workflowFixture(func(ctx context.Context, r coze.RunRequest) (coze.RunResponse, error) {
				if r.WorkflowID != "123" || strings.Contains(r.Parameters["input_string"].(string), "Bearer ") {
					t.Error("workflow configuration or credential leak")
				}
				out := map[string]any{"task_id": "task", "agent_type": "billing", "explanation": "账单分析基于当前交易证据。", "evidence_ids": []string{report.ID}}
				if tc.invent {
					out["evidence_ids"] = []string{"nonexistent"}
				}
				if tc.extra {
					out["total_expense"] = 999
				}
				return workflowResult(out), nil
			})
			x, _ := NewHTTPExecutor(s.URL, executorAuth(), runner, map[string]string{"billing": "123"}, nil)
			call := billingCall()
			call.UseCoze = true
			out, e := x.Run(context.Background(), call)
			if tc.extra || tc.invent {
				if entity.Code(e) != "MODEL_OUTPUT_INVALID" {
					t.Fatal(e)
				}
				return
			}
			if e != nil || out.WorkflowExecuteID != "fixture-execute" {
				t.Fatal(out, e)
			}
			var actual billing.Report
			json.Unmarshal(out.Result, &actual)
			if actual.TotalExpense != report.TotalExpense {
				t.Fatal("LLM changed authoritative amount")
			}
		})
	}
	x, _ := NewHTTPExecutor(s.URL, executorAuth(), nil, nil, nil)
	call := billingCall()
	call.UseCoze = true
	if _, e := x.Run(context.Background(), call); entity.Code(e) != "COZE_UNAVAILABLE" {
		t.Fatal(e)
	}
}
func TestSupervisorPlannerRejectsUntrustedWorkflowPlan(t *testing.T) {
	for _, tool := range []string{"detect_anomalies", "approve_transfer"} {
		t.Run(tool, func(t *testing.T) {
			runner := workflowFixture(func(context.Context, coze.RunRequest) (coze.RunResponse, error) {
				return workflowResult(map[string]any{"tasks": []TaskSpec{{Key: "a", AgentType: "billing", Tool: tool, Parameters: json.RawMessage(`{}`), Dependencies: []string{}, Priority: 1}}}), nil
			})
			x, _ := NewHTTPExecutor("http://127.0.0.1:8090", executorAuth(), runner, map[string]string{"supervisor": "123"}, nil)
			tasks, e := x.Plan(context.Background(), service.Meta{UserID: 42}, "分析账单", Bindings{})
			if tool == "detect_anomalies" {
				if e != nil || len(tasks) != 1 {
					t.Fatal(tasks, e)
				}
			} else if e == nil {
				t.Fatal("Supervisor escalated to final execution")
			}
		})
	}
}
func TestHTTPExecutorCancellationPropagates(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		close(started)
		select {
		case <-q.Context().Done():
		case <-release:
		}
	}))
	defer s.Close()
	defer close(release)
	x, _ := NewHTTPExecutor(s.URL, executorAuth(), nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := x.Run(ctx, billingCall()); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("task cancellation did not stop HTTP call")
	}
}

func TestHTTPExecutorPlanningRecomputesPersistedSnapshot(t *testing.T) {
	snap := planning.Snapshot{Period: "2026-10", BalanceMinor: 100000, Goals: []planning.GoalFunds{{ID: 2, TargetMinor: 10000, MonthlyMinor: 10000, Eligible: true}}}
	calculated, e := planning.Calculate(snap)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, _ := json.Marshal(snap)
	v := planning.View{Plan: planning.FinancialPlan{Base: entity.Base{ID: 1, UserID: 42}, CurrentVersion: 1}, Version: planning.PlanVersion{Base: entity.Base{ID: 3, UserID: 42}, PlanID: 1, Version: 1, SnapshotJSON: snapshot}, Result: calculated}
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "authoritative", true: "fabricated savings"}[changed], func(t *testing.T) {
			response := v
			if changed {
				response.Result.SuggestedSavingsMinor++
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method != "GET" || q.URL.Path != "/api/v1/financial-plans/1" {
					t.Error(q.Method, q.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": response})
			}))
			defer server.Close()
			x, _ := NewHTTPExecutor(server.URL, executorAuth(), nil, nil, nil)
			c := billingCall()
			c.AgentType = "savings"
			c.Tool = "get_financial_plan"
			c.Parameters = json.RawMessage(`{"plan_id":"1"}`)
			_, e := x.Run(context.Background(), c)
			if changed && entity.Code(e) != "TOOL_OUTPUT_INVALID" || !changed && e != nil {
				t.Fatal(e)
			}
		})
	}
}
func billingFixture(t *testing.T) billing.Report {
	t.Helper()
	p, e := billing.Normalize(billing.Input{From: "2026-10-01T00:00:00Z", To: "2026-11-01T00:00:00Z"})
	if e != nil {
		t.Fatal(e)
	}
	r, e := billing.Analyze(42, "detect_anomalies", p, billing.Snapshot{At: time.Now().UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	r.AuditID = "fixture-audit"
	return r
}
func billingCall() Call {
	return Call{Meta: service.Meta{UserID: 42, TraceID: "test", TaskID: "task"}, TaskID: "task", AgentType: "billing", Tool: "detect_anomalies", Parameters: json.RawMessage(`{"from":"2026-10-01T00:00:00Z","to":"2026-11-01T00:00:00Z"}`)}
}
func TestHTTPExecutorBankingIdentityAndStructuredResult(t *testing.T) {
	a := executorAuth()
	r := billingFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		actor, e := a.User(strings.TrimPrefix(q.Header.Get("Authorization"), "Bearer "))
		if e != nil || actor.UserID != "42" {
			t.Error("missing owner-bound JWT")
		}
		if q.URL.Path != "/api/v1/billing/detect_anomalies" || q.Header.Get("X-Task-ID") != "task" {
			t.Error("wrong API correlation")
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": r})
	}))
	defer server.Close()
	x, e := NewHTTPExecutor(server.URL, a, nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	out, e := x.Run(context.Background(), billingCall())
	if e != nil {
		t.Fatal(e)
	}
	if out.Status != "succeeded" || out.TaskID != "task" || out.FinancialExecutionEnabled || len(out.Evidence) == 0 {
		t.Fatal(out)
	}
}
func TestHTTPExecutorRejectsUnsafeBankResults(t *testing.T) {
	good := billingFixture(t)
	raw, _ := json.Marshal(good)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"wrong owner", strings.Replace(string(raw), `"user_id":"42"`, `"user_id":"43"`, 1), 200},
		{"fractional money", strings.Replace(string(raw), `"total_expense":0`, `"total_expense":0.5`, 1), 200},
		{"missing report", `{}`, 200}, {"financial enabled", strings.Replace(string(raw), `"financial_execution_enabled":false`, `"financial_execution_enabled":true`, 1), 200},
		{"HTTP error", string(raw), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"code":"OK","data":` + tc.body + `}`))
			}))
			defer s.Close()
			x, _ := NewHTTPExecutor(s.URL, executorAuth(), nil, nil, nil)
			if _, e := x.Run(context.Background(), billingCall()); e == nil {
				t.Fatal("unsafe tool result accepted")
			}
		})
	}
}
func TestHTTPExecutorNoRedirectNoUngatedProposal(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { hit = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { http.Redirect(w, q, target.URL, 302) }))
	defer s.Close()
	x, _ := NewHTTPExecutor(s.URL, executorAuth(), nil, nil, nil)
	if _, e := x.Run(context.Background(), billingCall()); e == nil || hit {
		t.Fatal("redirect followed")
	}
	c := billingCall()
	c.AgentType = "subscription"
	c.Tool = "create_cancel_intent"
	c.Parameters = json.RawMessage(`{"subscription_id":"1","expected_version":1}`)
	c.Confirmed = true
	if _, e := x.Run(context.Background(), c); entity.Code(e) != "GATEWAY_UNAVAILABLE" {
		t.Fatal(e)
	}
	c.Tool = "approve_transfer"
	if _, e := x.Run(context.Background(), c); e == nil {
		t.Fatal("execution tool allowed")
	}
}
