//go:build integration

package modelspend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
	"gorm.io/gorm"
)

// The parent runner creates a fresh UUID-labelled fixture, applies real migrations,
// and supplies the exact allowlisted database. This harness never migrates or
// cleans databases and cannot silently use the application's environment.
func spendHarness(t *testing.T, capacity int64) (*Service, *platform.Connections) {
	t.Helper()
	path := os.Getenv("BANKING_SPEND_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_SPEND_TEST_ENV requires a new isolated SQL fixture")
	}
	if os.Getenv("BANKING_TEST_ENVIRONMENT") != "isolated" {
		t.Fatal("BANKING_TEST_ENVIRONMENT=isolated is required")
	}
	env, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	name, host := cfg.MySQLDatabase, cfg.MySQLHost
	if cfg.DatabaseDriver == "postgres" {
		name, host = cfg.PostgresDatabase, cfg.PostgresHost
	}
	if !strings.HasSuffix(name, "_test") || name != os.Getenv("BANKING_SPEND_TEST_EXPECT_DATABASE") || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		t.Fatal("SQL fixture does not match the exact loopback test database allowlist")
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	if os.Getenv("BANKING_SPEND_TEST_SQL_DIAGNOSTICS") == "1" {
		conn.DB = conn.DB.Debug()
	}
	p := syntheticPolicy()
	p.ProjectID = "spend-" + uuid.NewString()
	reserve, err := p.ReserveUnits()
	if err != nil {
		t.Fatal(err)
	}
	p.Limit = FormatMoney(reserve * capacity)
	s, err := New(conn.DB, p)
	if err != nil {
		t.Fatal(err)
	}
	return s, conn
}

func reserveAttempt(t *testing.T, s *Service, key string) Attempt {
	t.Helper()
	a, err := s.Reserve(context.Background(), key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func poolState(t *testing.T, s *Service) Pool {
	t.Helper()
	var p Pool
	if err := s.db.Where("project_id=?", s.policy.ProjectID).Take(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func attemptState(t *testing.T, s *Service, id string) Attempt {
	t.Helper()
	var a Attempt
	if err := s.db.Where("id=?", id).Take(&a).Error; err != nil {
		t.Fatal(err)
	}
	return a
}

func reportFor(provider string) Report {
	return Report{Model: "synthetic-model", ProviderID: provider, Usage: Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}, ResponseBody: []byte(`{"id":"synthetic-provider","choices":[]}`), ContentType: "application/json"}
}

func settlementFor(s *Service, a Attempt, actual string) Settlement {
	return Settlement{AttemptID: a.ID, ProjectID: s.policy.ProjectID, Currency: s.policy.Currency, BillID: "synthetic-bill", LineID: a.ID, ProviderID: "synthetic-provider", ActualCost: actual, EvidenceDigest: strings.Repeat("b", 64)}
}

func adminFor(t *testing.T, s *Service, operator string) (*Reconciler, string) {
	t.Helper()
	token := strings.Repeat("z", 32)
	r, err := NewReconciler(s, token, operator)
	if err != nil {
		t.Fatal(err)
	}
	return r, token
}

func TestSpendSQLConcurrentAdmissionLocksWholeProject(t *testing.T) {
	s, _ := spendHarness(t, 1)
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := s.Reserve(context.Background(), fmt.Sprintf("user-%d", n), strings.Repeat("a", 64))
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, denied := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrQuota):
			denied++
		default:
			t.Fatalf("unexpected SQL admission result: %v", err)
		}
	}
	p := poolState(t, s)
	if accepted != 1 || denied != 7 || p.HeldUnits != p.LimitUnits || p.UsedUnits != 0 {
		t.Fatalf("global project budget overspent accepted=%d denied=%d pool=%+v", accepted, denied, p)
	}
}

func TestSpendSQLSameKeyConcurrencyAndCachedReplay(t *testing.T) {
	s, _ := spendHarness(t, 4)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reserve(ctx, "same-key", strings.Repeat("a", 64))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatal("duplicate dispatch admitted", accepted)
	}
	var a Attempt
	if err := s.db.Where("project_id=?", s.policy.ProjectID).Take(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, a.ID, reportFor("synthetic-provider")); err != nil {
		t.Fatal(err)
	}
	replay, err := s.Reserve(ctx, "same-key", strings.Repeat("a", 64))
	if err != nil || replay.ID != a.ID || replay.ResponseBody == nil || replay.ActualUnits != nil {
		t.Fatal("result not cached without independent settlement", replay, err)
	}
	if _, err := s.Reserve(ctx, "same-key", strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay parameters accepted", err)
	}
	p := poolState(t, s)
	if p.HeldUnits != a.ReservedUnits || p.UsedUnits != 0 {
		t.Fatal("unverified estimate released money", p)
	}
}

func TestSpendSQLUnknownSurvivesOldDayRestartAndChangedPolicy(t *testing.T) {
	s, _ := spendHarness(t, 1)
	a := reserveAttempt(t, s, "lost")
	if err := s.Unknown(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&Attempt{}).Where("id=?", a.ID).Update("created_at", time.Now().Add(-72*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	restarted, err := New(s.db, s.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Reserve(context.Background(), "new-day", strings.Repeat("a", 64)); !errors.Is(err, ErrQuota) {
		t.Fatal("restart/day reset unknown monetary hold", err)
	}
	changed := s.policy
	changed.Limit = "20"
	other, err := New(s.db, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Reserve(context.Background(), "new-owner-config", strings.Repeat("a", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("new policy reset existing project pool", err)
	}
	if p := poolState(t, s); p.HeldUnits != a.ReservedUnits || p.UsedUnits != 0 {
		t.Fatal(p)
	}
}

func TestSpendSQLReserveAuditFailureRollsBackAllWrites(t *testing.T) {
	s, conn := spendHarness(t, 2)
	callback := "spend_reserve_audit_fault_" + uuid.NewString()
	if err := conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (Event{}).TableName() {
			tx.AddError(errors.New("synthetic audit storage failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer conn.DB.Callback().Create().Remove(callback)
	if _, err := s.Reserve(context.Background(), "no-audit", strings.Repeat("a", 64)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("reservation without audit accepted", err)
	}
	for _, model := range []any{&Pool{}, &Attempt{}, &Event{}} {
		var count int64
		if err := conn.DB.Model(model).Where("project_id=?", s.policy.ProjectID).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("partial reservation committed", count, err)
		}
	}
}

func TestSpendSQLBillReplayAcrossOperatorsAndLateObservation(t *testing.T) {
	s, _ := spendHarness(t, 2)
	a := reserveAttempt(t, s, "bill")
	ctx := context.Background()
	if err := s.Unknown(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	r, token := adminFor(t, s, "operator-a")
	in := settlementFor(s, a, "0.00003")
	first, err := r.Reconcile(ctx, token, "row-1", in)
	if err != nil || first.Replay {
		t.Fatal(first, err)
	}
	other, _ := adminFor(t, s, "operator-b")
	second, err := other.Reconcile(ctx, token, "another-local-key", in)
	if err != nil || !second.Replay || first.EventID != second.EventID {
		t.Fatal("bill charged twice across operators", second, err)
	}
	if err := s.Observe(ctx, a.ID, reportFor("synthetic-provider")); err != nil {
		t.Fatal(err)
	}
	if err := s.Unknown(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if p.UsedUnits != 3000 || p.HeldUnits != 0 || got.ActualUnits == nil || *got.ActualUnits != 3000 || got.Status != "settled" || got.ResponseBody == nil {
		t.Fatal("late response overwrote settlement", p, got)
	}
	bad := in
	bad.ActualCost = "0.00004"
	if _, err := r.Reconcile(ctx, token, "row-1", bad); !errors.Is(err, ErrConflict) {
		t.Fatal("changed same bill accepted", err)
	}
	bad = in
	bad.BillID = "second-bill"
	if _, err := r.Reconcile(ctx, token, "new-row", bad); !errors.Is(err, ErrConflict) {
		t.Fatal("second bill overwrote settled attempt", err)
	}
	var count int64
	if err := s.db.Model(&Event{}).Where("project_id=? AND kind=?", s.policy.ProjectID, "settle").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("settlement replay wrote additional audit", count, err)
	}
}

func TestSpendSQLSettlementAuditFailureAndInFlightConfirmation(t *testing.T) {
	s, conn := spendHarness(t, 2)
	a := reserveAttempt(t, s, "active")
	r, token := adminFor(t, s, "operator")
	in := settlementFor(s, a, "0")
	if _, err := r.Reconcile(context.Background(), token, "active-row", in); !errors.Is(err, ErrUnknown) {
		t.Fatal("in-flight hold released", err)
	}
	in.ConfirmNoActiveRun = true
	callback := "spend_settle_audit_fault_" + uuid.NewString()
	if err := conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (Event{}).TableName() {
			tx.AddError(errors.New("synthetic settlement audit failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer conn.DB.Callback().Create().Remove(callback)
	if _, err := r.Reconcile(context.Background(), token, "active-row", in); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if p.UsedUnits != 0 || p.HeldUnits != a.ReservedUnits || got.ActualUnits != nil || got.Status != "reserved" {
		t.Fatal("audit failure partially settled money", p, got)
	}
}

func TestSpendSQLObservedBoundOverrunFreezesAndRetainsHold(t *testing.T) {
	s, _ := spendHarness(t, 3)
	a := reserveAttempt(t, s, "overrun")
	report := reportFor("synthetic-provider")
	report.Usage = Usage{PromptTokens: s.policy.ContextTokens + 1, TotalTokens: s.policy.ContextTokens + 1}
	if err := s.Observe(context.Background(), a.ID, report); !errors.Is(err, ErrBoundExceeded) {
		t.Fatal("unprovable bound accepted", err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if !p.Frozen || p.HeldUnits != a.ReservedUnits || p.UsedUnits != 0 || got.ActualUnits != nil || got.Status != "unknown" {
		t.Fatal("overrun lost committed hold/freeze", p, got)
	}
	if _, err := s.Reserve(context.Background(), "after-overrun", strings.Repeat("a", 64)); !errors.Is(err, ErrQuota) {
		t.Fatal("frozen project dispatched", err)
	}
	var n int64
	if err := s.db.Model(&Event{}).Where("attempt_id=? AND kind=?", a.ID, "freeze").Count(&n).Error; err != nil || n != 1 {
		t.Fatal("missing overrun audit", n, err)
	}
}

func TestSpendSQLModelMismatchCommitsFreezeAndRetainsHold(t *testing.T) {
	s, _ := spendHarness(t, 3)
	a := reserveAttempt(t, s, "wrong-model")
	report := reportFor("synthetic-provider")
	report.Model = "unpriced-model"
	if err := s.Observe(context.Background(), a.ID, report); !errors.Is(err, ErrBoundExceeded) {
		t.Fatal("unpriced supplier model accepted", err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if !p.Frozen || p.HeldUnits != a.ReservedUnits || p.UsedUnits != 0 || got.ActualUnits != nil || got.ResponseBody != nil || got.Status != "unknown" {
		t.Fatal("model mismatch did not preserve freeze/hold", p, got)
	}
}

func TestSpendSQLActualDebtOverrunFreezesWithoutTruncation(t *testing.T) {
	s, _ := spendHarness(t, 1)
	a := reserveAttempt(t, s, "debt")
	if err := s.Unknown(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	r, token := adminFor(t, s, "operator")
	want := a.ReservedUnits + 7
	result, err := r.Reconcile(context.Background(), token, "debt-row", settlementFor(s, a, FormatMoney(want)))
	if err != nil || !result.Frozen {
		t.Fatal(result, err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if !p.Frozen || p.UsedUnits != want || p.HeldUnits != 0 || got.ActualUnits == nil || *got.ActualUnits != want {
		t.Fatal("actual supplier debt truncated", p, got)
	}
}

func TestSpendSQLSettlementScopeAndProviderCannotReleaseMoney(t *testing.T) {
	s, _ := spendHarness(t, 2)
	a := reserveAttempt(t, s, "scope")
	if err := s.Observe(context.Background(), a.ID, reportFor("synthetic-provider")); err != nil {
		t.Fatal(err)
	}
	r, token := adminFor(t, s, "operator")
	for _, mutate := range []func(*Settlement){
		func(in *Settlement) { in.ProjectID = "other-project" },
		func(in *Settlement) { in.Currency = "CNY" },
		func(in *Settlement) { in.ProviderID = "other-provider" },
	} {
		in := settlementFor(s, a, "0")
		mutate(&in)
		if _, err := r.Reconcile(context.Background(), token, uuid.NewString(), in); err == nil {
			t.Fatal("mismatched settlement accepted")
		}
	}
	p := poolState(t, s)
	if p.HeldUnits != a.ReservedUnits || p.UsedUnits != 0 {
		t.Fatal("mismatched evidence released money", p)
	}
	rows, err := r.Pending(context.Background(), token)
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID || len(rows) > 100 {
		t.Fatal("pending is not project scoped", len(rows), err)
	}
}

func TestSpendSQLConcurrentIdenticalSettlementReleasesExactlyOnce(t *testing.T) {
	s, _ := spendHarness(t, 2)
	a := reserveAttempt(t, s, "concurrent-bill")
	ctx := context.Background()
	if err := s.Unknown(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	r, token := adminFor(t, s, "operator")
	in := settlementFor(s, a, "0.00003")
	var wg sync.WaitGroup
	results := make(chan SettlementResult, 6)
	errs := make(chan error, 6)
	start := make(chan struct{})
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := r.Reconcile(ctx, token, "same-bill-key", in)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh, replay := 0, 0
	for result := range results {
		if result.Replay {
			replay++
		} else {
			fresh++
		}
	}
	if p := poolState(t, s); fresh != 1 || replay != 5 || p.HeldUnits != 0 || p.UsedUnits != 3000 {
		t.Fatal("concurrent settlement applied multiple times", fresh, replay, p)
	}
}

func TestSpendSQLOperatorRequestKeyCannotMoveToAnotherBill(t *testing.T) {
	s, _ := spendHarness(t, 3)
	first := reserveAttempt(t, s, "bill-a")
	second := reserveAttempt(t, s, "bill-b")
	ctx := context.Background()
	for _, a := range []Attempt{first, second} {
		if err := s.Unknown(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, token := adminFor(t, s, "operator")
	if _, err := r.Reconcile(ctx, token, "local-key", settlementFor(s, first, "0")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, token, "local-key", settlementFor(s, second, "0")); !errors.Is(err, ErrConflict) {
		t.Fatal("operator request key rebound to another bill", err)
	}
	if p := poolState(t, s); p.HeldUnits != second.ReservedUnits || p.UsedUnits != 0 {
		t.Fatal("conflicting bill released second hold", p)
	}
}

func TestSpendSQLFreezeAuditFailureRollsBackObservation(t *testing.T) {
	s, conn := spendHarness(t, 2)
	a := reserveAttempt(t, s, "freeze-audit-failure")
	callback := "spend_freeze_audit_fault_" + uuid.NewString()
	if err := conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (Event{}).TableName() {
			tx.AddError(errors.New("synthetic freeze audit failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer conn.DB.Callback().Create().Remove(callback)
	report := reportFor("synthetic-provider")
	report.Model = "wrong-model"
	if err := s.Observe(context.Background(), a.ID, report); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if p.Frozen || p.HeldUnits != a.ReservedUnits || got.Status != "reserved" || got.ResponseDigest != "" || got.ProviderID != "" {
		t.Fatal("failed audit partially changed observation", p, got)
	}
}

func TestSpendSQLPendingIsBoundedAndDoesNotExposeResponseBodies(t *testing.T) {
	s, _ := spendHarness(t, 105)
	for n := range 101 {
		a := reserveAttempt(t, s, fmt.Sprintf("pending-%d", n))
		if n == 0 {
			if err := s.Observe(context.Background(), a.ID, reportFor("synthetic-provider")); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, token := adminFor(t, s, "operator")
	rows, err := r.Pending(context.Background(), token)
	if err != nil || len(rows) != 100 {
		t.Fatal("pending queue is not bounded", len(rows), err)
	}
	for _, row := range rows {
		if row.ProjectID != s.policy.ProjectID || row.ResponseBody != nil {
			t.Fatal("pending queue leaked another project or response", row.ID)
		}
	}
}

func TestSpendSQLReplayedOperatorKeyRemainsBoundToItsOriginalBill(t *testing.T) {
	s, _ := spendHarness(t, 3)
	first := reserveAttempt(t, s, "alias-a")
	second := reserveAttempt(t, s, "alias-b")
	ctx := context.Background()
	for _, a := range []Attempt{first, second} {
		if err := s.Unknown(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	opA, token := adminFor(t, s, "operator-a")
	in := settlementFor(s, first, "0.00003")
	settled, err := opA.Reconcile(ctx, token, "original-key", in)
	if err != nil {
		t.Fatal(err)
	}
	opB, _ := adminFor(t, s, "operator-b")
	alias, err := opB.Reconcile(ctx, token, "replayed-key", in)
	if err != nil || !alias.Replay || alias.EventID != settled.EventID {
		t.Fatal(alias, err)
	}
	if _, err := opB.Reconcile(ctx, token, "replayed-key", settlementFor(s, second, "0")); !errors.Is(err, ErrConflict) {
		t.Fatal("replayed operator key was rebound to a second bill", err)
	}
	var before, after int64
	if err := s.db.Model(&Event{}).Where("project_id=?", s.policy.ProjectID).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	exact, err := opB.Reconcile(ctx, token, "replayed-key", in)
	if err != nil || !exact.Replay || exact.EventID != settled.EventID {
		t.Fatal(exact, err)
	}
	if err := s.db.Model(&Event{}).Where("project_id=?", s.policy.ProjectID).Count(&after).Error; err != nil || after != before {
		t.Fatal("exact replay appended another event", before, after, err)
	}
	if p := poolState(t, s); p.HeldUnits != second.ReservedUnits || p.UsedUnits != 3000 {
		t.Fatal("alias request changed money", p)
	}
}

func TestSpendSQLProviderResponseCannotSettleTwoUnknownAttempts(t *testing.T) {
	s, _ := spendHarness(t, 3)
	first := reserveAttempt(t, s, "provider-bill-a")
	second := reserveAttempt(t, s, "provider-bill-b")
	ctx := context.Background()
	for _, a := range []Attempt{first, second} {
		if err := s.Unknown(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, token := adminFor(t, s, "operator")
	if _, err := r.Reconcile(ctx, token, "provider-row-a", settlementFor(s, first, "0.00003")); err != nil {
		t.Fatal(err)
	}
	// A different bill/line/key cannot attribute the same supplier response to
	// a second attempt. Component bill lines are not supported in this release.
	secondBill := settlementFor(s, second, "0")
	secondBill.BillID = "different-bill"
	if _, err := r.Reconcile(ctx, token, "provider-row-b", secondBill); !errors.Is(err, ErrConflict) {
		t.Fatal("same provider response released a second attempt hold", err)
	}
	if p := poolState(t, s); p.HeldUnits != second.ReservedUnits || p.UsedUnits != 3000 {
		t.Fatal("duplicate provider attribution changed money", p)
	}
	if got := attemptState(t, s, second.ID); got.ActualUnits != nil || got.ProviderID != "" || got.Status != "unknown" {
		t.Fatal("duplicate provider evidence mutated second attempt", got)
	}
}

func TestSpendSQLProviderResponseCannotBeObservedForTwoAttempts(t *testing.T) {
	s, _ := spendHarness(t, 3)
	first := reserveAttempt(t, s, "provider-report-a")
	second := reserveAttempt(t, s, "provider-report-b")
	ctx := context.Background()
	if err := s.Observe(ctx, first.ID, reportFor("synthetic-provider")); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, second.ID, reportFor("synthetic-provider")); !errors.Is(err, ErrConflict) {
		t.Fatal("same provider response attributed to two attempts", err)
	}
	if p := poolState(t, s); p.HeldUnits != first.ReservedUnits+second.ReservedUnits || p.UsedUnits != 0 {
		t.Fatal("duplicate provider report changed money", p)
	}
	if got := attemptState(t, s, second.ID); got.ResponseBody != nil || got.ProviderID != "" || got.Status != "reserved" {
		t.Fatal("duplicate provider report mutated second attempt", got)
	}
}

func TestSpendSQLWrongModelWithMissingUsageFreezesWithoutInventingCounters(t *testing.T) {
	s, _ := spendHarness(t, 2)
	a := reserveAttempt(t, s, "unknown-usage-wrong-model")
	report := Report{Model: "wrong-model", ProviderID: "synthetic-provider", UsageUnknown: true, ResponseBody: []byte(`{"id":"synthetic-provider","model":"wrong-model","choices":[]}`), ContentType: "application/json"}
	if err := s.Observe(context.Background(), a.ID, report); !errors.Is(err, ErrBoundExceeded) {
		t.Fatal("missing usage hid model mismatch", err)
	}
	p := poolState(t, s)
	got := attemptState(t, s, a.ID)
	if !p.Frozen || p.HeldUnits != a.ReservedUnits || got.UsageJSON != "" || got.EstimatedUnits != nil || got.ActualUnits != nil {
		t.Fatal("unknown usage was treated as free consumption", p, got)
	}
	var audit Event
	if err := s.db.Where("attempt_id=? AND kind=?", a.ID, "freeze").Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal([]byte(audit.SnapshotJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if usage, ok := evidence["usage"]; ok && string(usage) != "null" {
		t.Fatal("audit invented zero usage", string(usage))
	}
}
