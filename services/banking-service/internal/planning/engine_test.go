package planning

import "testing"

func ptr(n int64) *int64 { return &n }
func sample() Snapshot {
	return Snapshot{Period: "2026-10", Settings: Settings{LivingRemainingMinor: ptr(30000), EssentialRemainingMinor: ptr(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000, FutureIncomeMinor: ptr(0)}, BalanceMinor: 100000, Goals: []GoalFunds{{ID: 2, TargetMinor: 100000, MonthlyMinor: 40000, Eligible: true}, {ID: 1, TargetMinor: 100000, MonthlyMinor: 30000, Eligible: true}}, Blockers: []string{}, ReviewNotes: []string{}}
}

func TestExplicitGoalPriorityAndDeadlineRemainWithinSafeCash(t *testing.T) {
	s := sample()
	s.Settings.GoalPriorityIDs = []int64{2, 1}
	s.Goals[0].EndsAt = 1793491200000 // 2026-11-01T00:00:00Z, exclusive deadline
	result, err := Calculate(s)
	if err != nil {
		t.Fatal(err)
	}
	option := result.Options[1]
	if option.Allocations[0].GoalID != 2 || option.Allocations[0].AmountMinor != 40000 || option.Allocations[1].AmountMinor != 1250 {
		t.Fatal(option)
	}
	if option.Allocations[0].RequiredMonthlyMinor != 100000 || !option.Allocations[0].DeadlineRisk {
		t.Fatal("unfunded deadline not reported", option)
	}
	if option.RetainedMinor < 45000 {
		t.Fatal("priority consumed protected reserves")
	}
	s.Settings.GoalPriorityIDs = []int64{2, 2}
	if _, err := Calculate(s); err == nil {
		t.Fatal("duplicate priority accepted")
	}
}

func TestDeadlineReviewCountsCurrentMonthDepositsExactlyOnce(t *testing.T) {
	s := sample()
	s.Goals = []GoalFunds{{ID: 1, TargetMinor: 120000, CurrentMinor: 10000, MonthlyMinor: 10000, DepositedThisPeriodMinor: 10000, Eligible: true, EndsAt: 1822348800000}}
	result, err := Calculate(s)
	if err != nil {
		t.Fatal(err)
	}
	if result.Options[0].Allocations[0].DeadlineRisk {
		t.Fatal("on-track monthly deposit was ignored", result.Options[0])
	}
	s.Goals[0] = GoalFunds{ID: 1, TargetMinor: 10000, CurrentMinor: 3000, MonthlyMinor: 10000, DepositedThisPeriodMinor: 3000, Eligible: true, EndsAt: 1793491200000}
	s.BalanceMinor = 49000 // safe cap 4000; even aggressive savings leave a deadline gap
	result, err = Calculate(s)
	if err != nil || !result.Options[2].Allocations[0].DeadlineRisk {
		t.Fatal("current deposits counted twice", result, err)
	}
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
