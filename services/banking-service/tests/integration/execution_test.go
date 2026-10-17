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
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

func installExecutionHTTP(t *testing.T, h *coreHarness, sim subscription.Simulator) {
	t.Helper()
	h.server.Close()
	repo := repository.New(h.conn.DB)
	core := service.New(repo, risk.TrustedGateway{})
	subs := subscription.New(repo, risk.TrustedGateway{}, sim)
	h.server = httptest.NewServer(httpapi.NewWithOrchestration(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, core, nil, nil, subs, nil, nil, execution.New(h.conn.DB, sim)))
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
func execPrepare(t *testing.T, h *coreHarness, kind string, parameters any) execution.View {
	t.Helper()
	raw, e := json.Marshal(parameters)
	if e != nil {
		t.Fatal(e)
	}
	return executionView(t, h.call(t, "POST", "/api/v1/execution/operations", uuid.NewString(), execution.PrepareInput{Kind: kind, Parameters: raw}, 201))
}
func execAuthorize(t *testing.T, h *coreHarness, v execution.View) execution.View {
	t.Helper()
	return executionView(t, h.call(t, "POST", "/api/v1/execution/operations/"+v.Operation.ID+"/authorize", "", execution.AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce}, 200))
}
func execInput(v execution.View) execution.ExecuteInput {
	return execution.ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version}
}
func execRun(t *testing.T, h *coreHarness, v execution.View, key string) execution.View {
	t.Helper()
	return executionView(t, h.call(t, "POST", "/api/v1/execution/operations/"+v.Operation.ID+"/execute", key, execInput(v), 200))
}

type timeoutCancelSimulator struct{}

func (timeoutCancelSimulator) ValidateLocalCancellation(context.Context, *entity.Subscription) error {
	return context.DeadlineExceeded
}

func TestExecutionAdditionalBoundaries(t *testing.T) {
	t.Run("PersistedConsentWithoutLegacyBooleanAndKeyConflict", func(t *testing.T) {
		h := harness(t)
		installExecutionHTTP(t, h, subscription.LocalSimulator{})
		v := execPrepare(t, h, "open_account", service.AccountInput{DisplayName: "Authorized opening", InitialBalanceMinor: 1000, Currency: "CNY"})
		h.call(t, "POST", "/api/v1/execution/operations/"+v.Operation.ID+"/authorize", "", map[string]any{"confirmed": true}, 400)
		authorized := execAuthorize(t, h, v)
		result := execRun(t, h, authorized, "one-opening")
		if result.Operation.Status != "succeeded" {
			t.Fatalf("opening without boolean failed: %s", result.Operation.ErrorCode)
		}
		var account entity.BankAccount
		if e := json.Unmarshal(result.Operation.Result, &account); e != nil {
			t.Fatal(e)
		}
		h.balance(t, account.ID, 1000)
		replay := execRun(t, h, authorized, "one-opening")
		if replay.Operation.Status != "succeeded" {
			t.Fatal("replay changed result")
		}
		h.balance(t, account.ID, 1000)
		changed := execInput(authorized)
		changed.IntentDigest = "wrong"
		h.call(t, "POST", "/api/v1/execution/operations/"+v.Operation.ID+"/execute", "one-opening", changed, 409)
		raw := json.RawMessage(`{"display_name":"injected","initial_balance_minor":1,"confirmed":true}`)
		h.call(t, "POST", "/api/v1/execution/operations", uuid.NewString(), execution.PrepareInput{Kind: "open_account", Parameters: raw}, 400)
	})
	t.Run("AgentAndToolCannotAuthorizeOrExecute", func(t *testing.T) {
		h := harness(t)
		installExecutionHTTP(t, h, subscription.LocalSimulator{})
		v := execPrepare(t, h, "open_account", service.AccountInput{DisplayName: "User controlled"})
		agent, e := h.auth.IssueAgent(fmt.Sprint(h.uid), "billing", uuid.NewString(), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		for _, token := range []string{agent, h.cfg.ToolToken} {
			for _, suffix := range []string{"authorize", "execute"} {
				status, out, e := h.raw("POST", "/api/v1/execution/operations/"+v.Operation.ID+"/"+suffix, uuid.NewString(), map[string]any{}, token)
				if e != nil || status < 400 || out.Code == "OK" {
					t.Fatalf("privileged %s: status %d code %s err %v", suffix, status, out.Code, e)
				}
			}
		}
		for _, path := range []string{"/api/v1/simulation/accounts", "/api/v1/transfers", "/api/v1/transactions/import"} {
			h.call(t, "POST", path, uuid.NewString(), map[string]any{"confirmed": true}, 403)
		}
	})
	t.Run("DirectTrustedServiceBypassRejected", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		core := service.New(repository.New(h.conn.DB), risk.TrustedGateway{})
		m := service.Meta{UserID: h.uid}
		if _, e := core.CreateAccount(context.Background(), m, uuid.NewString(), service.AccountInput{DisplayName: "bypass", InitialBalanceMinor: 100, ConfirmSimulatedFunds: true}); entity.Code(e) != "APPROVAL_REQUIRED" {
			t.Fatalf("opening bypass: %v", e)
		}
		if _, e := core.CreateTransfer(context.Background(), m, uuid.NewString(), service.TransferInput{SourceAccountID: a.ID, TargetAccountID: b.ID, AmountMinor: 100}); entity.Code(e) != "APPROVAL_REQUIRED" {
			t.Fatalf("transfer bypass: %v", e)
		}
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 0)
	})
	for _, mode := range []string{"local_failure", "changed_version", "local_timeout"} {
		t.Run("Cancellation_"+mode, func(t *testing.T) {
			h := harness(t)
			a := h.account(t, 1000)
			sub := entity.Subscription{Base: entity.Base{UserID: h.uid}, BankAccountID: a.ID, Fingerprint: uuid.NewString(), MerchantName: "Test service", Category: "subscription", BillingInterval: "monthly", AmountMinor: 100, Currency: "CNY", NextBillingAt: time.Now().AddDate(0, 1, 0).UnixMilli(), ConfidenceBPS: 10000, Status: entity.SubscriptionActive, Version: 1, IsSimulated: true}
			if e := h.conn.DB.Create(&sub).Error; e != nil {
				t.Fatal(e)
			}
			var sim subscription.Simulator = subscription.LocalSimulator{}
			if mode == "local_failure" {
				sim = failingCancelSimulator{}
			}
			if mode == "local_timeout" {
				sim = timeoutCancelSimulator{}
			}
			installExecutionHTTP(t, h, sim)
			v := execAuthorize(t, h, execPrepare(t, h, "cancel_subscription", subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: 1}))
			if mode == "changed_version" {
				if e := h.conn.DB.Model(&entity.Subscription{}).Where("id=?", sub.ID).Update("version", 2).Error; e != nil {
					t.Fatal(e)
				}
			}
			if mode == "local_timeout" {
				h.call(t, "POST", "/api/v1/execution/operations/"+v.Operation.ID+"/execute", uuid.NewString(), execInput(v), 503)
				var op entity.OperationIntent
				if e := h.conn.DB.First(&op, "id=?", v.Operation.ID).Error; e != nil {
					t.Fatal(e)
				}
				if op.Status != "authorized" {
					t.Fatalf("timeout committed state %s", op.Status)
				}
			} else {
				result := execRun(t, h, v, uuid.NewString())
				if result.Operation.Status != "failed" {
					t.Fatalf("unsafe cancellation result: %+v", result.Operation)
				}
			}
			var saved entity.Subscription
			if e := h.conn.DB.First(&saved, sub.ID).Error; e != nil {
				t.Fatal(e)
			}
			if saved.Status != entity.SubscriptionActive {
				t.Fatal("failed cancellation changed subscription")
			}
			var consent entity.UserAuthorization
			if e := h.conn.DB.First(&consent, "id=?", v.Operation.AuthorizationID).Error; e != nil {
				t.Fatal(e)
			}
			if mode == "local_timeout" && consent.ConsumedAt != 0 {
				t.Fatal("timeout consumed authorization")
			}
			if mode != "local_timeout" && consent.ConsumedAt == 0 {
				t.Fatal("failed terminal attempt left reusable consent")
			}
			h.balance(t, a.ID, 1000)
		})
	}
}
