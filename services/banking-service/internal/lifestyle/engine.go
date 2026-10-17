package lifestyle

import (
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"strings"
)

func ValidateSpec(s Spec) error {
	if s.SchemaVersion != "2.0" || s.Currency != "CNY" || s.PeriodMonths < 1 || s.PeriodMonths > 120 || s.ExpenseReductionMonthlyMinor < 0 || s.ExpenseReductionMonthlyMinor > entity.MaxBalance || s.SavingsMonthlyMinor < 0 || s.SavingsMonthlyMinor > entity.MaxBalance {
		return entity.Fail("INVALID_ARGUMENT", "invalid v2 goal or integer amount")
	}
	for _, list := range [][]string{s.AdjustableCategories, s.ProtectedCategories, s.ProhibitedOperations} {
		if len(list) > 32 {
			return entity.Fail("INVALID_ARGUMENT", "too many constraints")
		}
		seen := map[string]bool{}
		for _, v := range list {
			if strings.TrimSpace(v) == "" || len(v) > 64 || seen[v] {
				return entity.Fail("INVALID_ARGUMENT", "invalid or duplicate constraint")
			}
			seen[v] = true
		}
	}
	for _, op := range s.ProhibitedOperations {
		switch op {
		case "transfer", "cancel_subscription", "reduce_essential_expenses", "all_financial_operations", "weight_based_incentives":
		default:
			return entity.Fail("INVALID_ARGUMENT", "unknown prohibited operation")
		}
	}
	return nil
}
func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func protected(name string, s Spec) bool {
	if categoryHas(s.ProtectedCategories, name) {
		return true
	}
	canonical, known := billing.CanonicalCategory(name)
	if known && (canonical == "dining" || canonical == "transport" || canonical == "housing" || canonical == "medical") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "food", "dining", "餐饮", "饮食", "groceries", "食品", "housing", "房租", "住房", "medical", "医疗", "healthcare", "transport", "交通", "通勤", "necessary", "必要支出":
		return true
	}
	return false
}
func categoryHas(list []string, name string) bool {
	canonical, known := billing.CanonicalCategory(name)
	for _, v := range list {
		if v == name {
			return true
		}
		c, k := billing.CanonicalCategory(v)
		if !k && strings.HasSuffix(v, "预算") {
			// Keep the original user category and evidence; only match a known
			// category with the exact budget suffix, without fuzzy inference.
			c, k = billing.CanonicalCategory(strings.TrimSuffix(v, "预算"))
		}
		if known && k && c == canonical {
			return true
		}
	}
	return false
}

// Calculate is deterministic. Observed history bounds optional reductions;
// reductions are proposed budget changes, never spendable account funds.
func Calculate(s Snapshot) (Result, error) {
	r := Result{SchemaVersion: "2.0", RuleVersion: "lifestyle-2", Status: "ready", Options: []Option{}, MissingInformation: []string{}, Conflicts: []string{}, RequiresConfirmation: true}
	if e := ValidateSpec(s.Goal); e != nil {
		return r, e
	}
	if s.BudgetMinor < 0 || s.BudgetMinor > entity.MaxBalance || len(s.Categories) > 100 {
		return r, entity.Fail("INVALID_ARGUMENT", "invalid budget")
	}
	if s.SavingsCeilingMinor != nil && (*s.SavingsCeilingMinor < 0 || *s.SavingsCeilingMinor > 100*entity.MaxBalance) {
		return r, entity.Fail("INVALID_ARGUMENT", "invalid cash ceiling")
	}
	if s.BudgetMinor == 0 || len(s.Categories) == 0 {
		r.MissingInformation = append(r.MissingInformation, "budget")
	}
	if !s.CompleteMonth {
		r.MissingInformation = append(r.MissingInformation, "complete_historical_month_billing_report")
	}
	if s.Goal.SavingsMonthlyMinor > 0 && s.SavingsCeilingMinor == nil {
		r.MissingInformation = append(r.MissingInformation, "financial_plan_with_known_necessary_expenses")
	}
	var total, capacity int64
	seen := map[string]bool{}
	limits := make([]int64, len(s.Categories))
	for i, c := range s.Categories {
		if seen[c.Name] || strings.TrimSpace(c.Name) == "" || c.AllocatedMinor < 0 || c.AllocatedMinor > entity.MaxBalance || c.ObservedMinor < 0 || c.ObservedMinor > entity.MaxBalance || c.EssentialReserveMinor < 0 || c.EssentialReserveMinor > c.AllocatedMinor {
			return r, entity.Fail("INVALID_ARGUMENT", "invalid category snapshot")
		}
		seen[c.Name] = true
		total += c.AllocatedMinor
		if !protected(c.Name, s.Goal) && categoryHas(s.Goal.AdjustableCategories, c.Name) {
			limits[i] = max(int64(0), min(c.ObservedMinor, c.AllocatedMinor)-c.EssentialReserveMinor)
			capacity += limits[i]
		}
	}
	if total > s.BudgetMinor {
		return r, entity.Fail("INVALID_ARGUMENT", "category allocation exceeds budget")
	}
	if len(s.Goal.AdjustableCategories) == 0 && s.Goal.ExpenseReductionMonthlyMinor > 0 {
		r.MissingInformation = append(r.MissingInformation, "user_adjustable_category_preferences")
	}
	if len(r.MissingInformation) > 0 {
		r.Status = "needs_information"
		return r, nil
	}
	achievable := min(capacity, s.Goal.ExpenseReductionMonthlyMinor)
	r.ReductionGapMinor = s.Goal.ExpenseReductionMonthlyMinor - achievable
	savings := s.Goal.SavingsMonthlyMinor
	if s.SavingsCeilingMinor != nil {
		savings = min(savings, *s.SavingsCeilingMinor)
	}
	r.SavingsGapMinor = s.Goal.SavingsMonthlyMinor - savings
	if r.ReductionGapMinor > 0 {
		r.Conflicts = append(r.Conflicts, "expense reduction exceeds evidence-backed optional budget; request a lower target or new preferences")
	}
	if r.SavingsGapMinor > 0 {
		r.Conflicts = append(r.Conflicts, "savings exceed current safe cash capacity; reduced spending is future only")
	}
	if len(r.Conflicts) > 0 {
		r.Status = "conflict"
	}
	for i, name := range []string{"conservative", "balanced", "active"} {
		// Divide before multiplying to avoid overflow; exact residual preserves cents.
		amount := achievable/int64(4)*int64(i+2) + achievable%4*int64(i+2)/4
		if i == 2 {
			amount = achievable
		}
		remaining := amount
		o := Option{Name: name, BudgetMinor: s.BudgetMinor, ExpenseReductionMinor: amount, SavingsMonthlyMinor: savings, Categories: []Allocation{}, Explanation: "Reduction forecasts compare the lower optional budget with historical spending. User-approved optional categories only; normal meals and necessary reserves stay protected. Future avoided spending is not cash or authorization."}
		for j, c := range s.Categories {
			cut := min(remaining, limits[j])
			remaining -= cut
			proposed := c.AllocatedMinor
			if cut > 0 {
				proposed = min(c.AllocatedMinor, c.ObservedMinor) - cut
			}
			o.BudgetMinor -= c.AllocatedMinor - proposed
			o.Categories = append(o.Categories, Allocation{HistoricalMinor: c.ObservedMinor, Category: c.Name, OriginalMinor: c.AllocatedMinor, ProposedMinor: proposed, Protected: protected(c.Name, s.Goal)})
		}
		r.Options = append(r.Options, o)
	}
	return r, nil
}
