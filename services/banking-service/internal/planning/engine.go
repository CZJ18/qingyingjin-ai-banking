package planning

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"math/big"
	"sort"
)

func bounded(n int64) bool { return n >= 0 && n <= entity.MaxBalance }
func ValidateSettings(s Settings) error {
	if len(s.AccountIDs) == 0 || len(s.AccountIDs) > 100 || len(s.SavingsGoalIDs) == 0 || len(s.SavingsGoalIDs) > 100 || len(s.LifeGoalIDs) > 100 || len(s.Cashflows) > 100 {
		return entity.Fail("INVALID_ARGUMENT", "1..100 accounts and savings goals required; inputs bounded to 100")
	}
	for _, ids := range [][]int64{s.AccountIDs, s.SavingsGoalIDs, s.LifeGoalIDs} {
		seen := map[int64]bool{}
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return entity.Fail("INVALID_ARGUMENT", "positive distinct source IDs required")
			}
			seen[id] = true
		}
	}
	for _, v := range []*int64{s.LivingRemainingMinor, s.EssentialRemainingMinor, s.FutureIncomeMinor} {
		if v != nil && !bounded(*v) {
			return entity.Fail("INVALID_ARGUMENT", "amount outside integer cent bounds")
		}
	}
	for _, v := range []int64{s.SafetyReserveMinor, s.CashBufferMinor, s.BaselineIncomeMinor} {
		if !bounded(v) {
			return entity.Fail("INVALID_ARGUMENT", "invalid reserves")
		}
	}
	for _, c := range s.Cashflows {
		if !bounded(c.AmountMinor) || c.DueAt <= 0 || len(c.Name) == 0 || len(c.Name) > 128 || (c.Kind != "income" && c.Kind != "necessary_expense") {
			return entity.Fail("INVALID_ARGUMENT", "dated income or additional necessary_expense required")
		}
	}
	return nil
}
func Calculate(s Snapshot) (Result, error) {
	r := Result{Currency: "CNY", Period: s.Period, BalanceMinor: s.BalanceMinor, Options: []Option{}, Blockers: append([]string{}, s.Blockers...), ReviewNotes: append([]string{}, s.ReviewNotes...), FutureAvoidedSpendMinor: s.CancelledForecastMinor}
	if s.BalanceMinor < 0 || s.BalanceMinor > 100*entity.MaxBalance {
		return r, entity.Fail("INVALID_ARGUMENT", "invalid aggregate balance")
	}
	for _, n := range []int64{s.LifeFloorMinor, s.EssentialFloorMinor, s.SubscriptionDueMinor, s.CancelledForecastMinor} {
		if n < 0 || n > 1000*entity.MaxBalance {
			return r, entity.Fail("INVALID_ARGUMENT", "derived source totals outside planning bounds")
		}
	}
	for _, v := range []*int64{s.Settings.LivingRemainingMinor, s.Settings.EssentialRemainingMinor, s.Settings.FutureIncomeMinor} {
		if v != nil && !bounded(*v) {
			return r, entity.Fail("INVALID_ARGUMENT", "invalid amount")
		}
	}
	for _, v := range []int64{s.Settings.SafetyReserveMinor, s.Settings.CashBufferMinor, s.Settings.BaselineIncomeMinor} {
		if !bounded(v) {
			return r, entity.Fail("INVALID_ARGUMENT", "invalid buffer")
		}
	}
	r.NecessaryReserveMinor = s.EssentialFloorMinor
	if v := s.Settings.EssentialRemainingMinor; v != nil {
		r.NecessaryReserveMinor = max(r.NecessaryReserveMinor, *v)
	} else if s.EssentialFloorMinor == 0 {
		r.Blockers = append(r.Blockers, "necessary expenses unknown")
	}
	r.LivingReserveMinor = max(s.LifeFloorMinor, r.NecessaryReserveMinor)
	if v := s.Settings.LivingRemainingMinor; v != nil {
		r.LivingReserveMinor = max(r.LivingReserveMinor, *v)
	} else if s.LifeFloorMinor == 0 {
		r.Blockers = append(r.Blockers, "normal living budget unknown")
	}
	for _, c := range s.Settings.Cashflows {
		if !bounded(c.AmountMinor) {
			return r, entity.Fail("INVALID_ARGUMENT", "invalid cashflow")
		}
		if c.Kind == "necessary_expense" {
			r.AdditionalObligationsMinor += c.AmountMinor
		}
	}
	r.AdditionalObligationsMinor += s.SubscriptionDueMinor
	r.SafetyReserveMinor = s.Settings.SafetyReserveMinor
	r.CashflowBufferMinor = s.Settings.CashBufferMinor
	if s.Settings.FutureIncomeMinor != nil {
		r.FutureIncomeMinor = *s.Settings.FutureIncomeMinor
	} else {
		for _, c := range s.Settings.Cashflows {
			if c.Kind == "income" {
				r.FutureIncomeMinor += c.AmountMinor
			}
		}
	}
	r.IncomeRiskReserveMinor = max(int64(0), s.Settings.BaselineIncomeMinor-r.FutureIncomeMinor)
	r.DisposableMinor = max(int64(0), s.BalanceMinor-r.LivingReserveMinor-r.AdditionalObligationsMinor-r.SafetyReserveMinor-r.CashflowBufferMinor-r.IncomeRiskReserveMinor)
	r.ReserveShortfallMinor = max(int64(0), r.LivingReserveMinor+r.AdditionalObligationsMinor+r.SafetyReserveMinor+r.CashflowBufferMinor+r.IncomeRiskReserveMinor-s.BalanceMinor)
	if r.ReserveShortfallMinor > 0 {
		r.ReviewNotes = append(r.ReviewNotes, "current funds cannot cover all protected reserves; zero savings avoids additional debits but does not resolve the cash shortfall")
	}
	goals := append([]GoalFunds{}, s.Goals...)
	sort.Slice(goals, func(i, j int) bool { return goals[i].ID < goals[j].ID })
	demands := make([]int64, len(goals))
	for i, g := range goals {
		if !bounded(g.TargetMinor) || !bounded(g.CurrentMinor) || !bounded(g.MonthlyMinor) || g.DepositedThisPeriodMinor < 0 || g.TargetMinor == 0 {
			return r, entity.Fail("INVALID_ARGUMENT", "invalid savings source")
		}
		if g.Eligible {
			demands[i] = min(max(int64(0), g.TargetMinor-g.CurrentMinor), max(int64(0), g.MonthlyMinor-g.DepositedThisPeriodMinor))
		}
		r.TargetDemandMinor += demands[i]
	}
	r.SavingsCapMinor = min(r.DisposableMinor, r.TargetDemandMinor)
	if len(r.Blockers) > 0 {
		r.SavingsCapMinor = 0
	}
	r.ConflictMinor = max(int64(0), r.TargetDemandMinor-r.SavingsCapMinor)
	names := []string{"conservative", "balanced", "aggressive"}
	percent := []int64{50, 75, 100}
	for k, name := range names {
		amount := r.SavingsCapMinor * percent[k] / 100
		o := Option{Name: name, SavingsMinor: amount, RetainedMinor: s.BalanceMinor - amount, Allocations: []Allocation{}, Explanation: "Protect living expenses, additional obligations and safety reserves first; allocate this option's percentage of the safe cap proportionally. Forecast income and avoided costs are not current funds."}
		o.Feasible = r.ReserveShortfallMinor == 0 && len(r.Blockers) == 0
		if !o.Feasible {
			o.Explanation = "Zero savings proposal only: protected reserves are underfunded or constraints require clarification. Resolve the reported blockers/shortfall before treating this as a feasible full allocation."
		}
		allocations := make([]int64, len(goals))
		remainders := make([]*big.Int, len(goals))
		var allocated int64
		for i := range goals {
			remainders[i] = big.NewInt(0)
			if r.TargetDemandMinor > 0 {
				product := new(big.Int).Mul(big.NewInt(amount), big.NewInt(demands[i]))
				q := new(big.Int)
				q.QuoRem(product, big.NewInt(r.TargetDemandMinor), remainders[i])
				allocations[i] = q.Int64()
				allocated += allocations[i]
			}
		}
		order := make([]int, len(goals))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(i, j int) bool { return remainders[order[i]].Cmp(remainders[order[j]]) > 0 })
		for _, i := range order {
			if allocated >= amount {
				break
			}
			if allocations[i] < demands[i] {
				allocations[i]++
				allocated++
			}
		}
		for i, g := range goals {
			o.Allocations = append(o.Allocations, Allocation{GoalID: g.ID, AmountMinor: allocations[i], UnmetMinor: demands[i] - allocations[i], ProgressBPS: g.CurrentMinor * 10000 / g.TargetMinor})
		}
		r.Options = append(r.Options, o)
	}
	r.SuggestedSavingsMinor = r.Options[1].SavingsMinor
	return r, nil
}
