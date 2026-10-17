//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type failingCancelSimulator struct{}

func (failingCancelSimulator) ValidateLocalCancellation(context.Context, *entity.Subscription) error {
	return errors.New("injected local cancel failure")
}
func TestSubscriptionMySQLAPI(t *testing.T) {
	h := harness(t)
	repo := repository.New(h.conn.DB)
	s := subscription.New(repo, risk.SimulationGateway{}, subscription.LocalSimulator{})
	reset := func(svc *subscription.Service) {
		h.server.Close()
		h.server = httptest.NewServer(httpapi.NewWithSubscriptions(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, nil, svc))
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
	}
	reset(s)
	analyze := func(tool string, in any) subscription.Report {
		r := h.call(t, "POST", "/api/v1/subscriptions/"+tool, "", in, 200)
		var out subscription.Report
		if e := json.Unmarshal(r.Data, &out); e != nil {
			t.Fatal(e)
		}
		if e := subscription.ValidateReport(out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	in := subscription.DetectInput{From: "2026-01-01T00:00:00Z", To: "2026-04-01T00:00:00Z"}
	t.Run("no transactions", func(t *testing.T) {
		r := analyze("detect_subscriptions", in)
		if len(r.Candidates) != 0 || r.TotalRecordedExpenseMinor != 0 {
			t.Fatal(r)
		}
	})
	var account entity.BankAccount
	r := h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), map[string]any{"display_name": "Subscription simulation", "initial_balance_minor": 100000, "confirm_simulated_funds": true}, 201)
	json.Unmarshal(r.Data, &account)
	for i, ts := range []string{"2026-01-31T12:00:00Z", "2026-02-28T12:00:00Z", "2026-03-31T12:00:00Z", "2026-03-31T12:01:00Z"} {
		at, _ := time.Parse(time.RFC3339, ts)
		amount := int64(2000)
		if i >= 2 {
			amount = 2200
		}
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{"bank_account_id": fmt.Sprint(account.ID), "external_transaction_id": uuid.NewString(), "direction": "expense", "amount_minor": amount, "category": "sports", "merchant_name": "Local Gym", "occurred_at": at.UnixMilli(), "effect_mode": "record_only"}, 201)
	}
	var sub entity.Subscription
	t.Run("recurring duplicate price and idempotent evidence", func(t *testing.T) {
		a := analyze("detect_subscriptions", in)
		b := analyze("detect_subscriptions", in)
		if len(a.Candidates) != 1 || a.Candidates[0].SubscriptionID != b.Candidates[0].SubscriptionID || len(b.Subscriptions[0].Payments) != 4 || len(a.Candidates[0].DuplicateTransactionIDs) != 2 || !a.Candidates[0].PriceChanged || a.TotalRecordedExpenseMinor != 8400 || a.Subscriptions[0].PossiblyIdle {
			t.Fatalf("%+v", a)
		}
		sub = b.Subscriptions[0].Subscription
		stored := h.call(t, "GET", fmt.Sprintf("/api/v1/subscriptions/analyses/%d", a.ID), "", nil, 200)
		var old subscription.Report
		json.Unmarshal(stored.Data, &old)
		if old.ID != a.ID {
			t.Fatal("report persistence")
		}
	})
	create := func(key string) entity.SubscriptionActionIntent {
		r := h.call(t, "POST", "/api/v1/subscriptions/create_cancel_intent", key, subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: sub.Version}, 201)
		var i entity.SubscriptionActionIntent
		json.Unmarshal(r.Data, &i)
		return i
	}
	confirm := func(i entity.SubscriptionActionIntent, decision string) entity.SubscriptionActionIntent {
		r := h.call(t, "POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: decision, ConfirmationHash: i.ConfirmationHash}, 200)
		var out entity.SubscriptionActionIntent
		json.Unmarshal(r.Data, &out)
		return out
	}
	t.Run("same merchant across accounts requires duplicate review", func(t *testing.T) {
		var other entity.BankAccount
		r := h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), map[string]any{"display_name": "Second subscription account", "initial_balance_minor": 0}, 201)
		json.Unmarshal(r.Data, &other)
		for _, ts := range []string{"2026-01-31T12:00:00Z", "2026-02-28T12:00:00Z", "2026-03-31T12:00:00Z"} {
			at, _ := time.Parse(time.RFC3339, ts)
			h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{"bank_account_id": fmt.Sprint(other.ID), "external_transaction_id": uuid.NewString(), "direction": "expense", "amount_minor": 2000, "category": "sports", "merchant_name": " Local Gym ", "occurred_at": at.UnixMilli(), "effect_mode": "record_only"}, 201)
		}
		r2 := analyze("detect_subscriptions", in)
		if len(r2.DuplicateGroups) != 1 || !r2.DuplicateGroups[0].RequiresUserReview || len(r2.Subscriptions) != 2 {
			t.Fatal(r2)
		}
	})
	t.Run("candidate cannot be cancelled", func(t *testing.T) {
		h.call(t, "POST", "/api/v1/subscriptions/create_cancel_intent", uuid.NewString(), subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: sub.Version}, 422)
	})
	t.Run("explicit local management", func(t *testing.T) {
		h.call(t, "POST", fmt.Sprintf("/api/v1/subscriptions/%d/manage", sub.ID), uuid.NewString(), subscription.ManageInput{ExpectedVersion: sub.Version}, 422)
		last := time.Now().AddDate(0, -4, 0).UnixMilli()
		r := h.call(t, "POST", fmt.Sprintf("/api/v1/subscriptions/%d/manage", sub.ID), uuid.NewString(), subscription.ManageInput{ExpectedVersion: sub.Version, ConfirmLocalSimulation: true, LastUsedAt: &last}, 200)
		json.Unmarshal(r.Data, &sub)
		if sub.Status != "active" {
			t.Fatal(sub)
		}
	})
	t.Run("six native tools with trusted subject and empty-body compatibility", func(t *testing.T) {
		auth := identity.New(h.cfg.JWTSecret, h.cfg.JWTIssuer, h.cfg.JWTAudience, h.cfg.ToolToken, fmt.Sprint(h.uid), "")
		server := httptest.NewServer(httpapi.NewWithSubscriptions(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, nil, s))
		defer server.Close()
		doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
		if e != nil {
			t.Fatal(e)
		}
		doc.Servers = openapi3.Servers{{URL: server.URL}}
		router, e := legacy.NewRouter(doc)
		if e != nil {
			t.Fatal(e)
		}
		call := func(tool string, body any, want int) {
			var raw []byte
			if body != nil {
				raw, _ = json.Marshal(body)
			}
			req, _ := http.NewRequest("POST", server.URL+"/tools/v1/subscriptions/"+tool, bytes.NewReader(raw))
			req.Header.Set("X-Banking-Tool-Key", h.cfg.ToolToken)
			req.Header.Set("Idempotency-Key", uuid.NewString())
			req.Header.Set("Content-Type", "application/json")
			resp, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Fatalf("%s %d %s", tool, resp.StatusCode, data)
			}
			route, params, e := router.FindRoute(req)
			if e != nil {
				t.Fatal(e)
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			v := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
			if want < 300 {
				if e = openapi3filter.ValidateRequest(context.Background(), v); e != nil {
					t.Fatal(e)
				}
			}
			if e = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: v, Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(data))}); e != nil {
				t.Fatal(e)
			}
		}
		call("detect_subscriptions", in, 200)
		call("get_subscription_details", subscription.IDInput{SubscriptionID: fmt.Sprint(sub.ID)}, 200)
		call("get_subscription_status", subscription.IDInput{SubscriptionID: fmt.Sprint(sub.ID)}, 200)
		call("analyze_subscription_cost", nil, 200)
		call("suggest_subscription_actions", nil, 200)
		call("create_cancel_intent", subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: sub.Version}, 200)
		call("get_subscription_status", map[string]any{"subscription_id": fmt.Sprint(sub.ID), "user_id": fmt.Sprint(h.uid + 1)}, 400)
		call("analyze_subscription_cost", map[string]any{"execute": true}, 400)
	})
	t.Run("intent idempotency and forged confirmation", func(t *testing.T) {
		key := uuid.NewString()
		i := create(key)
		j := create(key)
		if i.ID != j.ID {
			t.Fatal("duplicate intent")
		}
		h.call(t, "POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: "approve", ConfirmationHash: fmt.Sprintf("%064d", 0)}, 422)
		if confirm(i, "reject").Status != "rejected" {
			t.Fatal("reject failed")
		}
		if confirm(i, "reject").Status != "rejected" {
			t.Fatal("duplicate reject")
		}
		h.call(t, "POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: "approve", ConfirmationHash: i.ConfirmationHash}, 409)
	})
	t.Run("cancel failure and duplicate confirmation", func(t *testing.T) {
		reset(subscription.New(repo, risk.SimulationGateway{}, failingCancelSimulator{}))
		i := create(uuid.NewString())
		for n := 0; n < 2; n++ {
			out := confirm(i, "approve")
			if out.Status != "failed" || out.FailureCode != "CANCEL_FAILED" {
				t.Fatal(out)
			}
		}
		var current entity.Subscription
		h.conn.DB.First(&current, sub.ID)
		if current.Status != "active" {
			t.Fatal("failure changed subscription")
		}
		reset(s)
	})
	t.Run("nil gateway fails closed", func(t *testing.T) {
		reset(subscription.New(repo, nil, subscription.LocalSimulator{}))
		h.call(t, "POST", "/api/v1/subscriptions/create_cancel_intent", uuid.NewString(), subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: sub.Version}, 422)
		reset(s)
	})
	t.Run("expired intent", func(t *testing.T) {
		i := create(uuid.NewString())
		i.ExpiresAt = 1
		i.ConfirmationHash = entity.SubscriptionDigest(&i)
		if e := h.conn.DB.Model(&entity.SubscriptionActionIntent{}).Where("id=?", i.ID).Updates(map[string]any{"expires_at": 1, "confirmation_hash": i.ConfirmationHash}).Error; e != nil {
			t.Fatal(e)
		}
		if out := confirm(i, "approve"); out.Status != "expired" || out.FailureCode != "APPROVAL_EXPIRED" {
			t.Fatal(out)
		}
	})
	t.Run("foreign resources and denied audit", func(t *testing.T) {
		foreign := h.uid + 1
		h.conn.DB.Create(&entity.User{ID: foreign, DisplayName: "Other", Status: "active"})
		token, _ := h.auth.IssueUser(fmt.Sprint(foreign), "", time.Hour)
		i := create(uuid.NewString())
		for _, p := range []string{fmt.Sprintf("/api/v1/subscriptions/%d", sub.ID), fmt.Sprintf("/api/v1/subscription-intents/%d", i.ID)} {
			code, _, e := h.raw("GET", p, "", nil, token)
			if e != nil || code != 404 {
				t.Fatal(code, e)
			}
		}
		code, _, e := h.raw("POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: "approve", ConfirmationHash: i.ConfirmationHash}, token)
		if e != nil || code != 404 {
			t.Fatal(code, e)
		}
		var count int64
		h.conn.DB.Model(&entity.Audit{}).Where("user_id=? AND action='subscription.cancel.confirm' AND outcome='denied'", foreign).Count(&count)
		if count != 1 {
			t.Fatal("missing denial audit", count)
		}
	})
	t.Run("SQL rollback including approval", func(t *testing.T) {
		i := create(uuid.NewString())
		callback := "subscription_audit_fail_" + fmt.Sprint(h.uid)
		if e := h.conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
			if a, ok := db.Statement.Dest.(*entity.Audit); ok && a.UserID == h.uid && a.Action == "subscription.cancel.confirm" && a.Outcome == "executed" {
				db.AddError(db.Exec("INSERT INTO banking_subscription_injected_missing_table VALUES(1)").Error)
			}
		}); e != nil {
			t.Fatal(e)
		}
		defer h.conn.DB.Callback().Create().Remove(callback)

		h.call(t, "POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: "approve", ConfirmationHash: i.ConfirmationHash}, 503)
		var current entity.SubscriptionActionIntent
		h.conn.DB.First(&current, i.ID)
		var count int64
		h.conn.DB.Model(&entity.SubscriptionApproval{}).Where("intent_id=?", i.ID).Count(&count)
		if current.Status != "pending_approval" || count != 0 {
			t.Fatal("transaction did not roll back", current, count)
		}
		var currentSub entity.Subscription
		h.conn.DB.First(&currentSub, sub.ID)
		if currentSub.Status != "active" {
			t.Fatal("cancel did not roll back")
		}
	})
	t.Run("concurrent confirmation executes once stale proposal fails and funds unchanged", func(t *testing.T) {
		i := create(uuid.NewString())
		stale := create(uuid.NewString())
		var beforeBalance entity.BankAccount
		h.conn.DB.First(&beforeBalance, account.ID)
		var beforeLedger, beforeSavings int64
		h.conn.DB.Model(&entity.LedgerEntry{}).Where("user_id=?", h.uid).Count(&beforeLedger)
		h.conn.DB.Model(&entity.SavingsLedger{}).Where("user_id=?", h.uid).Count(&beforeSavings)
		var wg sync.WaitGroup
		ch := make(chan error, 10)
		for n := 0; n < 10; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, res, e := h.raw("POST", fmt.Sprintf("/api/v1/subscription-intents/%d/confirm", i.ID), "", subscription.ConfirmInput{Decision: "approve", ConfirmationHash: i.ConfirmationHash}, h.token)
				if e == nil {
					var out entity.SubscriptionActionIntent
					e = json.Unmarshal(res.Data, &out)
					if code != 200 || out.Status != "executed" {
						e = fmt.Errorf("%d %s", code, res.Code)
					}
				}
				ch <- e
			}()
		}
		wg.Wait()
		close(ch)
		for e := range ch {
			if e != nil {
				t.Fatal(e)
			}
		}
		if out := confirm(stale, "approve"); out.Status != "failed" || out.FailureCode != "VERSION_CONFLICT" {
			t.Fatal(out)
		}
		var approvals, executedAudits, ledger, savings int64
		h.conn.DB.Model(&entity.SubscriptionApproval{}).Where("intent_id=?", i.ID).Count(&approvals)
		h.conn.DB.Model(&entity.Audit{}).Where("user_id=? AND resource_id=? AND action='subscription.cancel.confirm' AND outcome='executed'", h.uid, i.ID).Count(&executedAudits)
		h.conn.DB.Model(&entity.LedgerEntry{}).Where("user_id=?", h.uid).Count(&ledger)
		h.conn.DB.Model(&entity.SavingsLedger{}).Where("user_id=?", h.uid).Count(&savings)
		var after entity.BankAccount
		h.conn.DB.First(&after, account.ID)
		if approvals != 1 || executedAudits != 1 || ledger != beforeLedger || savings != beforeSavings || after.BalanceMinor != beforeBalance.BalanceMinor {
			t.Fatal("repeat or financial mutation", approvals, executedAudits, ledger, savings, after)
		}
		cost := analyze("analyze_subscription_cost", struct{}{})
		if cost.CancelledAvoidedSpend.MonthlyMinor != 2200 || cost.CancelledAvoidedSpend.ThreeMonthMinor != 6600 || cost.CancelledAvoidedSpend.AnnualMinor != 26400 || cost.CancelledAvoidedSpend.AvailableBalanceIncreaseMinor != 0 {
			t.Fatal(cost)
		}
	})
	t.Run("tools cannot confirm or manage", func(t *testing.T) {
		for _, path := range []string{fmt.Sprintf("/tools/v1/subscription-intents/%d/confirm", sub.ID), fmt.Sprintf("/tools/v1/subscriptions/%d/manage", sub.ID)} {
			req, _ := http.NewRequest("POST", h.server.URL+path, nil)
			req.Header.Set("X-Banking-Tool-Key", h.cfg.ToolToken)
			resp, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			resp.Body.Close()
			if resp.StatusCode != 404 {
				t.Fatal(resp.StatusCode)
			}
		}
	})
	t.Run("all read tools and durable history", func(t *testing.T) {
		for _, tool := range []string{"get_subscription_details", "get_subscription_status"} {
			h.call(t, "POST", "/api/v1/subscriptions/"+tool, "", subscription.IDInput{SubscriptionID: fmt.Sprint(sub.ID)}, 200)
		}
		analyze("suggest_subscription_actions", struct{}{})
		h.call(t, "GET", fmt.Sprintf("/api/v1/subscriptions/%d/history", sub.ID), "", nil, 200)
	})
}
