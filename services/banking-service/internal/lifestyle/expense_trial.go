package lifestyle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"time"
	"unicode/utf8"
)

// A trial records a user's chosen behaviour, never a savings balance or mandate.
type ExpenseTrialInput struct {
	AccountID              int64                  `json:"account_id,string"`
	BillingReportID        string                 `json:"billing_report_id"`
	TargetPeriod           string                 `json:"target_period"`
	TargetMinor            int64                  `json:"target_minor"`
	Category               string                 `json:"category"`
	StatementCheck         *ExpenseStatementCheck `json:"statement_check,omitempty"`
	ExpectedReductionMinor *int64                 `json:"expected_reduction_minor,omitempty"`
}

type ExpenseTrialPlan struct {
	ExpectedReductionMinor int64                  `json:"expected_reduction_minor"`
	MonthlyLimitMinor      int64                  `json:"monthly_limit_minor"`
	StatementCheck         *ExpenseStatementCheck `json:"statement_check"`
}

// Retain the existing review JSON shape and old rows; only new plans add data.
type expenseTrialState struct {
	ExpenseTrialReview
	Plan *ExpenseTrialPlan `json:"plan,omitempty"`
}
type ExpenseTrial struct {
	entity.Base
	AccountID       int64           `json:"account_id,string"`
	BillingReportID string          `json:"billing_report_id"`
	BaselineDigest  string          `json:"baseline_digest"`
	TargetPeriod    string          `json:"target_period"`
	TargetMinor     int64           `json:"target_minor"`
	Category        string          `json:"category"`
	StartedAt       int64           `json:"started_at"`
	EndsAt          int64           `json:"ends_at"`
	Status          string          `json:"status"`
	Version         int64           `json:"version"`
	ReviewJSON      json.RawMessage `json:"-" gorm:"column:review_json"`
}

func (ExpenseTrial) TableName() string { return "banking_expense_trials" }

type ExpenseTrialProgress struct {
	From                   int64    `json:"from"`
	To                     int64    `json:"to"`
	TotalExpenseMinor      int64    `json:"total_expense_minor"`
	CategoryExpenseMinor   int64    `json:"category_expense_minor"`
	EvidenceTransactionIDs []string `json:"evidence_transaction_ids"`
	WindowComplete         bool     `json:"window_complete"`
	ConfirmedSavingsMinor  *int64   `json:"confirmed_savings_minor"`
}
type ExpenseTrialReviewInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Note            string `json:"note"`
}
type ExpenseTrialReview struct {
	ObservedAt int64                `json:"observed_at"`
	Note       string               `json:"note"`
	Progress   ExpenseTrialProgress `json:"progress"`
}
type ExpenseTrialView struct {
	Trial                     ExpenseTrial            `json:"trial"`
	Baseline                  billing.Category        `json:"baseline"`
	BaselinePeriod            billing.Period          `json:"baseline_period"`
	BaselineComplete          bool                    `json:"baseline_complete"`
	Progress                  ExpenseTrialProgress    `json:"progress"`
	Review                    *ExpenseTrialReview     `json:"review"`
	FinancialExecutionEnabled bool                    `json:"financial_execution_enabled"`
	Plan                      *ExpenseTrialPlan       `json:"plan,omitempty"`
	Comparison                *ExpenseTrialComparison `json:"comparison,omitempty"`
}

func optionalTrialCategory(v string) bool {
	return v == "shopping" || v == "subscriptions" || v == "sports" || v == "other"
}
func validateTrialInput(report billing.Report, user int64, in ExpenseTrialInput, now time.Time) error {
	if user <= 0 || report.UserID != user || len(report.Accounts) != 1 || report.Accounts[0].ID != in.AccountID || report.Accounts[0].UserID != user || !optionalTrialCategory(in.Category) {
		return entity.Fail("INVALID_ARGUMENT", "owned single-account report and an explicitly selected optional category required")
	}
	_, e := expensePreview(report, ExpensePreviewInput{BillingReportID: in.BillingReportID, TargetPeriod: in.TargetPeriod, TargetMinor: in.TargetMinor, AdjustableCategories: []string{in.Category}, StatementCheck: in.StatementCheck}, now)
	if e != nil {
		return e
	}
	if (in.StatementCheck == nil) != (in.ExpectedReductionMinor == nil) {
		return entity.Fail("INVALID_ARGUMENT", "核对后的试行方案必须同时提供月账单核对信息和本类计划少花金额")
	}
	if in.ExpectedReductionMinor != nil {
		var observed int64
		for _, c := range report.CategoryBreakdown {
			if c.Category == in.Category {
				observed = c.ExpenseMinor
			}
		}
		if *in.ExpectedReductionMinor <= 0 || *in.ExpectedReductionMinor > observed || *in.ExpectedReductionMinor > in.TargetMinor {
			return entity.Fail("INVALID_ARGUMENT", "本类计划少花金额必须在该类已记录支出和目标范围内")
		}
	}
	return nil
}
func trialReport(tx *gorm.DB, user int64, id string) (billing.Report, error) {
	var stored billing.StoredReport
	var report billing.Report
	if e := tx.Where("user_id=? AND id=?", user, id).First(&stored).Error; e != nil {
		return report, e
	}
	if json.Unmarshal(stored.ReportJSON, &report) != nil || billing.ValidateReport(report) != nil || report.UserID != user || report.ID != id {
		return report, entity.Fail("TOOL_OUTPUT_INVALID", "billing source integrity mismatch")
	}
	return report, nil
}
func trialProgress(trial ExpenseTrial, account entity.BankAccount, transactions []entity.Transaction, now time.Time) (ExpenseTrialProgress, error) {
	end := min(now.UnixMilli(), trial.EndsAt)
	out := ExpenseTrialProgress{From: trial.StartedAt, To: max(trial.StartedAt, end), EvidenceTransactionIDs: []string{}, WindowComplete: now.UnixMilli() >= trial.EndsAt}
	if end <= trial.StartedAt {
		return out, nil
	}
	from := time.UnixMilli(trial.StartedAt)
	to := time.UnixMilli(end)
	normalized, e := billing.Normalize(billing.Input{From: from.Format(time.RFC3339Nano), To: to.Format(time.RFC3339Nano), Timezone: "Asia/Shanghai", AccountID: &trial.AccountID, Limit: 100})
	if e != nil {
		return out, e
	}
	report, e := billing.Analyze(trial.UserID, "analyze_spending", normalized, billing.Snapshot{Accounts: []entity.BankAccount{account}, Transactions: transactions, At: now.UnixMilli()})
	if e != nil {
		return out, e
	}
	out.TotalExpenseMinor = report.TotalExpense
	for _, category := range report.CategoryBreakdown {
		if category.Category == trial.Category {
			out.CategoryExpenseMinor = category.ExpenseMinor
			out.EvidenceTransactionIDs = category.TransactionIDs
		}
	}
	// Missing entries and unequal comparison periods can never prove savings.
	return out, nil
}
func expenseTrialView(tx *gorm.DB, user int64, trial ExpenseTrial, now time.Time) (ExpenseTrialView, error) {
	out := ExpenseTrialView{Trial: trial, Baseline: billing.Category{Category: trial.Category, TransactionIDs: []string{}}}
	report, e := trialReport(tx, user, trial.BillingReportID)
	if e != nil {
		return out, e
	}
	if digest(report) != trial.BaselineDigest {
		return out, entity.Fail("VERSION_CONFLICT", "trial baseline changed")
	}
	out.BaselinePeriod = report.AnalysisPeriod
	for _, category := range report.CategoryBreakdown {
		if category.Category == trial.Category {
			out.Baseline = category
		}
	}
	source, e := expensePreview(report, ExpensePreviewInput{BillingReportID: trial.BillingReportID, TargetPeriod: trial.TargetPeriod, TargetMinor: trial.TargetMinor, AdjustableCategories: []string{trial.Category}}, time.UnixMilli(trial.StartedAt))
	if e != nil {
		return out, e
	}
	out.BaselineComplete = true
	for _, missing := range source.Result.MissingInformation {
		if missing == "complete_historical_month_billing_report" || missing == "budget" {
			out.BaselineComplete = false
		}
	}
	var account entity.BankAccount
	if e = tx.Where("user_id=? AND id=?", user, trial.AccountID).First(&account).Error; e != nil {
		return out, e
	}
	transactions := []entity.Transaction{}
	e = tx.Where("user_id=? AND bank_account_id=? AND kind='external' AND occurred_at>=? AND occurred_at<?", user, trial.AccountID, trial.StartedAt, min(now.UnixMilli(), trial.EndsAt)).Order("occurred_at,id").Limit(billing.MaxRows + 1).Find(&transactions).Error
	if e != nil {
		return out, e
	}
	out.Progress, e = trialProgress(trial, account, transactions, now)
	if e != nil {
		return out, e
	}
	if len(trial.ReviewJSON) > 0 {
		var state expenseTrialState
		if json.Unmarshal(trial.ReviewJSON, &state) != nil {
			return out, entity.Fail("TOOL_OUTPUT_INVALID", "invalid stored trial review")
		}
		if state.ObservedAt > 0 {
			out.Review = &state.ExpenseTrialReview
		}
		if state.Plan != nil {
			in := ExpenseTrialInput{AccountID: trial.AccountID, BillingReportID: trial.BillingReportID, TargetPeriod: trial.TargetPeriod, TargetMinor: trial.TargetMinor, Category: trial.Category, StatementCheck: state.Plan.StatementCheck, ExpectedReductionMinor: &state.Plan.ExpectedReductionMinor}
			if e = validateTrialInput(report, user, in, time.UnixMilli(trial.StartedAt)); e != nil {
				return out, e
			}
			if state.Plan.MonthlyLimitMinor != out.Baseline.ExpenseMinor-state.Plan.ExpectedReductionMinor {
				return out, entity.Fail("TOOL_OUTPUT_INVALID", "saved monthly reference changed")
			}
			out.Plan = state.Plan
			if out.Progress.WindowComplete {
				from, _ := time.Parse(time.RFC3339, report.AnalysisPeriod.From)
				referenceRows := []entity.Transaction{}
				if len(report.EvidenceTransactionIDs) > 0 {
					e = tx.Where("user_id=? AND bank_account_id=? AND id IN ? AND occurred_at>=? AND occurred_at<?", user, trial.AccountID, report.EvidenceTransactionIDs, from.UnixMilli(), from.AddDate(0, 0, 7).UnixMilli()).Limit(billing.MaxRows + 1).Find(&referenceRows).Error
					if e != nil {
						return out, e
					}
				}
				out.Comparison, e = trialReference(report, trial, out.Progress, referenceRows)
				if e != nil {
					return out, e
				}
			}
		}
	}
	return out, nil
}
func (s *Service) StartExpenseTrial(ctx context.Context, m service.Meta, key string, in ExpenseTrialInput) (ExpenseTrialView, error) {
	if in.AccountID <= 0 || len(in.BillingReportID) > 64 || !optionalTrialCategory(in.Category) {
		return ExpenseTrialView{}, entity.Fail("INVALID_ARGUMENT", "valid account, report and optional category required")
	}
	return atomic(s.repo, ctx, m.UserID, "lifestyle.expense_trial.start", key, in, func(tx *gorm.DB) (ExpenseTrialView, error) {
		now := time.Now()
		report, e := trialReport(tx, m.UserID, in.BillingReportID)
		if e != nil {
			return ExpenseTrialView{}, e
		}
		if e = validateTrialInput(report, m.UserID, in, now); e != nil {
			return ExpenseTrialView{}, e
		}
		var count int64
		if e = tx.Model(&ExpenseTrial{}).Where("user_id=?", m.UserID).Count(&count).Error; e != nil {
			return ExpenseTrialView{}, e
		}
		if count >= 1000 {
			return ExpenseTrialView{}, entity.Fail("ANALYSIS_LIMIT", "at most 1000 retained trials")
		}
		trial := ExpenseTrial{Base: entity.Base{UserID: m.UserID}, AccountID: in.AccountID, BillingReportID: in.BillingReportID, BaselineDigest: digest(report), TargetPeriod: in.TargetPeriod, TargetMinor: in.TargetMinor, Category: in.Category, StartedAt: now.UnixMilli(), EndsAt: now.AddDate(0, 0, 7).UnixMilli(), Status: "active", Version: 1}
		if in.StatementCheck != nil {
			var amount int64
			for _, c := range report.CategoryBreakdown {
				if c.Category == in.Category {
					amount = c.ExpenseMinor
				}
			}
			trial.ReviewJSON, e = json.Marshal(expenseTrialState{Plan: &ExpenseTrialPlan{ExpectedReductionMinor: *in.ExpectedReductionMinor, MonthlyLimitMinor: amount - *in.ExpectedReductionMinor, StatementCheck: in.StatementCheck}})
			if e != nil {
				return ExpenseTrialView{}, e
			}
		}
		if e = tx.Create(&trial).Error; e != nil {
			return ExpenseTrialView{}, e
		}
		if _, e = audit(tx, m, "lifestyle.expense_trial.start", trial.ID); e != nil {
			return ExpenseTrialView{}, e
		}
		return expenseTrialView(tx, m.UserID, trial, now)
	})
}
func (s *Service) ExpenseTrials(ctx context.Context, user, account int64, limit, offset int) ([]ExpenseTrialView, error) {
	out := []ExpenseTrialView{}
	if account <= 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "positive account scope required")
	}
	if _, _, e := page([]int{limit, offset}); e != nil {
		return out, e
	}
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		var owned entity.BankAccount
		if e := tx.Where("id=? AND user_id=?", account, user).First(&owned).Error; e != nil {
			return e
		}
		trials := []ExpenseTrial{}
		if e := tx.Where("user_id=? AND account_id=?", user, account).Order("id DESC").Limit(limit).Offset(offset).Find(&trials).Error; e != nil {
			return e
		}
		for _, trial := range trials {
			view, e := expenseTrialView(tx, user, trial, time.Now())
			if e != nil {
				return e
			}
			out = append(out, view)
		}
		return nil
	})
	return out, e
}
func (s *Service) ExpenseTrial(ctx context.Context, user, id int64) (ExpenseTrialView, error) {
	var out ExpenseTrialView
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		var trial ExpenseTrial
		if e := tx.Where("id=? AND user_id=?", id, user).First(&trial).Error; e != nil {
			return e
		}
		var e error
		out, e = expenseTrialView(tx, user, trial, time.Now())
		return e
	})
	return out, e
}
func (s *Service) ReviewExpenseTrial(ctx context.Context, m service.Meta, key string, id int64, in ExpenseTrialReviewInput) (ExpenseTrialView, error) {
	if id <= 0 || in.ExpectedVersion < 1 || len(in.Note) > 2000 || !utf8.ValidString(in.Note) {
		return ExpenseTrialView{}, entity.Fail("INVALID_ARGUMENT", "positive version and bounded UTF8 note required")
	}
	return atomic(s.repo, ctx, m.UserID, fmt.Sprintf("lifestyle.expense_trial.review.%d", id), key, in, func(tx *gorm.DB) (ExpenseTrialView, error) {
		var trial ExpenseTrial
		if e := tx.Where("id=? AND user_id=?", id, m.UserID).First(&trial).Error; e != nil {
			return ExpenseTrialView{}, e
		}
		if trial.Version != in.ExpectedVersion {
			return ExpenseTrialView{}, entity.Fail("VERSION_CONFLICT", "trial observation changed; reload before saving")
		}
		now := time.Now()
		view, e := expenseTrialView(tx, m.UserID, trial, now)
		if e != nil {
			return view, e
		}
		review := ExpenseTrialReview{ObservedAt: now.UnixMilli(), Note: in.Note, Progress: view.Progress}
		raw, e := json.Marshal(expenseTrialState{ExpenseTrialReview: review, Plan: view.Plan})
		if e != nil {
			return view, e
		}
		status := "active"
		if view.Progress.WindowComplete {
			status = "completed"
		}
		version := trial.Version + 1
		if e = tx.Model(&trial).Updates(map[string]any{"review_json": raw, "version": version, "status": status}).Error; e != nil {
			return view, e
		}
		trial.ReviewJSON, trial.Version, trial.Status = raw, version, status
		if _, e = audit(tx, m, "lifestyle.expense_trial.review", id); e != nil {
			return view, e
		}
		return expenseTrialView(tx, m.UserID, trial, now)
	})
}
