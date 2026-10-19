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
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coreHarness struct {
	conn     *platform.Connections
	server   *httptest.Server
	auth     *identity.Auth
	token    string
	uid      int64
	svc      *service.Core
	cfg      config.Config
	contract routers.Router
}
type response struct {
	Code string          `json:"code"`
	Data json.RawMessage `json:"data"`
}

type rejectingGateway struct{}

func fixtureCozeClient(t *testing.T, baseURL, pat string, timeout time.Duration) *coze.Client {
	t.Helper()
	client, err := coze.New(baseURL, pat, timeout, outbound.TestPolicy())
	if err != nil {
		t.Fatal("test Coze client initialization:", err)
	}
	return client
}

func (rejectingGateway) Check(context.Context, int64, *entity.TransferIntent, *entity.Approval) error {
	return entity.Fail("RISK_REJECTED", "injected policy refusal")
}
func (rejectingGateway) CheckFunding(context.Context, int64, *entity.Transaction, *entity.Audit) error {
	return entity.Fail("RISK_REJECTED", "injected funding policy refusal")
}

func harness(t *testing.T) *coreHarness {
	t.Helper()
	if p := os.Getenv("BANKING_TEST_ENV_FILE"); p != "" {
		if err := godotenv.Load(p); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	if err = migration.Apply(context.Background(), conn.SQL); err != nil {
		t.Fatal(err)
	}
	if err = migration.Apply(context.Background(), conn.SQL); err != nil {
		t.Fatal("repeat migration:", err)
	}
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID)
	svc := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	uid := rand.Int64N(1_000_000_000_000) + 1_000_000_000_000
	token, err := auth.IssueUser(fmt.Sprint(uid), "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httpapi.New(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), map[string]httpapi.Checker{"mysql": conn.CheckMySQL, "redis": conn.CheckRedis}, 2*time.Second, svc)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	doc.Servers = openapi3.Servers{{URL: server.URL}}
	contract, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	h := &coreHarness{conn: conn, server: server, auth: auth, token: token, uid: uid, svc: svc, cfg: cfg, contract: contract}
	h.call(t, "POST", "/api/v1/users/me", "", map[string]any{"display_name": "Integration simulation"}, 201)
	return h
}
func (h *coreHarness) raw(method, path, key string, body any, token string) (int, response, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return 0, response{}, err
		}
	}
	req, err := http.NewRequest(method, h.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Trace-ID", "core-integration")
	req.Header.Set("X-Task-ID", "module2-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, response{}, err
	}
	defer resp.Body.Close()
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, response{}, err
	}
	requestRoute, params, err := h.contract.FindRoute(req)
	if err != nil {
		return resp.StatusCode, response{}, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	validation := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: requestRoute, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if resp.StatusCode < 300 {
		if err = openapi3filter.ValidateRequest(context.Background(), validation); err != nil {
			return resp.StatusCode, response{}, fmt.Errorf("request contract %s %s: %w", method, path, err)
		}
	}
	if err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: validation, Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(responseBytes))}); err != nil {
		return resp.StatusCode, response{}, fmt.Errorf("response contract %s %s: %w", method, path, err)
	}
	var out response
	err = json.Unmarshal(responseBytes, &out)
	return resp.StatusCode, out, err
}
func (h *coreHarness) call(t *testing.T, method, path, key string, body any, status int) response {
	t.Helper()
	actual, out, err := h.raw(method, path, key, body, h.token)
	if err != nil {
		t.Fatal(err)
	}
	if actual != status {
		t.Fatalf("%s %s expected %d got %d code %s data %s", method, path, status, actual, out.Code, out.Data)
	}
	return out
}
func parse[T any](t *testing.T, out response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(out.Data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func (h *coreHarness) account(t *testing.T, amount int64) entity.BankAccount {
	return parse[entity.BankAccount](t, h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), service.AccountInput{DisplayName: "Simulation", Currency: "CNY", InitialBalanceMinor: amount, ConfirmSimulatedFunds: true}, 201))
}
func (h *coreHarness) balance(t *testing.T, id, amount int64) {
	t.Helper()
	b := parse[entity.AccountBalance](t, h.call(t, "GET", fmt.Sprintf("/api/v1/accounts/%d/balance", id), "", nil, 200))
	if b.BalanceMinor != amount || b.LedgerBalanceMinor != amount || !b.Reconciled {
		t.Fatalf("balance=%+v want %d", b, amount)
	}
}
func (h *coreHarness) transfer(t *testing.T, source, target, amount int64) entity.TransferIntent {
	return parse[entity.TransferIntent](t, h.call(t, "POST", "/api/v1/transfers", uuid.NewString(), service.TransferInput{SourceAccountID: source, TargetAccountID: target, AmountMinor: amount, Currency: "CNY"}, 201))
}
func (h *coreHarness) approve(t *testing.T, v entity.TransferIntent, status int) entity.TransferIntent {
	return parse[entity.TransferIntent](t, h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, status))
}
func TestBankingCore(t *testing.T) {
	t.Run("SafetyGatewayControlsAllFundingPaths", func(t *testing.T) {
		h := harness(t)
		blocked := service.New(repository.New(h.conn.DB), rejectingGateway{})
		m := service.Meta{UserID: h.uid, TraceID: "gateway-test"}
		if _, err := blocked.CreateAccount(context.Background(), m, uuid.NewString(), service.AccountInput{DisplayName: "blocked", InitialBalanceMinor: 100, Currency: "CNY", ConfirmSimulatedFunds: true}); entity.Code(err) != "RISK_REJECTED" {
			t.Fatalf("opening bypassed gateway: %v", err)
		}
		var accountCount int64
		h.conn.DB.Model(&entity.BankAccount{}).Where("user_id=?", h.uid).Count(&accountCount)
		if accountCount != 0 {
			t.Fatal("rejected opening account persisted")
		}
		a := h.account(t, 1000)
		b := h.account(t, 0)
		if _, err := blocked.ImportTransaction(context.Background(), m, uuid.NewString(), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 100, Currency: "CNY", Category: "food", OccurredAt: time.Now().UnixMilli(), EffectMode: "apply_simulated_effect", ConfirmSimulatedEffect: true}); entity.Code(err) != "RISK_REJECTED" {
			t.Fatalf("import bypassed gateway: %v", err)
		}
		v := h.transfer(t, a.ID, b.ID, 100)
		failed, err := blocked.ApproveTransfer(context.Background(), m, v.ID, service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash})
		if err != nil || failed.Status != "failed" || failed.FailureCode != "RISK_REJECTED" {
			t.Fatalf("transfer bypassed gateway: %+v %v", failed, err)
		}
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 0)
	})
	t.Run("OpeningLedgerOwnershipAndIntegerAmounts", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		h.balance(t, a.ID, 1000)
		rows := parse[[]entity.LedgerEntry](t, h.call(t, "GET", fmt.Sprintf("/api/v1/accounts/%d/ledger", a.ID), "", nil, 200))
		if len(rows) != 1 || rows[0].DeltaMinor != 1000 {
			t.Fatalf("opening ledger=%+v", rows)
		}
		other, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		for _, path := range []string{fmt.Sprintf("/api/v1/accounts/%d", a.ID), fmt.Sprintf("/api/v1/accounts/%d/balance", a.ID), fmt.Sprintf("/api/v1/accounts/%d/ledger", a.ID)} {
			status, out, err := h.raw("GET", path, "", nil, other)
			if err != nil || status != 404 || out.Code != "NOT_FOUND" {
				t.Fatalf("ownership %s %d %v", path, status, err)
			}
		}
		h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), service.AccountInput{DisplayName: "No approval", InitialBalanceMinor: 100}, 422)
		h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), map[string]any{"display_name": "fraction", "initial_balance_minor": 1.5}, 400)
		h.call(t, "PATCH", fmt.Sprintf("/api/v1/accounts/%d/status", a.ID), "", map[string]any{"status": "active", "balance_minor": 999999}, 400)
		status, _, err := h.raw("POST", "/api/v1/transfers", "", nil, h.cfg.ToolToken)
		if err != nil || status != 401 {
			t.Fatalf("tool privilege %d %v", status, err)
		}
	})
	t.Run("TransactionEffectsFilteringAndIdempotency", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		now := time.Now().UnixMilli()
		in := service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 100, Currency: "CNY", Category: "food", MerchantName: "Market", OccurredAt: now, EffectMode: "record_only"}
		key := uuid.NewString()
		first := parse[entity.Transaction](t, h.call(t, "POST", "/api/v1/transactions/import", key, in, 201))
		second := parse[entity.Transaction](t, h.call(t, "POST", "/api/v1/transactions/import", key, in, 201))
		if first.ID != second.ID {
			t.Fatal("duplicate import")
		}
		h.balance(t, a.ID, 1000)
		in.AmountMinor = 101
		h.call(t, "POST", "/api/v1/transactions/import", key, in, 409)
		in.AmountMinor = 100
		in.ExternalTransactionID = uuid.NewString()
		in.EffectMode = "apply_simulated_effect"
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 422)
		in.ConfirmSimulatedEffect = true
		applied := parse[entity.Transaction](t, h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 201))
		h.balance(t, a.ID, 900)
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 409)
		h.balance(t, a.ID, 900)
		out := h.call(t, "GET", fmt.Sprintf("/api/v1/transactions?bank_account_id=%d&category=food&merchant=Market&direction=expense&from=%d&to=%d&limit=1&offset=1", a.ID, now-1, now+1), "", nil, 200)
		page := parse[struct {
			Items []entity.Transaction `json:"items"`
			Total int64                `json:"total"`
		}](t, out)
		if page.Total != 2 || len(page.Items) != 1 {
			t.Fatalf("filtered page %+v", page)
		}
		if applied.JournalID == nil || applied.AuditID == "" || applied.TraceID != "core-integration" || applied.TaskID != "module2-test" {
			t.Fatal("correlation missing")
		}
		in.ExternalTransactionID = uuid.NewString()
		in.Direction = "income"
		in.AmountMinor = 200
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 201)
		h.balance(t, a.ID, 1100)
	})
	t.Run("InsufficientFrozenExpiredRejectedAndCrossTenant", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 100)
		b := h.account(t, 0)
		v := h.transfer(t, a.ID, b.ID, 101)
		failed := h.approve(t, v, 422)
		if failed.Status != "failed" || failed.FailureCode != "INSUFFICIENT_FUNDS" {
			t.Fatalf("failed %+v", failed)
		}
		h.approve(t, v, 422)
		h.balance(t, a.ID, 100)
		h.balance(t, b.ID, 0)
		h.call(t, "PATCH", fmt.Sprintf("/api/v1/accounts/%d/status", a.ID), "", map[string]string{"status": "frozen"}, 200)
		v = h.transfer(t, a.ID, b.ID, 10)
		failed = h.approve(t, v, 422)
		if failed.FailureCode != "ACCOUNT_FROZEN" {
			t.Fatal(failed)
		}
		h.balance(t, a.ID, 100)
		h.call(t, "PATCH", fmt.Sprintf("/api/v1/accounts/%d/status", a.ID), "", map[string]string{"status": "active"}, 200)
		v = h.transfer(t, a.ID, b.ID, 10)
		h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: fmt.Sprintf("%064d", 0)}, 409)
		h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "reject", ApprovalHash: v.ApprovalHash}, 200)
		h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, 409)
		expired := h.transfer(t, a.ID, b.ID, 10)
		expired.ExpiresAt = time.Now().Add(-time.Minute).UnixMilli()
		expired.ApprovalHash = entity.TransferDigest(&expired)
		if err := h.conn.DB.Model(&entity.TransferIntent{}).Where("id=?", expired.ID).Updates(map[string]any{"expires_at": expired.ExpiresAt, "approval_hash": expired.ApprovalHash}).Error; err != nil {
			t.Fatal(err)
		}
		failed = h.approve(t, expired, 422)
		if failed.FailureCode != "APPROVAL_EXPIRED" {
			t.Fatal(failed)
		}
		other, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		h.call(t, "POST", "/api/v1/users/me", "", map[string]string{"display_name": "self"}, 201)
		if _, err := h.svc.Register(context.Background(), service.Meta{UserID: h.uid + 1}, "other"); err != nil {
			t.Fatal(err)
		}
		status, _, err := h.raw("POST", "/api/v1/transfers", uuid.NewString(), service.TransferInput{SourceAccountID: a.ID, TargetAccountID: b.ID, AmountMinor: 1, Currency: "CNY"}, other)
		if err != nil || status != 404 {
			t.Fatalf("cross-tenant transfer %d %v", status, err)
		}
	})
	t.Run("RepeatedTransferApprovalPostsExactlyOnce", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		v := h.transfer(t, a.ID, b.ID, 100)
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				status, out, err := h.raw("POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, h.token)
				if err != nil {
					errs <- err
				} else if status != 200 {
					errs <- fmt.Errorf("status %d code %s", status, out.Code)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		h.balance(t, a.ID, 900)
		h.balance(t, b.ID, 100)
		var count int64
		h.conn.DB.Model(&entity.Transaction{}).Where("transfer_id=?", v.ID).Count(&count)
		if count != 2 {
			t.Fatalf("transfer transaction count %d", count)
		}
	})
	t.Run("ConcurrentIntentCreationUsesDatabaseIdempotency", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		key := uuid.NewString()
		in := service.TransferInput{SourceAccountID: a.ID, TargetAccountID: b.ID, AmountMinor: 100, Currency: "CNY"}
		var wg sync.WaitGroup
		ids := make(chan int64, 16)
		errs := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, err := h.svc.CreateTransfer(context.Background(), service.Meta{UserID: h.uid, TraceID: "idem-test"}, key, in)
				if err != nil {
					errs <- err
				} else {
					ids <- v.ID
				}
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		var original int64
		for id := range ids {
			if original == 0 {
				original = id
			}
			if original != id {
				t.Fatalf("multiple intents %d %d", original, id)
			}
		}
		if original == 0 {
			t.Fatal("no persisted intent")
		}
		in.AmountMinor = 101
		h.call(t, "POST", "/api/v1/transfers", key, in, 409)
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 0)
		other, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		if _, err := h.svc.Register(context.Background(), service.Meta{UserID: h.uid + 1}, "other"); err != nil {
			t.Fatal(err)
		}
		v, err := h.svc.Transfer(context.Background(), h.uid, original)
		if err != nil {
			t.Fatal(err)
		}
		status, _, err := h.raw("POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, other)
		if err != nil || status != 404 {
			t.Fatalf("unauthorized approval %d %v", status, err)
		}
	})
	t.Run("ConcurrentTransfersCannotOverdraw", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 500)
		b := h.account(t, 0)
		intents := make([]entity.TransferIntent, 12)
		for i := range intents {
			intents[i] = h.transfer(t, a.ID, b.ID, 100)
		}
		var wg sync.WaitGroup
		var success atomic.Int64
		errs := make(chan error, len(intents))
		for _, v := range intents {
			wg.Add(1)
			go func(v entity.TransferIntent) {
				defer wg.Done()
				status, out, err := h.raw("POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, h.token)
				if err != nil {
					errs <- err
				} else if status == 200 {
					success.Add(1)
				} else if status != 422 || out.Code != "INSUFFICIENT_FUNDS" {
					errs <- fmt.Errorf("status %d code %s", status, out.Code)
				}
			}(v)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if success.Load() != 5 {
			t.Fatalf("success %d", success.Load())
		}
		h.balance(t, a.ID, 0)
		h.balance(t, b.ID, 500)
	})
	t.Run("OppositeTransfersUseConsistentLockOrder", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 1000)
		var intents []entity.TransferIntent
		for i := 0; i < 8; i++ {
			intents = append(intents, h.transfer(t, a.ID, b.ID, 10), h.transfer(t, b.ID, a.ID, 10))
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(intents))
		for _, v := range intents {
			wg.Add(1)
			go func(v entity.TransferIntent) {
				defer wg.Done()
				_, err := h.svc.ApproveTransfer(context.Background(), service.Meta{UserID: h.uid, TraceID: "lock-test"}, v.ID, service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash})
				if err != nil {
					errs <- err
				}
			}(v)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 1000)
	})
	t.Run("TransactionRollbackAfterFirstBalanceWrite", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		v := h.transfer(t, a.ID, b.ID, 100)
		var writes atomic.Int64
		callback := "integration:fail-second-balance-write"
		if err := h.conn.DB.Callback().Update().Before("gorm:update").Register(callback, func(db *gorm.DB) {
			if db.Statement.Table == "banking_bank_accounts" && writes.Add(1) == 2 {
				db.AddError(errors.New("injected second account update failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, 503)
		h.conn.DB.Callback().Update().Remove(callback)
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 0)
		unchanged := parse[entity.TransferIntent](t, h.call(t, "GET", fmt.Sprintf("/api/v1/transfers/%d", v.ID), "", nil, 200))
		if unchanged.Status != "pending_approval" {
			t.Fatal("failure became final before commit")
		}
		var count int64
		h.conn.DB.Model(&entity.Transaction{}).Where("transfer_id=?", v.ID).Count(&count)
		if count != 0 {
			t.Fatal("partial transactions committed")
		}
		h.conn.DB.Model(&entity.Approval{}).Where("transfer_id=?", v.ID).Count(&count)
		if count != 0 {
			t.Fatal("approval committed after rollback")
		}
		h.approve(t, v, 200)
		h.balance(t, a.ID, 900)
		h.balance(t, b.ID, 100)
	})
	t.Run("AuditFailureRollsBackFundsAndJournals", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		v := h.transfer(t, a.ID, b.ID, 100)
		callback := "integration:fail-audit"
		h.conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
			if db.Statement.Table == "banking_core_audits" {
				db.AddError(errors.New("injected audit write failure"))
			}
		})
		h.call(t, "POST", fmt.Sprintf("/api/v1/transfers/%d/approvals", v.ID), "", service.ApprovalInput{Decision: "approve", ApprovalHash: v.ApprovalHash}, 503)
		h.conn.DB.Callback().Create().Remove(callback)
		h.balance(t, a.ID, 1000)
		h.balance(t, b.ID, 0)
		h.approve(t, v, 200)
	})
	t.Run("BudgetActualUsageAndSavingsLedger", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 10000)
		month := time.Now().UTC().Format("2006-01")
		b := parse[entity.Budget](t, h.call(t, "POST", "/api/v1/budgets", uuid.NewString(), service.BudgetInput{Month: month, Currency: "CNY", TotalMinor: 5000, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 2000, EssentialReserveMinor: 1500}, {Category: "transport", AllocatedMinor: 1000, EssentialReserveMinor: 500}}, EnforceReserve: true}, 201))
		in := service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 1000, Currency: "CNY", Category: "food", OccurredAt: time.Now().UnixMilli(), EffectMode: "apply_simulated_effect", ConfirmSimulatedEffect: true}
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 201)
		usage := parse[service.BudgetUsage](t, h.call(t, "GET", fmt.Sprintf("/api/v1/budgets/%d/usage", b.ID), "", nil, 200))
		if usage.SpentMinor != 1000 || usage.RemainingMinor != 4000 || usage.EssentialReserveRemainingMinor != 1000 || usage.AvailableAfterReserveMinor != 3000 {
			t.Fatalf("usage %+v", usage)
		}
		now := time.Now()
		g := parse[entity.SavingsGoal](t, h.call(t, "POST", "/api/v1/savings/goals", uuid.NewString(), service.GoalInput{Name: "规律运动和正常饮食", TargetMinor: 5000, Currency: "CNY", StartsAt: now.UnixMilli(), EndsAt: now.AddDate(0, 6, 0).UnixMilli(), MonthlyMinor: 1000}, 201))
		dep := parse[entity.TransferIntent](t, h.call(t, "POST", fmt.Sprintf("/api/v1/savings/goals/%d/deposits", g.ID), uuid.NewString(), service.GoalTransferInput{BankAccountID: a.ID, AmountMinor: 1000}, 201))
		h.balance(t, g.AccountID, 0)
		h.approve(t, dep, 200)
		got := parse[entity.SavingsGoal](t, h.call(t, "GET", fmt.Sprintf("/api/v1/savings/goals/%d", g.ID), "", nil, 200))
		if got.CurrentMinor != 1000 || got.ProgressBasisPoints != 2000 || got.Plan == nil {
			t.Fatalf("goal %+v", got)
		}
		h.call(t, "POST", "/api/v1/transfers", uuid.NewString(), service.TransferInput{SourceAccountID: a.ID, TargetAccountID: g.AccountID, AmountMinor: 1, Currency: "CNY"}, 400)
		withdrawal := parse[entity.TransferIntent](t, h.call(t, "POST", fmt.Sprintf("/api/v1/savings/goals/%d/withdrawals", g.ID), uuid.NewString(), service.GoalTransferInput{BankAccountID: a.ID, AmountMinor: 400}, 201))
		h.approve(t, withdrawal, 200)
		h.balance(t, g.AccountID, 600)
		h.balance(t, a.ID, 8400)
		ledger := parse[[]entity.SavingsLedger](t, h.call(t, "GET", fmt.Sprintf("/api/v1/savings/goals/%d/ledger", g.ID), "", nil, 200))
		if len(ledger) != 2 || ledger[0].DeltaMinor+ledger[1].DeltaMinor != 600 {
			t.Fatalf("savings ledger %+v", ledger)
		}
	})
	t.Run("LedgerBalanceAndPersistenceAcrossConnections", func(t *testing.T) {
		h := harness(t)
		a := h.account(t, 1000)
		b := h.account(t, 0)
		v := h.transfer(t, a.ID, b.ID, 100)
		h.approve(t, v, 200)
		var invalid int64
		err := h.conn.DB.Raw("SELECT COUNT(*) FROM (SELECT journal_id FROM banking_ledger_entries WHERE user_id=? GROUP BY journal_id HAVING COUNT(*)<>2 OR SUM(delta_minor)<>0 OR COUNT(DISTINCT currency)<>1) invalid", h.uid).Scan(&invalid).Error
		if err != nil || invalid != 0 {
			t.Fatalf("unbalanced journals=%d %v", invalid, err)
		}
		fresh, err := platform.Open(h.cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		persisted, err := repository.New(fresh.DB).Balance(context.Background(), h.uid, a.ID)
		if err != nil || persisted.BalanceMinor != 900 || !persisted.Reconciled {
			t.Fatalf("reconnect persistence %+v %v", persisted, err)
		}
	})
}
