package lifestyle

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Inject only the records requested by loadSnapshot. Any SQL access fails the
// test, so this regression never requires a database or runtime credentials.
type snapshotNoSQL struct{ t *testing.T }

func (c snapshotNoSQL) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	c.t.Fatal("snapshot regression attempted SQL prepare")
	return nil, nil
}
func (c snapshotNoSQL) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	c.t.Fatal("snapshot regression attempted SQL execution")
	return nil, nil
}
func (c snapshotNoSQL) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	c.t.Fatal("snapshot regression attempted SQL query")
	return nil, nil
}
func (c snapshotNoSQL) QueryRowContext(context.Context, string, ...any) *sql.Row {
	c.t.Fatal("snapshot regression attempted SQL row query")
	return nil
}

func snapshotFixture(t *testing.T, report billing.Report, accounts []entity.BankAccount) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: snapshotNoSQL{t}, SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := json.Marshal(Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 1,
		ExpenseReductionMonthlyMinor: 50000, AdjustableCategories: []string{"shopping"}})
	if err != nil {
		t.Fatal(err)
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		switch dest := tx.Statement.Dest.(type) {
		case *GoalVersion:
			*dest = GoalVersion{GoalID: 1, Version: 1, SpecJSON: specJSON}
		case *entity.Budget:
			*dest = entity.Budget{Base: entity.Base{ID: 2, UserID: 42}, TotalMinor: 200000}
		case *[]entity.BudgetCategory:
			*dest = []entity.BudgetCategory{{Category: "shopping", AllocatedMinor: 200000}}
		case *billing.StoredReport:
			*dest = billing.StoredReport{ID: report.ID, UserID: 42, ReportJSON: reportJSON}
		case *[]entity.BankAccount:
			*dest = append([]entity.BankAccount{}, accounts...)
		default:
			t.Fatalf("unexpected snapshot query destination %T", dest)
		}
		tx.RowsAffected = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLoadSnapshotHistoricalMonthRequiresVerifiedStatementCoverage(t *testing.T) {
	for _, timezone := range []string{"UTC", "Asia/Shanghai"} {
		t.Run(timezone, func(t *testing.T) {
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Date(2025, 9, 1, 0, 0, 0, 0, loc)
			end := start.AddDate(0, 1, 0)
			period, err := billing.Normalize(billing.Input{From: start.Format(time.RFC3339), To: end.Format(time.RFC3339), Timezone: timezone})
			if err != nil {
				t.Fatal(err)
			}
			account := entity.BankAccount{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}
			for _, tc := range []struct {
				name string
				at   int64
			}{
				{"missing_snapshot_time", 0},
				{"old_midmonth_snapshot", start.AddDate(0, 0, 14).UnixMilli()},
				{"one_millisecond_before_month_end", end.UnixMilli() - 1},
				{"at_month_end", end.UnixMilli()},
				{"after_month_end", end.Add(time.Hour).UnixMilli()},
			} {
				t.Run(tc.name, func(t *testing.T) {
					report, err := billing.Analyze(42, "generate_billing_report", period, billing.Snapshot{
						At: tc.at, Accounts: []entity.BankAccount{account}, Transactions: []entity.Transaction{{
							Base: entity.Base{ID: 8, UserID: 42}, BankAccountID: 7, Currency: "CNY",
							Kind: "external", Direction: "expense", Category: "shopping", AmountMinor: 200000,
							OccurredAt: start.Add(time.Hour).UnixMilli(),
						}},
					})
					if err != nil {
						t.Fatal(err)
					}
					snap, err := loadSnapshot(snapshotFixture(t, report, []entity.BankAccount{account}), 42,
						Goal{Base: entity.Base{ID: 1, UserID: 42}, CurrentVersion: 1},
						CompileInput{ExpectedVersion: 1, BudgetID: 2, BillingReportID: report.ID})
					if err != nil {
						t.Fatal(err)
					}
					result, err := Calculate(snap)
					if err != nil {
						t.Fatal(err)
					}
					if snap.CompleteMonth {
						t.Fatalf("snapshot time %d was treated as statement coverage; result=%+v", tc.at, result)
					}
					if result.Status != "needs_information" || len(result.Options) != 0 ||
						!has(result.MissingInformation, "complete_historical_month_billing_report") {
						t.Fatalf("unverified statement coverage generated a forecast: %+v", result)
					}
					if len(snap.EvidenceTransactionIDs) != 1 || len(snap.Categories) != 1 || snap.Categories[0].ObservedMinor != 200000 {
						t.Fatalf("recorded observations were lost: %+v", snap)
					}
				})
			}
		})
	}
}
