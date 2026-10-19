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
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/service"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLifestyleMySQLAPI(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	lives := lifestyle.New(h.conn.DB)
	bills := billing.New(billing.NewRepository(h.conn.DB), nil)
	h.server.Close()
	h.server = httptest.NewServer(httpapi.NewWithLifestyle(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, bills, nil, nil, nil, nil, lives))
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
	spec := lifestyle.Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 100000, HealthBackground: "减掉10斤", AdjustableCategories: []string{"shopping"}, ProtectedCategories: []string{}, ProhibitedOperations: []string{"transfer"}, Evidence: map[string]string{"period_months": "三个月", "expense_reduction_monthly_minor": "每月少花1000元", "adjustable:shopping": "可以调整shopping", "prohibited:transfer": "禁止转账"}}
	in := lifestyle.GoalInput{Text: "三个月减掉10斤，每月少花1000元，可以调整shopping，禁止转账", Spec: spec}
	var goal lifestyle.GoalView
	key := uuid.NewString()
	r := h.call(t, "POST", "/api/v2/lifestyle-goals", key, in, 201)
	if e = json.Unmarshal(r.Data, &goal); e != nil {
		t.Fatal(e)
	}
	id := goal.Goal.ID
	path := fmt.Sprintf("/api/v2/lifestyle-goals/%d", id)
	t.Run("idempotent create and conflicting key", func(t *testing.T) {
		r := h.call(t, "POST", "/api/v2/lifestyle-goals", key, in, 201)
		var got lifestyle.GoalView
		json.Unmarshal(r.Data, &got)
		if got.Goal.ID != id {
			t.Fatal("duplicate goal")
		}
		different := in
		different.Text += "保持正常生活"
		h.call(t, "POST", "/api/v2/lifestyle-goals", key, different, 409)
	})
	t.Run("missing budgets produce no amounts", func(t *testing.T) {
		r := h.call(t, "POST", path+"/compile", uuid.NewString(), lifestyle.CompileInput{ExpectedVersion: 1}, 201)
		var view lifestyle.PlanView
		json.Unmarshal(r.Data, &view)
		var result lifestyle.Result
		json.Unmarshal(view.Plan.ResultJSON, &result)
		if result.Status != "needs_information" || len(result.Options) != 0 || len(view.Recommendations) != 0 {
			t.Fatal(string(r.Data))
		}
	})
	var account entity.BankAccount
	r = h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), map[string]any{"display_name": "Lifestyle fixture", "initial_balance_minor": 1000000, "confirm_simulated_funds": true}, 201)
	json.Unmarshal(r.Data, &account)
	at := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, v := range []struct {
		cat    string
		amount int64
	}{{"food", 70000}, {"shopping", 60000}} {
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{"bank_account_id": fmt.Sprint(account.ID), "external_transaction_id": uuid.NewString(), "direction": "expense", "amount_minor": v.amount, "category": v.cat, "merchant_name": "模拟生活消费", "occurred_at": at, "effect_mode": "record_only"}, 201)
	}
	var budget entity.Budget
	r = h.call(t, "POST", "/api/v1/budgets", uuid.NewString(), map[string]any{"month": time.Now().UTC().Format("2006-01"), "total_minor": 200000, "enforce_reserve": true, "categories": []map[string]any{{"category": "food", "allocated_minor": 80000, "essential_reserve_minor": 80000}, {"category": "shopping", "allocated_minor": 120000, "essential_reserve_minor": 0}}}, 201)
	json.Unmarshal(r.Data, &budget)
	report, e := bills.Run(ctx, service.Meta{UserID: h.uid, TraceID: "p2-fixture"}, "generate_billing_report", billing.Input{From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z", Timezone: "UTC"})
	if e != nil {
		t.Fatal(e)
	}
	compile := lifestyle.CompileInput{ExpectedVersion: 1, BudgetID: budget.ID, BillingReportID: report.ID}
	compileKey := uuid.NewString()
	r = h.call(t, "POST", path+"/compile", compileKey, compile, 201)
	var plan lifestyle.PlanView
	json.Unmarshal(r.Data, &plan)
	t.Run("protected meals and multi goal conflict", func(t *testing.T) {
		var result lifestyle.Result
		json.Unmarshal(plan.Plan.ResultJSON, &result)
		if result.Status != "conflict" || result.ReductionGapMinor != 40000 || len(plan.Recommendations) != 3 {
			t.Fatal(string(r.Data))
		}
		for _, opt := range result.Options {
			if opt.Categories[0].ProposedMinor != 80000 || opt.SavingsMonthlyMinor != 0 {
				t.Fatal(opt)
			}
		}
	})
	t.Run("idempotent compilation", func(t *testing.T) {
		r := h.call(t, "POST", path+"/compile", compileKey, compile, 201)
		var same lifestyle.PlanView
		json.Unmarshal(r.Data, &same)
		if same.Plan.ID != plan.Plan.ID {
			t.Fatal("duplicate plan")
		}
	})
	t.Run("ownership and transaction rollback", func(t *testing.T) {
		other, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		status, _, e := h.raw("GET", path, "", nil, other)
		if e != nil || status != 404 {
			t.Fatal(status, e)
		}
		before, _ := lives.Plans(ctx, h.uid, id)
		bad := compile
		bad.BudgetID = budget.ID + 10000000
		h.call(t, "POST", path+"/compile", uuid.NewString(), bad, 404)
		after, _ := lives.Plans(ctx, h.uid, id)
		if len(before) != len(after) {
			t.Fatal("failed compile persisted partial plan")
		}
	})
	t.Run("unconfigured model returns truthful error", func(t *testing.T) {
		h.call(t, "POST", "/api/v2/lifestyle-goals/parse", "", lifestyle.ParseInput{Text: "我要健康生活"}, 503)
	})
	rec := plan.Recommendations[2]
	decision := lifestyle.DecisionInput{ExpectedVersion: rec.Version, Decision: "accept", PlanDigest: plan.Plan.InputDigest}
	dp := fmt.Sprintf("/api/v2/recommendations/%d/decisions", rec.ID)
	t.Run("agent cannot confirm user preferences", func(t *testing.T) {
		for _, role := range []string{"lifestyle", "billing"} {
			token, _ := h.auth.IssueAgent(fmt.Sprint(h.uid), role, "p2-test", time.Minute)
			status, _, e := h.raw("POST", dp, uuid.NewString(), decision, token)
			if e != nil || status != 403 {
				t.Fatal(role, status, e)
			}
		}
	})
	t.Run("signed specialist and supervisor call", func(t *testing.T) {
		executor, e := orchestrator.NewHTTPExecutor(h.server.URL, h.auth, nil, nil, nil, outbound.TestPolicy())
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(lifestyle.IDInput{PlanID: plan.Plan.ID})
		out, e := executor.Run(ctx, orchestrator.Call{TaskID: "p2-read", AgentType: "lifestyle", Tool: "get_lifestyle_plan", Mode: "read_only", Meta: service.Meta{UserID: h.uid, TraceID: "p2-test"}, Parameters: raw})
		if e != nil || len(out.Evidence) == 0 {
			t.Fatal(e, out)
		}
		token, _ := h.auth.IssueAgent(fmt.Sprint(h.uid), "billing", "p2-wrong", time.Minute)
		status, _, e := h.raw("GET", fmt.Sprintf("/api/v2/lifestyle-plans/%d", plan.Plan.ID), "", nil, token)
		if e != nil || status != 403 {
			t.Fatal(status, e)
		}
	})
	t.Run("agent compiles advisory plan with durable idempotency", func(t *testing.T) {
		other, e := lives.SaveGoal(ctx, service.Meta{UserID: h.uid}, uuid.NewString(), 0, in)
		if e != nil {
			t.Fatal(e)
		}
		executor, e := orchestrator.NewHTTPExecutor(h.server.URL, h.auth, nil, nil, nil, outbound.TestPolicy())
		if e != nil {
			t.Fatal(e)
		}
		ci := lifestyle.CompileToolInput{GoalID: other.Goal.ID, CompileInput: compile}
		raw, _ := json.Marshal(ci)
		call := orchestrator.Call{TaskID: "p2-compile", AgentType: "lifestyle", Tool: "compile_lifestyle_plan", Mode: "advice", Meta: service.Meta{UserID: h.uid, TraceID: "p2-compile"}, Parameters: raw, InvocationKey: uuid.NewString()}
		first, e := executor.Run(ctx, call)
		if e != nil {
			t.Fatal(e)
		}
		again, e := executor.Run(ctx, call)
		if e != nil || string(first.Result) != string(again.Result) {
			t.Fatal("advisory compile replay", e)
		}
		var view lifestyle.PlanView
		if e = json.Unmarshal(first.Result, &view); e != nil || len(view.Recommendations) != 3 {
			t.Fatal(e, view)
		}
		call.AgentType = "billing"
		if _, e = executor.Run(ctx, call); e == nil {
			t.Fatal("wrong role compiled plan")
		}
	})
	t.Run("concurrent decision exactly once", func(t *testing.T) {
		dk := uuid.NewString()
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		ids := make(chan int64, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				status, r, e := h.raw("POST", dp, dk, decision, h.token)
				if e != nil || status != 200 {
					errs <- fmt.Errorf("status %d: %v", status, e)
					return
				}
				var d lifestyle.Decision
				if e = json.Unmarshal(r.Data, &d); e != nil {
					errs <- e
					return
				}
				ids <- d.ID
			}()
		}
		wg.Wait()
		close(errs)
		close(ids)
		for e := range errs {
			t.Error(e)
		}
		var first int64
		for id := range ids {
			if id <= 0 {
				t.Fatal("missing decision ID")
			}
			if first == 0 {
				first = id
			}
			if id != first {
				t.Fatal("duplicate decision")
			}
		}
		h.call(t, "POST", dp, uuid.NewString(), decision, 409)
		var after entity.BankAccount
		h.conn.DB.First(&after, account.ID)
		if after.BalanceMinor != 1000000 {
			t.Fatal("suggestion changed cash")
		}
		var authCount int64
		h.conn.DB.Table("banking_user_authorizations").Where("user_id=?", h.uid).Count(&authCount)
		if authCount != 0 {
			t.Fatal("suggestion created execution authority")
		}
	})
	t.Run("source mutation invalidates pending decision", func(t *testing.T) {
		r := h.call(t, "POST", path+"/compile", uuid.NewString(), compile, 201)
		var newer lifestyle.PlanView
		json.Unmarshal(r.Data, &newer)
		h.conn.DB.Model(&entity.BudgetCategory{}).Where("user_id=? AND budget_id=? AND category='shopping'", h.uid, budget.ID).Update("allocated_minor", 119999)
		p := fmt.Sprintf("/api/v2/recommendations/%d/decisions", newer.Recommendations[0].ID)
		h.call(t, "POST", p, uuid.NewString(), lifestyle.DecisionInput{ExpectedVersion: 1, Decision: "accept", PlanDigest: newer.Plan.InputDigest}, 409)
	})
	t.Run("optimistic concurrent goal update", func(t *testing.T) {
		updated := in
		updated.ExpectedVersion = 1
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				status, _, e := h.raw("PUT", path, uuid.NewString(), updated, h.token)
				if e != nil {
					t.Error(e)
				}
				statuses <- status
			}()
		}
		wg.Wait()
		close(statuses)
		success, conflict := 0, 0
		for status := range statuses {
			if status == 200 {
				success++
			}
			if status == 409 {
				conflict++
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
	})
	t.Run("durable history survives service reconstruction", func(t *testing.T) {
		fresh := lifestyle.New(h.conn.DB)
		v, e := fresh.Goal(ctx, h.uid, id)
		if e != nil || len(v.Versions) != 2 || v.Goal.CurrentVersion != 2 {
			t.Fatal(v, e)
		}
		decisions, e := fresh.Decisions(ctx, h.uid, rec.ID)
		if e != nil || len(decisions) != 1 {
			t.Fatal(decisions, e)
		}
		h.call(t, "GET", path+"/plans", "", nil, 200)
		h.call(t, "GET", dp, "", nil, 200)
		r := h.call(t, "GET", path+"?limit=1&offset=1", "", nil, 200)
		var page lifestyle.GoalView
		json.Unmarshal(r.Data, &page)
		if len(page.Versions) != 1 || page.Versions[0].Version != 1 {
			t.Fatal("goal history pagination")
		}
		h.call(t, "GET", path+"?limit=101", "", nil, 400)
	})
	t.Run("expiration and explicit rejection", func(t *testing.T) {
		freshInput := compile
		freshInput.ExpectedVersion = 2
		r := h.call(t, "POST", path+"/compile", uuid.NewString(), freshInput, 201)
		var p lifestyle.PlanView
		json.Unmarshal(r.Data, &p)
		expired := p.Recommendations[0]
		if e := h.conn.DB.Model(&lifestyle.Recommendation{}).Where("id=?", expired.ID).Update("expires_at", time.Now().Add(-time.Minute).UnixMilli()).Error; e != nil {
			t.Fatal(e)
		}
		h.call(t, "POST", fmt.Sprintf("/api/v2/recommendations/%d/decisions", expired.ID), uuid.NewString(), lifestyle.DecisionInput{ExpectedVersion: 1, Decision: "accept", PlanDigest: p.Plan.InputDigest}, 409)
		rejected := p.Recommendations[1]
		h.call(t, "POST", fmt.Sprintf("/api/v2/recommendations/%d/decisions", rejected.ID), uuid.NewString(), lifestyle.DecisionInput{ExpectedVersion: 1, Decision: "reject", PlanDigest: p.Plan.InputDigest}, 200)
	})
	// Non-secret isolated fixture IDs for subsequent real Docker/restart checks.
	if err := os.MkdirAll(filepath.Join("..", "..", "output", "module10"), 0755); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(map[string]any{"user_id": fmt.Sprint(h.uid), "goal_id": fmt.Sprint(id), "budget_id": fmt.Sprint(budget.ID), "billing_report_id": report.ID, "account_id": fmt.Sprint(account.ID), "expected_version": 2, "source": "isolated MySQL API test fixture"})
	if err := os.WriteFile(filepath.Join("..", "..", "output", "module10", "fixture-state.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
}
