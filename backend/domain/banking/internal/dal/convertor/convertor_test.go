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
	"reflect"
	"testing"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
)

func TestEntityPORoundTrips(t *testing.T) {
	tests := []struct {
		name      string
		roundTrip func() (any, any)
	}{
		{"financial_profile", func() (any, any) {
			in := &entity.UserFinancialProfile{ID: 1, UserID: 2, ExternalCustomerID: "cus_3", DefaultCurrency: "CNY", MonthlyIncomeMinor: 400, HealthGoal: "fit", DietaryPreferences: json.RawMessage(`{"diet":"low salt"}`), RiskLevel: "low", Timezone: "Asia/Shanghai", CreatedAt: 5, UpdatedAt: 6}
			return in, FinancialProfileToEntity(FinancialProfileToPO(in))
		}},
		{"bank_account", func() (any, any) {
			in := &entity.BankAccount{ID: 1, UserID: 2, ExternalAccountID: "acct_3", DisplayName: "daily", AccountType: "debit", MaskedAccountNumber: "****1234", Currency: "CNY", BalanceMinor: 400, Status: entity.BankAccountStatusActive, CreatedAt: 5, UpdatedAt: 6}
			return in, BankAccountToEntity(BankAccountToPO(in))
		}},
		{"transaction", func() (any, any) {
			in := &entity.Transaction{ID: 1, UserID: 2, BankAccountID: 3, ExternalTransactionID: "txn_4", Direction: entity.TransactionDirectionExpense, AmountMinor: 500, Currency: "CNY", Category: "food", MerchantName: "shop", Description: "lunch", OccurredAt: 6, SourceMetadata: json.RawMessage(`{"source":"api"}`), CreatedAt: 7, UpdatedAt: 8}
			return in, TransactionToEntity(TransactionToPO(in))
		}},
		{"health_budget", func() (any, any) {
			in := &entity.HealthBudget{ID: 1, UserID: 2, PeriodStart: 3, PeriodEnd: 4, TotalAllocatedMinor: 500, FoodAllocatedMinor: 400, SpentMinor: 100, Currency: "CNY", Status: entity.HealthBudgetStatusActive, Version: 6, CreatedAt: 7, UpdatedAt: 8}
			return in, HealthBudgetToEntity(HealthBudgetToPO(in))
		}},
		{"food_item", func() (any, any) {
			in := &entity.FoodItem{ID: 1, UserID: 2, Provider: "provider", ExternalItemID: "item_3", Name: "apple", UnitPriceMinor: 400, Currency: "CNY", NutritionMetadata: json.RawMessage(`{"kcal":52}`), Status: entity.FoodItemStatusActive, CreatedAt: 5, UpdatedAt: 6}
			return in, FoodItemToEntity(FoodItemToPO(in))
		}},
		{"food_order", func() (any, any) {
			in := &entity.FoodOrder{ID: 1, UserID: 2, HealthBudgetID: 3, BankAccountID: 4, Provider: "provider", MerchantName: "market", TotalAmountMinor: 500, Currency: "CNY", Status: entity.FoodOrderStatusConfirmed, ExternalOrderID: "order_6", Items: []*entity.FoodOrderItem{{ID: 7, UserID: 2, FoodOrderID: 1, FoodItemID: 8, Name: "apple", Quantity: 2, UnitPriceMinor: 100, SubtotalMinor: 200, Currency: "CNY", CreatedAt: 9, UpdatedAt: 10}, {ID: 11, UserID: 2, FoodOrderID: 1, FoodItemID: 12, Name: "milk", Quantity: 3, UnitPriceMinor: 100, SubtotalMinor: 300, Currency: "CNY", CreatedAt: 13, UpdatedAt: 14}}, CreatedAt: 15, UpdatedAt: 16}
			return in, FoodOrderToEntity(FoodOrderToPO(in))
		}},
		{"payment", func() (any, any) {
			in := &entity.Payment{ID: 1, UserID: 2, FoodOrderID: 3, BankAccountID: 4, IdempotencyKey: "idem", ExternalPaymentID: "pay_5", AmountMinor: 600, Currency: "CNY", Status: entity.PaymentStatusSucceeded, FailureCode: "none", FailureMessage: "none", CreatedAt: 7, UpdatedAt: 8}
			return in, PaymentToEntity(PaymentToPO(in))
		}},
		{"transfer", func() (any, any) {
			in := &entity.Transfer{ID: 1, UserID: 2, SourceBankAccountID: 3, TargetReference: "target", IdempotencyKey: "idem", AmountMinor: 400, Currency: "CNY", Status: entity.TransferStatusSucceeded, Description: "save", CreatedAt: 5, UpdatedAt: 6}
			return in, TransferToEntity(TransferToPO(in))
		}},
		{"subscription", func() (any, any) {
			in := &entity.Subscription{ID: 1, UserID: 2, BankAccountID: 3, MerchantName: "gym", ExternalSubscriptionID: "sub_4", BillingInterval: "monthly", AmountMinor: 500, Currency: "CNY", NextBillingAt: 6, Status: entity.SubscriptionStatusActive, CreatedAt: 7, UpdatedAt: 8}
			return in, SubscriptionToEntity(SubscriptionToPO(in))
		}},
		{"agent_audit", func() (any, any) {
			in := &entity.AgentAudit{ID: 1, UserID: 2, AgentID: "agent", WorkflowID: "workflow", ToolID: "tool", RunID: "run", TraceID: "trace", Action: "pay", Outcome: entity.AgentAuditOutcomeSuccess, RequestJSON: json.RawMessage(`{"request":1}`), ResponseJSON: json.RawMessage(`{"response":2}`), ErrorCode: "none", ErrorMessage: "none", CreatedAt: 3}
			return in, AgentAuditToEntity(AgentAuditToPO(in))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, got := tt.roundTrip()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip mismatch\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

func TestExternalIDsUseNullablePOFields(t *testing.T) {
	tests := []struct {
		name              string
		empty             func() *string
		nonEmpty          func() *string
		fromNil           func() string
		normalizeEmptyPtr func(*string) *string
	}{
		{"ExternalCustomerID", func() *string { return FinancialProfileToPO(&entity.UserFinancialProfile{}).ExternalCustomerID }, func() *string {
			return FinancialProfileToPO(&entity.UserFinancialProfile{ExternalCustomerID: " customer "}).ExternalCustomerID
		}, func() string {
			return FinancialProfileToEntity(&model.UserFinancialProfile{ExternalCustomerID: nil}).ExternalCustomerID
		}, func(empty *string) *string {
			return FinancialProfileToPO(FinancialProfileToEntity(&model.UserFinancialProfile{ExternalCustomerID: empty})).ExternalCustomerID
		}},
		{"ExternalAccountID", func() *string { return BankAccountToPO(&entity.BankAccount{}).ExternalAccountID }, func() *string {
			return BankAccountToPO(&entity.BankAccount{ExternalAccountID: " account "}).ExternalAccountID
		}, func() string {
			return BankAccountToEntity(&model.BankAccount{ExternalAccountID: nil}).ExternalAccountID
		}, func(empty *string) *string {
			return BankAccountToPO(BankAccountToEntity(&model.BankAccount{ExternalAccountID: empty})).ExternalAccountID
		}},
		{"ExternalTransactionID", func() *string { return TransactionToPO(&entity.Transaction{}).ExternalTransactionID }, func() *string {
			return TransactionToPO(&entity.Transaction{ExternalTransactionID: " transaction "}).ExternalTransactionID
		}, func() string {
			return TransactionToEntity(&model.Transaction{ExternalTransactionID: nil}).ExternalTransactionID
		}, func(empty *string) *string {
			return TransactionToPO(TransactionToEntity(&model.Transaction{ExternalTransactionID: empty})).ExternalTransactionID
		}},
		{"ExternalOrderID", func() *string { return FoodOrderToPO(&entity.FoodOrder{}).ExternalOrderID }, func() *string { return FoodOrderToPO(&entity.FoodOrder{ExternalOrderID: " order "}).ExternalOrderID }, func() string { return FoodOrderToEntity(&model.FoodOrder{ExternalOrderID: nil}).ExternalOrderID }, func(empty *string) *string {
			return FoodOrderToPO(FoodOrderToEntity(&model.FoodOrder{ExternalOrderID: empty})).ExternalOrderID
		}},
		{"ExternalPaymentID", func() *string { return PaymentToPO(&entity.Payment{}).ExternalPaymentID }, func() *string { return PaymentToPO(&entity.Payment{ExternalPaymentID: " payment "}).ExternalPaymentID }, func() string { return PaymentToEntity(&model.Payment{ExternalPaymentID: nil}).ExternalPaymentID }, func(empty *string) *string {
			return PaymentToPO(PaymentToEntity(&model.Payment{ExternalPaymentID: empty})).ExternalPaymentID
		}},
		{"ExternalSubscriptionID", func() *string { return SubscriptionToPO(&entity.Subscription{}).ExternalSubscriptionID }, func() *string {
			return SubscriptionToPO(&entity.Subscription{ExternalSubscriptionID: " subscription "}).ExternalSubscriptionID
		}, func() string {
			return SubscriptionToEntity(&model.Subscription{ExternalSubscriptionID: nil}).ExternalSubscriptionID
		}, func(empty *string) *string {
			return SubscriptionToPO(SubscriptionToEntity(&model.Subscription{ExternalSubscriptionID: empty})).ExternalSubscriptionID
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.empty(); got != nil {
				t.Fatalf("empty ID pointer = %q, want nil", *got)
			}
			if got := tt.nonEmpty(); got == nil || *got != " "+map[string]string{"ExternalCustomerID": "customer", "ExternalAccountID": "account", "ExternalTransactionID": "transaction", "ExternalOrderID": "order", "ExternalPaymentID": "payment", "ExternalSubscriptionID": "subscription"}[tt.name]+" " {
				t.Fatalf("non-empty ID was changed or nil: %#v", got)
			}
			if got := tt.fromNil(); got != "" {
				t.Fatalf("nil pointer converted to %q, want empty", got)
			}
			empty := ""
			if got := tt.normalizeEmptyPtr(&empty); got != nil {
				t.Fatalf("empty PO pointer normalized to %#v, want nil", got)
			}
		})
	}
}

func TestConversionsDefensivelyCopyJSONAndItems(t *testing.T) {
	profile := &entity.UserFinancialProfile{DietaryPreferences: json.RawMessage(`{"a":1}`)}
	profilePO := FinancialProfileToPO(profile)
	profile.DietaryPreferences[2] = 'z'
	if profilePO.DietaryPreferences[2] == 'z' {
		t.Fatal("entity to PO JSON shares backing storage")
	}
	profileEntity := FinancialProfileToEntity(profilePO)
	profilePO.DietaryPreferences[2] = 'y'
	if profileEntity.DietaryPreferences[2] == 'y' {
		t.Fatal("PO to entity JSON shares backing storage")
	}

	transactionPO := TransactionToPO(&entity.Transaction{SourceMetadata: json.RawMessage(`{"b":2}`)})
	transactionEntity := TransactionToEntity(transactionPO)
	transactionPO.SourceMetadata[2] = 'x'
	if transactionEntity.SourceMetadata[2] == 'x' {
		t.Fatal("transaction JSON shares backing storage")
	}

	foodItem := &entity.FoodItem{NutritionMetadata: json.RawMessage(`{"kcal":52}`)}
	foodItemPO := FoodItemToPO(foodItem)
	foodItem.NutritionMetadata[2] = 'x'
	if foodItemPO.NutritionMetadata[2] == 'x' {
		t.Fatal("entity to PO nutrition JSON shares backing storage")
	}
	foodItemEntity := FoodItemToEntity(foodItemPO)
	foodItemPO.NutritionMetadata[2] = 'y'
	if foodItemEntity.NutritionMetadata[2] == 'y' {
		t.Fatal("PO to entity nutrition JSON shares backing storage")
	}

	audit := &entity.AgentAudit{RequestJSON: json.RawMessage(`{"r":1}`), ResponseJSON: json.RawMessage(`{"s":2}`)}
	auditPO := AgentAuditToPO(audit)
	audit.RequestJSON[2], audit.ResponseJSON[2] = 'x', 'y'
	if auditPO.RequestJSON[2] == 'x' || auditPO.ResponseJSON[2] == 'y' {
		t.Fatal("audit JSON shares backing storage")
	}

	order := &entity.FoodOrder{Items: []*entity.FoodOrderItem{{Name: "first"}, nil, {Name: "second"}}}
	orderPO := FoodOrderToPO(order)
	order.Items[0].Name = "changed"
	order.Items[2] = &entity.FoodOrderItem{Name: "replaced"}
	if orderPO.Items[0].Name != "first" || orderPO.Items[2].Name != "second" || orderPO.Items[1] != nil {
		t.Fatal("entity to PO items were not deeply copied")
	}
	orderEntity := FoodOrderToEntity(orderPO)
	orderPO.Items[0].Name = "changed again"
	orderPO.Items[2] = &model.FoodOrderItem{Name: "replaced again"}
	if orderEntity.Items[0].Name != "first" || orderEntity.Items[2].Name != "second" || orderEntity.Items[1] != nil {
		t.Fatal("PO to entity items were not deeply copied")
	}
}

func TestNilInputsReturnNil(t *testing.T) {
	checks := []any{FinancialProfileToPO(nil), FinancialProfileToEntity(nil), BankAccountToPO(nil), BankAccountToEntity(nil), TransactionToPO(nil), TransactionToEntity(nil), HealthBudgetToPO(nil), HealthBudgetToEntity(nil), FoodItemToPO(nil), FoodItemToEntity(nil), FoodOrderToPO(nil), FoodOrderToEntity(nil), PaymentToPO(nil), PaymentToEntity(nil), TransferToPO(nil), TransferToEntity(nil), SubscriptionToPO(nil), SubscriptionToEntity(nil), AgentAuditToPO(nil), AgentAuditToEntity(nil)}
	for i, got := range checks {
		if !isNil(got) {
			t.Fatalf("check %d = %#v, want nil", i, got)
		}
	}
}

func isNil(v any) bool { return v == nil || reflect.ValueOf(v).IsNil() }
