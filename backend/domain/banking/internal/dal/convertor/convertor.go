/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package convertor

import (
	"encoding/json"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
)

func cloneBytes(source []byte) []byte {
	if source == nil {
		return nil
	}
	clone := make([]byte, len(source))
	copy(clone, source)
	return clone
}

func cloneJSON(source json.RawMessage) json.RawMessage { return json.RawMessage(cloneBytes(source)) }

func stringToPtr(value string) *string {
	if value == "" {
		return nil
	}
	clone := value
	return &clone
}

func ptrToString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func FinancialProfileToPO(source *entity.UserFinancialProfile) *model.UserFinancialProfile {
	if source == nil {
		return nil
	}
	return &model.UserFinancialProfile{ID: source.ID, UserID: source.UserID, ExternalCustomerID: stringToPtr(source.ExternalCustomerID), DefaultCurrency: source.DefaultCurrency, MonthlyIncomeMinor: source.MonthlyIncomeMinor, HealthGoal: source.HealthGoal, DietaryPreferences: cloneBytes(source.DietaryPreferences), RiskLevel: source.RiskLevel, Timezone: source.Timezone, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FinancialProfileToEntity(source *model.UserFinancialProfile) *entity.UserFinancialProfile {
	if source == nil {
		return nil
	}
	return &entity.UserFinancialProfile{ID: source.ID, UserID: source.UserID, ExternalCustomerID: ptrToString(source.ExternalCustomerID), DefaultCurrency: source.DefaultCurrency, MonthlyIncomeMinor: source.MonthlyIncomeMinor, HealthGoal: source.HealthGoal, DietaryPreferences: cloneJSON(source.DietaryPreferences), RiskLevel: source.RiskLevel, Timezone: source.Timezone, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func BankAccountToPO(source *entity.BankAccount) *model.BankAccount {
	if source == nil {
		return nil
	}
	return &model.BankAccount{ID: source.ID, UserID: source.UserID, ExternalAccountID: stringToPtr(source.ExternalAccountID), DisplayName: source.DisplayName, AccountType: source.AccountType, MaskedAccountNumber: source.MaskedAccountNumber, Currency: source.Currency, BalanceMinor: source.BalanceMinor, Status: string(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func BankAccountToEntity(source *model.BankAccount) *entity.BankAccount {
	if source == nil {
		return nil
	}
	return &entity.BankAccount{ID: source.ID, UserID: source.UserID, ExternalAccountID: ptrToString(source.ExternalAccountID), DisplayName: source.DisplayName, AccountType: source.AccountType, MaskedAccountNumber: source.MaskedAccountNumber, Currency: source.Currency, BalanceMinor: source.BalanceMinor, Status: entity.BankAccountStatus(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func TransactionToPO(source *entity.Transaction) *model.Transaction {
	if source == nil {
		return nil
	}
	return &model.Transaction{ID: source.ID, UserID: source.UserID, BankAccountID: source.BankAccountID, ExternalTransactionID: stringToPtr(source.ExternalTransactionID), Direction: string(source.Direction), AmountMinor: source.AmountMinor, Currency: source.Currency, Category: source.Category, MerchantName: source.MerchantName, Description: source.Description, OccurredAt: source.OccurredAt, SourceMetadata: cloneBytes(source.SourceMetadata), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func TransactionToEntity(source *model.Transaction) *entity.Transaction {
	if source == nil {
		return nil
	}
	return &entity.Transaction{ID: source.ID, UserID: source.UserID, BankAccountID: source.BankAccountID, ExternalTransactionID: ptrToString(source.ExternalTransactionID), Direction: entity.TransactionDirection(source.Direction), AmountMinor: source.AmountMinor, Currency: source.Currency, Category: source.Category, MerchantName: source.MerchantName, Description: source.Description, OccurredAt: source.OccurredAt, SourceMetadata: cloneJSON(source.SourceMetadata), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func HealthBudgetToPO(source *entity.HealthBudget) *model.HealthBudget {
	if source == nil {
		return nil
	}
	return &model.HealthBudget{ID: source.ID, UserID: source.UserID, PeriodStart: source.PeriodStart, PeriodEnd: source.PeriodEnd, TotalAllocatedMinor: source.TotalAllocatedMinor, FoodAllocatedMinor: source.FoodAllocatedMinor, SpentMinor: source.SpentMinor, Currency: source.Currency, Status: string(source.Status), Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func HealthBudgetToEntity(source *model.HealthBudget) *entity.HealthBudget {
	if source == nil {
		return nil
	}
	return &entity.HealthBudget{ID: source.ID, UserID: source.UserID, PeriodStart: source.PeriodStart, PeriodEnd: source.PeriodEnd, TotalAllocatedMinor: source.TotalAllocatedMinor, FoodAllocatedMinor: source.FoodAllocatedMinor, SpentMinor: source.SpentMinor, Currency: source.Currency, Status: entity.HealthBudgetStatus(source.Status), Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodItemToPO(source *entity.FoodItem) *model.FoodItem {
	if source == nil {
		return nil
	}
	return &model.FoodItem{ID: source.ID, UserID: source.UserID, Provider: source.Provider, ExternalItemID: stringToPtr(source.ExternalItemID), Name: source.Name, UnitPriceMinor: source.UnitPriceMinor, Currency: source.Currency, NutritionMetadata: cloneBytes(source.NutritionMetadata), Status: string(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodItemToEntity(source *model.FoodItem) *entity.FoodItem {
	if source == nil {
		return nil
	}
	return &entity.FoodItem{ID: source.ID, UserID: source.UserID, Provider: source.Provider, ExternalItemID: ptrToString(source.ExternalItemID), Name: source.Name, UnitPriceMinor: source.UnitPriceMinor, Currency: source.Currency, NutritionMetadata: cloneJSON(source.NutritionMetadata), Status: entity.FoodItemStatus(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodOrderToPO(source *entity.FoodOrder) *model.FoodOrder {
	if source == nil {
		return nil
	}
	var items []*model.FoodOrderItem
	if source.Items != nil {
		items = make([]*model.FoodOrderItem, len(source.Items))
		for i, item := range source.Items {
			items[i] = FoodOrderItemToPO(item)
		}
	}
	return &model.FoodOrder{ID: source.ID, UserID: source.UserID, HealthBudgetID: source.HealthBudgetID, BankAccountID: source.BankAccountID, Provider: source.Provider, MerchantName: source.MerchantName, TotalAmountMinor: source.TotalAmountMinor, Currency: source.Currency, Status: string(source.Status), ExternalOrderID: stringToPtr(source.ExternalOrderID), Items: items, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodOrderToEntity(source *model.FoodOrder) *entity.FoodOrder {
	if source == nil {
		return nil
	}
	var items []*entity.FoodOrderItem
	if source.Items != nil {
		items = make([]*entity.FoodOrderItem, len(source.Items))
		for i, item := range source.Items {
			items[i] = FoodOrderItemToEntity(item)
		}
	}
	return &entity.FoodOrder{ID: source.ID, UserID: source.UserID, HealthBudgetID: source.HealthBudgetID, BankAccountID: source.BankAccountID, Provider: source.Provider, MerchantName: source.MerchantName, TotalAmountMinor: source.TotalAmountMinor, Currency: source.Currency, Status: entity.FoodOrderStatus(source.Status), ExternalOrderID: ptrToString(source.ExternalOrderID), Items: items, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodOrderItemToPO(source *entity.FoodOrderItem) *model.FoodOrderItem {
	if source == nil {
		return nil
	}
	return &model.FoodOrderItem{ID: source.ID, UserID: source.UserID, FoodOrderID: source.FoodOrderID, FoodItemID: source.FoodItemID, Name: source.Name, Quantity: source.Quantity, UnitPriceMinor: source.UnitPriceMinor, SubtotalMinor: source.SubtotalMinor, Currency: source.Currency, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func FoodOrderItemToEntity(source *model.FoodOrderItem) *entity.FoodOrderItem {
	if source == nil {
		return nil
	}
	return &entity.FoodOrderItem{ID: source.ID, UserID: source.UserID, FoodOrderID: source.FoodOrderID, FoodItemID: source.FoodItemID, Name: source.Name, Quantity: source.Quantity, UnitPriceMinor: source.UnitPriceMinor, SubtotalMinor: source.SubtotalMinor, Currency: source.Currency, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func PaymentToPO(source *entity.Payment) *model.Payment {
	if source == nil {
		return nil
	}
	return &model.Payment{ID: source.ID, UserID: source.UserID, FoodOrderID: source.FoodOrderID, BankAccountID: source.BankAccountID, IdempotencyKey: source.IdempotencyKey, ExternalPaymentID: stringToPtr(source.ExternalPaymentID), AmountMinor: source.AmountMinor, Currency: source.Currency, Status: string(source.Status), FailureCode: source.FailureCode, FailureMessage: source.FailureMessage, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func PaymentToEntity(source *model.Payment) *entity.Payment {
	if source == nil {
		return nil
	}
	return &entity.Payment{ID: source.ID, UserID: source.UserID, FoodOrderID: source.FoodOrderID, BankAccountID: source.BankAccountID, IdempotencyKey: source.IdempotencyKey, ExternalPaymentID: ptrToString(source.ExternalPaymentID), AmountMinor: source.AmountMinor, Currency: source.Currency, Status: entity.PaymentStatus(source.Status), FailureCode: source.FailureCode, FailureMessage: source.FailureMessage, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func TransferToPO(source *entity.Transfer) *model.Transfer {
	if source == nil {
		return nil
	}
	return &model.Transfer{ID: source.ID, UserID: source.UserID, SourceBankAccountID: source.SourceBankAccountID, TargetReference: source.TargetReference, IdempotencyKey: source.IdempotencyKey, AmountMinor: source.AmountMinor, Currency: source.Currency, Status: string(source.Status), Description: source.Description, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func TransferToEntity(source *model.Transfer) *entity.Transfer {
	if source == nil {
		return nil
	}
	return &entity.Transfer{ID: source.ID, UserID: source.UserID, SourceBankAccountID: source.SourceBankAccountID, TargetReference: source.TargetReference, IdempotencyKey: source.IdempotencyKey, AmountMinor: source.AmountMinor, Currency: source.Currency, Status: entity.TransferStatus(source.Status), Description: source.Description, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func SubscriptionToPO(source *entity.Subscription) *model.Subscription {
	if source == nil {
		return nil
	}
	return &model.Subscription{ID: source.ID, UserID: source.UserID, BankAccountID: source.BankAccountID, MerchantName: source.MerchantName, ExternalSubscriptionID: stringToPtr(source.ExternalSubscriptionID), BillingInterval: source.BillingInterval, AmountMinor: source.AmountMinor, Currency: source.Currency, NextBillingAt: source.NextBillingAt, Status: string(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func SubscriptionToEntity(source *model.Subscription) *entity.Subscription {
	if source == nil {
		return nil
	}
	return &entity.Subscription{ID: source.ID, UserID: source.UserID, BankAccountID: source.BankAccountID, MerchantName: source.MerchantName, ExternalSubscriptionID: ptrToString(source.ExternalSubscriptionID), BillingInterval: source.BillingInterval, AmountMinor: source.AmountMinor, Currency: source.Currency, NextBillingAt: source.NextBillingAt, Status: entity.SubscriptionStatus(source.Status), CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func AgentAuditToPO(source *entity.AgentAudit) *model.AgentAudit {
	if source == nil {
		return nil
	}
	return &model.AgentAudit{ID: source.ID, UserID: source.UserID, AgentID: source.AgentID, WorkflowID: source.WorkflowID, ToolID: source.ToolID, RunID: source.RunID, TraceID: source.TraceID, Action: source.Action, Outcome: string(source.Outcome), RequestJSON: cloneBytes(source.RequestJSON), ResponseJSON: cloneBytes(source.ResponseJSON), ErrorCode: source.ErrorCode, ErrorMessage: source.ErrorMessage, CreatedAt: source.CreatedAt}
}

func AgentAuditToEntity(source *model.AgentAudit) *entity.AgentAudit {
	if source == nil {
		return nil
	}
	return &entity.AgentAudit{ID: source.ID, UserID: source.UserID, AgentID: source.AgentID, WorkflowID: source.WorkflowID, ToolID: source.ToolID, RunID: source.RunID, TraceID: source.TraceID, Action: source.Action, Outcome: entity.AgentAuditOutcome(source.Outcome), RequestJSON: cloneJSON(source.RequestJSON), ResponseJSON: cloneJSON(source.ResponseJSON), ErrorCode: source.ErrorCode, ErrorMessage: source.ErrorMessage, CreatedAt: source.CreatedAt}
}
