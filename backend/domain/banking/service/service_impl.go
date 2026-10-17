package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/repository"
)

const maxRawJSONSize = 64 * 1024

type bankingService struct{ repo repository.Repository }

func (s *bankingService) ready() error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("%w: repository is nil", ErrInvalidArgument)
	}
	return nil
}

func validUserID(userID int64) error {
	if userID <= 0 {
		return ErrInvalidUserID
	}
	return nil
}

func validObjectID(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: ID must be positive", ErrInvalidArgument)
	}
	return nil
}

func validTenantObject(userID, id int64) error {
	if err := validUserID(userID); err != nil {
		return err
	}
	return validObjectID(id)
}

func normalizeCurrency(currency *string) error {
	normalized, err := entity.NormalizeCurrency(*currency)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	*currency = normalized
	return nil
}

func nonnegative(amounts ...int64) error {
	for _, amount := range amounts {
		if amount < 0 {
			return fmt.Errorf("%w: value must not be negative", ErrInvalidAmount)
		}
	}
	return nil
}

func required(value *string, name string) error {
	*value = strings.TrimSpace(*value)
	if *value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidArgument, name)
	}
	return nil
}

func cloneJSON(raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) > maxRawJSONSize {
		return nil, fmt.Errorf("%w: JSON exceeds 64 KiB", ErrInvalidArgument)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid JSON", ErrInvalidArgument)
	}
	return append(json.RawMessage(nil), raw...), nil
}

func normalizeSensitiveKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, key)
}

func containsSensitiveKey(value any) bool {
	sensitive := map[string]struct{}{
		"password": {}, "secret": {}, "token": {}, "accesstoken": {}, "cvv": {},
		"cardnumber": {}, "fullcardnumber": {}, "accountnumber": {},
	}
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if _, found := sensitive[normalizeSensitiveKey(key)]; found || containsSensitiveKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsSensitiveKey(child) {
				return true
			}
		}
	}
	return false
}

func cloneAuditJSON(raw json.RawMessage) (json.RawMessage, error) {
	cloned, err := cloneJSON(raw)
	if err != nil || cloned == nil {
		return cloned, err
	}
	var value any
	if err := json.Unmarshal(cloned, &value); err != nil {
		return nil, fmt.Errorf("%w: invalid audit JSON", ErrInvalidArgument)
	}
	if containsSensitiveKey(value) {
		return nil, fmt.Errorf("%w: audit JSON contains sensitive data", ErrInvalidArgument)
	}
	return cloned, nil
}

func (s *bankingService) UpsertFinancialProfile(ctx context.Context, v *entity.UserFinancialProfile) (*entity.UserFinancialProfile, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("%w: financial profile is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return nil, err
	}
	if err := nonnegative(v.MonthlyIncomeMinor); err != nil {
		return nil, err
	}
	if err := normalizeCurrency(&v.DefaultCurrency); err != nil {
		return nil, err
	}
	v.ExternalCustomerID = strings.TrimSpace(v.ExternalCustomerID)
	var err error
	v.DietaryPreferences, err = cloneJSON(v.DietaryPreferences)
	if err != nil {
		return nil, err
	}
	return s.repo.UpsertFinancialProfile(ctx, v)
}

func (s *bankingService) GetFinancialProfile(ctx context.Context, userID int64) (*entity.UserFinancialProfile, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.GetFinancialProfile(ctx, userID)
}

func validateBankAccount(v *entity.BankAccount, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: bank account is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if err := nonnegative(v.BalanceMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	v.ExternalAccountID = strings.TrimSpace(v.ExternalAccountID)
	if v.Status == "" {
		v.Status = entity.BankAccountStatusActive
	}
	switch v.Status {
	case entity.BankAccountStatusActive, entity.BankAccountStatusInactive, entity.BankAccountStatusClosed:
	default:
		return fmt.Errorf("%w: invalid bank account status", ErrInvalidArgument)
	}
	return nil
}

func (s *bankingService) CreateBankAccount(ctx context.Context, v *entity.BankAccount) (*entity.BankAccount, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateBankAccount(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateBankAccount(ctx, v)
}
func (s *bankingService) GetBankAccount(ctx context.Context, userID, id int64) (*entity.BankAccount, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetBankAccount(ctx, userID, id)
}
func (s *bankingService) ListBankAccounts(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.BankAccount, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListBankAccounts(ctx, userID, o)
}
func (s *bankingService) UpdateBankAccount(ctx context.Context, v *entity.BankAccount) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateBankAccount(v, true); err != nil {
		return err
	}
	return s.repo.UpdateBankAccount(ctx, v)
}

func validateTransaction(v *entity.Transaction) error {
	if v == nil {
		return fmt.Errorf("%w: transaction is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if err := validObjectID(v.BankAccountID); err != nil {
		return err
	}
	if err := nonnegative(v.AmountMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	switch v.Direction {
	case entity.TransactionDirectionIncome, entity.TransactionDirectionExpense:
	default:
		return fmt.Errorf("%w: invalid transaction direction", ErrInvalidArgument)
	}
	v.ExternalTransactionID = strings.TrimSpace(v.ExternalTransactionID)
	var err error
	v.SourceMetadata, err = cloneJSON(v.SourceMetadata)
	return err
}
func (s *bankingService) CreateTransaction(ctx context.Context, v *entity.Transaction) (*entity.Transaction, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateTransaction(v); err != nil {
		return nil, err
	}
	return s.repo.CreateTransaction(ctx, v)
}
func (s *bankingService) BatchCreateTransactions(ctx context.Context, values []*entity.Transaction) error {
	if err := s.ready(); err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("%w: transactions are required", ErrInvalidArgument)
	}
	var userID int64
	for _, v := range values {
		if err := validateTransaction(v); err != nil {
			return err
		}
		if userID == 0 {
			userID = v.UserID
		} else if userID != v.UserID {
			return fmt.Errorf("%w: transactions must have one user", ErrInvalidArgument)
		}
	}
	return s.repo.BatchCreateTransactions(ctx, values)
}
func (s *bankingService) GetTransaction(ctx context.Context, userID, id int64) (*entity.Transaction, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetTransaction(ctx, userID, id)
}
func (s *bankingService) GetTransactionByExternalID(ctx context.Context, userID, accountID int64, externalID string) (*entity.Transaction, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	if err := validObjectID(accountID); err != nil {
		return nil, err
	}
	if err := required(&externalID, "external transaction ID"); err != nil {
		return nil, err
	}
	return s.repo.GetTransactionByExternalID(ctx, userID, accountID, externalID)
}
func (s *bankingService) ListTransactions(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.Transaction, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListTransactions(ctx, userID, o)
}

func validateBudget(v *entity.HealthBudget, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: health budget is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if v.PeriodStart <= 0 || v.PeriodEnd <= v.PeriodStart {
		return fmt.Errorf("%w: invalid budget period", ErrInvalidArgument)
	}
	if err := nonnegative(v.TotalAllocatedMinor, v.FoodAllocatedMinor, v.SpentMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	if v.Status == "" {
		v.Status = entity.HealthBudgetStatusActive
	}
	switch v.Status {
	case entity.HealthBudgetStatusActive, entity.HealthBudgetStatusExhausted, entity.HealthBudgetStatusClosed:
	default:
		return fmt.Errorf("%w: invalid health budget status", ErrInvalidArgument)
	}
	return nil
}
func (s *bankingService) CreateHealthBudget(ctx context.Context, v *entity.HealthBudget) (*entity.HealthBudget, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateBudget(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateHealthBudget(ctx, v)
}
func (s *bankingService) GetHealthBudget(ctx context.Context, userID, id int64) (*entity.HealthBudget, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetHealthBudget(ctx, userID, id)
}
func (s *bankingService) ListHealthBudgets(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.HealthBudget, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListHealthBudgets(ctx, userID, o)
}
func (s *bankingService) UpdateHealthBudget(ctx context.Context, v *entity.HealthBudget) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if err := validateBudget(v, true); err != nil {
		return false, err
	}
	return s.repo.UpdateHealthBudget(ctx, v)
}

func validateFoodItem(v *entity.FoodItem, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: food item is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if err := required(&v.Provider, "provider"); err != nil {
		return err
	}
	if err := required(&v.Name, "name"); err != nil {
		return err
	}
	if err := nonnegative(v.UnitPriceMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	v.ExternalItemID = strings.TrimSpace(v.ExternalItemID)
	if v.Status == "" {
		v.Status = entity.FoodItemStatusActive
	}
	switch v.Status {
	case entity.FoodItemStatusActive, entity.FoodItemStatusInactive:
	default:
		return fmt.Errorf("%w: invalid food item status", ErrInvalidArgument)
	}
	var err error
	v.NutritionMetadata, err = cloneJSON(v.NutritionMetadata)
	return err
}
func (s *bankingService) CreateFoodItem(ctx context.Context, v *entity.FoodItem) (*entity.FoodItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateFoodItem(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateFoodItem(ctx, v)
}
func (s *bankingService) GetFoodItem(ctx context.Context, userID, id int64) (*entity.FoodItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetFoodItem(ctx, userID, id)
}
func (s *bankingService) ListFoodItems(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.FoodItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListFoodItems(ctx, userID, o)
}
func (s *bankingService) UpdateFoodItem(ctx context.Context, v *entity.FoodItem) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateFoodItem(v, true); err != nil {
		return err
	}
	return s.repo.UpdateFoodItem(ctx, v)
}

func validateFoodOrder(v *entity.FoodOrder, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: food order is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if v.HealthBudgetID < 0 || v.BankAccountID < 0 {
		return fmt.Errorf("%w: referenced IDs must not be negative", ErrInvalidArgument)
	}
	if err := required(&v.Provider, "provider"); err != nil {
		return err
	}
	if err := required(&v.MerchantName, "merchant name"); err != nil {
		return err
	}
	if err := nonnegative(v.TotalAmountMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	v.ExternalOrderID = strings.TrimSpace(v.ExternalOrderID)
	if v.Status == "" {
		v.Status = entity.FoodOrderStatusDraft
	}
	switch v.Status {
	case entity.FoodOrderStatusDraft, entity.FoodOrderStatusConfirmed, entity.FoodOrderStatusCancelled, entity.FoodOrderStatusCompleted:
	default:
		return fmt.Errorf("%w: invalid food order status", ErrInvalidArgument)
	}
	for _, item := range v.Items {
		if item == nil {
			return fmt.Errorf("%w: food order item is nil", ErrInvalidArgument)
		}
		if item.ID < 0 || item.FoodOrderID < 0 || item.FoodItemID < 0 {
			return fmt.Errorf("%w: invalid food order item ID", ErrInvalidArgument)
		}
		if item.UserID == 0 {
			item.UserID = v.UserID
		} else if item.UserID != v.UserID {
			return fmt.Errorf("%w: item user mismatch", ErrInvalidArgument)
		}
		if v.ID > 0 && item.FoodOrderID == 0 {
			item.FoodOrderID = v.ID
		} else if v.ID > 0 && item.FoodOrderID != v.ID {
			return fmt.Errorf("%w: item order mismatch", ErrInvalidArgument)
		}
		if item.Quantity <= 0 {
			return fmt.Errorf("%w: quantity must be positive", ErrInvalidArgument)
		}
		if err := nonnegative(item.UnitPriceMinor, item.SubtotalMinor); err != nil {
			return err
		}
		if strings.TrimSpace(item.Currency) == "" {
			item.Currency = v.Currency
		} else if err := normalizeCurrency(&item.Currency); err != nil {
			return err
		}
		item.Name = strings.TrimSpace(item.Name)
	}
	return nil
}
func (s *bankingService) CreateFoodOrder(ctx context.Context, v *entity.FoodOrder) (*entity.FoodOrder, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateFoodOrder(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateFoodOrder(ctx, v)
}
func (s *bankingService) GetFoodOrder(ctx context.Context, userID, id int64) (*entity.FoodOrder, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetFoodOrder(ctx, userID, id)
}
func (s *bankingService) ListFoodOrders(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.FoodOrder, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListFoodOrders(ctx, userID, o)
}
func (s *bankingService) UpdateFoodOrder(ctx context.Context, v *entity.FoodOrder) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateFoodOrder(v, true); err != nil {
		return err
	}
	return s.repo.UpdateFoodOrder(ctx, v)
}

func validatePayment(v *entity.Payment, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: payment is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if err := validObjectID(v.FoodOrderID); err != nil {
		return err
	}
	if err := validObjectID(v.BankAccountID); err != nil {
		return err
	}
	if err := required(&v.IdempotencyKey, "idempotency key"); err != nil {
		return err
	}
	v.ExternalPaymentID = strings.TrimSpace(v.ExternalPaymentID)
	if err := nonnegative(v.AmountMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	if v.Status == "" {
		v.Status = entity.PaymentStatusCreated
	}
	switch v.Status {
	case entity.PaymentStatusCreated, entity.PaymentStatusSucceeded, entity.PaymentStatusFailed, entity.PaymentStatusCancelled:
	default:
		return fmt.Errorf("%w: invalid payment status", ErrInvalidArgument)
	}
	return nil
}
func (s *bankingService) CreatePayment(ctx context.Context, v *entity.Payment) (*entity.Payment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validatePayment(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreatePayment(ctx, v)
}
func (s *bankingService) GetPayment(ctx context.Context, userID, id int64) (*entity.Payment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetPayment(ctx, userID, id)
}
func (s *bankingService) ListPayments(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.Payment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListPayments(ctx, userID, o)
}
func (s *bankingService) UpdatePaymentRecord(ctx context.Context, v *entity.Payment) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validatePayment(v, true); err != nil {
		return err
	}
	return s.repo.UpdatePaymentRecord(ctx, v)
}

func validateTransfer(v *entity.Transfer, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: transfer is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if err := validObjectID(v.SourceBankAccountID); err != nil {
		return err
	}
	if err := required(&v.TargetReference, "target reference"); err != nil {
		return err
	}
	if err := required(&v.IdempotencyKey, "idempotency key"); err != nil {
		return err
	}
	if err := nonnegative(v.AmountMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	if v.Status == "" {
		v.Status = entity.TransferStatusCreated
	}
	switch v.Status {
	case entity.TransferStatusCreated, entity.TransferStatusSucceeded, entity.TransferStatusFailed, entity.TransferStatusCancelled:
	default:
		return fmt.Errorf("%w: invalid transfer status", ErrInvalidArgument)
	}
	return nil
}
func (s *bankingService) CreateTransfer(ctx context.Context, v *entity.Transfer) (*entity.Transfer, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateTransfer(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateTransfer(ctx, v)
}
func (s *bankingService) GetTransfer(ctx context.Context, userID, id int64) (*entity.Transfer, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetTransfer(ctx, userID, id)
}
func (s *bankingService) ListTransfers(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.Transfer, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListTransfers(ctx, userID, o)
}
func (s *bankingService) UpdateTransferRecord(ctx context.Context, v *entity.Transfer) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateTransfer(v, true); err != nil {
		return err
	}
	return s.repo.UpdateTransferRecord(ctx, v)
}

func validateSubscription(v *entity.Subscription, update bool) error {
	if v == nil {
		return fmt.Errorf("%w: subscription is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if update {
		if err := validObjectID(v.ID); err != nil {
			return err
		}
	}
	if err := validObjectID(v.BankAccountID); err != nil {
		return err
	}
	if err := required(&v.MerchantName, "merchant name"); err != nil {
		return err
	}
	v.ExternalSubscriptionID = strings.TrimSpace(v.ExternalSubscriptionID)
	v.BillingInterval = strings.TrimSpace(v.BillingInterval)
	if err := nonnegative(v.AmountMinor); err != nil {
		return err
	}
	if err := normalizeCurrency(&v.Currency); err != nil {
		return err
	}
	if v.Status == "" {
		v.Status = entity.SubscriptionStatusActive
	}
	switch v.Status {
	case entity.SubscriptionStatusActive, entity.SubscriptionStatusPaused, entity.SubscriptionStatusCancelled:
	default:
		return fmt.Errorf("%w: invalid subscription status", ErrInvalidArgument)
	}
	return nil
}
func (s *bankingService) CreateSubscription(ctx context.Context, v *entity.Subscription) (*entity.Subscription, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateSubscription(v, false); err != nil {
		return nil, err
	}
	return s.repo.CreateSubscription(ctx, v)
}
func (s *bankingService) GetSubscription(ctx context.Context, userID, id int64) (*entity.Subscription, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetSubscription(ctx, userID, id)
}
func (s *bankingService) ListSubscriptions(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.Subscription, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListSubscriptions(ctx, userID, o)
}
func (s *bankingService) UpdateSubscription(ctx context.Context, v *entity.Subscription) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateSubscription(v, true); err != nil {
		return err
	}
	return s.repo.UpdateSubscription(ctx, v)
}

func validateAudit(v *entity.AgentAudit) error {
	if v == nil {
		return fmt.Errorf("%w: agent audit is nil", ErrInvalidArgument)
	}
	if err := validUserID(v.UserID); err != nil {
		return err
	}
	if err := required(&v.Action, "action"); err != nil {
		return err
	}
	switch v.Outcome {
	case entity.AgentAuditOutcomeSuccess, entity.AgentAuditOutcomeFailed:
	default:
		return fmt.Errorf("%w: invalid audit outcome", ErrInvalidArgument)
	}
	v.AgentID = strings.TrimSpace(v.AgentID)
	v.WorkflowID = strings.TrimSpace(v.WorkflowID)
	v.ToolID = strings.TrimSpace(v.ToolID)
	v.RunID = strings.TrimSpace(v.RunID)
	v.TraceID = strings.TrimSpace(v.TraceID)
	var err error
	v.RequestJSON, err = cloneAuditJSON(v.RequestJSON)
	if err != nil {
		return err
	}
	v.ResponseJSON, err = cloneAuditJSON(v.ResponseJSON)
	return err
}
func (s *bankingService) CreateAgentAudit(ctx context.Context, v *entity.AgentAudit) (*entity.AgentAudit, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validateAudit(v); err != nil {
		return nil, err
	}
	return s.repo.CreateAgentAudit(ctx, v)
}
func (s *bankingService) GetAgentAudit(ctx context.Context, userID, id int64) (*entity.AgentAudit, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validTenantObject(userID, id); err != nil {
		return nil, err
	}
	return s.repo.GetAgentAudit(ctx, userID, id)
}
func (s *bankingService) ListAgentAudits(ctx context.Context, userID int64, o *entity.ListOptions) ([]*entity.AgentAudit, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := validUserID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListAgentAudits(ctx, userID, o)
}

var _ Banking = (*bankingService)(nil)
