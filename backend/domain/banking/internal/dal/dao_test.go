package dal

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeIDGenerator struct {
	next int64
}

func (f *fakeIDGenerator) GenID(context.Context) (int64, error) {
	f.next++
	return f.next, nil
}

func (f *fakeIDGenerator) GenMultiIDs(_ context.Context, count int) ([]int64, error) {
	ids := make([]int64, count)
	for i := range ids {
		f.next++
		ids[i] = f.next
	}
	return ids, nil
}

func newTestDAO(t *testing.T) (*BankingDAO, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet SQL expectations: %v", err)
		}
	})
	return NewBankingDAO(db, &fakeIDGenerator{next: 100}), mock
}

func TestGetBankAccountScopesByTenantAndConverts(t *testing.T) {
	dao, mock := newTestDAO(t)
	rows := sqlmock.NewRows([]string{"id", "user_id", "external_account_id", "display_name", "account_type", "masked_account_number", "currency", "balance_minor", "status", "created_at", "updated_at"}).
		AddRow(7, 42, "ext", "Daily", "checking", "1234", "CNY", 9876, "active", 10, 11)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `banking_bank_accounts` WHERE user_id = ? AND id = ? ORDER BY `banking_bank_accounts`.`id` LIMIT ?")).
		WithArgs(int64(42), int64(7), 1).WillReturnRows(rows)

	got, err := dao.GetBankAccount(context.Background(), 42, 7)
	if err != nil || got == nil {
		t.Fatalf("GetBankAccount() = %#v, %v", got, err)
	}
	if got.ID != 7 || got.UserID != 42 || got.ExternalAccountID != "ext" || got.BalanceMinor != 9876 || got.Status != entity.BankAccountStatusActive {
		t.Fatalf("unexpected conversion: %#v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetBankAccountMissingReturnsNil(t *testing.T) {
	dao, mock := newTestDAO(t)
	mock.ExpectQuery("SELECT .*banking_bank_accounts.*WHERE user_id = \\? AND id = \\?").
		WithArgs(int64(42), int64(404), 1).WillReturnError(gorm.ErrRecordNotFound)
	got, err := dao.GetBankAccount(context.Background(), 42, 404)
	if err != nil || got != nil {
		t.Fatalf("GetBankAccount() = %#v, %v; want nil, nil", got, err)
	}
}

func TestListBankAccountsDefaultsAndTenantScope(t *testing.T) {
	dao, mock := newTestDAO(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `banking_bank_accounts` WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?")).
		WithArgs(int64(42), 50).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := dao.ListBankAccounts(context.Background(), 42, nil); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateSubscriptionScopesIdentityAndDoesNotSetIt(t *testing.T) {
	dao, mock := newTestDAO(t)
	s := &entity.Subscription{ID: 8, UserID: 42, BankAccountID: 2, MerchantName: "gym", BillingInterval: "month", AmountMinor: 3000, Currency: "CNY", NextBillingAt: 99, Status: entity.SubscriptionStatusActive, CreatedAt: 1}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `banking_subscriptions` SET `amount_minor`=\\?,`billing_interval`=\\?,`currency`=\\?,`external_subscription_id`=\\?,`merchant_name`=\\?,`next_billing_at`=\\?,`status`=\\?,`updated_at`=\\? WHERE user_id = \\? AND id = \\?").
		WithArgs(int64(3000), "month", "CNY", nil, "gym", int64(99), "active", sqlmock.AnyArg(), int64(42), int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := dao.UpdateSubscription(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.ID != 8 || s.UserID != 42 {
		t.Fatalf("identity mutated: %#v", s)
	}
}

func TestTransactionCommitRollbackAndScopedDAO(t *testing.T) {
	t.Run("commit and nested DAO uses tx", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT .*banking_bank_accounts.*WHERE user_id = \\? AND id = \\?").
			WithArgs(int64(42), int64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(7, 42))
		mock.ExpectCommit()
		err := dao.Transaction(context.Background(), func(ctx context.Context) error {
			_, err := dao.GetBankAccount(ctx, 42, 7)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rollback preserves callback error", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		want := errors.New("stop")
		mock.ExpectBegin()
		mock.ExpectRollback()
		got := dao.Transaction(context.Background(), func(context.Context) error { return want })
		if !errors.Is(got, want) {
			t.Fatalf("Transaction() error = %v, want %v", got, want)
		}
	})
	t.Run("nil callback", func(t *testing.T) {
		dao, _ := newTestDAO(t)
		if err := dao.Transaction(context.Background(), nil); err == nil {
			t.Fatal("Transaction(nil) error = nil")
		}
	})
}

func TestCreateAgentAuditGeneratesIdentityAndTimestamp(t *testing.T) {
	dao, mock := newTestDAO(t)
	a := &entity.AgentAudit{UserID: 42, AgentID: "agent", Action: "read", Outcome: entity.AgentAuditOutcomeSuccess}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `banking_agent_audits`").
		WithArgs(int64(42), "agent", "", "", "", "", "read", "success", sqlmock.AnyArg(), sqlmock.AnyArg(), "", "", sqlmock.AnyArg(), int64(101)).
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectCommit()
	got, err := dao.CreateAgentAudit(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 101 || got.CreatedAt == 0 {
		t.Fatalf("generated values missing: %#v", got)
	}
}

func TestUpdateHealthBudgetCAS(t *testing.T) {
	t.Run("success increments entity version", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		b := &entity.HealthBudget{ID: 9, UserID: 42, Version: 3, Currency: "CNY", Status: entity.HealthBudgetStatusActive}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_health_budgets` SET .*`version`=\\?.* WHERE user_id = \\? AND id = \\? AND version = \\?").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(4), int64(42), int64(9), int64(3)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		ok, err := dao.UpdateHealthBudget(context.Background(), b)
		if err != nil || !ok || b.Version != 4 || b.UpdatedAt == 0 {
			t.Fatalf("UpdateHealthBudget() = %v, %v, budget=%#v", ok, err, b)
		}
	})
	t.Run("conflict leaves entity unchanged", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		b := &entity.HealthBudget{ID: 9, UserID: 42, Version: 3}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_health_budgets`").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
		ok, err := dao.UpdateHealthBudget(context.Background(), b)
		if err != nil || ok || b.Version != 3 || b.UpdatedAt != 0 {
			t.Fatalf("UpdateHealthBudget() = %v, %v, budget=%#v", ok, err, b)
		}
	})
}

func TestBatchCreateTransactionsValidation(t *testing.T) {
	dao, mock := newTestDAO(t)
	if err := dao.BatchCreateTransactions(context.Background(), nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if err := dao.BatchCreateTransactions(context.Background(), []*entity.Transaction{nil}); err == nil {
		t.Fatal("nil element accepted")
	}
	if err := dao.BatchCreateTransactions(context.Background(), []*entity.Transaction{{UserID: 1}, {UserID: 2}}); err == nil {
		t.Fatal("mixed tenants accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("validation wrote to database: %v", err)
	}
}

func TestCreateFoodOrderPersistsParentAndTenantOwnedItemsInOneTransaction(t *testing.T) {
	dao, mock := newTestDAO(t)
	order := &entity.FoodOrder{UserID: 42, HealthBudgetID: 4, BankAccountID: 5, Provider: "p", MerchantName: "m", TotalAmountMinor: 800, Currency: "CNY", Status: entity.FoodOrderStatusDraft, Items: []*entity.FoodOrderItem{{UserID: 999, FoodOrderID: 999, FoodItemID: 6, Name: "rice", Quantity: 2, UnitPriceMinor: 400, SubtotalMinor: 800, Currency: "CNY"}}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*banking_health_budgets.*WHERE user_id = \\? AND id = \\?.*LIMIT \\?").
		WithArgs(int64(42), int64(4), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(4, 42))
	mock.ExpectQuery("SELECT .*banking_bank_accounts.*WHERE user_id = \\? AND id = \\?.*LIMIT \\?").
		WithArgs(int64(42), int64(5), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(5, 42))
	mock.ExpectQuery("SELECT .*banking_food_items.*WHERE user_id = \\? AND id = \\?.*LIMIT \\?").
		WithArgs(int64(42), int64(6), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(6, 42))
	mock.ExpectExec("INSERT INTO `banking_food_orders` .*VALUES \\(\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?\\)").
		WithArgs(int64(42), int64(4), int64(5), "p", "m", int64(800), "CNY", "draft", nil, sqlmock.AnyArg(), sqlmock.AnyArg(), int64(101)).
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec("INSERT INTO `banking_food_order_items`").
		WithArgs(int64(42), int64(101), int64(6), "rice", int64(2), int64(400), int64(800), "CNY", sqlmock.AnyArg(), sqlmock.AnyArg(), int64(102)).
		WillReturnResult(sqlmock.NewResult(102, 1))
	mock.ExpectCommit()
	got, err := dao.CreateFoodOrder(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].UserID != 42 || got.Items[0].FoodOrderID != got.ID {
		t.Fatalf("child ownership not enforced: %#v", got.Items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFoodOrderRollsBackOnNilItem(t *testing.T) {
	dao, mock := newTestDAO(t)
	order := &entity.FoodOrder{UserID: 42, Items: []*entity.FoodOrderItem{nil}}
	mock.ExpectBegin()
	mock.ExpectRollback()
	if _, err := dao.CreateFoodOrder(context.Background(), order); err == nil {
		t.Fatal("nil item accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetFoodOrderLoadsTenantScopedItems(t *testing.T) {
	dao, mock := newTestDAO(t)
	mock.ExpectQuery("SELECT .*banking_food_orders.*WHERE user_id = \\? AND id = \\?").
		WithArgs(int64(42), int64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status"}).AddRow(7, 42, "draft"))
	mock.ExpectQuery("SELECT .*banking_food_order_items.*WHERE user_id = \\? AND food_order_id = \\?").
		WithArgs(int64(42), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "food_order_id", "name"}).AddRow(8, 42, 7, "rice"))
	got, err := dao.GetFoodOrder(context.Background(), 42, 7)
	if err != nil || got == nil || len(got.Items) != 1 || got.Items[0].UserID != 42 {
		t.Fatalf("GetFoodOrder() = %#v, %v", got, err)
	}
}

func TestGetFoodOrderMissingDoesNotQueryItems(t *testing.T) {
	dao, mock := newTestDAO(t)
	mock.ExpectQuery("SELECT .*banking_food_orders.*WHERE user_id = \\? AND id = \\?").
		WithArgs(int64(42), int64(404), 1).WillReturnError(gorm.ErrRecordNotFound)
	got, err := dao.GetFoodOrder(context.Background(), 42, 404)
	if err != nil || got != nil {
		t.Fatalf("GetFoodOrder() = %#v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialRecordUpdatesDoNotChangeImmutableReferences(t *testing.T) {
	t.Run("payment", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		payment := &entity.Payment{ID: 8, UserID: 42, FoodOrderID: 70, BankAccountID: 71, IdempotencyKey: "immutable", AmountMinor: 900, Currency: "USD", ExternalPaymentID: "external", Status: entity.PaymentStatusSucceeded, FailureCode: "code", FailureMessage: "message"}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_payments` SET `external_payment_id`=\\?,`failure_code`=\\?,`failure_message`=\\?,`status`=\\?,`updated_at`=\\? WHERE user_id = \\? AND id = \\?").
			WithArgs("external", "code", "message", "succeeded", sqlmock.AnyArg(), int64(42), int64(8)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := dao.UpdatePaymentRecord(context.Background(), payment); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("transfer", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		transfer := &entity.Transfer{ID: 8, UserID: 42, SourceBankAccountID: 70, TargetReference: "immutable", IdempotencyKey: "immutable", AmountMinor: 900, Currency: "USD", Status: entity.TransferStatusSucceeded, Description: "done"}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_transfers` SET `description`=\\?,`status`=\\?,`updated_at`=\\? WHERE user_id = \\? AND id = \\?").
			WithArgs("done", "succeeded", sqlmock.AnyArg(), int64(42), int64(8)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := dao.UpdateTransferRecord(context.Background(), transfer); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("subscription", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		subscription := &entity.Subscription{ID: 8, UserID: 42, BankAccountID: 70, MerchantName: "gym", ExternalSubscriptionID: "ext", BillingInterval: "month", AmountMinor: 900, Currency: "CNY", NextBillingAt: 99, Status: entity.SubscriptionStatusPaused}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_subscriptions` SET `amount_minor`=\\?,`billing_interval`=\\?,`currency`=\\?,`external_subscription_id`=\\?,`merchant_name`=\\?,`next_billing_at`=\\?,`status`=\\?,`updated_at`=\\? WHERE user_id = \\? AND id = \\?").
			WithArgs(int64(900), "month", "CNY", "ext", "gym", int64(99), "paused", sqlmock.AnyArg(), int64(42), int64(8)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := dao.UpdateSubscription(context.Background(), subscription); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("food order", func(t *testing.T) {
		dao, mock := newTestDAO(t)
		order := &entity.FoodOrder{ID: 8, UserID: 42, HealthBudgetID: 70, BankAccountID: 71, Provider: "provider", MerchantName: "merchant", TotalAmountMinor: 900, Currency: "CNY", Status: entity.FoodOrderStatusConfirmed, ExternalOrderID: "ext"}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `banking_food_orders` SET `currency`=\\?,`external_order_id`=\\?,`merchant_name`=\\?,`provider`=\\?,`status`=\\?,`total_amount_minor`=\\?,`updated_at`=\\? WHERE user_id = \\? AND id = \\?").
			WithArgs("CNY", "ext", "merchant", "provider", "confirmed", int64(900), sqlmock.AnyArg(), int64(42), int64(8)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := dao.UpdateFoodOrder(context.Background(), order); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNonCASUpdatesReturnNotFoundWithoutMutatingTimestamp(t *testing.T) {
	tests := []struct {
		name  string
		table string
		call  func(*BankingDAO) (int64, error)
	}{
		{name: "bank account", table: "banking_bank_accounts", call: func(d *BankingDAO) (int64, error) {
			value := &entity.BankAccount{ID: 8, UserID: 42, UpdatedAt: 123}
			err := d.UpdateBankAccount(context.Background(), value)
			return value.UpdatedAt, err
		}},
		{name: "food item", table: "banking_food_items", call: func(d *BankingDAO) (int64, error) {
			value := &entity.FoodItem{ID: 8, UserID: 42, UpdatedAt: 123}
			err := d.UpdateFoodItem(context.Background(), value)
			return value.UpdatedAt, err
		}},
		{name: "payment", table: "banking_payments", call: func(d *BankingDAO) (int64, error) {
			value := &entity.Payment{ID: 8, UserID: 42, UpdatedAt: 123}
			err := d.UpdatePaymentRecord(context.Background(), value)
			return value.UpdatedAt, err
		}},
		{name: "transfer", table: "banking_transfers", call: func(d *BankingDAO) (int64, error) {
			value := &entity.Transfer{ID: 8, UserID: 42, UpdatedAt: 123}
			err := d.UpdateTransferRecord(context.Background(), value)
			return value.UpdatedAt, err
		}},
		{name: "subscription", table: "banking_subscriptions", call: func(d *BankingDAO) (int64, error) {
			value := &entity.Subscription{ID: 8, UserID: 42, UpdatedAt: 123}
			err := d.UpdateSubscription(context.Background(), value)
			return value.UpdatedAt, err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dao, mock := newTestDAO(t)
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `" + tt.table + "` SET .* WHERE user_id = \\? AND id = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			updatedAt, err := tt.call(dao)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("error = %v, want gorm.ErrRecordNotFound", err)
			}
			if updatedAt != 123 {
				t.Fatalf("UpdatedAt = %d, want unchanged 123", updatedAt)
			}
		})
	}
}

func TestUpdateFoodOrderNotFoundRollsBackBeforeItems(t *testing.T) {
	dao, mock := newTestDAO(t)
	order := &entity.FoodOrder{ID: 8, UserID: 42, UpdatedAt: 123, Items: []*entity.FoodOrderItem{{FoodItemID: 99}}}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `banking_food_orders` SET .* WHERE user_id = \\? AND id = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err := dao.UpdateFoodOrder(context.Background(), order)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("error = %v, want gorm.ErrRecordNotFound", err)
	}
	if order.UpdatedAt != 123 {
		t.Fatalf("UpdatedAt = %d, want unchanged 123", order.UpdatedAt)
	}
}

func TestUpsertFinancialProfileAlwaysRefreshesUpdatedAt(t *testing.T) {
	dao, mock := newTestDAO(t)
	profile := &entity.UserFinancialProfile{ID: 7, UserID: 42, DefaultCurrency: "CNY", CreatedAt: 10, UpdatedAt: 1}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `banking_financial_profiles` .*ON DUPLICATE KEY UPDATE").
		WithArgs(int64(42), nil, "CNY", int64(0), "", sqlmock.AnyArg(), "", "", int64(10), sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .*banking_financial_profiles.*WHERE user_id = \\?.*LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "default_currency", "created_at", "updated_at"}).AddRow(7, 42, "CNY", 10, 999))
	got, err := dao.UpsertFinancialProfile(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.UpdatedAt <= 1 || got.UpdatedAt <= 1 {
		t.Fatalf("UpdatedAt was not refreshed: input=%d returned=%d", profile.UpdatedAt, got.UpdatedAt)
	}
}

func TestCreateFoodOrderCommitFailureReturnsNilEntity(t *testing.T) {
	dao, mock := newTestDAO(t)
	order := &entity.FoodOrder{UserID: 42}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `banking_food_orders`").WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
	got, err := dao.CreateFoodOrder(context.Background(), order)
	if err == nil || got != nil {
		t.Fatalf("CreateFoodOrder() = %#v, %v; want nil, commit error", got, err)
	}
}

func TestUpdateFoodOrderCommitFailureDoesNotMutateTimestamp(t *testing.T) {
	dao, mock := newTestDAO(t)
	order := &entity.FoodOrder{ID: 8, UserID: 42, UpdatedAt: 123}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `banking_food_orders` SET .* WHERE user_id = \\? AND id = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
	err := dao.UpdateFoodOrder(context.Background(), order)
	if err == nil {
		t.Fatal("UpdateFoodOrder() error = nil, want commit error")
	}
	if order.UpdatedAt != 123 {
		t.Fatalf("UpdatedAt = %d, want unchanged 123", order.UpdatedAt)
	}
}
