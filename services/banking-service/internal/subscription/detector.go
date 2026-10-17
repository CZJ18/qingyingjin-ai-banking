package subscription

import (
	"crypto/sha256"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
	"sort"
	"strings"
	"time"
)

type Candidate struct {
	Fingerprint             string   `json:"fingerprint"`
	AccountID               int64    `json:"account_id,string"`
	MerchantName            string   `json:"merchant_name"`
	Category                string   `json:"category"`
	Interval                string   `json:"billing_interval"`
	AmountMinor             int64    `json:"amount_minor"`
	NextBillingAt           int64    `json:"next_billing_at"`
	ConfidenceBPS           int64    `json:"confidence_bps"`
	ConfidenceBasis         string   `json:"confidence_basis"`
	PriceChanged            bool     `json:"price_changed"`
	UsageStatus             string   `json:"usage_status"`
	EvidenceTransactionIDs  []string `json:"evidence_transaction_ids"`
	DuplicateTransactionIDs []string `json:"duplicate_transaction_ids"`
	SubscriptionID          string   `json:"subscription_id"`
}
type ForecastResult struct {
	MonthlyMinor                  int64  `json:"monthly_minor"`
	ThreeMonthMinor               int64  `json:"three_month_minor"`
	AnnualMinor                   int64  `json:"annual_minor"`
	AvailableBalanceIncreaseMinor int64  `json:"available_balance_increase_minor"`
	Kind                          string `json:"kind"`
	Rounding                      string `json:"rounding"`
}

func Forecast(amount int64, interval string) (ForecastResult, error) {
	f := ForecastResult{Kind: "estimated_avoided_future_spend", Rounding: "floor_each_horizon; weekly=52/year; monthly=12/year"}
	if amount <= 0 || amount > 1000000000000 {
		return f, entity.Fail("INVALID_ARGUMENT", "price must be 1..1000000000000 integer CNY cents")
	}
	annual := amount
	switch interval {
	case "weekly":
		annual *= 52
	case "monthly":
		annual *= 12
	case "quarterly":
		annual *= 4
	case "annual":
	default:
		return f, entity.Fail("INVALID_ARGUMENT", "unsupported billing interval")
	}
	f.AnnualMinor = annual
	f.MonthlyMinor = annual / 12
	f.ThreeMonthMinor = annual / 4
	return f, nil
}
func fingerprint(account int64, merchant string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", account, strings.ToLower(strings.TrimSpace(merchant))))))
}
func NextCharge(last time.Time, interval string) time.Time {
	if interval == "weekly" {
		return last.AddDate(0, 0, 7)
	}
	months := 1
	switch interval {
	case "quarterly":
		months = 3
	case "annual":
		months = 12
	}
	first := time.Date(last.Year(), last.Month()+time.Month(months), 1, last.Hour(), last.Minute(), last.Second(), last.Nanosecond(), last.Location())
	day := last.Day()
	max := first.AddDate(0, 1, -1).Day()
	if day > max {
		day = max
	}
	return first.AddDate(0, 0, day-1)
}
func Detect(rows []entity.Transaction) ([]Candidate, error) {
	groups := map[string][]entity.Transaction{}
	for _, t := range rows {
		if t.Kind != "external" || t.Direction != "expense" || strings.TrimSpace(t.MerchantName) == "" {
			continue
		}
		if t.Currency != "CNY" || t.AmountMinor <= 0 || t.AmountMinor > 1000000000000 {
			return nil, entity.Fail("INVALID_TRANSACTION_DATA", "invalid subscription evidence")
		}
		k := fingerprint(t.BankAccountID, t.MerchantName)
		groups[k] = append(groups[k], t)
	}
	out := []Candidate{}
	for k, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			if group[i].OccurredAt == group[j].OccurredAt {
				return group[i].ID < group[j].ID
			}
			return group[i].OccurredAt < group[j].OccurredAt
		})
		unique := []entity.Transaction{}
		duplicates := map[int64]bool{}
		for _, t := range group {
			dupe := false
			for j := len(unique) - 1; j >= 0; j-- {
				p := unique[j]
				if t.OccurredAt-p.OccurredAt > 300000 {
					break
				}
				if t.AmountMinor == p.AmountMinor {
					duplicates[t.ID] = true
					duplicates[p.ID] = true
					dupe = true
					break
				}
			}
			if !dupe {
				unique = append(unique, t)
			}
		}
		if len(unique) < 3 {
			continue
		}
		cadence := ""
		for _, c := range []string{"weekly", "monthly", "quarterly", "annual"} {
			fits := true
			for i := 1; i < len(unique); i++ {
				a := time.UnixMilli(unique[i-1].OccurredAt).UTC()
				b := time.UnixMilli(unique[i].OccurredAt).UTC()
				delta := b.Sub(NextCharge(a, c))
				tol := 72 * time.Hour
				if c == "weekly" {
					tol = 24 * time.Hour
				}
				if delta < -tol || delta > tol {
					fits = false
					break
				}
			}
			if fits {
				cadence = c
				break
			}
		}
		if cadence == "" {
			continue
		}
		last := unique[len(unique)-1]
		bps := int64(7500)
		if len(unique) >= 4 {
			bps = 9000
		}
		c := Candidate{Fingerprint: k, AccountID: last.BankAccountID, MerchantName: strings.TrimSpace(last.MerchantName), Category: last.Category, Interval: cadence, AmountMinor: last.AmountMinor, NextBillingAt: NextCharge(time.UnixMilli(last.OccurredAt).UTC(), cadence).UnixMilli(), ConfidenceBPS: bps, ConfidenceBasis: fmt.Sprintf("rule_score_not_probability: %d distinct charges, all adjacent intervals fit %s; tolerance monthly/quarterly/annual 3 days, weekly 1 day", len(unique), cadence), UsageStatus: "unknown", EvidenceTransactionIDs: []string{}, DuplicateTransactionIDs: []string{}}
		for _, t := range group {
			c.EvidenceTransactionIDs = append(c.EvidenceTransactionIDs, fmt.Sprint(t.ID))
			if duplicates[t.ID] {
				c.DuplicateTransactionIDs = append(c.DuplicateTransactionIDs, fmt.Sprint(t.ID))
			}
		}
		for _, t := range unique {
			if t.AmountMinor != last.AmountMinor {
				c.PriceChanged = true
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fingerprint < out[j].Fingerprint })
	return out, nil
}
