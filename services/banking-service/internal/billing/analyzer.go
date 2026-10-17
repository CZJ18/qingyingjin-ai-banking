package billing

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

const MaxRows = 10000

type Normalized struct {
	Input
	Start, End, LoadFrom, LoadTo time.Time
	Location                     *time.Location
}

func Normalize(in Input) (Normalized, error) {
	p := Normalized{Input: in}
	bad := func() (Normalized, error) {
		return p, entity.Fail("INVALID_ARGUMENT", "RFC3339 half-open period <=366 days, positive account ID, timezone UTC/Asia/Shanghai and bounded pagination required")
	}
	if in.Timezone == "" {
		p.Timezone = "Asia/Shanghai"
	}
	if p.Timezone != "UTC" && p.Timezone != "Asia/Shanghai" {
		return bad()
	}
	p.Location, _ = time.LoadLocation(p.Timezone)
	var e error
	p.Start, e = time.Parse(time.RFC3339, in.From)
	if e != nil {
		return bad()
	}
	p.End, e = time.Parse(time.RFC3339, in.To)
	if e != nil {
		return bad()
	}
	if p.Start.Year() < 1971 || p.End.Year() > 2100 || !p.End.After(p.Start) || p.End.Sub(p.Start) > 366*24*time.Hour || in.AccountID != nil && *in.AccountID <= 0 || in.Offset < 0 || in.Offset > MaxRows || in.Limit < 0 || in.Limit > 100 {
		return bad()
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	floor := func(t time.Time) *time.Time {
		u := t.UTC()
		v := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
		return &v
	}
	p.LoadFrom = p.Start.Add(-p.End.Sub(p.Start))
	month := *floor(p.Start)
	if month.Before(p.LoadFrom) {
		p.LoadFrom = month
	}
	p.LoadTo = (*floor(p.End.Add(-time.Nanosecond))).AddDate(0, 1, 0)
	return p, nil
}
func category(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "dining", "food", "餐饮", "饮食", "groceries", "食品":
		return "dining", true
	case "transport", "transportation", "交通":
		return "transport", true
	case "shopping", "购物":
		return "shopping", true
	case "sports", "exercise", "fitness", "运动":
		return "sports", true
	case "subscription", "subscriptions", "订阅":
		return "subscriptions", true
	case "housing", "住房", "rent":
		return "housing", true
	case "medical", "healthcare", "医疗":
		return "medical", true
	case "education", "教育":
		return "education", true
	case "other", "其他":
		return "other", true
	default:
		return "uncategorized", false
	}
}
func evidence(t entity.Transaction) Evidence {
	cat, _ := category(t.Category)
	return Evidence{t.ID, t.BankAccountID, t.OccurredAt, t.AmountMinor, t.Direction, t.Category, cat, t.MerchantName, t.EffectMode}
}
func txID(t entity.Transaction) string { return fmt.Sprint(t.ID) }
func add(dest *int64, n int64) error {
	if n < 0 || *dest > math.MaxInt64-n {
		return entity.Fail("ANALYSIS_LIMIT", "analysis amount overflow")
	}
	*dest += n
	return nil
}
func Analyze(user int64, tool string, p Normalized, s Snapshot) (Report, error) {
	r := Report{ID: uuid.NewString(), UserID: user, Tool: tool, AnalysisPeriod: Period{p.From, p.To, p.Timezone}, Currency: "CNY", CategoryBreakdown: []Category{}, BudgetStatus: []BudgetStatus{}, Anomalies: []Anomaly{}, PotentialSavings: Savings{ReviewCandidates: []Candidate{}}, EvidenceTransactionIDs: []string{}, Daily: []Bucket{}, Weekly: []Bucket{}, Monthly: []Bucket{}, Transactions: []Evidence{}, Accounts: []entity.BankAccount{}, Findings: []Finding{}, SnapshotAt: s.At}
	r.Trend = Trend{PreviousPeriod: Period{p.Start.Add(-p.End.Sub(p.Start)).Format(time.RFC3339), p.From, p.Timezone}, PreviousTransactionIDs: []string{}, Status: "insufficient_baseline"}
	validTool := false
	for _, name := range Tools {
		if name == tool {
			validTool = true
		}
	}
	if !validTool {
		return r, entity.Fail("INVALID_ARGUMENT", "unknown read-only billing tool")
	}
	if len(s.Transactions) > MaxRows {
		return r, entity.Fail("ANALYSIS_LIMIT", "too many transactions; shorten the period")
	}
	for _, a := range s.Accounts {
		if a.UserID != user {
			return r, entity.Fail("FORBIDDEN", "snapshot owner mismatch")
		}
		if p.AccountID == nil || a.ID == *p.AccountID {
			r.Accounts = append(r.Accounts, a)
		}
	}
	cats := map[string]*Category{}
	daily, weekly, monthly := map[string]*Bucket{}, map[string]*Bucket{}, map[string]*Bucket{}
	current := []entity.Transaction{}
	previous := []entity.Transaction{}
	sorted := append([]entity.Transaction{}, s.Transactions...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].OccurredAt == sorted[j].OccurredAt {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].OccurredAt < sorted[j].OccurredAt
	})
	for _, t := range sorted {
		if t.UserID != user {
			return r, entity.Fail("FORBIDDEN", "snapshot owner mismatch")
		}
		if t.Kind != "external" {
			continue
		}
		if t.Currency != "CNY" || t.AmountMinor <= 0 || (t.Direction != "income" && t.Direction != "expense") {
			return r, entity.Fail("INVALID_TRANSACTION_DATA", "unsupported currency/direction/amount in persisted transaction")
		}
		if p.AccountID != nil && t.BankAccountID != *p.AccountID {
			continue
		}
		at := time.UnixMilli(t.OccurredAt)
		if !at.Before(p.Start.Add(-p.End.Sub(p.Start))) && at.Before(p.Start) {
			if t.Direction == "expense" {
				if e := add(&r.Trend.PreviousExpenseMinor, t.AmountMinor); e != nil {
					return r, e
				}
				previous = append(previous, t)
				r.Trend.PreviousTransactionIDs = append(r.Trend.PreviousTransactionIDs, txID(t))
			}
			continue
		}
		if at.Before(p.Start) || !at.Before(p.End) {
			continue
		}
		current = append(current, t)
		r.EvidenceTransactionIDs = append(r.EvidenceTransactionIDs, txID(t))
		r.TransactionCount++
		if t.Direction == "income" {
			if e := add(&r.TotalIncome, t.AmountMinor); e != nil {
				return r, e
			}
		} else {
			if e := add(&r.TotalExpense, t.AmountMinor); e != nil {
				return r, e
			}
			cat, _ := category(t.Category)
			if cats[cat] == nil {
				cats[cat] = &Category{Category: cat, TransactionIDs: []string{}}
			}
			if e := add(&cats[cat].ExpenseMinor, t.AmountMinor); e != nil {
				return r, e
			}
			cats[cat].TransactionIDs = append(cats[cat].TransactionIDs, txID(t))
		}
		local := at.In(p.Location)
		weekday := (int(local.Weekday()) + 6) % 7
		for key, groups := range map[string]map[string]*Bucket{local.Format("2006-01-02"): daily, local.AddDate(0, 0, -weekday).Format("2006-01-02") + "/week": weekly, local.Format("2006-01"): monthly} {
			if groups[key] == nil {
				groups[key] = &Bucket{Label: key, TransactionIDs: []string{}}
			}
			b := groups[key]
			if t.Direction == "income" {
				if e := add(&b.IncomeMinor, t.AmountMinor); e != nil {
					return r, e
				}
			} else {
				if e := add(&b.ExpenseMinor, t.AmountMinor); e != nil {
					return r, e
				}
			}
			b.TransactionIDs = append(b.TransactionIDs, txID(t))
		}
	}
	for i, t := range current {
		if i >= p.Offset && len(r.Transactions) < p.Limit {
			r.Transactions = append(r.Transactions, evidence(t))
		}
	}
	for _, c := range cats {
		r.CategoryBreakdown = append(r.CategoryBreakdown, *c)
	}
	sort.Slice(r.CategoryBreakdown, func(i, j int) bool { return r.CategoryBreakdown[i].Category < r.CategoryBreakdown[j].Category })
	buckets := func(m map[string]*Bucket) []Bucket {
		out := []Bucket{}
		for _, v := range m {
			out = append(out, *v)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
		return out
	}
	r.Daily = buckets(daily)
	r.Weekly = buckets(weekly)
	r.Monthly = buckets(monthly)
	r.Trend.CurrentExpenseMinor = r.TotalExpense
	r.Trend.DeltaMinor = r.TotalExpense - r.Trend.PreviousExpenseMinor
	if r.Trend.PreviousExpenseMinor > 0 {
		v := new(big.Int).Mul(big.NewInt(r.Trend.DeltaMinor), big.NewInt(10000))
		v.Quo(v, big.NewInt(r.Trend.PreviousExpenseMinor))
		if v.IsInt64() {
			n := v.Int64()
			r.Trend.ChangeBasisPoints = &n
		}
		r.Trend.Status = "observed_change"
	}
	prior := map[string][]int64{}
	for _, t := range previous {
		cat, _ := category(t.Category)
		prior[cat] = append(prior[cat], t.AmountMinor)
	}
	for _, v := range prior {
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	}
	last := map[string]entity.Transaction{}
	for _, t := range current {
		if t.Direction != "expense" {
			continue
		}
		cat, known := category(t.Category)
		flag := func(kind, rule string, ev []Evidence) {
			r.Anomalies = append(r.Anomalies, Anomaly{kind, rule, true, ev, false})
		}
		if !known {
			flag("category_unknown", "Unrecognized persisted category; no inferred reclassification", []Evidence{evidence(t)})
		}
		baseline := prior[cat]
		if t.AmountMinor >= 100000 {
			kind := "large_amount_review"
			rule := "Declared threshold >=100000 CNY cents; not a fraud determination"
			if len(baseline) >= 5 && baseline[(len(baseline)-1)/2] > 0 && t.AmountMinor/baseline[(len(baseline)-1)/2] >= 3 {
				kind = "unusual_amount"
				rule = "At least five previous-period category expenses; >=3x lower median and >=100000 cents"
			}
			flag(kind, rule, []Evidence{evidence(t)})
		}
		merchant := strings.TrimSpace(strings.ToLower(t.MerchantName))
		if merchant != "" {
			key := fmt.Sprintf("%d/%s/%d", t.BankAccountID, merchant, t.AmountMinor)
			if old, ok := last[key]; ok && t.OccurredAt-old.OccurredAt <= 5*60*1000 {
				flag("possible_duplicate", "Same account, merchant, CNY amount within five minutes; both remain counted", []Evidence{evidence(old), evidence(t)})
				r.PotentialSavings.ReviewCandidates = append(r.PotentialSavings.ReviewCandidates, Candidate{"duplicate_review", t.AmountMinor, []string{txID(old), txID(t)}, true})
			}
			last[key] = t
		}
	}
	for _, b := range s.Budgets {
		start, e := time.Parse("2006-01", b.Month)
		if e != nil || b.UserID != user || b.Currency != "CNY" {
			return r, entity.Fail("INVALID_TRANSACTION_DATA", "invalid budget snapshot")
		}
		end := start.AddDate(0, 1, 0)
		if !end.After(p.Start) || !start.Before(p.End) {
			continue
		}
		u := BudgetStatus{TransactionIDs: []string{}, BudgetID: b.ID, Month: b.Month, Scope: "all_user_accounts_full_UTC_month", TotalMinor: b.TotalMinor, Categories: []BudgetCategory{}}
		spent := map[string]int64{}
		for _, t := range sorted {
			at := time.UnixMilli(t.OccurredAt)
			if t.Kind == "external" && t.Direction == "expense" && !at.Before(start) && at.Before(end) {
				u.TransactionIDs = append(u.TransactionIDs, txID(t))
				if e := add(&u.SpentMinor, t.AmountMinor); e != nil {
					return r, e
				}
				v := spent[t.Category]
				if e := add(&v, t.AmountMinor); e != nil {
					return r, e
				}
				spent[t.Category] = v
			}
		}
		for _, c := range b.Categories {
			reserve := c.EssentialReserveMinor - spent[c.Category]
			if reserve < 0 {
				reserve = 0
			}
			u.EssentialReserveRemainingMinor += reserve
			u.Categories = append(u.Categories, BudgetCategory{c.Category, c.AllocatedMinor, spent[c.Category], c.AllocatedMinor - spent[c.Category], reserve})
		}
		u.RemainingMinor = u.TotalMinor - u.SpentMinor
		u.AvailableAfterReserveMinor = u.RemainingMinor
		for _, rule := range b.Rules {
			if rule.Kind == "enforce_reserve" && rule.Enabled {
				u.AvailableAfterReserveMinor -= u.EssentialReserveRemainingMinor
			}
		}
		r.BudgetStatus = append(r.BudgetStatus, u)
	}
	r.Findings = append(r.Findings, Finding{"totals", fmt.Sprintf("期间记录收入%d分，支出%d分；只含external交易，历史record_only事实计入，不等于账户余额变化。", r.TotalIncome, r.TotalExpense), append([]string{}, r.EvidenceTransactionIDs...)})
	for i, a := range r.Anomalies {
		ids := []string{}
		for _, ev := range a.Evidence {
			ids = append(ids, fmt.Sprint(ev.TransactionID))
		}
		r.Findings = append(r.Findings, Finding{fmt.Sprintf("anomaly-%d", i), a.Rule + "；需要核查，不是已确认损失。", ids})
	}
	return r, nil
}
