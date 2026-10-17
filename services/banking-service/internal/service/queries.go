package service

import (
	"context"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
)

func (s *Core) Configured() bool { return s != nil && s.repo != nil }
func (s *Core) User(ctx context.Context, user int64) (*entity.User, error) {
	return s.repo.User(ctx, user)
}
func (s *Core) Account(ctx context.Context, user, id int64) (*entity.BankAccount, error) {
	return s.repo.Account(ctx, user, id)
}
func (s *Core) Balance(ctx context.Context, user, id int64) (entity.AccountBalance, error) {
	return s.repo.Balance(ctx, user, id)
}
func (s *Core) Accounts(ctx context.Context, user int64, o *legacy.ListOptions) ([]entity.BankAccount, error) {
	return repository.List[entity.BankAccount](s.repo, ctx, user, nil, o)
}
func (s *Core) AccountLedger(ctx context.Context, user, id int64, o *legacy.ListOptions) ([]entity.LedgerEntry, error) {
	if _, err := s.repo.Account(ctx, user, id); err != nil {
		return nil, err
	}
	return repository.List[entity.LedgerEntry](s.repo, ctx, user, map[string]any{"account_id": id}, o)
}

type TransactionFilter = repository.TransactionFilter

func (s *Core) Transactions(ctx context.Context, user int64, f TransactionFilter) ([]entity.Transaction, int64, error) {
	if f.AccountID > 0 {
		if _, err := s.Account(ctx, user, f.AccountID); err != nil {
			return nil, 0, err
		}
	}
	return s.repo.Transactions(ctx, user, f)
}
func (s *Core) Transaction(ctx context.Context, user, id int64) (*entity.Transaction, error) {
	return repository.Owned[entity.Transaction](s.repo, ctx, user, id)
}
func (s *Core) Transfer(ctx context.Context, user, id int64) (*entity.TransferIntent, error) {
	return repository.Owned[entity.TransferIntent](s.repo, ctx, user, id)
}
func (s *Core) Budgets(ctx context.Context, user int64, month string, o *legacy.ListOptions) ([]entity.Budget, error) {
	filters := map[string]any{}
	if month != "" {
		filters["month"] = month
	}
	return repository.List[entity.Budget](s.repo, ctx, user, filters, o)
}
func (s *Core) Goals(ctx context.Context, user int64, o *legacy.ListOptions) ([]entity.SavingsGoal, error) {
	rows, err := repository.List[entity.SavingsGoal](s.repo, ctx, user, nil, o)
	if err != nil {
		return nil, err
	}
	for i, g := range rows {
		v, err := s.Goal(ctx, user, g.ID)
		if err != nil {
			return nil, err
		}
		rows[i] = *v
	}
	return rows, nil
}
func (s *Core) GoalLedger(ctx context.Context, user, id int64, o *legacy.ListOptions) ([]entity.SavingsLedger, error) {
	if _, err := s.Goal(ctx, user, id); err != nil {
		return nil, err
	}
	return repository.List[entity.SavingsLedger](s.repo, ctx, user, map[string]any{"goal_id": id}, o)
}
func (s *Core) Audits(ctx context.Context, user int64, o *legacy.ListOptions) ([]entity.Audit, error) {
	return repository.List[entity.Audit](s.repo, ctx, user, nil, o)
}
