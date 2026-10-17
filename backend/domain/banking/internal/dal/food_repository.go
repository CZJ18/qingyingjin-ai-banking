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

func (d *BankingDAO) CreateFoodItem(ctx context.Context, item *entity.FoodItem) (*entity.FoodItem, error) {
	if item == nil {
		return nil, errors.New("food item must not be nil")
	}
	if err := prepareCreate(ctx, d, &item.ID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	po := convertor.FoodItemToPO(item)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.FoodItemToEntity(po), nil
}

func (d *BankingDAO) GetFoodItem(ctx context.Context, userID, id int64) (*entity.FoodItem, error) {
	var po model.FoodItem
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.FoodItemToEntity(&po), nil
}

func (d *BankingDAO) ListFoodItems(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.FoodItem, error) {
	var rows []*model.FoodItem
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.FoodItem, len(rows))
	for i, row := range rows {
		result[i] = convertor.FoodItemToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateFoodItem(ctx context.Context, item *entity.FoodItem) error {
	if item == nil {
		return errors.New("food item must not be nil")
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"provider": item.Provider, "external_item_id": nullableString(item.ExternalItemID), "name": item.Name, "unit_price_minor": item.UnitPriceMinor, "currency": item.Currency, "nutrition_metadata": []byte(item.NutritionMetadata), "status": string(item.Status), "updated_at": now}
	result := d.dbFor(ctx).WithContext(ctx).Model(&model.FoodItem{}).Where("user_id = ? AND id = ?", item.UserID, item.ID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	item.UpdatedAt = now
	return nil
}

func (d *BankingDAO) CreateFoodOrder(ctx context.Context, order *entity.FoodOrder) (*entity.FoodOrder, error) {
	if order == nil {
		return nil, errors.New("food order must not be nil")
	}
	var created *entity.FoodOrder
	err := d.Transaction(ctx, func(txCtx context.Context) error {
		if err := validateFoodOrderReferences(txCtx, d, order); err != nil {
			return err
		}
		if err := prepareCreate(txCtx, d, &order.ID, &order.CreatedAt, &order.UpdatedAt); err != nil {
			return err
		}
		parent := convertor.FoodOrderToPO(order)
		parent.Items = nil
		if err := d.dbFor(txCtx).WithContext(txCtx).Create(parent).Error; err != nil {
			return err
		}
		items, err := d.prepareOrderItems(txCtx, order)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			if err := d.dbFor(txCtx).WithContext(txCtx).Create(&items).Error; err != nil {
				return err
			}
		}
		parent.Items = items
		created = convertor.FoodOrderToEntity(parent)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func validateFoodOrderReferences(ctx context.Context, d *BankingDAO, order *entity.FoodOrder) error {
	if order.HealthBudgetID > 0 {
		if err := requireOwned[model.HealthBudget](ctx, d.dbFor(ctx), order.UserID, order.HealthBudgetID, "health budget"); err != nil {
			return err
		}
	}
	if order.BankAccountID > 0 {
		if err := requireOwned[model.BankAccount](ctx, d.dbFor(ctx), order.UserID, order.BankAccountID, "bank account"); err != nil {
			return err
		}
	}
	return validateFoodOrderItems(ctx, d, order)
}

func validateFoodOrderItems(ctx context.Context, d *BankingDAO, order *entity.FoodOrder) error {
	seen := make(map[int64]struct{})
	for i, item := range order.Items {
		if item == nil {
			return fmt.Errorf("food order item at index %d must not be nil", i)
		}
		if item.FoodItemID > 0 {
			if _, ok := seen[item.FoodItemID]; ok {
				continue
			}
			if err := requireOwned[model.FoodItem](ctx, d.dbFor(ctx), order.UserID, item.FoodItemID, "food item"); err != nil {
				return err
			}
			seen[item.FoodItemID] = struct{}{}
		}
	}
	return nil
}

func (d *BankingDAO) prepareOrderItems(ctx context.Context, order *entity.FoodOrder) ([]*model.FoodOrderItem, error) {
	missing := 0
	for i, item := range order.Items {
		if item == nil {
			return nil, fmt.Errorf("food order item at index %d must not be nil", i)
		}
		if item.ID == 0 {
			missing++
		}
	}
	ids, err := d.nextIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	result := make([]*model.FoodOrderItem, len(order.Items))
	idIndex := 0
	for i, item := range order.Items {
		if item.ID == 0 {
			item.ID = ids[idIndex]
			idIndex++
		}
		item.UserID = order.UserID
		item.FoodOrderID = order.ID
		if item.CreatedAt == 0 {
			item.CreatedAt = now
		}
		if item.UpdatedAt == 0 {
			item.UpdatedAt = now
		}
		result[i] = convertor.FoodOrderItemToPO(item)
	}
	return result, nil
}

func (d *BankingDAO) GetFoodOrder(ctx context.Context, userID, id int64) (*entity.FoodOrder, error) {
	var po model.FoodOrder
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND food_order_id = ?", userID, id).Find(&po.Items).Error; err != nil {
		return nil, err
	}
	return convertor.FoodOrderToEntity(&po), nil
}

func (d *BankingDAO) ListFoodOrders(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.FoodOrder, error) {
	var rows []*model.FoodOrder
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.FoodOrder, len(rows))
	for i, row := range rows {
		row.Items = nil
		result[i] = convertor.FoodOrderToEntity(row)
	}
	return result, nil
}

func (d *BankingDAO) UpdateFoodOrder(ctx context.Context, order *entity.FoodOrder) error {
	if order == nil {
		return errors.New("food order must not be nil")
	}
	now := time.Now().UnixMilli()
	err := d.Transaction(ctx, func(txCtx context.Context) error {
		updates := map[string]any{"provider": order.Provider, "merchant_name": order.MerchantName, "total_amount_minor": order.TotalAmountMinor, "currency": order.Currency, "status": string(order.Status), "external_order_id": nullableString(order.ExternalOrderID), "updated_at": now}
		result := d.dbFor(txCtx).WithContext(txCtx).Model(&model.FoodOrder{}).Where("user_id = ? AND id = ?", order.UserID, order.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if order.Items != nil {
			if err := validateFoodOrderItems(txCtx, d, order); err != nil {
				return err
			}
			if err := d.dbFor(txCtx).WithContext(txCtx).Where("user_id = ? AND food_order_id = ?", order.UserID, order.ID).Delete(&model.FoodOrderItem{}).Error; err != nil {
				return err
			}
			items, err := d.prepareOrderItems(txCtx, order)
			if err != nil {
				return err
			}
			if len(items) > 0 {
				if err := d.dbFor(txCtx).WithContext(txCtx).Create(&items).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	order.UpdatedAt = now
	return nil
}
