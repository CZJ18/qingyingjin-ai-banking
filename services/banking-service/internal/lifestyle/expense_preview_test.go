package lifestyle

import (
	"github.com/qingyingjin/banking-service/internal/billing"
	"testing"
	"time"
)

func previewReport() billing.Report {
	return billing.Report{ID: "synthetic-report", UserID: 42, Currency: "CNY", TotalExpense: 300000, SnapshotAt: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC).UnixMilli(),
		AnalysisPeriod:         billing.Period{From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z", Timezone: "Asia/Shanghai"},
		CategoryBreakdown:      []billing.Category{{Category: "dining", ExpenseMinor: 100000}, {Category: "shopping", ExpenseMinor: 200000}},
		EvidenceTransactionIDs: []string{"synthetic-1", "synthetic-2"}}
}
func TestExpensePreviewClosedMonthNeedsVerifiedStatementCoverage(t *testing.T) {
	in := ExpensePreviewInput{BillingReportID: "synthetic-report", TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}}
	v, e := expensePreview(previewReport(), in, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if e != nil || v.Result.Status != "needs_information" || len(v.Result.Options) != 0 ||
		!has(v.Result.MissingInformation, "complete_historical_month_billing_report") {
		t.Fatalf("%+v %v", v, e)
	}
	if v.HistoricalExpenseMinor != 300000 || len(v.EvidenceTransactionIDs) != 2 || v.TargetMinor != 50000 || v.TargetPeriod != "2026-11" {
		t.Fatal("recorded observations or the user's target were lost", v)
	}
	if v.Result.FinancialExecutionEnabled || v.Source != "deterministic_lifestyle_rules" {
		t.Fatal("advice became model response or authorization")
	}
}
func TestExpensePreviewSparseCurrentDataCannotBecomeAFeasibleForecast(t *testing.T) {
	in := ExpensePreviewInput{BillingReportID: "synthetic-report", TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}}
	r := previewReport()
	r.AnalysisPeriod = billing.Period{From: "2026-09-30T16:00:00Z", To: "2026-10-31T16:00:00Z", Timezone: "Asia/Shanghai"}
	v, e := expensePreview(r, in, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if e != nil || v.Result.Status != "needs_information" || len(v.Result.Options) != 0 {
		t.Fatalf("%+v %v", v, e)
	}
}

func TestExpensePreviewOldPartialSnapshotDoesNotBecomeCompleteLater(t *testing.T) {
	in := ExpensePreviewInput{BillingReportID: "synthetic-report", TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}}
	r := previewReport()
	r.SnapshotAt = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC).UnixMilli()
	v, e := expensePreview(r, in, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if e != nil || v.Result.Status != "needs_information" || len(v.Result.Options) != 0 {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestExpensePreviewRefusesExpandedOrInvalidGoal(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	base := ExpensePreviewInput{BillingReportID: "synthetic-report", TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}}
	for _, change := range []func(*ExpensePreviewInput){func(v *ExpensePreviewInput) { v.TargetPeriod = "2026-12" }, func(v *ExpensePreviewInput) { v.TargetMinor = 0 }, func(v *ExpensePreviewInput) { v.TargetMinor = -1 }, func(v *ExpensePreviewInput) { v.AdjustableCategories = nil }, func(v *ExpensePreviewInput) { v.AdjustableCategories = []string{"arbitrary-category"} }, func(v *ExpensePreviewInput) { v.BillingReportID = "other-report" }} {
		v := base
		change(&v)
		if _, e := expensePreview(previewReport(), v, now); e == nil {
			t.Fatal("invalid scope accepted", v)
		}
	}
}
func TestExpensePreviewUnverifiedCoverageCannotInferCutsOrAnUnmetTarget(t *testing.T) {
	in := ExpensePreviewInput{BillingReportID: "synthetic-report", TargetPeriod: "2026-11", TargetMinor: 250000, AdjustableCategories: []string{"shopping", "dining"}}
	v, e := expensePreview(previewReport(), in, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if e != nil || v.Result.Status != "needs_information" || len(v.Result.Options) != 0 ||
		v.Result.ReductionGapMinor != 0 || !has(v.Result.MissingInformation, "complete_historical_month_billing_report") {
		t.Fatalf("%+v %v", v, e)
	}
}
