package entity

import (
	"errors"
	"fmt"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
)

const MaxBalance int64 = 1_000_000_000_000

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string    { return e.Code + ": " + e.Message }
func Fail(code, msg string) error { return &Error{Code: code, Message: msg} }
func Code(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "DEPENDENCY_UNAVAILABLE"
}
func Money(amount int64, currency string) (legacy.Money, error) {
	m, err := legacy.NewMoney(amount, currency)
	if err != nil || m.Currency != "CNY" || amount > MaxBalance {
		return legacy.Money{}, Fail("INVALID_ARGUMENT", "amount must be integer minor units between 0 and 1000000000000; currency must be CNY")
	}
	return m, nil
}
func CheckedBalance(current, delta int64) (int64, error) {
	if current < 0 || current > MaxBalance || delta > MaxBalance || delta < -MaxBalance {
		return 0, Fail("AMOUNT_LIMIT", "balance or movement exceeds simulation limit")
	}
	if delta < 0 && current < -delta {
		return 0, Fail("INSUFFICIENT_FUNDS", "insufficient simulated balance")
	}
	if delta > 0 && current > MaxBalance-delta {
		return 0, Fail("AMOUNT_LIMIT", "balance exceeds simulation limit")
	}
	return current + delta, nil
}
func ValidateLedger(rows []LedgerEntry) error {
	if len(rows) != 2 || rows[0].JournalID == "" || rows[0].JournalID != rows[1].JournalID || rows[0].Currency != rows[1].Currency || rows[0].DeltaMinor == 0 || rows[0].DeltaMinor > MaxBalance || rows[0].DeltaMinor < -MaxBalance || rows[1].DeltaMinor != -rows[0].DeltaMinor {
		return Fail("LEDGER_UNBALANCED", fmt.Sprintf("journal must have two opposite nonzero entries in same currency"))
	}
	return nil
}
