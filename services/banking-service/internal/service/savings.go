package service

import (
	"context"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"time"
)

type GoalInput struct {
	Name         string `json:"name"`
	TargetMinor  int64  `json:"target_minor"`
	Currency     string `json:"currency,omitempty"`
	StartsAt     int64  `json:"starts_at"`
	EndsAt       int64  `json:"ends_at"`
	MonthlyMinor int64  `json:"monthly_minor"`
}

func (s *Core) CreateGoal(ctx context.Context, m Meta, key string, in GoalInput) (entity.SavingsGoal, error) {
	var zero entity.SavingsGoal
	if err := risk.CheckSensitive(ctx, s.gateway, m.UserID, "create_savings_goal", in); err != nil {
		return zero, err
	}
	money, err := entity.Money(in.TargetMinor, in.Currency)
	if err != nil {
		return zero, err
	}
	in.Currency = money.Currency
	if !text(in.Name, 128) || in.TargetMinor <= 0 || in.StartsAt <= 0 || in.StartsAt > time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || in.EndsAt <= in.StartsAt || in.EndsAt-in.StartsAt > int64(10*365*24*time.Hour/time.Millisecond) || in.MonthlyMinor <= 0 || in.MonthlyMinor > entity.MaxBalance {
		return zero, entity.Fail("INVALID_ARGUMENT", "valid name, positive target/monthly amount and period up to ten years required")
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "savings.create", key, in, func(tx *repository.Tx) (entity.SavingsGoal, error) {
		if err := tx.RequireUser(); err != nil {
			return zero, err
		}
		account := entity.BankAccount{Base: entity.Base{UserID: m.UserID}, DisplayName: in.Name, AccountType: "savings", MaskedAccountNumber: "SIM-****", Currency: in.Currency, Status: "active", IsSimulated: true, Version: 1}
		if err := tx.Insert(&account); err != nil {
			return zero, err
		}
		g := entity.SavingsGoal{Base: entity.Base{UserID: m.UserID}, Name: in.Name, TargetMinor: in.TargetMinor, Currency: in.Currency, StartsAt: in.StartsAt, EndsAt: in.EndsAt, AccountID: account.ID, Status: "active"}
		if err := tx.Insert(&g); err != nil {
			return zero, err
		}
		if err := tx.BindGoal(account.ID, g.ID); err != nil {
			return zero, err
		}
		plan := entity.SavingsPlan{UserID: m.UserID, GoalID: g.ID, MonthlyMinor: in.MonthlyMinor, Cadence: "monthly", Status: "manual"}
		if err := tx.Insert(&plan); err != nil {
			return zero, err
		}
		g.Plan = &plan
		if err := tx.Audit(audit(m, "savings.create", g.ID, "succeeded", "")); err != nil {
			return zero, err
		}
		return g, nil
	})
}
func (s *Core) Goal(ctx context.Context, user, id int64) (*entity.SavingsGoal, error) {
	g, err := repository.Owned[entity.SavingsGoal](s.repo, ctx, user, id)
	if err != nil {
		return nil, err
	}
	a, err := s.repo.Account(ctx, user, g.AccountID)
	if err != nil {
		return nil, err
	}
	g.CurrentMinor = a.BalanceMinor
	g.ProgressBasisPoints = g.CurrentMinor * 10000 / g.TargetMinor
	plans, err := repository.List[entity.SavingsPlan](s.repo, ctx, user, map[string]any{"goal_id": id}, &legacy.ListOptions{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(plans) != 1 {
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "savings plan missing")
	}
	g.Plan = &plans[0]
	return g, nil
}

type GoalTransferInput struct {
	BankAccountID int64  `json:"bank_account_id,string"`
	AmountMinor   int64  `json:"amount_minor"`
	Description   string `json:"description"`
}

func (s *Core) GoalTransfer(ctx context.Context, m Meta, id int64, key string, in GoalTransferInput, withdraw bool) (entity.TransferIntent, error) {
	g, err := s.Goal(ctx, m.UserID, id)
	if err != nil {
		return entity.TransferIntent{}, err
	}
	input := TransferInput{SourceAccountID: in.BankAccountID, TargetAccountID: g.AccountID, AmountMinor: in.AmountMinor, Currency: g.Currency, Description: in.Description}
	purpose := "savings_deposit"
	if withdraw {
		input.SourceAccountID = g.AccountID
		input.TargetAccountID = in.BankAccountID
		purpose = "savings_withdrawal"
	}
	return s.createTransfer(ctx, m, key, input, purpose, &g.ID)
}
