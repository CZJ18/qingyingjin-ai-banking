package modelgateway

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPaidGatewayRequiresFreshApprovalEvenWithUserVerifier(t *testing.T) {
	_, err := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier,
		VerifyIdentity: func(context.Context, string) (int64, error) { return 42, nil },
		Outbound:       outbound.Policy{Environment: "production"}}, &memoryBudget{})
	if err == nil {
		t.Fatal("paid gateway accepted identity without a fresh bounded approval")
	}
}

func TestSimulationApprovalCannotAuthorizeDevelopmentOrProduction(t *testing.T) {
	p := policy()
	a := constructorApproval(p)
	a.Simulation = true
	a.ProviderOrigin = "http://127.0.0.1:29489/v1"
	cfg := Config{Policy: p, Approval: &a, RunID: a.RunID, Database: a.Database, GatewayToken: token, SupplierKey: supplier, BaseURL: a.ProviderOrigin}
	for _, environment := range []string{"development", "production"} {
		cfg.Outbound = outbound.Policy{Environment: environment}
		if _, err := New(cfg, &approvalBoundBudget{fingerprint: a.Fingerprint()}); err == nil {
			t.Fatal("simulation enabled outside test", environment)
		}
	}
	cfg.Outbound = outbound.TestPolicy()
	if _, err := New(cfg, &approvalBoundBudget{fingerprint: a.Fingerprint()}); err != nil {
		t.Fatal("explicit bounded simulation denied", err)
	}
}

type approvalBoundBudget struct {
	memoryBudget
	fingerprint string
}

func (b *approvalBoundBudget) ApprovalFingerprint() string { return b.fingerprint }

func constructorApproval(p modelspend.Policy) modelspend.Approval {
	hash, _ := p.Hash()
	return modelspend.Approval{ID: "synthetic-constructor-only", RunID: "synthetic-run", ProviderOrigin: "https://api.deepseek.com/v1", Model: p.Model, ProjectID: p.ProjectID, Database: "synthetic_test", Currency: p.Currency, Amount: "1", PolicySHA256: hash, Scope: "constructor only; no supplier request authorized", UserID: 42, TaskRequestSHA256: strings.Repeat("b", 64), WorkflowID: "123", WorkflowSHA256: strings.Repeat("c", 64), ValidFrom: p.ValidFrom, ValidUntil: p.ValidUntil, MaxRequests: 1, MaxConcurrent: 1, ContextTokens: p.ContextTokens, MaxOutputTokens: p.MaxOutputTokens}
}

func TestPaidGatewayAcceptsMatchingApprovedLedgerWithoutDispatch(t *testing.T) {
	p := policy()
	a := constructorApproval(p)
	cfg := Config{Policy: p, GatewayToken: token, SupplierKey: supplier, VerifyIdentity: func(context.Context, string) (int64, error) { return 42, nil }, Outbound: outbound.Policy{Environment: "production"}, Approval: &a, RunID: a.RunID, Database: a.Database}
	if a.Validate(p, a.RunID, a.Database, time.Now()) != nil {
		t.Fatal("invalid synthetic constructor fixture")
	}
	if _, e := New(cfg, &approvalBoundBudget{fingerprint: a.Fingerprint()}); e != nil {
		t.Fatal(e)
	}
	if _, e := New(cfg, &approvalBoundBudget{fingerprint: strings.Repeat("a", 64)}); e == nil {
		t.Fatal("unbound ledger accepted")
	}
}

func TestReservationReceiptSurvivesIdempotentReplay(t *testing.T) {
	budget := &memoryBudget{}
	handler, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, budget)
	first := call(handler, validBody, "receipt-key", "Bearer "+token)
	replay := call(handler, validBody, "receipt-key", "Bearer "+token)
	if first.Code != 200 || replay.Code != 200 || first.Header().Get("X-Model-Reservation-ID") == "" || first.Header().Get("X-Model-Reservation-ID") != replay.Header().Get("X-Model-Reservation-ID") || first.Header().Get("X-Model-Request-Fingerprint") == "" || len(budget.attempts) != 1 {
		t.Fatal("missing stable reservation receipt", first.Header(), replay.Header())
	}
}
