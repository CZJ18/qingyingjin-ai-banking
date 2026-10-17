/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package model

type UserFinancialProfile struct {
	ID                 int64   `gorm:"column:id;primaryKey"`
	UserID             int64   `gorm:"column:user_id"`
	ExternalCustomerID *string `gorm:"column:external_customer_id"`
	DefaultCurrency    string  `gorm:"column:default_currency"`
	MonthlyIncomeMinor int64   `gorm:"column:monthly_income_minor"`
	HealthGoal         string  `gorm:"column:health_goal"`
	DietaryPreferences []byte  `gorm:"column:dietary_preferences;type:json"`
	RiskLevel          string  `gorm:"column:risk_level"`
	Timezone           string  `gorm:"column:timezone"`
	CreatedAt          int64   `gorm:"column:created_at"`
	UpdatedAt          int64   `gorm:"column:updated_at"`
}

func (*UserFinancialProfile) TableName() string { return "banking_financial_profiles" }

type BankAccount struct {
	ID                  int64   `gorm:"column:id;primaryKey"`
	UserID              int64   `gorm:"column:user_id"`
	ExternalAccountID   *string `gorm:"column:external_account_id"`
	DisplayName         string  `gorm:"column:display_name"`
	AccountType         string  `gorm:"column:account_type"`
	MaskedAccountNumber string  `gorm:"column:masked_account_number"`
	Currency            string  `gorm:"column:currency"`
	BalanceMinor        int64   `gorm:"column:balance_minor"`
	Status              string  `gorm:"column:status"`
	CreatedAt           int64   `gorm:"column:created_at"`
	UpdatedAt           int64   `gorm:"column:updated_at"`
}

func (*BankAccount) TableName() string { return "banking_bank_accounts" }

type Transaction struct {
	ID                    int64   `gorm:"column:id;primaryKey"`
	UserID                int64   `gorm:"column:user_id"`
	BankAccountID         int64   `gorm:"column:bank_account_id"`
	ExternalTransactionID *string `gorm:"column:external_transaction_id"`
	Direction             string  `gorm:"column:direction"`
	AmountMinor           int64   `gorm:"column:amount_minor"`
	Currency              string  `gorm:"column:currency"`
	Category              string  `gorm:"column:category"`
	MerchantName          string  `gorm:"column:merchant_name"`
	Description           string  `gorm:"column:description"`
	OccurredAt            int64   `gorm:"column:occurred_at"`
	SourceMetadata        []byte  `gorm:"column:source_metadata;type:json"`
	CreatedAt             int64   `gorm:"column:created_at"`
	UpdatedAt             int64   `gorm:"column:updated_at"`
}

func (*Transaction) TableName() string { return "banking_transactions" }

type HealthBudget struct {
	ID                  int64  `gorm:"column:id;primaryKey"`
	UserID              int64  `gorm:"column:user_id"`
	PeriodStart         int64  `gorm:"column:period_start"`
	PeriodEnd           int64  `gorm:"column:period_end"`
	TotalAllocatedMinor int64  `gorm:"column:total_allocated_minor"`
	FoodAllocatedMinor  int64  `gorm:"column:food_allocated_minor"`
	SpentMinor          int64  `gorm:"column:spent_minor"`
	Currency            string `gorm:"column:currency"`
	Status              string `gorm:"column:status"`
	Version             int64  `gorm:"column:version"`
	CreatedAt           int64  `gorm:"column:created_at"`
	UpdatedAt           int64  `gorm:"column:updated_at"`
}

func (*HealthBudget) TableName() string { return "banking_health_budgets" }

type FoodItem struct {
	ID                int64   `gorm:"column:id;primaryKey"`
	UserID            int64   `gorm:"column:user_id"`
	Provider          string  `gorm:"column:provider"`
	ExternalItemID    *string `gorm:"column:external_item_id"`
	Name              string  `gorm:"column:name"`
	UnitPriceMinor    int64   `gorm:"column:unit_price_minor"`
	Currency          string  `gorm:"column:currency"`
	NutritionMetadata []byte  `gorm:"column:nutrition_metadata;type:json"`
	Status            string  `gorm:"column:status"`
	CreatedAt         int64   `gorm:"column:created_at"`
	UpdatedAt         int64   `gorm:"column:updated_at"`
}

func (*FoodItem) TableName() string { return "banking_food_items" }

type FoodOrder struct {
	ID               int64   `gorm:"column:id;primaryKey"`
	UserID           int64   `gorm:"column:user_id"`
	HealthBudgetID   int64   `gorm:"column:health_budget_id"`
	BankAccountID    int64   `gorm:"column:bank_account_id"`
	Provider         string  `gorm:"column:provider"`
	MerchantName     string  `gorm:"column:merchant_name"`
	TotalAmountMinor int64   `gorm:"column:total_amount_minor"`
	Currency         string  `gorm:"column:currency"`
	Status           string  `gorm:"column:status"`
	ExternalOrderID  *string `gorm:"column:external_order_id"`
	// Items is a temporary aggregate carrier for conversion only. GORM must
	// ignore it: DAOs persist items explicitly in the order transaction and
	// load them with a second query while enforcing matching UserID and
	// FoodOrderID values.
	Items     []*FoodOrderItem `gorm:"-"`
	CreatedAt int64            `gorm:"column:created_at"`
	UpdatedAt int64            `gorm:"column:updated_at"`
}

func (*FoodOrder) TableName() string { return "banking_food_orders" }

type FoodOrderItem struct {
	ID             int64  `gorm:"column:id;primaryKey"`
	UserID         int64  `gorm:"column:user_id"`
	FoodOrderID    int64  `gorm:"column:food_order_id"`
	FoodItemID     int64  `gorm:"column:food_item_id"`
	Name           string `gorm:"column:name"`
	Quantity       int64  `gorm:"column:quantity"`
	UnitPriceMinor int64  `gorm:"column:unit_price_minor"`
	SubtotalMinor  int64  `gorm:"column:subtotal_minor"`
	Currency       string `gorm:"column:currency"`
	CreatedAt      int64  `gorm:"column:created_at"`
	UpdatedAt      int64  `gorm:"column:updated_at"`
}

func (*FoodOrderItem) TableName() string { return "banking_food_order_items" }

type Payment struct {
	ID                int64   `gorm:"column:id;primaryKey"`
	UserID            int64   `gorm:"column:user_id"`
	FoodOrderID       int64   `gorm:"column:food_order_id"`
	BankAccountID     int64   `gorm:"column:bank_account_id"`
	IdempotencyKey    string  `gorm:"column:idempotency_key"`
	ExternalPaymentID *string `gorm:"column:external_payment_id"`
	AmountMinor       int64   `gorm:"column:amount_minor"`
	Currency          string  `gorm:"column:currency"`
	Status            string  `gorm:"column:status"`
	FailureCode       string  `gorm:"column:failure_code"`
	FailureMessage    string  `gorm:"column:failure_message"`
	CreatedAt         int64   `gorm:"column:created_at"`
	UpdatedAt         int64   `gorm:"column:updated_at"`
}

func (*Payment) TableName() string { return "banking_payments" }

type Transfer struct {
	ID                  int64  `gorm:"column:id;primaryKey"`
	UserID              int64  `gorm:"column:user_id"`
	SourceBankAccountID int64  `gorm:"column:source_bank_account_id"`
	TargetReference     string `gorm:"column:target_reference"`
	IdempotencyKey      string `gorm:"column:idempotency_key"`
	AmountMinor         int64  `gorm:"column:amount_minor"`
	Currency            string `gorm:"column:currency"`
	Status              string `gorm:"column:status"`
	Description         string `gorm:"column:description"`
	CreatedAt           int64  `gorm:"column:created_at"`
	UpdatedAt           int64  `gorm:"column:updated_at"`
}

func (*Transfer) TableName() string { return "banking_transfers" }

type Subscription struct {
	ID                     int64   `gorm:"column:id;primaryKey"`
	UserID                 int64   `gorm:"column:user_id"`
	BankAccountID          int64   `gorm:"column:bank_account_id"`
	MerchantName           string  `gorm:"column:merchant_name"`
	ExternalSubscriptionID *string `gorm:"column:external_subscription_id"`
	BillingInterval        string  `gorm:"column:billing_interval"`
	AmountMinor            int64   `gorm:"column:amount_minor"`
	Currency               string  `gorm:"column:currency"`
	NextBillingAt          int64   `gorm:"column:next_billing_at"`
	Status                 string  `gorm:"column:status"`
	CreatedAt              int64   `gorm:"column:created_at"`
	UpdatedAt              int64   `gorm:"column:updated_at"`
}

func (*Subscription) TableName() string { return "banking_subscriptions" }

type AgentAudit struct {
	ID           int64  `gorm:"column:id;primaryKey"`
	UserID       int64  `gorm:"column:user_id"`
	AgentID      string `gorm:"column:agent_id"`
	WorkflowID   string `gorm:"column:workflow_id"`
	ToolID       string `gorm:"column:tool_id"`
	RunID        string `gorm:"column:run_id"`
	TraceID      string `gorm:"column:trace_id"`
	Action       string `gorm:"column:action"`
	Outcome      string `gorm:"column:outcome"`
	RequestJSON  []byte `gorm:"column:request_json;type:json"`
	ResponseJSON []byte `gorm:"column:response_json;type:json"`
	ErrorCode    string `gorm:"column:error_code"`
	ErrorMessage string `gorm:"column:error_message"`
	CreatedAt    int64  `gorm:"column:created_at"`
}

func (*AgentAudit) TableName() string { return "banking_agent_audits" }
