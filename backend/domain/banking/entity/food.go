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

import "encoding/json"

type FoodItemStatus string

const (
	FoodItemStatusActive   FoodItemStatus = "active"
	FoodItemStatusInactive FoodItemStatus = "inactive"
)

type FoodOrderStatus string

const (
	FoodOrderStatusDraft     FoodOrderStatus = "draft"
	FoodOrderStatusConfirmed FoodOrderStatus = "confirmed"
	FoodOrderStatusCancelled FoodOrderStatus = "cancelled"
	FoodOrderStatusCompleted FoodOrderStatus = "completed"
)

type FoodItem struct {
	ID                int64
	UserID            int64
	Provider          string
	ExternalItemID    string
	Name              string
	UnitPriceMinor    int64
	Currency          string
	NutritionMetadata json.RawMessage
	Status            FoodItemStatus
	CreatedAt         int64
	UpdatedAt         int64
}

type FoodOrder struct {
	ID               int64
	UserID           int64
	HealthBudgetID   int64
	BankAccountID    int64
	Provider         string
	MerchantName     string
	TotalAmountMinor int64
	Currency         string
	Status           FoodOrderStatus
	ExternalOrderID  string
	Items            []*FoodOrderItem
	CreatedAt        int64
	UpdatedAt        int64
}

type FoodOrderItem struct {
	ID             int64
	UserID         int64
	FoodOrderID    int64
	FoodItemID     int64
	Name           string
	Quantity       int64
	UnitPriceMinor int64
	SubtotalMinor  int64
	Currency       string
	CreatedAt      int64
	UpdatedAt      int64
}
