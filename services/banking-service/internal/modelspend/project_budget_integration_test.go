//go:build integration

package modelspend

import (
	"context"
	"errors"
	"testing"
)

func TestSQLProjectBudgetPreflightIsReadOnlyAndTracksAtomicHolds(t *testing.T) {
	s, conn := spendHarness(t, 2)
	ctx := context.Background()
	read := func() Pool {
		t.Helper()
		p, err := s.ProjectBudget(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	initial := read()
	if initial.ProjectID != s.policy.ProjectID || initial.PolicyHash != s.policyHash || initial.Currency != s.policy.Currency || initial.LimitUnits != s.reserveUnits*2 || initial.HeldUnits != 0 || initial.UsedUnits != 0 {
		t.Fatal("missing project did not yield the unused policy allowance")
	}
	var count int64
	if err := conn.DB.Model(&Pool{}).Where("project_id=?", s.policy.ProjectID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("preflight created a pool", count, err)
	}
	a := reserveAttempt(t, s, "read-only-preflight")
	if err := s.Unknown(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	before := poolState(t, s)
	for i := 0; i < 3; i++ {
		if p := read(); p != before || p.HeldUnits != s.reserveUnits {
			t.Fatal("preflight changed or lost the unknown hold")
		}
	}
	if after := poolState(t, s); after != before {
		t.Fatal("preflight mutated existing pool")
	}
	changed := s.policy
	changed.PriceVersion = "different-reviewed-snapshot"
	other, err := New(conn.DB, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.ProjectBudget(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("preflight trusted a pool from another policy", err)
	}
}
