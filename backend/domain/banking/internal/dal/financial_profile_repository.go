package dal

import (
	"context"
	"errors"
	"time"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/convertor"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (d *BankingDAO) UpsertFinancialProfile(ctx context.Context, profile *entity.UserFinancialProfile) (*entity.UserFinancialProfile, error) {
	if profile == nil {
		return nil, errors.New("financial profile must not be nil")
	}
	if err := prepareCreate(ctx, d, &profile.ID, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
		return nil, err
	}
	profile.UpdatedAt = time.Now().UnixMilli()
	po := convertor.FinancialProfileToPO(profile)
	updates := []string{"external_customer_id", "default_currency", "monthly_income_minor", "health_goal", "dietary_preferences", "risk_level", "timezone", "updated_at"}
	err := d.dbFor(ctx).WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns(updates)}).Create(po).Error
	if err != nil {
		return nil, err
	}
	return d.GetFinancialProfile(ctx, profile.UserID)
}

func (d *BankingDAO) GetFinancialProfile(ctx context.Context, userID int64) (*entity.UserFinancialProfile, error) {
	var po model.UserFinancialProfile
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.FinancialProfileToEntity(&po), nil
}
