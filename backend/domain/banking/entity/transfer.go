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

type TransferStatus string

const (
	TransferStatusCreated   TransferStatus = "created"
	TransferStatusSucceeded TransferStatus = "succeeded"
	TransferStatusFailed    TransferStatus = "failed"
	TransferStatusCancelled TransferStatus = "cancelled"
)

type Transfer struct {
	ID                  int64
	UserID              int64
	SourceBankAccountID int64
	TargetReference     string
	IdempotencyKey      string
	AmountMinor         int64
	Currency            string
	Status              TransferStatus
	Description         string
	CreatedAt           int64
	UpdatedAt           int64
}
