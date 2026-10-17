package service

import (
	"context"
	"errors"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/repository"
)

var (
	ErrInvalidUserID   = errors.New("banking: invalid user id")
	ErrInvalidAmount   = errors.New("banking: invalid amount")
	ErrInvalidCurrency = errors.New("banking: invalid currency")
	ErrInvalidArgument = errors.New("banking: invalid argument")
)

// Banking is the sole domain entry point for banking use cases. It deliberately
// excludes persistence transaction controls.
type Banking interface {
	UpsertFinancialProfile(context.Context, *entity.UserFinancialProfile) (*entity.UserFinancialProfile, error)
	GetFinancialProfile(context.Context, int64) (*entity.UserFinancialProfile, error)
	CreateBankAccount(context.Context, *entity.BankAccount) (*entity.BankAccount, error)
	GetBankAccount(context.Context, int64, int64) (*entity.BankAccount, error)
	ListBankAccounts(context.Context, int64, *entity.ListOptions) ([]*entity.BankAccount, error)
	UpdateBankAccount(context.Context, *entity.BankAccount) error
	CreateTransaction(context.Context, *entity.Transaction) (*entity.Transaction, error)
	BatchCreateTransactions(context.Context, []*entity.Transaction) error
	GetTransaction(context.Context, int64, int64) (*entity.Transaction, error)
	GetTransactionByExternalID(context.Context, int64, int64, string) (*entity.Transaction, error)
	ListTransactions(context.Context, int64, *entity.ListOptions) ([]*entity.Transaction, error)
	CreateHealthBudget(context.Context, *entity.HealthBudget) (*entity.HealthBudget, error)
	GetHealthBudget(context.Context, int64, int64) (*entity.HealthBudget, error)
	ListHealthBudgets(context.Context, int64, *entity.ListOptions) ([]*entity.HealthBudget, error)
	UpdateHealthBudget(context.Context, *entity.HealthBudget) (bool, error)
	CreateFoodItem(context.Context, *entity.FoodItem) (*entity.FoodItem, error)
	GetFoodItem(context.Context, int64, int64) (*entity.FoodItem, error)
	ListFoodItems(context.Context, int64, *entity.ListOptions) ([]*entity.FoodItem, error)
	UpdateFoodItem(context.Context, *entity.FoodItem) error
	CreateFoodOrder(context.Context, *entity.FoodOrder) (*entity.FoodOrder, error)
	GetFoodOrder(context.Context, int64, int64) (*entity.FoodOrder, error)
	ListFoodOrders(context.Context, int64, *entity.ListOptions) ([]*entity.FoodOrder, error)
	UpdateFoodOrder(context.Context, *entity.FoodOrder) error
	CreatePayment(context.Context, *entity.Payment) (*entity.Payment, error)
	GetPayment(context.Context, int64, int64) (*entity.Payment, error)
	ListPayments(context.Context, int64, *entity.ListOptions) ([]*entity.Payment, error)
	UpdatePaymentRecord(context.Context, *entity.Payment) error
	CreateTransfer(context.Context, *entity.Transfer) (*entity.Transfer, error)
	GetTransfer(context.Context, int64, int64) (*entity.Transfer, error)
	ListTransfers(context.Context, int64, *entity.ListOptions) ([]*entity.Transfer, error)
	UpdateTransferRecord(context.Context, *entity.Transfer) error
	CreateSubscription(context.Context, *entity.Subscription) (*entity.Subscription, error)
	GetSubscription(context.Context, int64, int64) (*entity.Subscription, error)
	ListSubscriptions(context.Context, int64, *entity.ListOptions) ([]*entity.Subscription, error)
	UpdateSubscription(context.Context, *entity.Subscription) error
	CreateAgentAudit(context.Context, *entity.AgentAudit) (*entity.AgentAudit, error)
	GetAgentAudit(context.Context, int64, int64) (*entity.AgentAudit, error)
	ListAgentAudits(context.Context, int64, *entity.ListOptions) ([]*entity.AgentAudit, error)
}

func NewBankingService(repo repository.Repository) Banking {
	return &bankingService{repo: repo}
}
