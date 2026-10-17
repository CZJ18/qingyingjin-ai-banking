package service

import (
	"context"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"time"
)

type CategoryInput struct {
	Category              string `json:"category"`
	AllocatedMinor        int64  `json:"allocated_minor"`
	EssentialReserveMinor int64  `json:"essential_reserve_minor"`
}
type BudgetInput struct {
	Month          string          `json:"month"`
	Currency       string          `json:"currency,omitempty"`
	TotalMinor     int64           `json:"total_minor"`
	Categories     []CategoryInput `json:"categories"`
	EnforceReserve bool            `json:"enforce_reserve"`
}

func (s *Core) CreateBudget(ctx context.Context, m Meta, key string, in BudgetInput) (entity.Budget, error) {
	var zero entity.Budget
	if err := risk.CheckSensitive(ctx, s.gateway, m.UserID, "create_budget", in); err != nil {
		return zero, err
	}
	month, err := time.Parse("2006-01", in.Month)
	if err != nil || month.Year() < 2000 || month.Year() > 2100 {
		return zero, entity.Fail("INVALID_ARGUMENT", "month must be YYYY-MM in 2000..2100 (UTC)")
	}
	money, err := entity.Money(in.TotalMinor, in.Currency)
	if err != nil {
		return zero, err
	}
	in.Currency = money.Currency
	if in.TotalMinor <= 0 || len(in.Categories) == 0 || len(in.Categories) > 100 {
		return zero, entity.Fail("INVALID_ARGUMENT", "positive total and 1..100 categories required")
	}
	var total int64
	seen := map[string]bool{}
	for _, c := range in.Categories {
		if !text(c.Category, 64) || seen[c.Category] || c.AllocatedMinor < 0 || c.AllocatedMinor > in.TotalMinor || c.EssentialReserveMinor < 0 || c.EssentialReserveMinor > c.AllocatedMinor {
			return zero, entity.Fail("INVALID_ARGUMENT", "invalid or duplicate budget category")
		}
		seen[c.Category] = true
		total += c.AllocatedMinor
		if total > in.TotalMinor {
			return zero, entity.Fail("INVALID_ARGUMENT", "category allocations exceed total")
		}
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "budget.create", key, in, func(tx *repository.Tx) (entity.Budget, error) {
		if err := tx.RequireUser(); err != nil {
			return zero, err
		}
		b := entity.Budget{Base: entity.Base{UserID: m.UserID}, Month: in.Month, Currency: in.Currency, TotalMinor: in.TotalMinor, Categories: []entity.BudgetCategory{}, Rules: []entity.BudgetRule{}}
		if err := tx.Insert(&b); err != nil {
			return zero, err
		}
		for _, c := range in.Categories {
			row := entity.BudgetCategory{UserID: m.UserID, BudgetID: b.ID, Category: c.Category, AllocatedMinor: c.AllocatedMinor, EssentialReserveMinor: c.EssentialReserveMinor}
			if err := tx.Insert(&row); err != nil {
				return zero, err
			}
			b.Categories = append(b.Categories, row)
		}
		rule := entity.BudgetRule{UserID: m.UserID, BudgetID: b.ID, Kind: "enforce_reserve", Enabled: in.EnforceReserve}
		if err := tx.Insert(&rule); err != nil {
			return zero, err
		}
		b.Rules = append(b.Rules, rule)
		if err := tx.Audit(audit(m, "budget.create", b.ID, "succeeded", "")); err != nil {
			return zero, err
		}
		return b, nil
	})
}
func (s *Core) Budget(ctx context.Context, user, id int64) (*entity.Budget, error) {
	b, err := repository.Owned[entity.Budget](s.repo, ctx, user, id)
	if err != nil {
		return nil, err
	}
	opts := &legacy.ListOptions{Limit: 100}
	b.Categories, err = repository.List[entity.BudgetCategory](s.repo, ctx, user, map[string]any{"budget_id": id}, opts)
	if err != nil {
		return nil, err
	}
	b.Rules, err = repository.List[entity.BudgetRule](s.repo, ctx, user, map[string]any{"budget_id": id}, opts)
	return b, err
}

type CategoryUsage struct {
	Category                       string `json:"category"`
	AllocatedMinor                 int64  `json:"allocated_minor"`
	SpentMinor                     int64  `json:"spent_minor"`
	RemainingMinor                 int64  `json:"remaining_minor"`
	EssentialReserveRemainingMinor int64  `json:"essential_reserve_remaining_minor"`
}
type BudgetUsage struct {
	Budget                         *entity.Budget  `json:"budget"`
	SpentMinor                     int64           `json:"spent_minor"`
	RemainingMinor                 int64           `json:"remaining_minor"`
	EssentialReserveRemainingMinor int64           `json:"essential_reserve_remaining_minor"`
	AvailableAfterReserveMinor     int64           `json:"available_after_reserve_minor"`
	Categories                     []CategoryUsage `json:"categories"`
	UnallocatedCategorySpendMinor  int64           `json:"unallocated_category_spend_minor"`
}

func (s *Core) BudgetUsage(ctx context.Context, user, id int64) (*BudgetUsage, error) {
	b, err := s.Budget(ctx, user, id)
	if err != nil {
		return nil, err
	}
	start, _ := time.Parse("2006-01", b.Month)
	rows, err := s.repo.Spending(ctx, user, start.UnixMilli(), start.AddDate(0, 1, 0).UnixMilli(), b.Currency)
	if err != nil {
		return nil, err
	}
	u := &BudgetUsage{Budget: b, Categories: []CategoryUsage{}}
	byCategory := map[string]int64{}
	for _, r := range rows {
		byCategory[r.Category] = r.SpentMinor
		u.SpentMinor += r.SpentMinor
	}
	for _, c := range b.Categories {
		spent := byCategory[c.Category]
		delete(byCategory, c.Category)
		reserve := c.EssentialReserveMinor - spent
		if reserve < 0 {
			reserve = 0
		}
		u.Categories = append(u.Categories, CategoryUsage{Category: c.Category, AllocatedMinor: c.AllocatedMinor, SpentMinor: spent, RemainingMinor: c.AllocatedMinor - spent, EssentialReserveRemainingMinor: reserve})
		u.EssentialReserveRemainingMinor += reserve
	}
	for _, spent := range byCategory {
		u.UnallocatedCategorySpendMinor += spent
	}
	u.RemainingMinor = b.TotalMinor - u.SpentMinor
	u.AvailableAfterReserveMinor = u.RemainingMinor
	for _, r := range b.Rules {
		if r.Kind == "enforce_reserve" && r.Enabled {
			u.AvailableAfterReserveMinor -= u.EssentialReserveRemainingMinor
		}
	}
	return u, nil
}
