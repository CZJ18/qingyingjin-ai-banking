package modelspend

import (
	"testing"
	"time"
)

func TestMonthlyAdmissionExactBoundaryAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		limit, used, held, reserve int64
		ok                         bool
	}{
		{100, 40, 50, 10, true}, {100, 40, 50, 11, false},
		{0, 0, 0, 1, false}, {100, -1, 0, 1, false},
		{100, 101, 0, 1, false}, {100, 0, 101, 1, false},
		{9223372036854775807, 9223372036854775800, 7, 1, false},
	} {
		if got := monthlyCanReserve(tc.limit, tc.used, tc.held, tc.reserve); got != tc.ok {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}

func TestMonthlyWindowUsesShanghaiNotHostTimezone(t *testing.T) {
	before := time.Date(2026, 10, 31, 15, 59, 59, 0, time.UTC)
	if monthlyPeriod(before) != "2026-10" || monthlyPeriod(before.Add(time.Second)) != "2026-11" {
		t.Fatal("Shanghai month boundary")
	}
}
