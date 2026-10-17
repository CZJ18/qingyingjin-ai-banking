package subscription

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func TestRecurringEvidence(t *testing.T) {
	rows := []entity.Transaction{}
	for i, day := range []string{"2026-01-31", "2026-02-28", "2026-03-31"} {
		d, _ := time.Parse("2006-01-02", day)
		rows = append(rows, entity.Transaction{Base: entity.Base{ID: int64(i + 1), UserID: 42}, BankAccountID: 3, Kind: "external", Direction: "expense", Currency: "CNY", MerchantName: "Fitness", Category: "sports", AmountMinor: 2000 + int64(i)*100, OccurredAt: d.UnixMilli()})
	}
	rows = append(rows, rows[2])
	rows[3].ID = 4
	rows[3].OccurredAt += 60000
	got, e := Detect(rows)
	if e != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, e)
	}
	c := got[0]
	if c.Interval != "monthly" || !c.PriceChanged || len(c.DuplicateTransactionIDs) != 2 || c.ConfidenceBPS < 7000 || len(c.EvidenceTransactionIDs) != 4 {
		t.Fatalf("%+v", c)
	}
	if c.UsageStatus != "unknown" {
		t.Fatal("payments do not prove inactivity")
	}
}
func TestInsufficientOrIrregularEvidence(t *testing.T) {
	rows := []entity.Transaction{{Base: entity.Base{ID: 1}, BankAccountID: 2, MerchantName: "Shop", Currency: "CNY", Kind: "external", Direction: "expense", AmountMinor: 1200, OccurredAt: 1000000000000}}
	got, e := Detect(rows)
	if e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
	rows = append(rows, rows[0])
	rows[1].ID = 2
	rows[1].OccurredAt += 60000
	got, e = Detect(rows)
	if e != nil || len(got) != 0 {
		t.Fatal("duplicate charges are not recurring evidence", got, e)
	}
}
func TestForecastRounding(t *testing.T) {
	f, e := Forecast(1000, "weekly")
	if e != nil || f.MonthlyMinor != 4333 || f.ThreeMonthMinor != 13000 || f.AnnualMinor != 52000 || f.AvailableBalanceIncreaseMinor != 0 {
		t.Fatal(f, e)
	}
	f, e = Forecast(24000, "annual")
	if e != nil || f.MonthlyMinor != 2000 || f.AnnualMinor != 24000 {
		t.Fatal(f, e)
	}
	if _, e = Forecast(-1, "monthly"); e == nil {
		t.Fatal("negative price accepted")
	}
}

func TestIrregularAndExcludedFacts(t *testing.T) {
	rows := []entity.Transaction{}
	for i, day := range []string{"2026-01-01", "2026-01-20", "2026-03-30"} {
		d, _ := time.Parse("2006-01-02", day)
		rows = append(rows, entity.Transaction{Base: entity.Base{ID: int64(i + 1)}, BankAccountID: 1, MerchantName: "Shop", Currency: "CNY", Kind: "external", Direction: "expense", AmountMinor: 1000, OccurredAt: d.UnixMilli()})
	}
	got, e := Detect(rows)
	if e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
	for i := range rows {
		rows[i].Kind = "internal_transfer"
	}
	got, e = Detect(rows)
	if e != nil || len(got) != 0 {
		t.Fatal("internal money is not subscription", got, e)
	}
	rows[0].Kind = "external"
	rows[0].AmountMinor = -1
	if _, e = Detect(rows); e == nil {
		t.Fatal("invalid evidence accepted")
	}
}
func TestNextChargeEndOfMonth(t *testing.T) {
	for _, pair := range []struct{ from, interval, want string }{{"2026-01-31", "monthly", "2026-02-28"}, {"2024-02-29", "annual", "2025-02-28"}, {"2026-11-30", "quarterly", "2027-02-28"}, {"2026-12-30", "weekly", "2027-01-06"}} {
		d, _ := time.Parse("2006-01-02", pair.from)
		if got := NextCharge(d, pair.interval).Format("2006-01-02"); got != pair.want {
			t.Fatal(got, pair.want)
		}
	}
}
