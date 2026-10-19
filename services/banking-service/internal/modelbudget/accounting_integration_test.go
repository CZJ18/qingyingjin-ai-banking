//go:build integration

package modelbudget

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReconciliationAuditFailureRollsBackReservationAndUsage(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	id, _, _ := s.Begin(ctx, user, "audit-failure", "123", strings.Repeat("f", 64))
	if e := s.Complete(ctx, user, id, nil, nil, "", false); e != nil {
		t.Fatal(e)
	}
	c.DB.Callback().Create().Before("gorm:create").Register("synthetic_audit_fault", func(tx *gorm.DB) {
		if tx.Statement.Table == "banking_model_reconciliations" {
			tx.AddError(errors.New("synthetic audit unavailable"))
		}
	})
	defer c.DB.Callback().Create().Remove("synthetic_audit_fault")
	token := strings.Repeat("z", 32)
	admin, _ := NewReconciler(c.DB, token, "operator")
	n := int64(3)
	if e := admin.Reconcile(ctx, token, "audit-failure", ReconcileInput{UserID: user, CallID: id, Tokens: &n, EvidenceRef: "synthetic:failure"}); e == nil {
		t.Fatal("settlement without audit")
	}
	var w Window
	c.DB.Where("user_id=?", user).Take(&w)
	var call Call
	c.DB.Where("id=?", id).Take(&call)
	if w.TokensUsed != 0 || w.TokensReserved != 10 || call.ActualTokens != nil || call.ReservedTokens != 10 {
		t.Fatal("partial settlement committed", w, call)
	}
}

func TestPostgresRuntimeRoleCannotWriteReconciliationAudit(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	if c.DB.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL role-privilege regression")
	}
	ctx := context.Background()
	id, _, _ := s.Begin(ctx, user, "runtime-role", "123", strings.Repeat("a", 64))
	if e := s.Complete(ctx, user, id, nil, nil, "", false); e != nil {
		t.Fatal(e)
	}
	role := fmt.Sprintf("hardening_audit_%d", user)
	for _, q := range []string{"CREATE ROLE " + role + " NOLOGIN", "GRANT USAGE ON SCHEMA public TO " + role, "GRANT SELECT,UPDATE ON banking_users,banking_model_calls,banking_model_budget_windows TO " + role, "GRANT SELECT ON banking_model_reconciliations TO " + role} {
		if e := c.DB.Exec(q).Error; e != nil {
			t.Fatal(e)
		}
	}
	token := strings.Repeat("z", 32)
	n := int64(3)
	e := c.DB.Connection(func(db *gorm.DB) error {
		if e := db.Exec("SET ROLE " + role).Error; e != nil {
			return e
		}
		defer db.Exec("RESET ROLE")
		admin, _ := NewReconciler(db, token, "runtime-probe")
		if e := admin.Reconcile(ctx, token, "runtime-denied", ReconcileInput{UserID: user, CallID: id, Tokens: &n, EvidenceRef: "synthetic:permissions"}); e == nil {
			return errors.New("runtime role wrote audit")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	var w Window
	c.DB.Where("user_id=?", user).Take(&w)
	var call Call
	c.DB.Where("id=?", id).Take(&call)
	if w.TokensUsed != 0 || w.TokensReserved != 10 || call.ActualTokens != nil {
		t.Fatal("permission error partially settled budget")
	}
}

func TestRealLocalHTTPPersistsUntrustedCostAndPendingUsage(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 20, DailyTokens: 300, ReservationTokens: 10})
	for index, tc := range []struct {
		cost  string
		token string
		ok    bool
		state string
	}{
		{`"0.00000"`, `,"token":3`, true, "reported_unverified"},
		{`null`, "", true, "missing"},
		{`"-1.00"`, `,"token":3`, false, "invalid"}, {`"NaN"`, `,"token":3`, false, "invalid"},
		{`"Infinity"`, `,"token":3`, false, "invalid"}, {`"1e309"`, `,"token":3`, false, "invalid"},
		{`"0.123456789"`, `,"token":3`, false, "invalid"}, {`123`, `,"token":3`, false, "invalid"},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"code":0,"execute_id":"456","data":"{}","cost":`+tc.cost+tc.token+`}`)
			}))
			defer server.Close()
			client, e := coze.NewBudgeted(server.URL, "synthetic-only", time.Second, s, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			key := fmt.Sprintf("http-%d", index)
			_, e = client.RunSyncWorkflow(coze.WithIdentity(context.Background(), user, key), coze.RunRequest{WorkflowID: "123", TraceID: key})
			if (e == nil) != tc.ok {
				t.Fatal("unexpected model result", e)
			}
			var call Call
			if e = c.DB.Where("user_id=?", user).Order("created_at DESC,id DESC").Where("request_key=?", fmt.Sprintf("%x", sha256.Sum256([]byte(key)))).Take(&call).Error; e != nil {
				t.Fatal(e)
			}
			if call.CostStatus != tc.state || call.CostCurrency != "" || call.CostUnit != "" || call.ConfirmedCost != nil || len(call.ReportDigest) != 64 {
				t.Fatal(call)
			}
			if index == 0 {
				if call.ActualTokens == nil || *call.ActualTokens != 3 || call.UsageStatus != "settled" {
					t.Fatal(call)
				}
			} else {
				if call.ActualTokens != nil || call.ReservedTokens != 10 || call.UsageStatus != "pending" {
					t.Fatal(call)
				}
			}
		})
	}
}

func TestConcurrentAndLateSettlementCannotReleaseTwice(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	id, _, _ := s.Begin(ctx, user, "lost", "123", strings.Repeat("d", 64))
	if e := s.Complete(ctx, user, id, nil, nil, "", false); e != nil {
		t.Fatal(e)
	}
	token := strings.Repeat("z", 32)
	admin, _ := NewReconciler(c.DB, token, "operator")
	n := int64(3)
	in := ReconcileInput{UserID: user, CallID: id, Tokens: &n, EvidenceRef: "provider:confirmed-closed"}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errors <- admin.Reconcile(ctx, token, "one-settlement", in) }()
	}
	close(start)
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	// A late successful HTTP response must not apply the same tokens again.
	if e := s.Complete(ctx, user, id, []byte(`{"code":0,"execute_id":"456","data":"{}","token":3}`), &n, "0.00", true); e != nil {
		t.Fatal(e)
	}
	var w Window
	c.DB.Where("user_id=?", user).Take(&w)
	if w.CallsUsed != 1 || w.TokensUsed != 3 || w.TokensReserved != 0 {
		t.Fatal(w)
	}
}

func TestReconciliationRequiresCostMetadataAndPreservesUnknownReservations(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	id, _, _ := s.Begin(ctx, user, "crash", "123", strings.Repeat("e", 64))
	token := strings.Repeat("z", 32)
	admin, _ := NewReconciler(c.DB, token, "operator")
	n := int64(2)
	in := ReconcileInput{UserID: user, CallID: id, Tokens: &n, EvidenceRef: "provider:confirmed-stopped"}
	if e := admin.Reconcile(ctx, token, "crash", in); e != coze.ErrOutcomeUnknown {
		t.Fatal("in-flight reservation released", e)
	}
	in.ConfirmNoActiveRun = true
	if e := admin.Reconcile(ctx, token, "crash", in); e != nil {
		t.Fatal(e)
	}
	cost := "0.25"
	in = ReconcileInput{UserID: user, CallID: id, ActualCost: &cost, EvidenceRef: "invoice:synthetic-only"}
	if e := admin.Reconcile(ctx, token, "cost", in); e == nil {
		t.Fatal("missing money metadata accepted")
	}
	in.Currency = "USD"
	in.Unit = "currency_major"
	if e := admin.Reconcile(ctx, token, "cost", in); e != nil {
		t.Fatal(e)
	}
	var call Call
	c.DB.Where("id=?", id).Take(&call)
	if call.ConfirmedCost == nil || *call.ConfirmedCost != "0.25" || call.CostStatus != "reconciled" || call.UsageStatus != "reconciled" {
		t.Fatal(call)
	}
}

func TestUnknownUsageReconciliationIsAuthenticatedAtomicAndIdempotent(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	id, _, e := s.Begin(ctx, user, "missing", "123", strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte(`{"code":0,"execute_id":"456","data":"{}"}`)
	if e = s.Complete(ctx, user, id, raw, nil, "", true); e != nil {
		t.Fatal(e)
	}
	var call Call
	c.DB.Where("id=?", id).Take(&call)
	if call.UsageStatus != "pending" || call.ActualTokens != nil || call.ReservedTokens != 10 {
		t.Fatal(call)
	}
	token := strings.Repeat("z", 32)
	admin, _ := NewReconciler(c.DB, token, "operator-test")
	usage := int64(3)
	in := ReconcileInput{UserID: user, CallID: id, Tokens: &usage, EvidenceRef: "invoice:synthetic-verified-1"}
	if e = admin.Reconcile(ctx, "wrong", "k1", in); e == nil {
		t.Fatal("unauthenticated settlement")
	}
	if e = admin.Reconcile(ctx, token, "k1", in); e != nil {
		t.Fatal(e)
	}
	restart, _ := NewReconciler(c.DB, token, "operator-test")
	if e = restart.Reconcile(ctx, token, "k1", in); e != nil {
		t.Fatal(e)
	}
	var w Window
	c.DB.Where("user_id=?", user).Take(&w)
	if w.CallsUsed != 1 || w.TokensUsed != 3 || w.TokensReserved != 0 {
		t.Fatal(w)
	}
	var count int64
	c.DB.Model(&Reconciliation{}).Where("call_id=?", id).Count(&count)
	if count != 1 {
		t.Fatal("duplicate audit")
	}
	usage = 4
	if e = restart.Reconcile(ctx, token, "k1", in); e != coze.ErrRequestConflict {
		t.Fatal("changed settlement accepted", e)
	}
	if e = restart.Reconcile(ctx, token, "k2", in); e != coze.ErrRequestConflict {
		t.Fatal("second settlement accepted", e)
	}
}

func TestInvalidCostEvidenceCannotSettleMoneyOrReleaseUnknownBudget(t *testing.T) {
	s, c, user := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	id, _, _ := s.Begin(ctx, user, "invalid-cost", "123", strings.Repeat("b", 64))
	if e := s.RecordReport(ctx, user, id, coze.CostReport{Raw: "Infinity", ExecuteID: "123", ResponseDigest: strings.Repeat("c", 64)}); e != nil {
		t.Fatal(e)
	}
	if e := s.Complete(ctx, user, id, nil, nil, "", false); e != nil {
		t.Fatal(e)
	}
	var v Call
	c.DB.Where("id=?", id).Take(&v)
	if v.CostStatus != "invalid" || v.RawReportedCost == nil || *v.RawReportedCost != "Infinity" || v.ValidatedReportedCost != nil || v.ConfirmedCost != nil || v.ReservedTokens != 10 {
		t.Fatal(v)
	}
	admin, _ := NewReconciler(c.DB, strings.Repeat("z", 32), "operator-test")
	pending, e := admin.Pending(ctx, strings.Repeat("z", 32))
	if e != nil || len(pending) == 0 {
		t.Fatal("pending evidence inaccessible", e)
	}
}
