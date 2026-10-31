package lifestyle

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"gorm.io/gorm"
)

// ExpensePreviewInput is a user-authored what-if goal, never a savings deposit,
// persisted preference, model fee approval or financial execution instruction.
type ExpensePreviewInput struct {
	BillingReportID      string                 `json:"billing_report_id"`
	TargetPeriod         string                 `json:"target_period"`
	TargetMinor          int64                  `json:"target_minor"`
	AdjustableCategories []string               `json:"adjustable_categories"`
	StatementCheck       *ExpenseStatementCheck `json:"statement_check,omitempty"`
}

// User-entered statement totals are an explicit planning assumption. Matching
// them verifies arithmetic and scope, never bank authenticity or future savings.
type ExpenseStatementCheck struct {
	FullMonthConfirmed bool   `json:"full_month_confirmed"`
	TransactionCount   *int64 `json:"transaction_count"`
	IncomeMinor        *int64 `json:"income_minor"`
	ExpenseMinor       *int64 `json:"expense_minor"`
}
type ExpensePreview struct {
	Source                 string                 `json:"source"`
	BillingReportID        string                 `json:"billing_report_id"`
	BaselinePeriod         billing.Period         `json:"baseline_period"`
	TargetPeriod           string                 `json:"target_period"`
	TargetMinor            int64                  `json:"target_minor"`
	HistoricalExpenseMinor int64                  `json:"historical_expense_minor"`
	EvidenceTransactionIDs []string               `json:"evidence_transaction_ids"`
	Result                 Result                 `json:"result"`
	BaselineStatus         string                 `json:"baseline_status"`
	StatementCheck         *ExpenseStatementCheck `json:"statement_check,omitempty"`
}

func checkExpenseStatement(report billing.Report, check *ExpenseStatementCheck, now time.Time) error {
	if check == nil {
		return nil
	}
	bad := func() error {
		return entity.Fail("INVALID_ARGUMENT", "月账单核对失败：请核对同一账户已结束的完整月份、全部交易笔数和收支汇总；用户确认不是银行认证。")
	}
	if !check.FullMonthConfirmed || check.TransactionCount == nil || check.IncomeMinor == nil || check.ExpenseMinor == nil ||
		*check.TransactionCount < 0 || *check.TransactionCount > billing.MaxRows || *check.IncomeMinor < 0 || *check.IncomeMinor > entity.MaxBalance || *check.ExpenseMinor < 0 || *check.ExpenseMinor > entity.MaxBalance ||
		*check.TransactionCount != report.TransactionCount || *check.IncomeMinor != report.TotalIncome || *check.ExpenseMinor != report.TotalExpense ||
		report.Tool != "generate_billing_report" || len(report.Accounts) != 1 || report.Accounts[0].UserID != report.UserID || report.AnalysisPeriod.Timezone != "Asia/Shanghai" {
		return bad()
	}
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return e
	}
	from, e1 := time.Parse(time.RFC3339, report.AnalysisPeriod.From)
	to, e2 := time.Parse(time.RFC3339, report.AnalysisPeriod.To)
	if e1 != nil || e2 != nil {
		return bad()
	}
	start := time.Date(from.In(loc).Year(), from.In(loc).Month(), 1, 0, 0, 0, 0, loc)
	if !from.Equal(start) || !to.Equal(start.AddDate(0, 1, 0)) || now.Before(to) || report.SnapshotAt < to.UnixMilli() {
		return bad()
	}
	return nil
}

func expensePreview(report billing.Report, in ExpensePreviewInput, now time.Time) (ExpensePreview, error) {
	var out ExpensePreview
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return out, err
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, loc).Format("2006-01")
	if in.BillingReportID == "" || len(in.BillingReportID) > 64 || in.BillingReportID != report.ID || report.Currency != "CNY" ||
		in.TargetPeriod != next || in.TargetMinor <= 0 || in.TargetMinor > entity.MaxBalance || in.AdjustableCategories == nil || len(in.AdjustableCategories) > 32 {
		return out, entity.Fail("INVALID_ARGUMENT", "owned report, next Shanghai month and a positive integer-cent reduction target required")
	}
	preferences := []string{}
	seen := map[string]bool{}
	for _, raw := range in.AdjustableCategories {
		category, known := billing.CanonicalCategory(raw)
		if !known || seen[category] {
			return out, entity.Fail("INVALID_ARGUMENT", "distinct known adjustable categories required")
		}
		seen[category] = true
		preferences = append(preferences, category)
	}
	from, e1 := time.Parse(time.RFC3339, report.AnalysisPeriod.From)
	to, e2 := time.Parse(time.RFC3339, report.AnalysisPeriod.To)
	_, e3 := time.LoadLocation(report.AnalysisPeriod.Timezone)
	if e1 != nil || e2 != nil || e3 != nil || !to.After(from) {
		return out, entity.Fail("TOOL_OUTPUT_INVALID", "invalid source report period")
	}
	if err := checkExpenseStatement(report, in.StatementCheck, now); err != nil {
		return out, err
	}
	// Without an explicit matching user review, retain the existing missing-data
	// path. Reviewed totals enable a conditional what-if, not verified coverage.
	snap := Snapshot{Goal: Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 1,
		ExpenseReductionMonthlyMinor: in.TargetMinor, AdjustableCategories: preferences},
		BudgetMinor: report.TotalExpense, CompleteMonth: in.StatementCheck != nil, Categories: []Category{}}
	for _, c := range report.CategoryBreakdown {
		snap.Categories = append(snap.Categories, Category{Name: c.Category, AllocatedMinor: c.ExpenseMinor, ObservedMinor: c.ExpenseMinor})
	}
	result, err := Calculate(snap)
	if err != nil {
		return out, err
	}
	status := "unverified"
	if in.StatementCheck != nil {
		status = "user_reconciled"
	}
	return ExpensePreview{Source: "deterministic_lifestyle_rules", BillingReportID: report.ID, BaselinePeriod: report.AnalysisPeriod, BaselineStatus: status, StatementCheck: in.StatementCheck,
		TargetPeriod: in.TargetPeriod, TargetMinor: in.TargetMinor, HistoricalExpenseMinor: report.TotalExpense,
		EvidenceTransactionIDs: append([]string{}, report.EvidenceTransactionIDs...), Result: result}, nil
}

func (s *Service) PreviewExpenseReduction(ctx context.Context, user int64, in ExpensePreviewInput) (ExpensePreview, error) {
	var out ExpensePreview
	if user <= 0 {
		return out, entity.Fail("UNAUTHENTICATED", "authenticated owner required")
	}
	err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if e := tx.Model(&entity.User{}).Where("id=? AND status='active'", user).Count(&active).Error; e != nil {
			return e
		}
		if active != 1 {
			return entity.Fail("FORBIDDEN", "active owner required")
		}
		var stored billing.StoredReport
		if e := tx.Where("id=? AND user_id=?", in.BillingReportID, user).First(&stored).Error; e != nil {
			return e
		}
		var report billing.Report
		if json.Unmarshal(stored.ReportJSON, &report) != nil || billing.ValidateReport(report) != nil || report.UserID != user || report.ID != stored.ID {
			return entity.Fail("TOOL_OUTPUT_INVALID", "source report ownership or integrity mismatch")
		}
		var e error
		out, e = expensePreview(report, in, time.Now())
		return e
	}, &sql.TxOptions{ReadOnly: true})
	return out, coreRepo.DBError(err)
}
