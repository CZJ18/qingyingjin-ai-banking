package dal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/infra/idgen"
	"gorm.io/gorm"
)

type transactionContextKey struct{}

type BankingDAO struct {
	db          *gorm.DB
	idGenerator idgen.IDGenerator
}

func NewBankingDAO(db *gorm.DB, generator idgen.IDGenerator) *BankingDAO {
	return &BankingDAO{db: db, idGenerator: generator}
}

func (d *BankingDAO) dbFor(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(transactionContextKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return d.db
}

func (d *BankingDAO) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("banking transaction callback must not be nil")
	}
	return d.dbFor(ctx).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, transactionContextKey{}, tx))
	})
}

func (d *BankingDAO) nextID(ctx context.Context) (int64, error) {
	if d.idGenerator == nil {
		return 0, errors.New("banking ID generator must not be nil")
	}
	return d.idGenerator.GenID(ctx)
}

func (d *BankingDAO) nextIDs(ctx context.Context, count int) ([]int64, error) {
	if count == 0 {
		return nil, nil
	}
	if d.idGenerator == nil {
		return nil, errors.New("banking ID generator must not be nil")
	}
	ids, err := d.idGenerator.GenMultiIDs(ctx, count)
	if err != nil {
		return nil, err
	}
	if len(ids) != count {
		return nil, fmt.Errorf("banking ID generator returned %d IDs, want %d", len(ids), count)
	}
	return ids, nil
}

func prepareCreate(ctx context.Context, d *BankingDAO, id *int64, createdAt, updatedAt *int64) error {
	if *id == 0 {
		generated, err := d.nextID(ctx)
		if err != nil {
			return err
		}
		*id = generated
	}
	now := time.Now().UnixMilli()
	if *createdAt == 0 {
		*createdAt = now
	}
	if updatedAt != nil && *updatedAt == 0 {
		*updatedAt = now
	}
	return nil
}

func applyList(db *gorm.DB, options *entity.ListOptions) *gorm.DB {
	normalized := entity.NormalizeListOptions(options)
	return db.Order("created_at DESC, id DESC").Limit(normalized.Limit).Offset(normalized.Offset)
}

func requireOwned[T any](ctx context.Context, db *gorm.DB, userID, id int64, label string) error {
	if userID <= 0 || id <= 0 {
		return fmt.Errorf("%s ownership requires positive user and resource IDs", label)
	}
	var value T
	err := db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%s %d is not owned by user %d", label, id, userID)
	}
	return err
}
