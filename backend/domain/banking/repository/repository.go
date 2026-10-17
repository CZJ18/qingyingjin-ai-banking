/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package repository

import (
	"context"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
)

// Repository tenant contract:
//   - Create methods must validate that the entity UserID owns every referenced
//     resource. Callers cannot bypass tenant ownership checks by supplying IDs.
//   - Update methods, including the update branch of an upsert, must use both
//     entity.UserID and entity.ID in the persistence predicate. They must never
//     update the stored ID or UserID columns.
//
// Implementations must enforce these rules even when a service has already
// validated the request.

// FinancialProfileRepository stores the tenant's financial profile.
type FinancialProfileRepository interface {
	// UpsertFinancialProfile applies the repository tenant contract. Its update
	// branch must not change the persisted ID or UserID.
	UpsertFinancialProfile(ctx context.Context, profile *entity.UserFinancialProfile) (*entity.UserFinancialProfile, error)
	GetFinancialProfile(ctx context.Context, userID int64) (*entity.UserFinancialProfile, error)
}

// BankAccountRepository stores tenant-owned bank accounts.
type BankAccountRepository interface {
	CreateBankAccount(ctx context.Context, account *entity.BankAccount) (*entity.BankAccount, error)
	GetBankAccount(ctx context.Context, userID, id int64) (*entity.BankAccount, error)
	ListBankAccounts(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.BankAccount, error)
	// UpdateBankAccount predicates by account.UserID and account.ID and does not
	// update either identity column.
	UpdateBankAccount(ctx context.Context, account *entity.BankAccount) error
}

// TransactionRepository stores tenant-owned financial transactions.
type TransactionRepository interface {
	CreateTransaction(ctx context.Context, transaction *entity.Transaction) (*entity.Transaction, error)
	// BatchCreateTransactions rejects batches containing more than one UserID.
	// Ownership of every referenced account must also be validated.
	BatchCreateTransactions(ctx context.Context, transactions []*entity.Transaction) error
	GetTransaction(ctx context.Context, userID, id int64) (*entity.Transaction, error)
	GetTransactionByExternalID(ctx context.Context, userID, bankAccountID int64, externalID string) (*entity.Transaction, error)
	ListTransactions(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Transaction, error)
}

// HealthBudgetRepository stores tenant-owned health budgets.
type HealthBudgetRepository interface {
	CreateHealthBudget(ctx context.Context, budget *entity.HealthBudget) (*entity.HealthBudget, error)
	GetHealthBudget(ctx context.Context, userID, id int64) (*entity.HealthBudget, error)
	ListHealthBudgets(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.HealthBudget, error)
	// UpdateHealthBudget predicates by budget.UserID and budget.ID and does not
	// update either identity column.
	UpdateHealthBudget(ctx context.Context, budget *entity.HealthBudget) (bool, error)
}

// FoodRepository stores tenant-owned food catalog entries and order aggregates.
type FoodRepository interface {
	CreateFoodItem(ctx context.Context, item *entity.FoodItem) (*entity.FoodItem, error)
	GetFoodItem(ctx context.Context, userID, id int64) (*entity.FoodItem, error)
	ListFoodItems(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.FoodItem, error)
	// UpdateFoodItem predicates by item.UserID and item.ID and does not update
	// either identity column.
	UpdateFoodItem(ctx context.Context, item *entity.FoodItem) error
	CreateFoodOrder(ctx context.Context, order *entity.FoodOrder) (*entity.FoodOrder, error)
	GetFoodOrder(ctx context.Context, userID, id int64) (*entity.FoodOrder, error)
	ListFoodOrders(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.FoodOrder, error)
	// UpdateFoodOrder predicates by order.UserID and order.ID and does not update
	// either identity column. Item persistence must share the parent transaction
	// and enforce matching UserID and FoodOrderID values.
	UpdateFoodOrder(ctx context.Context, order *entity.FoodOrder) error
}

// PaymentRepository stores tenant-owned payment records.
type PaymentRepository interface {
	CreatePayment(ctx context.Context, payment *entity.Payment) (*entity.Payment, error)
	GetPayment(ctx context.Context, userID, id int64) (*entity.Payment, error)
	ListPayments(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Payment, error)
	// UpdatePaymentRecord predicates by payment.UserID and payment.ID and does
	// not update either identity column.
	UpdatePaymentRecord(ctx context.Context, payment *entity.Payment) error
}

// TransferRepository stores tenant-owned transfer records.
type TransferRepository interface {
	CreateTransfer(ctx context.Context, transfer *entity.Transfer) (*entity.Transfer, error)
	GetTransfer(ctx context.Context, userID, id int64) (*entity.Transfer, error)
	ListTransfers(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Transfer, error)
	// UpdateTransferRecord predicates by transfer.UserID and transfer.ID and
	// does not update either identity column.
	UpdateTransferRecord(ctx context.Context, transfer *entity.Transfer) error
}

// SubscriptionRepository stores tenant-owned subscription records.
type SubscriptionRepository interface {
	CreateSubscription(ctx context.Context, subscription *entity.Subscription) (*entity.Subscription, error)
	GetSubscription(ctx context.Context, userID, id int64) (*entity.Subscription, error)
	ListSubscriptions(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.Subscription, error)
	// UpdateSubscription predicates by subscription.UserID and subscription.ID
	// and does not update either identity column.
	UpdateSubscription(ctx context.Context, subscription *entity.Subscription) error
}

// AgentAuditRepository stores tenant-owned agent audit records.
type AgentAuditRepository interface {
	CreateAgentAudit(ctx context.Context, audit *entity.AgentAudit) (*entity.AgentAudit, error)
	GetAgentAudit(ctx context.Context, userID, id int64) (*entity.AgentAudit, error)
	ListAgentAudits(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.AgentAudit, error)
}

// TransactionManager runs fn atomically using the transaction-scoped context.
type TransactionManager interface {
	Transaction(ctx context.Context, fn func(context.Context) error) error
}

// Repository is the complete banking persistence contract.
type Repository interface {
	FinancialProfileRepository
	BankAccountRepository
	TransactionRepository
	HealthBudgetRepository
	FoodRepository
	PaymentRepository
	TransferRepository
	SubscriptionRepository
	AgentAuditRepository
	TransactionManager
}
