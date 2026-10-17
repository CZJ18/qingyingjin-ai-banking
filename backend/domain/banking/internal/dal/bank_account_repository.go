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

func (d *BankingDAO) CreateBankAccount(ctx context.Context, account *entity.BankAccount) (*entity.BankAccount, error) {
	if account == nil {
		return nil, errors.New("bank account must not be nil")
	}
	if err := prepareCreate(ctx, d, &account.ID, &account.CreatedAt, &account.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.BankAccountToPO(account)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.BankAccountToEntity(po), nil
}

func (d *BankingDAO) GetBankAccount(ctx context.Context, userID, id int64) (*entity.BankAccount, error) {
	var po model.BankAccount
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.BankAccountToEntity(&po), nil
}

func (d *BankingDAO) ListBankAccounts(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.BankAccount, error) {
	var rows []*model.BankAccount
	err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]*entity.BankAccount, len(rows))
	for i, row := range rows {
		result[i] = convertor.BankAccountToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateBankAccount(ctx context.Context, account *entity.BankAccount) error {
	if account == nil {
		return errors.New("bank account must not be nil")
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"external_account_id": nullableString(account.ExternalAccountID), "display_name": account.DisplayName, "account_type": account.AccountType, "masked_account_number": account.MaskedAccountNumber, "currency": account.Currency, "balance_minor": account.BalanceMinor, "status": string(account.Status), "updated_at": now}
	result := d.dbFor(ctx).WithContext(ctx).Model(&model.BankAccount{}).Where("user_id = ? AND id = ?", account.UserID, account.ID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	account.UpdatedAt = now
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
