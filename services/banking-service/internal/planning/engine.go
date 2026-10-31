package planning

import (
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
	"math/big"
	"sort"
	"time"
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
	if _, err := priorityRanks(s.GoalPriorityIDs, s.SavingsGoalIDs); err != nil {
		return err
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
		knownIncome := false
		for _, c := range s.Settings.Cashflows {
			if c.Kind == "income" {
				knownIncome = true
				r.FutureIncomeMinor += c.AmountMinor
			}
		}
		if !knownIncome {
			r.ReviewNotes = append(r.ReviewNotes, "future income unknown; conservatively assumed zero, not verified zero income")
		}
	}
	r.IncomeRiskReserveMinor = max(int64(0), s.Settings.BaselineIncomeMinor-r.FutureIncomeMinor)
	r.DisposableMinor = max(int64(0), s.BalanceMinor-r.LivingReserveMinor-r.AdditionalObligationsMinor-r.SafetyReserveMinor-r.CashflowBufferMinor-r.IncomeRiskReserveMinor)
	r.ReserveShortfallMinor = max(int64(0), r.LivingReserveMinor+r.AdditionalObligationsMinor+r.SafetyReserveMinor+r.CashflowBufferMinor+r.IncomeRiskReserveMinor-s.BalanceMinor)
	if r.ReserveShortfallMinor > 0 {
		r.ReviewNotes = append(r.ReviewNotes, "current funds cannot cover all protected reserves; zero savings avoids additional debits but does not resolve the cash shortfall")
	}
	goals := append([]GoalFunds{}, s.Goals...)
	goalIDs := make([]int64, len(goals))
	for i, g := range goals {
		goalIDs[i] = g.ID
	}
	ranks, err := priorityRanks(s.Settings.GoalPriorityIDs, goalIDs)
	if err != nil {
		return r, err
	}
	sort.Slice(goals, func(i, j int) bool {
		if len(ranks) > 0 {
			return ranks[goals[i].ID] < ranks[goals[j].ID]
		}
		return goals[i].ID < goals[j].ID
	})
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
		o.Explanation = fmt.Sprintf("CNY cents: current balance %d; protected reserves %d (living %d, additional obligations %d, safety %d, cash buffer %d, income risk %d). Safe savings cap min(disposable %d, target demand %d) = %d; %d%% gives savings %d and retained cash %d. Feasible only while source balances and constraints remain valid; future income and avoided spending are not current funds. This option may leave target demand unmet; it grants no financial execution authority.", s.BalanceMinor, s.BalanceMinor-r.DisposableMinor+r.ReserveShortfallMinor, r.LivingReserveMinor, r.AdditionalObligationsMinor, r.SafetyReserveMinor, r.CashflowBufferMinor, r.IncomeRiskReserveMinor, r.DisposableMinor, r.TargetDemandMinor, r.SavingsCapMinor, percent[k], amount, o.RetainedMinor)
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
		if len(ranks) > 0 {
			remaining := amount
			for i := range goals {
				allocations[i] = min(remaining, demands[i])
				remaining -= allocations[i]
			}
			o.Explanation += " User-selected goal priority is applied in order; no unrequested priority is inferred."
		}
		for i, g := range goals {
			required := int64(0)
			if g.EndsAt > 0 && g.CurrentMinor < g.TargetMinor {
				base, e := time.Parse("2006-01", s.Period)
				if e != nil {
					return r, entity.Fail("INVALID_ARGUMENT", "valid planning period required for deadline calculation")
				}
				end := time.UnixMilli(g.EndsAt).Add(-time.Nanosecond).UTC()
				months := int64((end.Year()-base.Year())*12 + int(end.Month()) - int(base.Month()) + 1)
				months = max(int64(1), months)
				// Review this month's total contribution, including deposits already
				// reflected in current funds exactly once. Do not count them again
				// as additional cash available to allocate.
				remaining := max(int64(0), g.TargetMinor-g.CurrentMinor) + min(g.CurrentMinor, g.DepositedThisPeriodMinor)
				required = remaining / months
				if remaining%months != 0 {
					required++
				}
			}
			o.Allocations = append(o.Allocations, Allocation{GoalID: g.ID, AmountMinor: allocations[i], UnmetMinor: demands[i] - allocations[i], ProgressBPS: g.CurrentMinor * 10000 / g.TargetMinor, DeadlineAt: g.EndsAt, RequiredMonthlyMinor: required, DeadlineRisk: g.EndsAt > 0 && required > allocations[i]+min(g.CurrentMinor, g.DepositedThisPeriodMinor)})
		}
		r.Options = append(r.Options, o)
	}
	r.SuggestedSavingsMinor = r.Options[1].SavingsMinor
	return r, nil
}

// An optional priority must be an exact permutation of the owned selected goals.
func priorityRanks(priorities, ids []int64) (map[int64]int, error) {
	ranks := map[int64]int{}
	if len(priorities) == 0 {
		return ranks, nil
	}
	if len(priorities) != len(ids) {
		return nil, entity.Fail("INVALID_ARGUMENT", "priority must list every selected savings goal exactly once")
	}
	allowed := map[int64]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for i, id := range priorities {
		if !allowed[id] {
			return nil, entity.Fail("INVALID_ARGUMENT", "priority contains an unselected or duplicate goal")
		}
		ranks[id] = i
		delete(allowed, id)
	}
	return ranks, nil
}
