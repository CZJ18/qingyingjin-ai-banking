package entity

import "testing"

func TestCheckedBalance(t *testing.T) {
	for _, v := range []struct {
		current, delta, want int64
		code                 string
	}{
		{100, -100, 0, ""}, {100, -101, 0, "INSUFFICIENT_FUNDS"}, {MaxBalance, 1, 0, "AMOUNT_LIMIT"}, {0, 1, 1, ""},
	} {
		got, err := CheckedBalance(v.current, v.delta)
		if Code(err) != v.code || (err == nil && got != v.want) {
			t.Fatalf("balance(%d,%d)=%d,%v", v.current, v.delta, got, err)
		}
	}
}
func TestLedgerRequiresBalancedSameCurrencyPair(t *testing.T) {
	for _, v := range []struct {
		rows []LedgerEntry
		good bool
	}{
		{[]LedgerEntry{{JournalID: "j", Currency: "CNY", DeltaMinor: 100}, {JournalID: "j", Currency: "CNY", DeltaMinor: -100}}, true},
		{[]LedgerEntry{{JournalID: "j", Currency: "CNY", DeltaMinor: 100}, {JournalID: "j", Currency: "CNY", DeltaMinor: -99}}, false},
		{[]LedgerEntry{{JournalID: "j", Currency: "CNY", DeltaMinor: 100}, {JournalID: "j", Currency: "USD", DeltaMinor: -100}}, false},
		{[]LedgerEntry{{JournalID: "j", Currency: "CNY", DeltaMinor: 0}, {JournalID: "j", Currency: "CNY", DeltaMinor: 0}}, false},
	} {
		if (ValidateLedger(v.rows) == nil) != v.good {
			t.Fatalf("ledger validation=%+v", v)
		}
	}
}
