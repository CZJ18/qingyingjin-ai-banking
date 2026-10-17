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

func (d *BankingDAO) CreateSubscription(ctx context.Context, subscription *entity.Subscription) (*entity.Subscription, error) {
	if subscription == nil {
		return nil, errors.New("subscription must not be nil")
	}
	if err := requireOwned[model.BankAccount](ctx, d.dbFor(ctx), subscription.UserID, subscription.BankAccountID, "bank account"); err != nil {
		return nil, err
	}
	if err := prepareCreate(ctx, d, &subscription.ID, &subscription.CreatedAt, &subscription.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.SubscriptionToPO(subscription)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.SubscriptionToEntity(po), nil
}

func (d *BankingDAO) GetSubscription(ctx context.Context, userID, id int64) (*entity.Subscription, error) {
	var po model.Subscription
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.SubscriptionToEntity(&po), nil
}

func (d *BankingDAO) ListSubscriptions(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Subscription, error) {
	var rows []*model.Subscription
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.Subscription, len(rows))
	for i, row := range rows {
		result[i] = convertor.SubscriptionToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateSubscription(ctx context.Context, subscription *entity.Subscription) error {
	if subscription == nil {
		return errors.New("subscription must not be nil")
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"merchant_name": subscription.MerchantName, "external_subscription_id": nullableString(subscription.ExternalSubscriptionID), "billing_interval": subscription.BillingInterval, "amount_minor": subscription.AmountMinor, "currency": subscription.Currency, "next_billing_at": subscription.NextBillingAt, "status": string(subscription.Status), "updated_at": now}
	r := d.dbFor(ctx).WithContext(ctx).Model(&model.Subscription{}).Where("user_id = ? AND id = ?", subscription.UserID, subscription.ID).Updates(updates)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	subscription.UpdatedAt = now
	return nil
}
