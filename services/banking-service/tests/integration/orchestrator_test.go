//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type orchestrationFlushFixture struct {
	http.ResponseWriter
	once     *sync.Once
	complete func()
}

func (w orchestrationFlushFixture) Flush() {
	w.once.Do(w.complete)
	w.ResponseWriter.(http.Flusher).Flush()
}

func TestOrchestratorProtectedAPIAndSSE(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bills := billing.New(billing.NewRepository(h.conn.DB), nil)
	subs := subscription.New(repository.New(h.conn.DB), risk.SimulationGateway{}, subscription.LocalSimulator{})
	plans := planning.New(h.conn.DB)
	// Real protected Banking APIs, not fixture tool results.
	toolServer := httptest.NewServer(httpapi.NewWithPlanning(h.auth, log, nil, time.Second, h.svc, nil, bills, subs, plans))
	t.Cleanup(toolServer.Close)
	executor, e := orchestrator.NewHTTPExecutor(toolServer.URL, h.auth, nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	jobs := orchestrator.New(h.conn.DB, executor, executor)
	h.server.Close()
	h.server = httptest.NewServer(httpapi.NewWithOrchestration(h.auth, log, nil, time.Second, h.svc, nil, bills, subs, plans, jobs))
	t.Cleanup(h.server.Close)
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: h.server.URL}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	a := h.account(t, 100000)
	goal := parse[entity.SavingsGoal](t, h.call(t, "POST", "/api/v1/savings/goals", uuid.NewString(), map[string]any{"name": "模块7模拟健康目标", "target_minor": 60000, "monthly_minor": 20000, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "ends_at": time.Now().AddDate(0, 3, 0).UnixMilli()}, 201))
	planned := parse[planning.View](t, h.call(t, "POST", "/api/v1/financial-plans", "", planning.CreateInput{Name: "module7 real planning", Settings: planning.Settings{AccountIDs: []int64{a.ID}, SavingsGoalIDs: []int64{goal.ID}, LifeGoalIDs: []int64{}, LivingRemainingMinor: cents(30000), EssentialRemainingMinor: cents(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000, FutureIncomeMinor: cents(0), Cashflows: []planning.Cashflow{}}}, 201))
	at, _ := time.Parse(time.RFC3339, "2026-01-12T12:00:00Z")
	for i := range 2 {
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{"bank_account_id": fmt.Sprint(a.ID), "external_transaction_id": uuid.NewString(), "direction": "expense", "amount_minor": 1200, "category": "subscription", "merchant_name": "模拟会员重复证据", "occurred_at": at.UnixMilli() + int64(i)*60000, "effect_mode": "record_only"}, 201)
	}
	period := json.RawMessage(fmt.Sprintf(`{"from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z","account_id":"%d"}`, a.ID))
	key := uuid.NewString()
	input := orchestrator.SubmitInput{Tasks: []orchestrator.TaskSpec{{Key: "bill", AgentType: "billing", Tool: "generate_billing_report", Parameters: period, Dependencies: []string{}}, {Key: "cost", AgentType: "subscription", Tool: "analyze_subscription_cost", Parameters: json.RawMessage(`{}`), Dependencies: []string{"bill"}}}}
	input.Tasks = append(input.Tasks, orchestrator.TaskSpec{Key: "savings", AgentType: "savings", Tool: "replan_financial_plan", Parameters: json.RawMessage(fmt.Sprintf(`{"plan_id":"%d"}`, planned.Plan.ID)), Dependencies: []string{"bill", "cost"}})
	v := parse[orchestrator.View](t, h.call(t, "POST", "/api/v1/orchestrations", key, input, 201))
	defer h.conn.DB.Model(&orchestrator.Job{}).Where("user_id=?", h.uid).Updates(map[string]any{"status": "cancelled", "lease_token": ""})
	dup := parse[orchestrator.View](t, h.call(t, "POST", "/api/v1/orchestrations", key, input, 201))
	if dup.Job.ID != v.Job.ID {
		t.Fatal("API idempotency failed")
	}
	for range 4 {
		if _, e = jobs.ProcessOne(ctx); e != nil {
			t.Fatal(e)
		}
	}
	v = parse[orchestrator.View](t, h.call(t, "GET", "/api/v1/orchestrations/"+v.Job.ID, "", nil, 200))
	if v.Job.Status != "succeeded" || len(v.Tasks) != 4 {
		t.Fatalf("duplicate verification not scheduled: status=%s tasks=%d", v.Job.Status, len(v.Tasks))
	}
	final := parse[orchestrator.Result](t, h.call(t, "GET", "/api/v1/orchestrations/"+v.Job.ID+"/result", "", nil, 200))
	if len(final.Outputs) != 4 || final.FinancialExecutionEnabled {
		t.Fatal("bad final result")
	}
	found := false
	for _, out := range final.Outputs {
		if out.AgentType == "billing" {
			var report billing.Report
			json.Unmarshal(out.Result, &report)
			if report.TotalExpense != 2400 || len(report.EvidenceTransactionIDs) != 2 {
				t.Fatal("actual financial results lost")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing billing result")
	}
	tr := parse[orchestrator.TraceView](t, h.call(t, "GET", "/api/v1/orchestrations/"+v.Job.ID+"/trace", "", nil, 200))
	if len(tr.Executions) != 4 || len(tr.Events) < 5 {
		t.Fatal("missing durable trace")
	}
	request, _ := http.NewRequest("GET", h.server.URL+"/api/v1/orchestrations/"+v.Job.ID+"/events", nil)
	request.Header.Set("Authorization", "Bearer "+h.token)
	request.Header.Set("Last-Event-ID", fmt.Sprint(tr.Events[0].ID))
	resp, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(raw), "event: task") {
		t.Fatalf("SSE status %d: %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "amount_minor") || strings.Contains(string(raw), "merchant_name") {
		t.Fatal("SSE leaked financial payload")
	}
	// Ownership checked before sending the SSE stream headers.
	other := h.uid + 1
	h.conn.DB.Create(&entity.User{ID: other, DisplayName: "other module7 fixture", Status: "active"})
	token, _ := h.auth.IssueUser(fmt.Sprint(other), "", time.Minute)
	request, _ = http.NewRequest("GET", h.server.URL+"/api/v1/orchestrations/"+v.Job.ID+"/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	resp, e = http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("foreign SSE allowed")
	}
	h.balance(t, a.ID, 100000)
	h.balance(t, goal.AccountID, 0)
	t.Run("SSE drains completion committed during replay", func(t *testing.T) {
		pending := parse[orchestrator.View](t, h.call(t, "POST", "/api/v1/orchestrations", uuid.NewString(), orchestrator.SubmitInput{Tasks: []orchestrator.TaskSpec{{Key: "cost", AgentType: "subscription", Tool: "analyze_subscription_cost", Parameters: json.RawMessage(`{}`), Dependencies: []string{}}}}, 201))
		var once sync.Once
		finished := make(chan error, 1)
		race := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := orchestrationFlushFixture{ResponseWriter: w, once: &once, complete: func() { _, err := jobs.ProcessOne(ctx); finished <- err }}
			h.server.Config.Handler.ServeHTTP(wrapped, r)
		}))
		defer race.Close()
		q, _ := http.NewRequest("GET", race.URL+"/api/v1/orchestrations/"+pending.Job.ID+"/events", nil)
		q.Header.Set("Authorization", "Bearer "+h.token)
		response, err := http.DefaultClient.Do(q)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err = <-finished; err != nil {
			t.Fatal(err)
		}
		tail := parse[orchestrator.TraceView](t, h.call(t, "GET", "/api/v1/orchestrations/"+pending.Job.ID+"/trace", "", nil, 200))
		if strings.Count(string(data), "event: task\n") != len(tail.Events) {
			t.Fatal("terminal SSE omitted final committed events", string(data))
		}
	})
}
