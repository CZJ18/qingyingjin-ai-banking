package lifestyle

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func checkedMonth(t *testing.T) billing.Report {
	t.Helper()
	account := entity.BankAccount{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}
	period, e := billing.Normalize(billing.Input{From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z", Timezone: "Asia/Shanghai", AccountID: &account.ID, Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	r, e := billing.Analyze(42, "generate_billing_report", period, billing.Snapshot{Accounts: []entity.BankAccount{account}, At: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC).UnixMilli(), Transactions: []entity.Transaction{
		{Base: entity.Base{ID: 1, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "expense", Currency: "CNY", Category: "shopping", AmountMinor: 200000, OccurredAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{Base: entity.Base{ID: 2, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "expense", Currency: "CNY", Category: "dining", AmountMinor: 100000, OccurredAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{Base: entity.Base{ID: 3, UserID: 42}, BankAccountID: 7, Kind: "external", Direction: "income", Currency: "CNY", Category: "salary", AmountMinor: 500000, OccurredAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()},
	}})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func i64(v int64) *int64 { return &v }
func statementCheck() *ExpenseStatementCheck {
	return &ExpenseStatementCheck{FullMonthConfirmed: true, TransactionCount: i64(3), IncomeMinor: i64(500000), ExpenseMinor: i64(300000)}
}

func TestExpenseStatementCheckedTotalsProduceConditionalOptions(t *testing.T) {
	r := checkedMonth(t)
	in := ExpensePreviewInput{BillingReportID: r.ID, TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}, StatementCheck: statementCheck()}
	v, e := expensePreview(r, in, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC))
	if e != nil || v.BaselineStatus != "user_reconciled" || v.Result.Status != "ready" || len(v.Result.Options) != 3 {
		t.Fatalf("%+v %v", v, e)
	}
	if v.Result.Options[2].ExpenseReductionMinor != 50000 || v.Result.Options[2].Categories[0].Protected && v.Result.Options[2].Categories[0].ProposedMinor != v.Result.Options[2].Categories[0].OriginalMinor {
		t.Fatal("amounts or necessary spending changed", v)
	}
	if v.Result.FinancialExecutionEnabled || v.Source != "deterministic_lifestyle_rules" {
		t.Fatal("forecast became authorization")
	}
}
func TestExpenseStatementMissingMismatchingOrPartialMonthCannotPass(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	r := checkedMonth(t)
	for _, change := range []func(*ExpenseStatementCheck){func(c *ExpenseStatementCheck) { c.FullMonthConfirmed = false }, func(c *ExpenseStatementCheck) { c.TransactionCount = nil }, func(c *ExpenseStatementCheck) { c.IncomeMinor = nil }, func(c *ExpenseStatementCheck) { c.ExpenseMinor = nil }, func(c *ExpenseStatementCheck) { c.ExpenseMinor = i64(299999) }, func(c *ExpenseStatementCheck) { c.TransactionCount = i64(4) }, func(c *ExpenseStatementCheck) { c.IncomeMinor = i64(-1) }} {
		in := ExpensePreviewInput{BillingReportID: r.ID, TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}, StatementCheck: statementCheck()}
		change(in.StatementCheck)
		if _, e := expensePreview(r, in, now); e == nil {
			t.Fatal("missing/mismatching declaration accepted", in.StatementCheck)
		}
	}
	in := ExpensePreviewInput{BillingReportID: r.ID, TargetPeriod: "2026-11", TargetMinor: 50000, AdjustableCategories: []string{"shopping"}, StatementCheck: statementCheck()}
	for _, change := range []func(*billing.Report){func(v *billing.Report) { v.AnalysisPeriod.From = "2026-09-01T16:00:00Z" }, func(v *billing.Report) { v.SnapshotAt = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC).UnixMilli() }, func(v *billing.Report) { v.AnalysisPeriod.Timezone = "UTC" }, func(v *billing.Report) { v.Accounts = append(v.Accounts, v.Accounts[0]) }} {
		v := r
		change(&v)
		if _, e := expensePreview(v, in, now); e == nil {
			t.Fatal("partial/ambiguous month accepted")
		}
	}
	in.StatementCheck = nil
	v, e := expensePreview(r, in, now)
	if e != nil || v.BaselineStatus != "unverified" || v.Result.Status != "needs_information" || len(v.Result.Options) != 0 {
		t.Fatal("legacy coverage silently upgraded", v, e)
	}
}
func TestExpenseStatementInsufficientOptionalSpacePreservesTargetAndProtectedCosts(t *testing.T) {
	r := checkedMonth(t)
	v, e := expensePreview(r, ExpensePreviewInput{BillingReportID: r.ID, TargetPeriod: "2026-11", TargetMinor: 250000, AdjustableCategories: []string{"shopping", "dining"}, StatementCheck: statementCheck()}, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC))
	if e != nil || v.Result.Status != "conflict" || v.Result.ReductionGapMinor != 50000 || v.TargetMinor != 250000 {
		t.Fatal(v, e)
	}
	for _, o := range v.Result.Options {
		for _, c := range o.Categories {
			if c.Category == "dining" && c.ProposedMinor != 100000 {
				t.Fatal("necessary meals cut")
			}
		}
	}
}

func TestExpenseStatementTrialPlanRequiresMatchingCheckAndKeepsLegacyReview(t *testing.T) {
	r := checkedMonth(t)
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	in := ExpenseTrialInput{AccountID: 7, BillingReportID: r.ID, TargetPeriod: "2026-11", TargetMinor: 50000, Category: "shopping", StatementCheck: statementCheck(), ExpectedReductionMinor: i64(30000)}
	if e := validateTrialInput(r, 42, in, now); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*ExpenseTrialInput){func(v *ExpenseTrialInput) { v.ExpectedReductionMinor = nil }, func(v *ExpenseTrialInput) { v.StatementCheck = nil }, func(v *ExpenseTrialInput) { v.ExpectedReductionMinor = i64(50001) }, func(v *ExpenseTrialInput) { v.Category = "dining" }, func(v *ExpenseTrialInput) { v.AccountID = 8 }} {
		bad := in
		change(&bad)
		if validateTrialInput(r, 42, bad, now) == nil {
			t.Fatal("expanded/unchecked trial plan accepted")
		}
	}
	plan := &ExpenseTrialPlan{ExpectedReductionMinor: 30000, MonthlyLimitMinor: 170000, StatementCheck: statementCheck()}
	stored, e := json.Marshal(expenseTrialState{Plan: plan})
	if e != nil {
		t.Fatal(e)
	}
	var state expenseTrialState
	if json.Unmarshal(stored, &state) != nil || state.Plan.ExpectedReductionMinor != 30000 || state.ObservedAt != 0 {
		t.Fatal("new plan was fabricated as an observation")
	}
	raw := []byte(`{"observed_at":123,"note":"legacy","progress":{"from":1,"to":2,"total_expense_minor":3,"category_expense_minor":2,"evidence_transaction_ids":[],"window_complete":false,"confirmed_savings_minor":null}}`)
	state = expenseTrialState{}
	if json.Unmarshal(raw, &state) != nil || state.Note != "legacy" || state.Plan != nil {
		t.Fatal("old review unreadable")
	}
}
