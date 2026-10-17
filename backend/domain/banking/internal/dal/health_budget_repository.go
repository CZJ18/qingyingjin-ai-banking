package dal

import (
	"context"
	"errors"
	"time"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/convertor"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
	"gorm.io/gorm"
)

func (d *BankingDAO) CreateHealthBudget(ctx context.Context, budget *entity.HealthBudget) (*entity.HealthBudget, error) {
	if budget == nil {
		return nil, errors.New("health budget must not be nil")
	}
	if err := prepareCreate(ctx, d, &budget.ID, &budget.CreatedAt, &budget.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.HealthBudgetToPO(budget)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.HealthBudgetToEntity(po), nil
}

func (d *BankingDAO) GetHealthBudget(ctx context.Context, userID, id int64) (*entity.HealthBudget, error) {
	var po model.HealthBudget
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.HealthBudgetToEntity(&po), nil
}

func (d *BankingDAO) ListHealthBudgets(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.HealthBudget, error) {
	var rows []*model.HealthBudget
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.HealthBudget, len(rows))
	for i, row := range rows {
		result[i] = convertor.HealthBudgetToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateHealthBudget(ctx context.Context, budget *entity.HealthBudget) (bool, error) {
	if budget == nil {
		return false, errors.New("health budget must not be nil")
	}
	now := time.Now().UnixMilli()
	nextVersion := budget.Version + 1
	updates := map[string]any{"period_start": budget.PeriodStart, "period_end": budget.PeriodEnd, "total_allocated_minor": budget.TotalAllocatedMinor, "food_allocated_minor": budget.FoodAllocatedMinor, "spent_minor": budget.SpentMinor, "currency": budget.Currency, "status": string(budget.Status), "updated_at": now, "version": nextVersion}
	result := d.dbFor(ctx).WithContext(ctx).Model(&model.HealthBudget{}).Where("user_id = ? AND id = ? AND version = ?", budget.UserID, budget.ID, budget.Version).Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	budget.Version = nextVersion
	budget.UpdatedAt = now
	return true, nil
}
