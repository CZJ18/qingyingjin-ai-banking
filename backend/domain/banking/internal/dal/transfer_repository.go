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

func (d *BankingDAO) CreateTransfer(ctx context.Context, transfer *entity.Transfer) (*entity.Transfer, error) {
	if transfer == nil {
		return nil, errors.New("transfer must not be nil")
	}
	if err := requireOwned[model.BankAccount](ctx, d.dbFor(ctx), transfer.UserID, transfer.SourceBankAccountID, "source bank account"); err != nil {
		return nil, err
	}
	if err := prepareCreate(ctx, d, &transfer.ID, &transfer.CreatedAt, &transfer.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.TransferToPO(transfer)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.TransferToEntity(po), nil
}

func (d *BankingDAO) GetTransfer(ctx context.Context, userID, id int64) (*entity.Transfer, error) {
	var po model.Transfer
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.TransferToEntity(&po), nil
}

func (d *BankingDAO) ListTransfers(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Transfer, error) {
	var rows []*model.Transfer
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.Transfer, len(rows))
	for i, row := range rows {
		result[i] = convertor.TransferToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateTransferRecord(ctx context.Context, transfer *entity.Transfer) error {
	if transfer == nil {
		return errors.New("transfer must not be nil")
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"status": string(transfer.Status), "description": transfer.Description, "updated_at": now}
	r := d.dbFor(ctx).WithContext(ctx).Model(&model.Transfer{}).Where("user_id = ? AND id = ?", transfer.UserID, transfer.ID).Updates(updates)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	transfer.UpdatedAt = now
	return nil
}
