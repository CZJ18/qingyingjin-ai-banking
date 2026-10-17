package billing

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func rows() []entity.Transaction {
	return []entity.Transaction{{Base: entity.Base{ID: 1, UserID: 42}, BankAccountID: 1, AmountMinor: 2000, Direction: "expense", Category: "餐饮", MerchantName: "模拟餐厅", Currency: "CNY", Kind: "external", OccurredAt: time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC).UnixMilli()}, {Base: entity.Base{ID: 2, UserID: 42}, BankAccountID: 1, AmountMinor: 2000, Direction: "expense", Category: "餐饮", MerchantName: "模拟餐厅", Currency: "CNY", Kind: "external", OccurredAt: time.Date(2026, 1, 31, 12, 1, 0, 0, time.UTC).UnixMilli()}}
}
func TestAnalysis(t *testing.T) {
	in := Input{From: "2026-01-01T00:00:00Z", To: "2026-03-01T00:00:00Z", Timezone: "UTC"}
	p, e := Normalize(in)
	if e != nil {
		t.Fatal(e)
	}
	empty, e := Analyze(42, "analyze_spending", p, Snapshot{})
	if e != nil || empty.TotalExpense != 0 || len(empty.Anomalies) != 0 {
		t.Fatal(empty, e)
	}
	s := Snapshot{Transactions: rows()}
	r, e := Analyze(42, "detect_anomalies", p, s)
	if e != nil || r.TotalExpense != 4000 || r.CategoryBreakdown[0].Category != "dining" || len(r.Anomalies) != 1 || r.PotentialSavings.ConfirmedMinor != 0 {
		t.Fatal(r, e)
	}
	s.Transactions[0].Category = "invalid-category"
	r, e = Analyze(42, "generate_billing_report", p, s)
	if e != nil || len(r.Anomalies) < 2 {
		t.Fatal(r, e)
	}
	s.Transactions[0].AmountMinor = 200000
	r, e = Analyze(42, "detect_anomalies", p, s)
	if e != nil || len(r.Anomalies) < 2 {
		t.Fatal(r, e)
	}
	s.Transactions[0].Kind = "internal_transfer"
	r, e = Analyze(42, "analyze_spending", p, s)
	if e != nil || r.TotalExpense != 2000 {
		t.Fatal("transfer counted as expense", r, e)
	}
}

func TestTrendTimezoneAndIntegerSafety(t *testing.T) {
	in := Input{From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z", Timezone: "Asia/Shanghai"}
	p, _ := Normalize(in)
	tx := rows()[0]
	tx.AmountMinor = 200000
	tx.OccurredAt = time.Date(2026, 1, 31, 16, 0, 0, 0, time.UTC).UnixMilli()
	s := Snapshot{Transactions: []entity.Transaction{tx}}
	for i := 0; i < 5; i++ {
		old := tx
		old.ID = int64(i + 10)
		old.AmountMinor = 1000
		old.OccurredAt = time.Date(2025, 12, 5+i, 12, 0, 0, 0, time.UTC).UnixMilli()
		s.Transactions = append(s.Transactions, old)
	}
	r, e := Analyze(42, "detect_anomalies", p, s)
	if e != nil || r.Trend.ChangeBasisPoints == nil || *r.Trend.ChangeBasisPoints != 390000 || r.Daily[0].Label != "2026-02-01" || r.Anomalies[0].Type != "unusual_amount" {
		t.Fatal(r, e)
	}
	if e := ValidateReport(r); e != nil {
		t.Fatal(e)
	}
	var n int64 = 9223372036854775807
	if add(&n, 1) == nil {
		t.Fatal("integer overflow accepted")
	}
	for _, in := range []Input{{From: "bad", To: "bad"}, {From: "2026-02-01T00:00:00Z", To: "2026-01-01T00:00:00Z"}, {From: "2024-01-01T00:00:00Z", To: "2026-01-01T00:00:00Z"}} {
		if _, e := Normalize(in); e == nil {
			t.Fatal("invalid period accepted")
		}
	}
}
