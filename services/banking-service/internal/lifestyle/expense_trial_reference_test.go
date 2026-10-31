package lifestyle

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func TestExpenseTrialReferenceUsesEqualSevenDaysAndNeverConfirmsSavings(t *testing.T) {
	report := checkedMonth(t)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	trial := ExpenseTrial{Base: entity.Base{UserID: 42}, AccountID: 7, Category: "shopping", StartedAt: start.UnixMilli(), EndsAt: start.AddDate(0, 0, 7).UnixMilli()}
	progress := ExpenseTrialProgress{From: trial.StartedAt, To: trial.EndsAt, WindowComplete: true, TotalExpenseMinor: 180000, CategoryExpenseMinor: 100000}
	rows := []entity.Transaction{
		{Base: entity.Base{ID: 1, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "expense", Currency: "CNY", Category: "shopping", AmountMinor: 200000, OccurredAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{Base: entity.Base{ID: 3, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "income", Currency: "CNY", Category: "salary", AmountMinor: 500000, OccurredAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{Base: entity.Base{ID: 9, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "expense", Currency: "CNY", Category: "shopping", AmountMinor: 99999, OccurredAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).UnixMilli()},
	}
	v, e := trialReference(report, trial, progress, rows)
	if e != nil || v == nil || v.Status != "recorded_reference_only" || v.CategoryExpenseMinor != 200000 || v.TotalExpenseMinor != 200000 || *v.CategoryDifferenceMinor != 100000 || *v.TotalDifferenceMinor != 20000 || progress.ConfirmedSavingsMinor != nil {
		t.Fatal(v, e)
	}
	if v.BaselinePeriod.From != "2026-08-31T16:00:00Z" || v.BaselinePeriod.To != "2026-09-07T16:00:00Z" {
		t.Fatal("full month was compared with seven days", v)
	}
	// Spending can move to another category. A category reduction must not hide
	// higher total expenses or become a confirmed saving.
	progress.TotalExpenseMinor = 240000
	v, e = trialReference(report, trial, progress, rows)
	if e != nil || *v.CategoryDifferenceMinor != 100000 || *v.TotalDifferenceMinor != -40000 || progress.ConfirmedSavingsMinor != nil {
		t.Fatal("category reduction hid higher total spending", v, e)
	}
	progress.WindowComplete = false
	if v, e := trialReference(report, trial, progress, rows); e != nil || v != nil {
		t.Fatal("partial observation got full-window difference", v, e)
	}
}
func TestExpenseTrialReferenceMissingOrChangedSourceCannotLookLikeSavings(t *testing.T) {
	r := checkedMonth(t)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	trial := ExpenseTrial{Base: entity.Base{UserID: 42}, AccountID: 7, Category: "shopping", StartedAt: start.UnixMilli(), EndsAt: start.AddDate(0, 0, 7).UnixMilli()}
	progress := ExpenseTrialProgress{From: trial.StartedAt, To: trial.EndsAt, WindowComplete: true}
	v, e := trialReference(r, trial, progress, nil)
	if e != nil || v == nil || v.Status != "source_changed" || v.CategoryDifferenceMinor != nil || v.TotalDifferenceMinor != nil {
		t.Fatal("missing baseline became a zero/confirmed saving", v, e)
	}
}
