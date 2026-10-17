package service

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

// Invalid requests must fail before any repository/database access.
func TestInvalidInputsNeverReachRepository(t *testing.T) {
	s := New(nil, nil)
	ctx := context.Background()
	m := Meta{UserID: 42}
	cases := []struct {
		name string
		run  func() error
	}{
		{"negative opening", func() error {
			_, e := s.CreateAccount(ctx, m, "key", AccountInput{DisplayName: "x", InitialBalanceMinor: -1})
			return e
		}},
		{"opening confirmation", func() error {
			_, e := s.CreateAccount(ctx, m, "key", AccountInput{DisplayName: "x", InitialBalanceMinor: 1})
			return e
		}},
		{"self transfer", func() error {
			_, e := s.CreateTransfer(ctx, m, "key", TransferInput{SourceAccountID: 1, TargetAccountID: 1, AmountMinor: 1})
			return e
		}},
		{"zero transfer", func() error {
			_, e := s.CreateTransfer(ctx, m, "key", TransferInput{SourceAccountID: 1, TargetAccountID: 2, AmountMinor: 0})
			return e
		}},
		{"unsupported currency", func() error {
			_, e := s.CreateTransfer(ctx, m, "key", TransferInput{SourceAccountID: 1, TargetAccountID: 2, AmountMinor: 1, Currency: "USD"})
			return e
		}},
		{"invalid direction", func() error {
			_, e := s.ImportTransaction(ctx, m, "key", TransactionInput{BankAccountID: 1, ExternalTransactionID: "x", Direction: "other", AmountMinor: 1, Category: "food", OccurredAt: 1, EffectMode: "record_only"})
			return e
		}},
		{"budget exceeds total", func() error {
			_, e := s.CreateBudget(ctx, m, "key", BudgetInput{Month: "2026-10", TotalMinor: 100, Categories: []CategoryInput{{Category: "food", AllocatedMinor: 101}}})
			return e
		}},
		{"reserve exceeds allocation", func() error {
			_, e := s.CreateBudget(ctx, m, "key", BudgetInput{Month: "2026-10", TotalMinor: 100, Categories: []CategoryInput{{Category: "food", AllocatedMinor: 50, EssentialReserveMinor: 51}}})
			return e
		}},
		{"duplicate category", func() error {
			_, e := s.CreateBudget(ctx, m, "key", BudgetInput{Month: "2026-10", TotalMinor: 100, Categories: []CategoryInput{{Category: "food", AllocatedMinor: 10}, {Category: "food", AllocatedMinor: 10}}})
			return e
		}},
		{"invalid savings period", func() error {
			now := time.Now().UnixMilli()
			_, e := s.CreateGoal(ctx, m, "key", GoalInput{Name: "regular meals", TargetMinor: 100, MonthlyMinor: 10, StartsAt: now, EndsAt: now})
			return e
		}},
		{"invalid decision", func() error { _, e := s.ApproveTransfer(ctx, m, 1, ApprovalInput{Decision: "yes"}); return e }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if e := c.run(); e == nil || entity.Code(e) == "DEPENDENCY_UNAVAILABLE" {
				t.Fatalf("expected domain validation, got %v", e)
			}
		})
	}
}
