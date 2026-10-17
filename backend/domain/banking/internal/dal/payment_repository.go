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

func (d *BankingDAO) CreatePayment(ctx context.Context, payment *entity.Payment) (*entity.Payment, error) {
	if payment == nil {
		return nil, errors.New("payment must not be nil")
	}
	if err := requireOwned[model.FoodOrder](ctx, d.dbFor(ctx), payment.UserID, payment.FoodOrderID, "food order"); err != nil {
		return nil, err
	}
	if err := requireOwned[model.BankAccount](ctx, d.dbFor(ctx), payment.UserID, payment.BankAccountID, "bank account"); err != nil {
		return nil, err
	}
	if err := prepareCreate(ctx, d, &payment.ID, &payment.CreatedAt, &payment.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.PaymentToPO(payment)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.PaymentToEntity(po), nil
}

func (d *BankingDAO) GetPayment(ctx context.Context, userID, id int64) (*entity.Payment, error) {
	var po model.Payment
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.PaymentToEntity(&po), nil
}

func (d *BankingDAO) ListPayments(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Payment, error) {
	var rows []*model.Payment
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.Payment, len(rows))
	for i, row := range rows {
		result[i] = convertor.PaymentToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdatePaymentRecord(ctx context.Context, payment *entity.Payment) error {
	if payment == nil {
		return errors.New("payment must not be nil")
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"external_payment_id": nullableString(payment.ExternalPaymentID), "status": string(payment.Status), "failure_code": payment.FailureCode, "failure_message": payment.FailureMessage, "updated_at": now}
	r := d.dbFor(ctx).WithContext(ctx).Model(&model.Payment{}).Where("user_id = ? AND id = ?", payment.UserID, payment.ID).Updates(updates)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	payment.UpdatedAt = now
	return nil
}
