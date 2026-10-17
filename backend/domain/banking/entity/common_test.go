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

package entity

import (
	"math"
	"testing"
)

func TestMoneyYuanString(t *testing.T) {
	tests := []struct {
		name        string
		amountMinor int64
		want        string
	}{
		{name: "zero", amountMinor: 0, want: "0.00"},
		{name: "one cent", amountMinor: 1, want: "0.01"},
		{name: "one yuan and five cents", amountMinor: 105, want: "1.05"},
		{name: "whole yuan", amountMinor: 1200, want: "12.00"},
		{name: "negative", amountMinor: -105, want: "-1.05"},
		{name: "minimum int64", amountMinor: math.MinInt64, want: "-92233720368547758.08"},
		{name: "maximum int64", amountMinor: math.MaxInt64, want: "92233720368547758.07"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money := Money{AmountMinor: tt.amountMinor, Currency: DefaultCurrency}
			if got := money.YuanString(); got != tt.want {
				t.Fatalf("YuanString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMoneyNewMoneyNormalizesCurrency(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		want     string
	}{
		{name: "empty defaults to CNY", currency: "", want: "CNY"},
		{name: "trims whitespace", currency: " cny ", want: "CNY"},
		{name: "uppercases currency", currency: "usd", want: "USD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(105, tt.currency)
			if err != nil {
				t.Fatalf("NewMoney() error = %v", err)
			}
			if money.AmountMinor != 105 {
				t.Fatalf("NewMoney() AmountMinor = %d, want 105", money.AmountMinor)
			}
			if money.Currency != tt.want {
				t.Fatalf("NewMoney() Currency = %q, want %q", money.Currency, tt.want)
			}
		})
	}
}

func TestMoneyNormalizeCurrency(t *testing.T) {
	tests := []struct {
		currency string
		want     string
	}{
		{currency: "", want: "CNY"},
		{currency: " cny ", want: "CNY"},
		{currency: "usd", want: "USD"},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			got, err := NormalizeCurrency(tt.currency)
			if err != nil {
				t.Fatalf("NormalizeCurrency() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeCurrency() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMoneyNewMoneyRejectsNegativeAmount(t *testing.T) {
	if _, err := NewMoney(-1, DefaultCurrency); err == nil {
		t.Fatal("NewMoney() error = nil, want an error for a negative amount")
	}
}

func TestMoneyNewMoneyRejectsInvalidCurrency(t *testing.T) {
	invalidCurrencies := []string{"CN", "CNY1", "CN1", "C-N", "人民币"}
	for _, currency := range invalidCurrencies {
		t.Run(currency, func(t *testing.T) {
			if _, err := NewMoney(1, currency); err == nil {
				t.Fatalf("NewMoney() error = nil, want an error for currency %q", currency)
			}
		})
	}
}

func TestNormalizeListOptions(t *testing.T) {
	tests := []struct {
		name    string
		options *ListOptions
		want    ListOptions
	}{
		{name: "nil defaults", options: nil, want: ListOptions{Limit: 50, Offset: 0}},
		{name: "negative limit defaults", options: &ListOptions{Limit: -1, Offset: 7}, want: ListOptions{Limit: 50, Offset: 7}},
		{name: "zero limit defaults", options: &ListOptions{Limit: 0, Offset: 8}, want: ListOptions{Limit: 50, Offset: 8}},
		{name: "minimum positive limit remains", options: &ListOptions{Limit: 1, Offset: 9}, want: ListOptions{Limit: 1, Offset: 9}},
		{name: "maximum limit remains", options: &ListOptions{Limit: 100, Offset: 10}, want: ListOptions{Limit: 100, Offset: 10}},
		{name: "limit above maximum is clamped", options: &ListOptions{Limit: 101, Offset: -1}, want: ListOptions{Limit: 100, Offset: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeListOptions(tt.options)
			if got != tt.want {
				t.Fatalf("NormalizeListOptions() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHealthBudgetAvailableMinor(t *testing.T) {
	tests := []struct {
		name   string
		budget HealthBudget
		want   int64
	}{
		{
			name:   "returns unspent food allocation",
			budget: HealthBudget{FoodAllocatedMinor: 10000, SpentMinor: 2501},
			want:   7499,
		},
		{
			name:   "clamps an overspent budget to zero",
			budget: HealthBudget{FoodAllocatedMinor: 100, SpentMinor: 101},
			want:   0,
		},
		{
			name:   "zero allocation and zero spending",
			budget: HealthBudget{FoodAllocatedMinor: 0, SpentMinor: 0},
			want:   0,
		},
		{
			name:   "equal spending exhausts allocation",
			budget: HealthBudget{FoodAllocatedMinor: 100, SpentMinor: 100},
			want:   0,
		},
		{
			name:   "maximum allocation with no spending",
			budget: HealthBudget{FoodAllocatedMinor: math.MaxInt64, SpentMinor: 0},
			want:   math.MaxInt64,
		},
		{
			name:   "maximum allocation fully spent",
			budget: HealthBudget{FoodAllocatedMinor: math.MaxInt64, SpentMinor: math.MaxInt64},
			want:   0,
		},
		{
			name:   "negative allocation remains unavailable without overflow",
			budget: HealthBudget{FoodAllocatedMinor: math.MinInt64, SpentMinor: math.MaxInt64},
			want:   0,
		},
		{
			name:   "negative spending cannot expand allocation",
			budget: HealthBudget{FoodAllocatedMinor: 100, SpentMinor: -1},
			want:   100,
		},
		{
			name:   "minimum spending cannot overflow maximum allocation",
			budget: HealthBudget{FoodAllocatedMinor: math.MaxInt64, SpentMinor: math.MinInt64},
			want:   math.MaxInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.budget.AvailableMinor(); got != tt.want {
				t.Fatalf("AvailableMinor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBankingStatusValues(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "bank account active", got: string(BankAccountStatusActive), want: "active"},
		{name: "bank account inactive", got: string(BankAccountStatusInactive), want: "inactive"},
		{name: "bank account closed", got: string(BankAccountStatusClosed), want: "closed"},
		{name: "transaction income", got: string(TransactionDirectionIncome), want: "income"},
		{name: "transaction expense", got: string(TransactionDirectionExpense), want: "expense"},
		{name: "health budget active", got: string(HealthBudgetStatusActive), want: "active"},
		{name: "health budget exhausted", got: string(HealthBudgetStatusExhausted), want: "exhausted"},
		{name: "health budget closed", got: string(HealthBudgetStatusClosed), want: "closed"},
		{name: "food item active", got: string(FoodItemStatusActive), want: "active"},
		{name: "food item inactive", got: string(FoodItemStatusInactive), want: "inactive"},
		{name: "food order draft", got: string(FoodOrderStatusDraft), want: "draft"},
		{name: "food order confirmed", got: string(FoodOrderStatusConfirmed), want: "confirmed"},
		{name: "food order cancelled", got: string(FoodOrderStatusCancelled), want: "cancelled"},
		{name: "food order completed", got: string(FoodOrderStatusCompleted), want: "completed"},
		{name: "payment created", got: string(PaymentStatusCreated), want: "created"},
		{name: "payment succeeded", got: string(PaymentStatusSucceeded), want: "succeeded"},
		{name: "payment failed", got: string(PaymentStatusFailed), want: "failed"},
		{name: "payment cancelled", got: string(PaymentStatusCancelled), want: "cancelled"},
		{name: "transfer created", got: string(TransferStatusCreated), want: "created"},
		{name: "transfer succeeded", got: string(TransferStatusSucceeded), want: "succeeded"},
		{name: "transfer failed", got: string(TransferStatusFailed), want: "failed"},
		{name: "transfer cancelled", got: string(TransferStatusCancelled), want: "cancelled"},
		{name: "subscription active", got: string(SubscriptionStatusActive), want: "active"},
		{name: "subscription paused", got: string(SubscriptionStatusPaused), want: "paused"},
		{name: "subscription cancelled", got: string(SubscriptionStatusCancelled), want: "cancelled"},
		{name: "agent audit success", got: string(AgentAuditOutcomeSuccess), want: "success"},
		{name: "agent audit failed", got: string(AgentAuditOutcomeFailed), want: "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("status value = %q, want %q", tt.got, tt.want)
			}
		})
	}
}
