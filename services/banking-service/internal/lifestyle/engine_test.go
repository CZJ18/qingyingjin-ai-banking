package lifestyle

import "testing"

func TestSeparateReductionAndSavings(t *testing.T) {
	s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 100000, SavingsMonthlyMinor: 20000, AdjustableCategories: []string{"shopping"}}, BudgetMinor: 200000, Categories: []Category{{Name: "food", AllocatedMinor: 80000, ObservedMinor: 70000}, {Name: "shopping", AllocatedMinor: 120000, ObservedMinor: 60000}}, CompleteMonth: true}
	ceiling := int64(20000)
	s.SavingsCeilingMinor = &ceiling
	r, e := Calculate(s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "conflict" || len(r.Options) != 3 {
		t.Fatalf("%+v", r)
	}
	for _, o := range r.Options {
		if o.ExpenseReductionMinor > 60000 || o.SavingsMonthlyMinor != 20000 || o.Categories[0].ProposedMinor != 80000 {
			t.Fatalf("unsafe %+v", o)
		}
	}
	if r.ReductionGapMinor != 40000 {
		t.Fatal(r.ReductionGapMinor)
	}
}

func TestMissingBudgetAndMealProtection(t *testing.T) {
	r, e := Calculate(Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3}, CompleteMonth: true})
	if e != nil || r.Status != "needs_information" || len(r.Options) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = Calculate(Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 50000, AdjustableCategories: []string{"food"}}, BudgetMinor: 100000, CompleteMonth: true, Categories: []Category{{Name: "food", AllocatedMinor: 100000, ObservedMinor: 90000}}})
	if e != nil || r.Options[2].ExpenseReductionMinor != 0 {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestInvalidMoney(t *testing.T) {
	_, e := Calculate(Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: -1}})
	if e == nil {
		t.Fatal("negative accepted")
	}
}

func TestSavingsCashConflictAndForecast(t *testing.T) {
	zero := int64(0)
	s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 10000, SavingsMonthlyMinor: 20000, AdjustableCategories: []string{"shopping"}}, BudgetMinor: 50000, Categories: []Category{{Name: "shopping", AllocatedMinor: 50000, ObservedMinor: 30000}}, CompleteMonth: true, SavingsCeilingMinor: &zero}
	r, e := Calculate(s)
	if e != nil || r.SavingsGapMinor != 20000 || r.Options[2].SavingsMonthlyMinor != 0 || r.Options[2].ExpenseReductionMinor != 10000 || r.Options[2].Categories[0].ProposedMinor != 20000 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestNoImplicitPreferences(t *testing.T) {
	r, e := Calculate(Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 10000}, BudgetMinor: 100000, Categories: []Category{{Name: "shopping", AllocatedMinor: 100000, ObservedMinor: 50000}}, CompleteMonth: true})
	if e != nil || r.Status != "needs_information" || len(r.Options) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestNecessaryAliasesProtected(t *testing.T) {
	for _, name := range []string{"rent", "transportation", "dining", "食品", "医疗"} {
		s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 1000, AdjustableCategories: []string{name}}, BudgetMinor: 10000, CompleteMonth: true, Categories: []Category{{Name: name, AllocatedMinor: 10000, ObservedMinor: 9000}}}
		r, e := Calculate(s)
		if e != nil || r.Options[2].Categories[0].ProposedMinor != 10000 {
			t.Fatal(name, r, e)
		}
	}
}

func TestExplicitCategoryBudgetSuffixPreservesReductionEvidence(t *testing.T) {
	s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 10000, AdjustableCategories: []string{"购物预算"}, Evidence: map[string]string{"adjustable:购物预算": "可调整购物预算"}}, BudgetMinor: 30000, CompleteMonth: true, Categories: []Category{{Name: "shopping", AllocatedMinor: 30000, ObservedMinor: 20000}}}
	r, err := Calculate(s)
	if err != nil || r.Status != "ready" || r.ReductionGapMinor != 0 || len(r.Options) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	for i, expected := range []int64{5000, 7500, 10000} {
		if r.Options[i].ExpenseReductionMinor != expected || r.Options[i].Categories[0].ProposedMinor != 20000-expected {
			t.Fatalf("option %d: %+v", i, r.Options[i])
		}
	}
	if s.Goal.AdjustableCategories[0] != "购物预算" || s.Goal.Evidence["adjustable:购物预算"] != "可调整购物预算" {
		t.Fatal("original category or evidence was modified")
	}
}

func TestExplicitProtectedCategoryBudgetSuffix(t *testing.T) {
	s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 10000, AdjustableCategories: []string{"exercise"}, ProtectedCategories: []string{"运动预算"}}, BudgetMinor: 30000, CompleteMonth: true, Categories: []Category{{Name: "exercise", AllocatedMinor: 30000, ObservedMinor: 20000}}}
	r, err := Calculate(s)
	if err != nil || r.Status != "conflict" || len(r.Options) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, option := range r.Options {
		if option.ExpenseReductionMinor != 0 || !option.Categories[0].Protected || option.Categories[0].ProposedMinor != 30000 {
			t.Fatalf("protected exercise budget changed: %+v", option)
		}
	}
}

func TestUnknownOrAmbiguousCategoryBudgetDoesNotAuthorizeReduction(t *testing.T) {
	for _, preference := range []string{"其他预算", "购物和运动预算", "减少购物预算", "购物预算调整", "预算", "购物预算预算"} {
		t.Run(preference, func(t *testing.T) {
			s := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 10000, AdjustableCategories: []string{preference}}, BudgetMinor: 30000, CompleteMonth: true, Categories: []Category{{Name: "shopping", AllocatedMinor: 30000, ObservedMinor: 20000}}}
			r, err := Calculate(s)
			if err != nil || r.Status != "conflict" || len(r.Options) != 3 || r.Options[2].ExpenseReductionMinor != 0 || r.Options[2].Categories[0].ProposedMinor != 30000 {
				t.Fatalf("unknown preference authorized reduction: %+v %v", r, err)
			}
		})
	}
}
