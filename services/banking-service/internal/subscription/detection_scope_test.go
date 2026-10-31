package subscription

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
)

// This fixture uses GORM's actual query builder and injected synthetic records.
// Any SQL execution or connection attempt fails; no runtime environment is read.
type detectionNoSQL struct{ t *testing.T }

func (c detectionNoSQL) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	c.t.Fatal("detection regression attempted SQL prepare")
	return nil, nil
}
func (c detectionNoSQL) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	c.t.Fatal("detection regression attempted SQL execution")
	return nil, nil
}
func (c detectionNoSQL) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	c.t.Fatal("detection regression attempted SQL query")
	return nil, nil
}
func (c detectionNoSQL) QueryRowContext(context.Context, string, ...any) *sql.Row {
	c.t.Fatal("detection regression attempted SQL row query")
	return nil
}
func (c detectionNoSQL) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	return c, nil
}
func (c detectionNoSQL) Commit() error   { return nil }
func (c detectionNoSQL) Rollback() error { return nil }

type detectionFixture struct {
	service *Service
	queries []string
}

func newDetectionFixture(t *testing.T, includeSelected bool) *detectionFixture {
	t.Helper()
	f := &detectionFixture{}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: detectionNoSQL{t}, SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	subs := []entity.Subscription{{Base: entity.Base{ID: 1, UserID: 42}, BankAccountID: 3, MerchantName: "other account", Category: "subscription", BillingInterval: "monthly", AmountMinor: 100, Currency: "CNY", ConfidenceBPS: 9000, Status: "active", Version: 1, IsSimulated: true}}
	if includeSelected {
		subs = append(subs, entity.Subscription{Base: entity.Base{ID: 2, UserID: 42}, BankAccountID: 23, MerchantName: "selected account", Category: "subscription", BillingInterval: "monthly", AmountMinor: 200, Currency: "CNY", ConfidenceBPS: 9000, Status: "active", Version: 1, IsSimulated: true})
	}
	payments := map[int64][]entity.SubscriptionPayment{
		1: {{Base: entity.Base{ID: 1, UserID: 42}, SubscriptionID: 1, TransactionID: 11, AmountMinor: 100, OccurredAt: start}},
		2: {
			{Base: entity.Base{ID: 2, UserID: 42}, SubscriptionID: 2, TransactionID: 12, AmountMinor: 100, OccurredAt: start - 1},
			{Base: entity.Base{ID: 3, UserID: 42}, SubscriptionID: 2, TransactionID: 13, AmountMinor: 200, OccurredAt: start},
			{Base: entity.Base{ID: 4, UserID: 42}, SubscriptionID: 2, TransactionID: 14, AmountMinor: 300, OccurredAt: end - 1},
			{Base: entity.Base{ID: 5, UserID: 42}, SubscriptionID: 2, TransactionID: 15, AmountMinor: 400, OccurredAt: end},
		},
	}
	err = db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		callbacks.BuildQuerySQL(tx)
		query := tx.Statement.SQL.String()
		f.queries = append(f.queries, query)
		switch dest := tx.Statement.Dest.(type) {
		case *entity.User:
			*dest = entity.User{ID: 42, Status: "active"}
		case *entity.BankAccount:
			*dest = entity.BankAccount{Base: entity.Base{ID: 23, UserID: 42}, Currency: "CNY"}
		case *[]entity.Transaction:
			*dest = []entity.Transaction{}
		case *[]entity.Subscription:
			*dest = []entity.Subscription{}
			for _, sub := range subs {
				if strings.Contains(query, "bank_account_id=?") && sub.BankAccountID != 23 {
					continue
				}
				*dest = append(*dest, sub)
			}
		case *[]entity.SubscriptionPayment:
			*dest = []entity.SubscriptionPayment{}
			id := tx.Statement.Vars[1].(int64)
			for _, payment := range payments[id] {
				if strings.Contains(query, "occurred_at>=?") && (payment.OccurredAt < start || payment.OccurredAt >= end) {
					continue
				}
				*dest = append(*dest, payment)
			}
		case *[]entity.SubscriptionActionIntent:
			*dest = []entity.SubscriptionActionIntent{}
		default:
			t.Fatalf("unexpected query destination %T", dest)
		}
		tx.RowsAffected = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
		switch value := tx.Statement.Dest.(type) {
		case *entity.SubscriptionAnalysis:
			value.ID = 7
		case *entity.Audit:
		default:
			t.Fatalf("unexpected write to synthetic fixture: %T", value)
		}
		tx.RowsAffected = 1
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Update().Replace("gorm:update", func(tx *gorm.DB) { tx.RowsAffected = 1 }); err != nil {
		t.Fatal(err)
	}
	f.service = New(repository.New(db), nil, nil)
	return f
}

func TestDetectionReportKeepsSelectedAccountAndHalfOpenPeriod(t *testing.T) {
	f := newDetectionFixture(t, true)
	out, err := f.service.Analyze(context.Background(), service.Meta{UserID: 42}, "detect_subscriptions", DetectInput{AccountID: "23", From: "2026-09-01T00:00:00Z", To: "2026-10-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Subscriptions) != 1 || out.Subscriptions[0].Subscription.BankAccountID != 23 {
		t.Fatalf("selected-account detection leaked other accounts: %+v", out.Subscriptions)
	}
	d := out.Subscriptions[0]
	if len(d.Payments) != 2 || d.Payments[0].TransactionID != 13 || d.Payments[1].TransactionID != 14 || out.TotalRecordedExpenseMinor != 500 || strings.Join(out.EvidenceTransactionIDs, ",") != "13,14" {
		t.Fatalf("detection report or aggregates escaped half-open interval: payments=%+v total=%d evidence=%v", d.Payments, out.TotalRecordedExpenseMinor, out.EvidenceTransactionIDs)
	}
	if len(d.OperationHistory) != 0 {
		t.Fatal("detection included unrelated operation history")
	}
	queryLog := strings.Join(f.queries, "\n")
	for _, want := range []string{"bank_account_id=?", "sp.occurred_at>=?", "sp.occurred_at<?", "subscription_id=? AND occurred_at>=? AND occurred_at<?"} {
		if !strings.Contains(queryLog, want) {
			t.Fatalf("required SQL scope absent: %s\n%s", want, queryLog)
		}
	}
}

func TestDetectionEmptySelectedAccountDoesNotReuseOtherAccounts(t *testing.T) {
	f := newDetectionFixture(t, false)
	out, err := f.service.Analyze(context.Background(), service.Meta{UserID: 42}, "detect_subscriptions", DetectInput{AccountID: "23", From: "2026-09-01T00:00:00Z", To: "2026-10-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Subscriptions) != 0 || len(out.Candidates) != 0 || len(out.EvidenceTransactionIDs) != 0 || out.TotalRecordedExpenseMinor != 0 || out.ActiveRecurringForecast.MonthlyMinor != 0 || out.CancelledAvoidedSpend.MonthlyMinor != 0 {
		t.Fatalf("empty selected scope reused unrelated subscriptions: %+v", out)
	}
}

func TestLegacyCostReportKeepsAllLinkedEvidence(t *testing.T) {
	f := newDetectionFixture(t, true)
	out, err := f.service.Analyze(context.Background(), service.Meta{UserID: 42}, "analyze_subscription_cost", DetectInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Subscriptions) != 2 || out.TotalRecordedExpenseMinor != 1100 || len(out.EvidenceTransactionIDs) != 5 {
		t.Fatal(fmt.Sprintf("unscoped existing cost report semantics changed: %+v", out))
	}
}
