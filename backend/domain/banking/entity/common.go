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
	"fmt"
	"strconv"
	"strings"
)

const DefaultCurrency = "CNY"

type ListOptions struct {
	Limit  int
	Offset int
}

type Money struct {
	AmountMinor int64
	Currency    string
}

func NewMoney(amountMinor int64, currency string) (Money, error) {
	if amountMinor < 0 {
		return Money{}, fmt.Errorf("amount must not be negative: %d", amountMinor)
	}

	normalizedCurrency, err := NormalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}

	return Money{
		AmountMinor: amountMinor,
		Currency:    normalizedCurrency,
	}, nil
}

func NormalizeCurrency(currency string) (string, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return DefaultCurrency, nil
	}
	if len(currency) != 3 {
		return "", fmt.Errorf("currency must contain exactly 3 ASCII letters: %q", currency)
	}
	for i := range currency {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return "", fmt.Errorf("currency must contain exactly 3 ASCII letters: %q", currency)
		}
	}

	return currency, nil
}

func (m Money) YuanString() string {
	minor := strconv.FormatInt(m.AmountMinor, 10)
	negative := strings.HasPrefix(minor, "-")
	if negative {
		minor = strings.TrimPrefix(minor, "-")
	}
	if len(minor) < 3 {
		minor = strings.Repeat("0", 3-len(minor)) + minor
	}

	result := minor[:len(minor)-2] + "." + minor[len(minor)-2:]
	if negative {
		return "-" + result
	}
	return result
}

func NormalizeListOptions(options *ListOptions) ListOptions {
	if options == nil {
		return ListOptions{Limit: 50}
	}

	normalized := *options
	if normalized.Limit <= 0 {
		normalized.Limit = 50
	} else if normalized.Limit > 100 {
		normalized.Limit = 100
	}
	if normalized.Offset < 0 {
		normalized.Offset = 0
	}

	return normalized
}
