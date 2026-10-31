package lifestyle

import (
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"strconv"
	"time"
)

type ExpenseTrialComparison struct {
	Status                  string         `json:"status"`
	BaselinePeriod          billing.Period `json:"baseline_period"`
	CategoryExpenseMinor    int64          `json:"category_expense_minor"`
	TotalExpenseMinor       int64          `json:"total_expense_minor"`
	CategoryDifferenceMinor *int64         `json:"category_difference_minor"`
	TotalDifferenceMinor    *int64         `json:"total_difference_minor"`
}

// Equal-duration recorded differences are reference information, not causal or
// verified savings. Snapshot membership and totals must still match source SQL.
func trialReference(report billing.Report, trial ExpenseTrial, progress ExpenseTrialProgress, rows []entity.Transaction) (*ExpenseTrialComparison, error) {
	if !progress.WindowComplete {
		return nil, nil
	}
	from, e := time.Parse(time.RFC3339, report.AnalysisPeriod.From)
	if e != nil {
		return nil, e
	}
	end := from.AddDate(0, 0, 7)
	out := &ExpenseTrialComparison{Status: "source_changed", BaselinePeriod: billing.Period{From: from.Format(time.RFC3339), To: end.Format(time.RFC3339), Timezone: "Asia/Shanghai"}}
	if progress.From != trial.StartedAt || progress.To != trial.EndsAt || trial.EndsAt-trial.StartedAt != int64(7*24*time.Hour/time.Millisecond) {
		return out, nil
	}
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return nil, e
	}
	expectedIDs := map[string]bool{}
	var expectedTotal int64
	for _, day := range report.Daily {
		at, e := time.ParseInLocation("2006-01-02", day.Label, loc)
		if e != nil {
			return out, nil
		}
		if !at.Before(from) && at.Before(end) {
			expectedTotal += day.ExpenseMinor
			for _, id := range day.TransactionIDs {
				expectedIDs[id] = true
			}
		}
	}
	membership := map[int64]bool{}
	category := map[int64]bool{}
	for _, id := range report.EvidenceTransactionIDs {
		v, e := strconv.ParseInt(id, 10, 64)
		if e != nil || v <= 0 {
			return out, nil
		}
		membership[v] = true
	}
	for _, c := range report.CategoryBreakdown {
		if c.Category == trial.Category {
			for _, id := range c.TransactionIDs {
				v, e := strconv.ParseInt(id, 10, 64)
				if e != nil || v <= 0 {
					return out, nil
				}
				category[v] = true
			}
		}
	}
	actualIDs := map[string]bool{}
	var sum, categorySum int64
	for _, row := range rows {
		if !membership[row.ID] || row.UserID != trial.UserID || row.BankAccountID != trial.AccountID || row.Kind != "external" || row.Currency != "CNY" || row.OccurredAt < from.UnixMilli() || row.OccurredAt >= end.UnixMilli() {
			continue
		}
		id := strconv.FormatInt(row.ID, 10)
		if actualIDs[id] || row.AmountMinor < 0 {
			return out, nil
		}
		actualIDs[id] = true
		if row.Direction == "expense" {
			sum += row.AmountMinor
			if category[row.ID] {
				categorySum += row.AmountMinor
			}
		}
	}
	if len(actualIDs) != len(expectedIDs) || sum != expectedTotal {
		return out, nil
	}
	for id := range expectedIDs {
		if !actualIDs[id] {
			return out, nil
		}
	}
	out.Status = "recorded_reference_only"
	out.TotalExpenseMinor = sum
	out.CategoryExpenseMinor = categorySum
	d := categorySum - progress.CategoryExpenseMinor
	total := sum - progress.TotalExpenseMinor
	out.CategoryDifferenceMinor = &d
	out.TotalDifferenceMinor = &total
	return out, nil
}
