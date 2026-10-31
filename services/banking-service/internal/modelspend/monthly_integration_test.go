//go:build integration

package modelspend

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLMonthlyConcurrentHoldsOwnerIsolationAndSettlement(t *testing.T) {
	s, conn := spendHarness(t, 100)
	ctx := WithUser(context.Background(), 42)
	if e := s.ProvisionUserLimit(ctx, 42, FormatMoney(s.reserveUnits)); e != nil {
		t.Fatal(e)
	}
	if e := s.ProvisionUserLimit(ctx, 43, FormatMoney(s.reserveUnits)); e != nil {
		t.Fatal(e)
	}
	// Reprovisioning cannot reset a quota or grant a larger limit.
	if e := s.ProvisionUserLimit(ctx, 42, FormatMoney(s.reserveUnits*2)); !errors.Is(e, ErrConflict) {
		t.Fatal("quota increased", e)
	}
	barrier := make(chan struct{})
	type result struct {
		attempt Attempt
		err     error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			a, e := s.Reserve(ctx, fmt.Sprintf("parallel-%d", i), strings.Repeat("a", 64))
			results <- result{a, e}
		}(i)
	}
	close(barrier)
	wg.Wait()
	close(results)
	accepted, denied := 0, 0
	var a Attempt
	for r := range results {
		if r.err == nil {
			accepted++
			a = r.attempt
		} else if errors.Is(r.err, ErrUserQuota) {
			denied++
		} else {
			t.Fatal(r.err)
		}
	}
	if accepted != 1 || denied != 15 {
		t.Fatal("concurrent quota violation", accepted, denied)
	}
	window, e := s.UserBudget(ctx, 42)
	if e != nil || window.HeldUnits != s.reserveUnits || window.UsedUnits != 0 {
		t.Fatal(window, e)
	}
	// A newly constructed service reads durable holds, rather than memory state.
	fresh, e := New(conn.DB, s.policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.Reserve(ctx, "after-restart", strings.Repeat("a", 64)); !errors.Is(e, ErrUserQuota) {
		t.Fatal("restart lost hold", e)
	}
	// The exact same client key is independent for another authenticated user.
	other, e := fresh.Reserve(WithUser(context.Background(), 43), "after-restart", strings.Repeat("a", 64))
	if e != nil || other.ID == a.ID {
		t.Fatal("cross-user isolation", e)
	}
	if e = s.Unknown(ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reserve(ctx, "unknown-is-not-free", strings.Repeat("a", 64)); !errors.Is(e, ErrUserQuota) {
		t.Fatal("unknown released quota", e)
	}
	secret := strings.Repeat("q", 40)
	r, e := NewReconciler(s, secret, "test-operator")
	if e != nil {
		t.Fatal(e)
	}
	bill := Settlement{AttemptID: a.ID, ProjectID: s.policy.ProjectID, Currency: s.policy.Currency, BillID: "synthetic-test-bill", LineID: "one", ProviderID: "synthetic-provider-monthly", ActualCost: FormatMoney(s.reserveUnits / 2), EvidenceDigest: strings.Repeat("b", 64)}
	if _, e = r.Reconcile(ctx, secret, "review-one", bill); e != nil {
		t.Fatal(e)
	}
	if out, e := r.Reconcile(ctx, secret, "review-one", bill); e != nil || !out.Replay {
		t.Fatal("settlement replay", out, e)
	}
	after, e := s.UserBudget(ctx, 42)
	if e != nil || after.HeldUnits != 0 || after.UsedUnits != s.reserveUnits/2 {
		t.Fatal("monthly settlement", after, e)
	}
	foreign, e := s.UserBudget(ctx, 43)
	if e != nil || foreign.HeldUnits != s.reserveUnits || foreign.UsedUnits != 0 {
		t.Fatal("foreign settlement modified", foreign, e)
	}
	if e := s.Unknown(ctx, other.ID); e != nil {
		t.Fatal(e)
	}
	overrun := bill
	overrun.AttemptID, overrun.LineID, overrun.ProviderID, overrun.ActualCost = other.ID, "two", "synthetic-provider-overrun", FormatMoney(s.reserveUnits*2)
	if _, e := r.Reconcile(ctx, secret, "review-overrun", overrun); e != nil {
		t.Fatal(e)
	}
	debt, e := s.UserBudget(ctx, 43)
	if e != nil || !debt.Frozen || debt.UsedUnits != s.reserveUnits*2 || debt.HeldUnits != 0 {
		t.Fatal("supplier debt truncated/unfrozen", debt, e)
	}
	// Inspect the durable linkage and audit, without publishing prompts/tokens.
	var binding AttemptOwner
	if e = conn.DB.Where("id=? AND user_id=?", a.ID, 42).Take(&binding).Error; e != nil || binding.WindowID != window.ID {
		t.Fatal(binding, e)
	}
	t.Logf("synthetic SQL evidence: user=42 period=%s reserved_before=%d settled_after=%d reserved_after=%d accepted=%d denied=%d attempt_id=%s", after.Period, window.HeldUnits, after.UsedUnits, after.HeldUnits, accepted, denied, a.ID)
}

func TestSQLMonthlyAuditFailureRollsBackBothPoolsAndOwnership(t *testing.T) {
	s, conn := spendHarness(t, 100)
	ctx := WithUser(context.Background(), 42)
	if e := s.ProvisionUserLimit(ctx, 42, FormatMoney(s.reserveUnits)); e != nil {
		t.Fatal(e)
	}
	callback := "monthly_audit_failure_" + uuid.NewString()
	if e := conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (Event{}).TableName() {
			tx.AddError(errors.New("synthetic audit failure"))
		}
	}); e != nil {
		t.Fatal(e)
	}
	defer conn.DB.Callback().Create().Remove(callback)
	if _, e := s.Reserve(ctx, "audit-failure", strings.Repeat("a", 64)); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	var count int64
	if e := conn.DB.Model(&UserWindow{}).Where("limit_id=?", userLimitID(s.policy.ProjectID, 42)).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("monthly hold partially committed", count, e)
	}
	if e := conn.DB.Model(&Pool{}).Where("project_id=?", s.policy.ProjectID).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("project hold partially committed", count, e)
	}
	if e := conn.DB.Model(&Attempt{}).Where("project_id=?", s.policy.ProjectID).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("attempt partially committed", count, e)
	}
	window, e := s.UserBudget(ctx, 42)
	if e != nil || window.HeldUnits != 0 || window.UsedUnits != 0 {
		t.Fatal(window, e)
	}
}

func TestSQLMonthlyRolloverKeepsHistoricalUnknownHold(t *testing.T) {
	s, conn := spendHarness(t, 100)
	ctx := WithUser(context.Background(), 42)
	if e := s.ProvisionUserLimit(ctx, 42, FormatMoney(s.reserveUnits)); e != nil {
		t.Fatal(e)
	}
	// Explicit old-month row represents a durable unknown call spanning a month.
	oldPeriod := monthlyPeriod(time.Now().AddDate(0, -1, 0))
	limitID := userLimitID(s.policy.ProjectID, 42)
	old := UserWindow{ID: hashText(limitID + "\x00" + oldPeriod), LimitID: limitID, UserID: 42, Period: oldPeriod, Currency: s.policy.Currency, LimitUnits: s.reserveUnits, HeldUnits: s.reserveUnits}
	if e := conn.DB.Create(&old).Error; e != nil {
		t.Fatal(e)
	}
	if _, e := s.Reserve(ctx, "new-month", strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	var persisted UserWindow
	if e := conn.DB.Where("id=?", old.ID).Take(&persisted).Error; e != nil || persisted.HeldUnits != old.HeldUnits || persisted.UsedUnits != 0 {
		t.Fatal("old debt erased on rollover", persisted, e)
	}
}
