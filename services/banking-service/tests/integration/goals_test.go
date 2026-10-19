//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cents(n int64) *int64 { return &n }
func goalFixture() goal.Parsed {
	return goal.Parsed{GoalType: "healthy_lifestyle", PeriodMonths: cents(3), SavingsMonthlyMinor: cents(20000), Currency: "CNY", Constraints: []goal.Constraint{}, ProhibitedOperations: []string{}, MissingInformation: []string{}, Evidence: map[string]string{"period_months": "三个月", "savings_monthly_minor": "每月储蓄200元"}}
}
func TestGoalCompilerMySQLAPI(t *testing.T) {
	h := harness(t)
	var mode atomic.Value
	mode.Store("normal")
	var calls atomic.Int64
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			WorkflowID string `json:"workflow_id"`
			Parameters string `json:"parameters"`
			IsAsync    bool   `json:"is_async"`
		}
		if r.URL.Path != "/v1/workflow/run" || r.Header.Get("Authorization") != "Bearer integration-only-pat" || json.NewDecoder(r.Body).Decode(&req) != nil || req.IsAsync || req.WorkflowID != "123" {
			t.Error("wrong local Coze sync contract")
		}
		current := mode.Load().(string)
		if current == "slow" {
			started <- struct{}{}
			<-release
		}
		p := goalFixture()
		if current == "ambiguous" {
			p.GoalType = "unclear"
			p.PeriodMonths = nil
			p.SavingsMonthlyMinor = nil
			p.Evidence = map[string]string{}
		}
		raw, _ := json.Marshal(p)
		if current == "bad" {
			raw = []byte(`{"task_type":"execute_transfer"}`)
		}
		if current == "unavailable" {
			w.WriteHeader(503)
			return
		}
		data, _ := json.Marshal(map[string]string{"output": string(raw)})
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "execute_id": "456", "data": string(data)})
	}))
	defer upstream.Close()
	client := fixtureCozeClient(t, upstream.URL, "integration-only-pat", 5*time.Second)
	repo := goal.NewRepository(h.conn.DB)
	svc := goal.NewService(repo, &goal.Parser{Runner: client, WorkflowID: "123"}, h.svc)
	install := func(s *goal.Service) {
		h.server.Close()
		h.server = httptest.NewServer(httpapi.NewWithGoals(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, s))
		t.Cleanup(h.server.Close)
		doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
		if err != nil {
			t.Fatal(err)
		}
		doc.Servers = openapi3.Servers{{URL: h.server.URL}}
		h.contract, err = legacy.NewRouter(doc)
		if err != nil {
			t.Fatal(err)
		}
	}
	install(svc)
	decode := func(r response) goal.View {
		var v goal.View
		if err := json.Unmarshal(r.Data, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	create := func(in goal.Input) goal.View {
		return decode(h.call(t, "POST", "/api/v1/goals", uuid.NewString(), in, 201))
	}
	good := goal.Input{Text: "未来三个月，我想规律饮食和运动，每月储蓄200元", Context: goal.FinancialContext{MonthlyIncomeMinor: cents(500000), SpendingBudgetMinor: cents(400000), EssentialExpensesMinor: cents(200000)}}
	var initial goal.View
	t.Run("create replay compile immutable plan and no funds", func(t *testing.T) {
		key := uuid.NewString()
		initial = decode(h.call(t, "POST", "/api/v1/goals", key, good, 201))
		again := decode(h.call(t, "POST", "/api/v1/goals", key, good, 201))
		if again.Goal.ID != initial.Goal.ID {
			t.Fatal("duplicate goal")
		}
		changed := good
		changed.Text += "不同输入"
		h.call(t, "POST", "/api/v1/goals", key, changed, 409)
		path := fmt.Sprintf("/api/v1/goals/%d", initial.Goal.ID)
		h.call(t, "GET", path+"/plan", "", nil, 404)
		before := calls.Load()
		parsed := decode(h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, 200))
		if parsed.Plan == nil || parsed.Plan.Status != "pending_confirmation" {
			t.Fatal(parsed)
		}
		h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, 200)
		if calls.Load() != before+1 {
			t.Fatal("duplicate Coze run")
		}
		var plan goal.Plan
		if json.Unmarshal(parsed.Plan.PlanJSON, &plan) != nil || goal.ValidatePlan(plan) != nil {
			t.Fatal("invalid stored plan")
		}
		h.call(t, "GET", path+"/plan", "", nil, 200)
		var dependencies, constraints int64
		h.conn.DB.Model(&goal.TaskDependency{}).Where("plan_id=?", parsed.Plan.ID).Count(&dependencies)
		h.conn.DB.Model(&goal.GoalConstraint{}).Where("goal_version_id=?", parsed.Version.ID).Count(&constraints)
		if dependencies != 5 || constraints < 2 {
			t.Fatal("missing persisted graph or safety constraints", dependencies, constraints)
		}
		for _, table := range []any{&entity.LedgerEntry{}, &entity.TransferIntent{}, &entity.BankAccount{}, &entity.Transaction{}, &entity.SavingsGoal{}} {
			var n int64
			if e := h.conn.DB.Model(table).Where("user_id=?", h.uid).Count(&n).Error; e != nil || n != 0 {
				t.Fatal("compiler changed financial tables", n, e)
			}
		}
	})
	t.Run("ownership and tool credential isolation", func(t *testing.T) {
		token, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		for _, suffix := range []string{"", "/plan", "/versions", "/compare?from=1&to=1"} {
			status, _, e := h.raw("GET", fmt.Sprintf("/api/v1/goals/%d%s", initial.Goal.ID, suffix), "", nil, token)
			if e != nil || status != 404 {
				t.Fatal(status, e)
			}
		}
		status, _, e := h.raw("POST", fmt.Sprintf("/api/v1/goals/%d/parse", initial.Goal.ID), "", map[string]any{"expected_version": 1}, token)
		if e != nil || status != 404 {
			t.Fatal(status, e)
		}
		status, _, e = h.raw("GET", fmt.Sprintf("/api/v1/goals/%d", initial.Goal.ID), "", nil, h.cfg.ToolToken)
		if e != nil || status != 401 {
			t.Fatal(status, e)
		}
	})
	for _, tc := range []struct {
		name, mode, status string
		in                 goal.Input
	}{
		{"missing budget", "normal", "awaiting_clarification", goal.Input{Text: good.Text}},
		{"conflicting savings", "normal", "conflict", goal.Input{Text: good.Text, Context: goal.FinancialContext{MonthlyIncomeMinor: cents(410000), SpendingBudgetMinor: cents(400000), EssentialExpensesMinor: cents(200000)}}},
		{"ambiguous goal", "ambiguous", "awaiting_clarification", goal.Input{Text: "希望健康生活，合理存点钱"}},
		{"weight incentives", "normal", "conflict", goal.Input{Text: good.Text + "，体重下降奖励奖金", Context: good.Context}},
		{"explicit transfer prohibition", "normal", "pending_confirmation", goal.Input{Text: good.Text + "，禁止转账", Context: good.Context}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			g := create(tc.in)
			out := decode(h.call(t, "POST", fmt.Sprintf("/api/v1/goals/%d/parse", g.Goal.ID), "", map[string]any{"expected_version": 1}, 200))
			if out.Plan == nil || out.Plan.Status != tc.status {
				t.Fatal(out)
			}
			if tc.name == "explicit transfer prohibition" {
				var p goal.Plan
				json.Unmarshal(out.Plan.PlanJSON, &p)
				if len(p.Tasks) != 5 {
					t.Fatal("prohibited proposal")
				}
			}
		})
	}
	t.Run("persistent budget snapshot reserve conflict and foreign budget", func(t *testing.T) {
		mode.Store("normal")
		result := h.call(t, "POST", "/api/v1/budgets", uuid.NewString(), map[string]any{"month": "2026-10", "total_minor": 150000, "enforce_reserve": true, "categories": []map[string]any{{"category": "necessary_living", "allocated_minor": 150000, "essential_reserve_minor": 100000}}}, 201)
		var budget entity.Budget
		if json.Unmarshal(result.Data, &budget) != nil {
			t.Fatal("budget decode")
		}
		in := goal.Input{Text: good.Text, Context: goal.FinancialContext{MonthlyIncomeMinor: cents(160000), BudgetID: &budget.ID}}
		g := create(in)
		out := decode(h.call(t, "POST", fmt.Sprintf("/api/v1/goals/%d/parse", g.Goal.ID), "", map[string]any{"expected_version": 1}, 200))
		if out.Plan.Status != "conflict" {
			t.Fatal(out)
		}
		var plan goal.Plan
		json.Unmarshal(out.Plan.PlanJSON, &plan)
		if len(plan.BudgetSnapshot) == 0 || plan.FinancialContext.EssentialExpensesMinor == nil || *plan.FinancialContext.EssentialExpensesMinor != 100000 {
			t.Fatal("lost reserve snapshot")
		}
		in.Context.EssentialExpensesMinor = cents(0)
		g = create(in)
		out = decode(h.call(t, "POST", fmt.Sprintf("/api/v1/goals/%d/parse", g.Goal.ID), "", map[string]any{"expected_version": 1}, 200))
		if out.Plan.Status != "conflict" {
			t.Fatal("reduced reserve accepted")
		}
		foreign, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		status, _, err := h.raw("POST", "/api/v1/goals", uuid.NewString(), in, foreign)
		if err != nil || status != 404 {
			t.Fatal(status, err)
		}
	})
	for _, tc := range []struct {
		name, mode, code string
		status           int
	}{{"invalid model and financial task", "bad", "MODEL_OUTPUT_INVALID", 422}, {"upstream unavailable", "unavailable", "COZE_UNAVAILABLE", 503}} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			g := create(good)
			path := fmt.Sprintf("/api/v1/goals/%d", g.Goal.ID)
			r := h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, tc.status)
			if r.Code != tc.code {
				t.Fatal(r.Code)
			}
			persisted := decode(h.call(t, "GET", path, "", nil, 200))
			if persisted.Version.ParseStatus != "failed" || persisted.Plan != nil {
				t.Fatal(persisted)
			}
			mode.Store("normal")
			retry := decode(h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, 200))
			if retry.Version.ParseStatus != "parsed" {
				t.Fatal("explicit retry failed")
			}
		})
	}
	t.Run("version history comparison and optimistic conflict", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/goals/%d", initial.Goal.ID)
		updated := good
		updated.Text += "，偏好步行"
		updated.ExpectedVersion = 1
		v := decode(h.call(t, "PUT", path, "", updated, 200))
		if v.Goal.CurrentVersion != 2 || v.Plan != nil {
			t.Fatal(v)
		}
		h.call(t, "PUT", path, "", updated, 409)
		history := h.call(t, "GET", path+"/versions", "", nil, 200)
		var versions []goal.GoalVersion
		json.Unmarshal(history.Data, &versions)
		if len(versions) != 2 || versions[1].ParseStatus != "parsed" {
			t.Fatal(versions)
		}
		compare := h.call(t, "GET", path+"/compare?from=1&to=2", "", nil, 200)
		var result goal.Comparison
		json.Unmarshal(compare.Data, &result)
		if len(result.Differences) < 2 {
			t.Fatal(result)
		}
		h.call(t, "GET", path+"/plan?version=1", "", nil, 200)
		h.call(t, "GET", path+"/plan", "", nil, 404)
	})
	t.Run("concurrent parse and stale inference cannot overwrite new version", func(t *testing.T) {
		mode.Store("slow")
		g := create(good)
		path := fmt.Sprintf("/api/v1/goals/%d", g.Goal.ID)
		var wg sync.WaitGroup
		wg.Add(1)
		var status int
		var err error
		go func() {
			defer wg.Done()
			status, _, err = h.raw("POST", path+"/parse", "", map[string]any{"expected_version": 1}, h.token)
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("Coze call not started")
		}
		h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, 409)
		updated := good
		updated.Text += "，新偏好"
		updated.ExpectedVersion = 1
		h.call(t, "PUT", path, "", updated, 200)
		close(release)
		wg.Wait()
		if err != nil || status != 200 {
			t.Fatal(status, err)
		}
		mode.Store("normal")
		current := decode(h.call(t, "GET", path, "", nil, 200))
		if current.Goal.CurrentVersion != 2 || current.Goal.Status != "unparsed" || current.Plan != nil {
			t.Fatal("stale overwrite", current)
		}
		h.call(t, "GET", path+"/plan?version=1", "", nil, 200)
	})
	t.Run("compile persistence rollback", func(t *testing.T) {
		g := create(good)
		callback := "goals-fault-" + uuid.NewString()
		injected := errors.New("injected task graph failure")
		h.conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "banking_task_dependencies" {
				tx.AddError(injected)
			}
		})
		defer h.conn.DB.Callback().Create().Remove(callback)
		h.call(t, "POST", fmt.Sprintf("/api/v1/goals/%d/parse", g.Goal.ID), "", map[string]any{"expected_version": 1}, 503)
		var n int64
		h.conn.DB.Model(&goal.TaskPlan{}).Where("goal_id=?", g.Goal.ID).Count(&n)
		if n != 0 {
			t.Fatal("partial plan committed")
		}
		// Parsing lease remains visible on failure; explicit new version permits retry.
		view, e := svc.View(context.Background(), h.uid, g.Goal.ID, 0)
		if e != nil || view.Version.ParseStatus != "parsing" {
			t.Fatal(view, e)
		}
	})
	t.Run("unconfigured parser persists no fake output", func(t *testing.T) {
		install(goal.NewService(repo, nil, h.svc))
		g := create(good)
		path := fmt.Sprintf("/api/v1/goals/%d", g.Goal.ID)
		h.call(t, "POST", path+"/parse", "", map[string]any{"expected_version": 1}, 503)
		view := decode(h.call(t, "GET", path, "", nil, 200))
		if view.Version.ParseStatus != "unparsed" || view.Plan != nil {
			t.Fatal(view)
		}
	})
}
