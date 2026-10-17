package planning

import "testing"

func ptr(n int64) *int64 { return &n }
func sample() Snapshot {
	return Snapshot{Period: "2026-10", Settings: Settings{LivingRemainingMinor: ptr(30000), EssentialRemainingMinor: ptr(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000, FutureIncomeMinor: ptr(0)}, BalanceMinor: 100000, Goals: []GoalFunds{{ID: 2, TargetMinor: 100000, MonthlyMinor: 40000, Eligible: true}, {ID: 1, TargetMinor: 100000, MonthlyMinor: 30000, Eligible: true}}, Blockers: []string{}, ReviewNotes: []string{}}
}
func TestRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Snapshot)
		cap    int64
	}{
		{"baseline", func(*Snapshot) {}, 55000}, {"insufficient", func(s *Snapshot) { s.BalanceMinor = 1000 }, 0}, {"income_reduced", func(s *Snapshot) { s.Settings.BaselineIncomeMinor = 50000; s.Settings.FutureIncomeMinor = ptr(20000) }, 25000}, {"emergency", func(s *Snapshot) {
			s.Settings.Cashflows = []Cashflow{{Name: "medical", Kind: "necessary_expense", AmountMinor: 50000, DueAt: 1791000000000}}
		}, 5000}, {"future_savings_not_cash", func(s *Snapshot) { s.CancelledForecastMinor = 90000 }, 55000}, {"unknown_living", func(s *Snapshot) { s.Settings.LivingRemainingMinor = nil }, 0}, {"unverified_constraint", func(s *Snapshot) { s.Blockers = []string{"unknown mandatory constraint"} }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sample()
			tc.change(&s)
			r, e := Calculate(s)
			if e != nil {
				t.Fatal(e)
			}
			if r.SavingsCapMinor != tc.cap {
				t.Fatalf("cap %d want %d", r.SavingsCapMinor, tc.cap)
			}
			if len(r.Options) != 3 {
				t.Fatal("three options")
			}
			for _, o := range r.Options {
				if o.SavingsMinor > r.SavingsCapMinor {
					t.Fatal("unsafe")
				}
				var sum int64
				for _, a := range o.Allocations {
					sum += a.AmountMinor
				}
				if sum != o.SavingsMinor {
					t.Fatal("allocation mismatch")
				}
			}
		})
	}
}
func TestStableAllocationAndCompletedSavings(t *testing.T) {
	s := sample()
	s.Goals[0].DepositedThisPeriodMinor = 40000
	s.Goals[1].CurrentMinor = 95000
	r, e := Calculate(s)
	if e != nil || r.SavingsCapMinor != 5000 {
		t.Fatal(r, e)
	}
	s.Goals[0], s.Goals[1] = s.Goals[1], s.Goals[0]
	r2, _ := Calculate(s)
	if r2.Options[2].Allocations[0] != r.Options[2].Allocations[0] {
		t.Fatal("order dependent")
	}
}
func TestBounds(t *testing.T) {
	s := sample()
	s.BalanceMinor = -1
	if _, e := Calculate(s); e == nil {
		t.Fatal("negative input")
	}
	s = sample()
	s.Settings.Cashflows = []Cashflow{{Kind: "income", AmountMinor: 1000000000001}}
	if _, e := Calculate(s); e == nil {
		t.Fatal("overflow input")
	}
}
