package planning

import (
	"testing"
	"time"
)

func TestRecurringProtection(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	for _, tc := range []struct {
		interval string
		want     int64
	}{{"weekly", 5000}, {"monthly", 1000}, {"quarterly", 1000}, {"annual", 1000}} {
		t.Run(tc.interval, func(t *testing.T) {
			n, _, e := subscriptionReserve(1000, start.UnixMilli(), tc.interval, start, end, start)
			if e != nil || n != tc.want {
				t.Fatal(n, e)
			}
		})
	}
	n, overdue, e := subscriptionReserve(1000, start.AddDate(0, -1, 0).UnixMilli(), "monthly", start, end, start)
	if e != nil || !overdue || n != 2000 {
		t.Fatal(n, overdue, e)
	}
}
func TestReserveShortfallHonesty(t *testing.T) {
	s := sample()
	s.BalanceMinor = 100
	r, e := Calculate(s)
	if e != nil || r.ReserveShortfallMinor != 44900 {
		t.Fatal(r, e)
	}
	for _, o := range r.Options {
		if o.Feasible || o.SavingsMinor != 0 {
			t.Fatal("claimed impossible allocation feasible")
		}
	}
}
func TestOverflowRecurringProtection(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, _, e := subscriptionReserve(1000000000000, start.AddDate(-100, 0, 0).UnixMilli(), "weekly", start, start.AddDate(0, 1, 0), start); e == nil {
		t.Fatal("overflow/unsupported history accepted")
	}
}
