package dal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/convertor"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
	"gorm.io/gorm"
)

func (d *BankingDAO) CreateTransaction(ctx context.Context, transaction *entity.Transaction) (*entity.Transaction, error) {
	if transaction == nil {
		return nil, errors.New("transaction must not be nil")
	}
	if err := requireOwned[model.BankAccount](ctx, d.dbFor(ctx), transaction.UserID, transaction.BankAccountID, "bank account"); err != nil {
		return nil, err
	}
	if err := prepareCreate(ctx, d, &transaction.ID, &transaction.CreatedAt, &transaction.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.TransactionToPO(transaction)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.TransactionToEntity(po), nil
}

func (d *BankingDAO) BatchCreateTransactions(ctx context.Context, transactions []*entity.Transaction) error {
	if len(transactions) == 0 {
		return nil
	}
	userID := int64(0)
	missing := 0
	for i, transaction := range transactions {
		if transaction == nil {
			return fmt.Errorf("transaction at index %d must not be nil", i)
		}
		if transaction.UserID <= 0 {
			return fmt.Errorf("transaction at index %d has invalid user ID", i)
		}
		if userID == 0 {
			userID = transaction.UserID
		} else if transaction.UserID != userID {
			return errors.New("transaction batch must contain exactly one user ID")
		}
		if transaction.ID == 0 {
			missing++
		}
	}
	return d.Transaction(ctx, func(txCtx context.Context) error {
		seenAccounts := make(map[int64]struct{})
		for _, transaction := range transactions {
			if _, seen := seenAccounts[transaction.BankAccountID]; seen {
				continue
			}
			if err := requireOwned[model.BankAccount](txCtx, d.dbFor(txCtx), userID, transaction.BankAccountID, "bank account"); err != nil {
				return err
			}
			seenAccounts[transaction.BankAccountID] = struct{}{}
		}
		ids, err := d.nextIDs(txCtx, missing)
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		pos := make([]*model.Transaction, len(transactions))
		idIndex := 0
		for i, transaction := range transactions {
			if transaction.ID == 0 {
				transaction.ID = ids[idIndex]
				idIndex++
			}
			if transaction.CreatedAt == 0 {
				transaction.CreatedAt = now
			}
			if transaction.UpdatedAt == 0 {
				transaction.UpdatedAt = now
			}
			pos[i] = convertor.TransactionToPO(transaction)
		}
		return d.dbFor(txCtx).WithContext(txCtx).Create(&pos).Error
	})
}

func (d *BankingDAO) GetTransaction(ctx context.Context, userID, id int64) (*entity.Transaction, error) {
	var po model.Transaction
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.TransactionToEntity(&po), nil
}

func (d *BankingDAO) GetTransactionByExternalID(ctx context.Context, userID, bankAccountID int64, externalID string) (*entity.Transaction, error) {
	var po model.Transaction
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND bank_account_id = ? AND external_transaction_id = ?", userID, bankAccountID, externalID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.TransactionToEntity(&po), nil
}

func (d *BankingDAO) ListTransactions(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Transaction, error) {
	var rows []*model.Transaction
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.Transaction, len(rows))
	for i, row := range rows {
		result[i] = convertor.TransactionToEntity(row)
	}
	return result, nil
}
