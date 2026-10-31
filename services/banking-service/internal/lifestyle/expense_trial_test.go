package lifestyle

import (
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestExpenseTrialKeepsRecordedProgressWithoutClaimingVerifiedBaseline(t *testing.T) {
	start := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	account := entity.BankAccount{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}
	period, err := billing.Normalize(billing.Input{From: "2026-08-31T16:00:00Z", To: "2026-09-30T16:00:00Z", Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := billing.Analyze(42, "generate_billing_report", period, billing.Snapshot{At: start.UnixMilli(),
		Accounts: []entity.BankAccount{account}, Transactions: []entity.Transaction{{
			Base: entity.Base{ID: 8, UserID: 42}, BankAccountID: 7, Currency: "CNY", Kind: "external",
			Direction: "expense", Category: "shopping", AmountMinor: 200000,
			OccurredAt: time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC).UnixMilli(),
		}}})
	if err != nil {
		t.Fatal(err)
	}
	db := snapshotFixture(t, report, []entity.BankAccount{account})
	originalQuery := db.Callback().Query().Get("gorm:query")
	err = db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		switch dest := tx.Statement.Dest.(type) {
		case *entity.BankAccount:
			*dest = account
		case *[]entity.Transaction:
			*dest = []entity.Transaction{{Base: entity.Base{ID: 9, UserID: 42}, BankAccountID: 7, Currency: "CNY",
				Kind: "external", Direction: "expense", Category: "shopping", AmountMinor: 6000, OccurredAt: start.UnixMilli()}}
		default:
			originalQuery(tx)
		}
		tx.RowsAffected = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	trial := ExpenseTrial{Base: entity.Base{ID: 1, UserID: 42}, AccountID: 7, BillingReportID: report.ID,
		BaselineDigest: digest(report), TargetPeriod: "2026-11", TargetMinor: 50000, Category: "shopping",
		StartedAt: start.UnixMilli(), EndsAt: start.AddDate(0, 0, 7).UnixMilli()}
	view, err := expenseTrialView(db, 42, trial, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if view.BaselineComplete || view.Baseline.ExpenseMinor != 200000 || view.Progress.CategoryExpenseMinor != 6000 ||
		len(view.Progress.EvidenceTransactionIDs) != 1 || view.Progress.ConfirmedSavingsMinor != nil || view.FinancialExecutionEnabled {
		t.Fatalf("unverified baseline must retain recorded progress without claiming savings: %+v", view)
	}
}

func TestExpenseTrialOnlyExplicitOptionalCategoryAndOwnedSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	report := previewReport()
	report.Accounts = []entity.BankAccount{{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}}
	good := ExpenseTrialInput{AccountID: 7, BillingReportID: report.ID, TargetPeriod: "2026-11", TargetMinor: 50000, Category: "shopping"}
	if e := validateTrialInput(report, 42, good, now); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*ExpenseTrialInput){func(v *ExpenseTrialInput) { v.AccountID = 9 }, func(v *ExpenseTrialInput) { v.Category = "dining" }, func(v *ExpenseTrialInput) { v.Category = "housing" }, func(v *ExpenseTrialInput) { v.Category = "unknown" }, func(v *ExpenseTrialInput) { v.TargetMinor = 0 }, func(v *ExpenseTrialInput) { v.TargetPeriod = "2026-12" }} {
		bad := good
		change(&bad)
		if validateTrialInput(report, 42, bad, now) == nil {
			t.Fatal("invalid or expanded trial accepted", bad)
		}
	}
	if validateTrialInput(report, 99, good, now) == nil {
		t.Fatal("cross-user report accepted")
	}
}

func TestExpenseTrialProgressUsesReadOnlyAccountCategoryAndHalfOpenWindow(t *testing.T) {
	start := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	trial := ExpenseTrial{Base: entity.Base{ID: 1, UserID: 42}, AccountID: 7, Category: "sports", StartedAt: start.UnixMilli(), EndsAt: start.AddDate(0, 0, 7).UnixMilli()}
	account := entity.BankAccount{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}
	makeTx := func(id, accountID, amount int64, category, kind, direction string, at time.Time) entity.Transaction {
		return entity.Transaction{Base: entity.Base{ID: id, UserID: 42}, BankAccountID: accountID, Currency: "CNY", AmountMinor: amount, Category: category, Kind: kind, Direction: direction, OccurredAt: at.UnixMilli()}
	}
	txs := []entity.Transaction{makeTx(1, 7, 6000, "sports", "external", "expense", start), makeTx(2, 7, 18000, "dining", "external", "expense", start.Add(time.Hour)), makeTx(3, 8, 99999, "sports", "external", "expense", start), makeTx(4, 7, 9999, "sports", "external", "income", start), makeTx(5, 7, 9999, "sports", "transfer", "expense", start), makeTx(6, 7, 777, "sports", "external", "expense", start.AddDate(0, 0, 7))}
	progress, e := trialProgress(trial, account, txs, start.AddDate(0, 0, 8))
	if e != nil {
		t.Fatal(e)
	}
	if progress.CategoryExpenseMinor != 6000 || progress.TotalExpenseMinor != 24000 || len(progress.EvidenceTransactionIDs) != 1 || progress.EvidenceTransactionIDs[0] != "1" || !progress.WindowComplete || progress.ConfirmedSavingsMinor != nil {
		t.Fatalf("unsafe trial review: %+v", progress)
	}
	early, e := trialProgress(trial, account, txs, start.Add(30*time.Minute))
	if e != nil || early.WindowComplete || early.TotalExpenseMinor != 6000 || early.ConfirmedSavingsMinor != nil {
		t.Fatal(early, e)
	}
	empty, e := trialProgress(trial, account, nil, start)
	if e != nil || empty.ConfirmedSavingsMinor != nil || empty.WindowComplete {
		t.Fatal(empty, e)
	}
}
