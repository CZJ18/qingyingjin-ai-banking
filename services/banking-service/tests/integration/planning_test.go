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
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAdaptiveSavingsMySQL(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	p := planning.New(h.conn.DB)
	m := service.Meta{UserID: h.uid, TraceID: "planning-integration", TaskID: "module6-test"}
	subs := subscription.New(repository.New(h.conn.DB), risk.SimulationGateway{}, subscription.LocalSimulator{})
	h.server.Close()
	h.server = httptest.NewServer(httpapi.NewWithPlanning(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, nil, subs, p))
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
	g := parse[entity.SavingsGoal](t, h.call(t, "POST", "/api/v1/savings/goals", uuid.NewString(), service.GoalInput{Name: "Healthy routine", TargetMinor: 100000, MonthlyMinor: 70000, StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().AddDate(0, 3, 0).UnixMilli()}, 201))
	drain := func() {
		t.Helper()
		for i := 0; i < 20000; i++ {
			found, err := p.ProcessOne(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				return
			}
		}
		t.Fatal("queue did not drain")
	}
	drain()
	settings := planning.Settings{AccountIDs: []int64{a.ID}, SavingsGoalIDs: []int64{g.ID}, LifeGoalIDs: []int64{}, LivingRemainingMinor: cents(30000), EssentialRemainingMinor: cents(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000, FutureIncomeMinor: cents(0), Cashflows: []planning.Cashflow{}}
	v := parse[planning.View](t, h.call(t, "POST", "/api/v1/financial-plans", "", planning.CreateInput{Name: "October", Settings: settings}, 201))
	path := fmt.Sprintf("/api/v1/financial-plans/%d", v.Plan.ID)
	t.Run("three safe options conflict and advisory confirmation", func(t *testing.T) {
		if v.Result.SavingsCapMinor != 55000 || v.Result.ConflictMinor != 15000 || len(v.Result.Options) != 3 {
			t.Fatal(v.Result)
		}
		d := planning.DecisionInput{ExpectedVersion: v.Version.Version, DataDigest: v.Version.DataDigest, Option: "balanced", ConfirmAdvice: true}
		first := parse[planning.PlanDecision](t, h.call(t, "POST", path+"/decisions", "", d, 200))
		again := parse[planning.PlanDecision](t, h.call(t, "POST", path+"/decisions", "", d, 200))
		if first.ID != again.ID {
			t.Fatal("duplicate decision")
		}
		h.balance(t, a.ID, 100000)
		h.balance(t, g.AccountID, 0)
	})
	t.Run("income loss emergency and missing information", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			s    planning.Settings
			want int64
		}{
			{"income", func() planning.Settings {
				s := settings
				s.BaselineIncomeMinor = 50000
				s.FutureIncomeMinor = cents(20000)
				return s
			}(), 25000},
			{"emergency", func() planning.Settings {
				s := settings
				s.Cashflows = []planning.Cashflow{{Name: "necessary medical", Kind: "necessary_expense", AmountMinor: 50000, DueAt: time.Now().UnixMilli()}}
				return s
			}(), 5000},
			{"missing", func() planning.Settings { s := settings; s.LivingRemainingMinor = nil; return s }(), 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				v = parse[planning.View](t, h.call(t, "PUT", path+"/settings", "", planning.UpdateInput{ExpectedVersion: v.Version.Version, Settings: tc.s}, 200))
				if v.Result.SavingsCapMinor != tc.want || !v.Version.NeedsConfirmation {
					t.Fatal(v.Result)
				}
			})
		}
		v = parse[planning.View](t, h.call(t, "PUT", path+"/settings", "", planning.UpdateInput{ExpectedVersion: v.Version.Version, Settings: settings}, 200))
	})
	t.Run("transaction event and stale confirmation", func(t *testing.T) {
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 90000, Currency: "CNY", Category: "food", OccurredAt: time.Now().UnixMilli(), EffectMode: "apply_simulated_effect", ConfirmSimulatedEffect: true}, 201)
		h.call(t, "POST", path+"/decisions", "", planning.DecisionInput{ExpectedVersion: v.Version.Version, DataDigest: v.Version.DataDigest, Option: "balanced", ConfirmAdvice: true}, 409)
		drain()
		v = parse[planning.View](t, h.call(t, "GET", path, "", nil, 200))
		if v.Result.SavingsCapMinor != 0 || v.Result.BalanceMinor != 10000 {
			t.Fatal(v.Result)
		}
		h.balance(t, a.ID, 10000)
	})
	t.Run("duplicate events and consistent dynamic result", func(t *testing.T) {
		before := v.Version.Version
		in := planning.EventInput{Type: "necessary_expense_changed", ResourceID: fmt.Sprint(v.Plan.ID), Reason: "same known data"}
		key := uuid.NewString()
		event := parse[planning.ReplanEvent](t, h.call(t, "POST", "/api/v1/replan-events", key, in, 201))
		replay := parse[planning.ReplanEvent](t, h.call(t, "POST", "/api/v1/replan-events", key, in, 201))
		if event.ID != replay.ID {
			t.Fatal("not deduped")
		}
		in.Reason = "different"
		h.call(t, "POST", "/api/v1/replan-events", key, in, 409)
		drain()
		current, err := p.Get(ctx, h.uid, v.Plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version.Version != before+1 || current.Version.DataDigest != v.Version.DataDigest {
			t.Fatal("duplicate or nondeterministic", current.Version, v.Version)
		}
		drain()
		v = current
		h.call(t, "GET", path+"/versions", "", nil, 200)
	})
	t.Run("persistent queue retry and rollback", func(t *testing.T) {
		before := v.Version.Version
		ev, err := p.Publish(ctx, m, uuid.NewString(), planning.EventInput{Type: "account_changed", ResourceID: fmt.Sprint(a.ID), Reason: "rollback test"})
		if err != nil {
			t.Fatal(err)
		}
		callback := "module6_fault_" + uuid.NewString()
		h.conn.DB.Callback().Create().After("gorm:create").Register(callback, func(db *gorm.DB) {
			if a, ok := db.Statement.Dest.(*entity.Audit); ok && a.UserID == h.uid && a.Action == "planning.replan" {
				db.AddError(errors.New("test final audit failure"))
			}
		})
		found, err := p.ProcessOne(ctx)
		h.conn.DB.Callback().Create().Remove(callback)
		if !found || err == nil {
			t.Fatal("fault did not execute")
		}
		current, err := p.Get(ctx, h.uid, v.Plan.ID)
		if err != nil || current.Version.Version != before {
			t.Fatal("partial version persisted", current, err)
		}
		var row planning.ReplanEvent
		if e := h.conn.DB.First(&row, ev.ID).Error; e != nil || row.Status != "pending" || row.Attempts != 1 {
			t.Fatal(row, e)
		}
		// Advance retry scheduling only, never money; recreate service as after restart.
		if e := h.conn.DB.Model(&planning.ReplanEvent{}).Where("id=?", ev.ID).Update("retry_at", 0).Error; e != nil {
			t.Fatal(e)
		}
		p = planning.New(h.conn.DB)
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Version.Version != before+1 {
			t.Fatal(v.Version)
		}
	})
	t.Run("concurrent duplicate publishing", func(t *testing.T) {
		key := uuid.NewString()
		in := planning.EventInput{Type: "account_changed", ResourceID: fmt.Sprint(a.ID), Reason: "concurrent dedup"}
		ids := make(chan int64, 8)
		errs := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); r, e := p.Publish(ctx, m, key, in); ids <- r.ID; errs <- e }()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		var id int64
		for i := range ids {
			if id != 0 && i != id {
				t.Fatal("dedup race")
			}
			id = i
		}
		before := v.Version.Version
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Version.Version != before+1 {
			t.Fatal(v.Version)
		}
	})
	t.Run("transaction rollback leaves no outbox mutation", func(t *testing.T) {
		var before int64
		h.conn.DB.Model(&planning.ReplanEvent{}).Where("user_id=?", h.uid).Count(&before)
		err := h.conn.DB.Transaction(func(tx *gorm.DB) error {
			if e := tx.Model(&entity.BankAccount{}).Where("id=?", a.ID).Update("status", "frozen").Error; e != nil {
				return e
			}
			return errors.New("rollback")
		})
		if err == nil {
			t.Fatal("expected rollback")
		}
		var after int64
		h.conn.DB.Model(&planning.ReplanEvent{}).Where("user_id=?", h.uid).Count(&after)
		if after != before {
			t.Fatal("orphan event")
		}
		h.balance(t, a.ID, 10000)
	})
	t.Run("subscription cancellation future savings isolated", func(t *testing.T) {
		sub := entity.Subscription{Base: entity.Base{UserID: h.uid}, BankAccountID: a.ID, Fingerprint: fmt.Sprintf("%064d", a.ID), MerchantName: "Simulation", Category: "sports", BillingInterval: "monthly", AmountMinor: 2000, Currency: "CNY", NextBillingAt: time.Now().UnixMilli(), ConfidenceBPS: 9000, Status: "active", Version: 1, IsSimulated: true}
		if e := h.conn.DB.Create(&sub).Error; e != nil {
			t.Fatal(e)
		}
		drain()
		current, _ := p.Get(ctx, h.uid, v.Plan.ID)
		if current.Result.AdditionalObligationsMinor != 2000 {
			t.Fatal(current.Result)
		}
		intent, e := subs.CreateIntent(ctx, m, uuid.NewString(), subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: 1})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = subs.Confirm(ctx, m, intent.ID, subscription.ConfirmInput{Decision: "approve", ConfirmationHash: intent.ConfirmationHash}); e != nil {
			t.Fatal(e)
		}
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Result.FutureAvoidedSpendMinor != 2000 || v.Result.BalanceMinor != 10000 || v.Result.SavingsCapMinor != 0 {
			t.Fatal(v.Result)
		}
		h.balance(t, a.ID, 10000)
		h.balance(t, g.AccountID, 0)
	})
	t.Run("foreign account and plan denied", func(t *testing.T) {
		other := harness(t)
		foreign := other.account(t, 1000)
		s := settings
		s.AccountIDs = []int64{foreign.ID}
		h.call(t, "POST", "/api/v1/financial-plans", "", planning.CreateInput{Name: "foreign", Settings: s}, 404)
		if _, e := p.Get(ctx, other.uid, v.Plan.ID); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
	})
	t.Run("goal settings compare and frozen account", func(t *testing.T) {
		in := planning.GoalUpdateInput{ExpectedTargetMinor: g.TargetMinor, ExpectedMonthlyMinor: g.Plan.MonthlyMinor, ExpectedEndsAt: g.EndsAt, TargetMinor: 90000, MonthlyMinor: 10000, EndsAt: g.EndsAt}
		h.call(t, "PUT", fmt.Sprintf("/api/v1/savings/goals/%d/settings", g.ID), "", in, 200)
		h.call(t, "PUT", fmt.Sprintf("/api/v1/savings/goals/%d/settings", g.ID), "", in, 409)
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Result.TargetDemandMinor != 10000 {
			t.Fatal(v.Result)
		}
		h.call(t, "PATCH", fmt.Sprintf("/api/v1/accounts/%d/status", a.ID), "", map[string]string{"status": "frozen"}, 200)
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Result.BalanceMinor != 0 || v.Result.SavingsCapMinor != 0 {
			t.Fatal(v.Result)
		}
		h.call(t, "PATCH", fmt.Sprintf("/api/v1/accounts/%d/status", a.ID), "", map[string]string{"status": "active"}, 200)
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
	})
	t.Run("life goal update and unresolved constraints", func(t *testing.T) {
		repo := goal.NewRepository(h.conn.DB)
		fc := goal.FinancialContext{MonthlyIncomeMinor: cents(100000), SpendingBudgetMinor: cents(30000), EssentialExpensesMinor: cents(20000)}
		life, e := repo.Create(ctx, m, uuid.NewString(), goal.Input{Text: "三个月每月储蓄200元，保持正常生活", Context: fc})
		if e != nil {
			t.Fatal(e)
		}
		settings.LifeGoalIDs = []int64{life.Goal.ID}
		settings.FutureIncomeMinor = cents(100000)
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		v = parse[planning.View](t, h.call(t, "PUT", path+"/settings", "", planning.UpdateInput{ExpectedVersion: v.Version.Version, Settings: settings}, 200))
		if len(v.Result.Blockers) == 0 {
			t.Fatal("unparsed goal ignored")
		}
		attempt, e := repo.Claim(ctx, m, life.Goal.ID, 1, "test-only-fixture")
		if e != nil {
			t.Fatal(e)
		}
		parsed := goalFixture()
		compiled, e := goal.Compile(parsed, fc, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = repo.Finalize(ctx, m, attempt, parsed, compiled, "test-only-execute", nil); e != nil {
			t.Fatal(e)
		}
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		mismatch := false
		for _, b := range v.Result.Blockers {
			if b == "life goal and selected savings goal definitions differ; reconcile explicitly" {
				mismatch = true
			}
		}
		if !mismatch {
			t.Fatal(v.Result.Blockers)
		}
		if _, e = repo.Update(ctx, m, life.Goal.ID, goal.Input{Text: "我要重新安排目标", Context: fc, ExpectedVersion: 1}); e != nil {
			t.Fatal(e)
		}
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		var snap planning.Snapshot
		if e = json.Unmarshal(v.Version.SnapshotJSON, &snap); e != nil {
			t.Fatal(e)
		}
		found := false
		for _, src := range snap.Sources {
			if src.Type == "life_goal_version" && src.Version == 2 {
				found = true
			}
		}
		if !found || len(v.Result.Blockers) == 0 {
			t.Fatal(snap)
		}
	})
	t.Run("concurrent workers manual replan and replay", func(t *testing.T) {
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		before := v.Version.Version
		key := uuid.NewString()
		event := planning.EventInput{Type: "account_changed", ResourceID: fmt.Sprint(a.ID), Reason: "concurrent workers"}
		if _, e := p.Publish(ctx, m, key, event); e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := p.ProcessOne(ctx); errs <- e }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, e := p.Publish(ctx, m, key, event); errs <- e }()
		wg.Add(1)
		go func() { defer wg.Done(); _, e := p.Replan(ctx, m, v.Plan.ID); errs <- e }()
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		drain()
		v, _ = p.Get(ctx, h.uid, v.Plan.ID)
		if v.Version.Version != before+2 {
			t.Fatal(v.Version)
		}
		page := parse[[]planning.PlanVersion](t, h.call(t, "GET", path+"/versions?limit=1&offset=1", "", nil, 200))
		if len(page) != 1 || page[0].Version != v.Version.Version-1 {
			t.Fatal(page)
		}
	})
}
