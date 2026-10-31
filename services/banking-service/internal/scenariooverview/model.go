// Package scenariooverview defines a bounded read-only scenario contract.
// It is deliberately separate from execution so task planning cannot authorize money.
package scenariooverview

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"io"
	"strconv"
	"strings"
	"time"
)

type Input struct {
	Scenario  string `json:"scenario"`
	AccountID string `json:"account_id"`
}

func ID(v string) (int64, error) {
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != v {
		return 0, entity.Fail("INVALID_ARGUMENT", "a canonical positive selected account is required")
	}
	return n, nil
}
func (in Input) Validate() error {
	switch in.Scenario {
	case "transfer", "cards", "investment", "subscriptions", "birthday":
	default:
		return entity.Fail("INVALID_ARGUMENT", "an existing scenario is required")
	}
	_, e := ID(in.AccountID)
	return e
}

type Item struct {
	ID                    string `json:"id"`
	Kind                  string `json:"kind"`
	Status                string `json:"status"`
	Label                 string `json:"label,omitempty"`
	Currency              string `json:"currency,omitempty"`
	AmountMinor           *int64 `json:"amount_minor,omitempty"`
	Version               int64  `json:"version,omitempty"`
	CreditLimitMinor      *int64 `json:"credit_limit_minor,omitempty"`
	CreditAvailableMinor  *int64 `json:"credit_available_minor,omitempty"`
	TransactionLimitMinor *int64 `json:"transaction_limit_minor,omitempty"`
	MinimumMinor          *int64 `json:"minimum_minor,omitempty"`
	RiskLevel             int    `json:"risk_level,omitempty"`
	LockDays              *int   `json:"lock_days,omitempty"`
	BillingInterval       string `json:"billing_interval,omitempty"`
	NextBillingAt         int64  `json:"next_billing_at,omitempty"`
	MonthDay              string `json:"month_day,omitempty"`
	NextDate              string `json:"next_date,omitempty"`
	DaysUntil             *int   `json:"days_until,omitempty"`
}

type Output struct {
	Scenario    string   `json:"scenario"`
	AccountID   string   `json:"account_id"`
	UserID      string   `json:"user_id"`
	IsSimulated bool     `json:"is_simulated"`
	Items       []Item   `json:"items"`
	Limitations []string `json:"limitations"`
	Truncated   bool     `json:"truncated"`
}

func (out Output) ValidateFor(in Input, user int64) error {
	bad := func() error { return entity.Fail("TOOL_OUTPUT_INVALID", "bounded owned scenario evidence required") }
	if in.Validate() != nil || user <= 0 || out.Scenario != in.Scenario || out.AccountID != in.AccountID || out.UserID != strconv.FormatInt(user, 10) || !out.IsSimulated || out.Items == nil || len(out.Items) > 50 || out.Limitations == nil || len(out.Limitations) == 0 || len(out.Limitations) > 16 || out.Truncated && len(out.Items) != 50 {
		return bad()
	}
	for _, s := range out.Limitations {
		if strings.TrimSpace(s) == "" || len(s) > 2048 {
			return bad()
		}
	}
	seen := map[string]bool{}
	for _, v := range out.Items {
		if v.ID == "" || len(v.ID) > 128 || v.Status == "" || seen[v.Kind+":"+v.ID] || v.Currency != "" && v.Currency != "CNY" || len(v.Label) > 128 || v.Version < 0 || v.NextBillingAt < 0 {
			return bad()
		}
		seen[v.Kind+":"+v.ID] = true
		for _, n := range []*int64{v.AmountMinor, v.CreditLimitMinor, v.CreditAvailableMinor, v.TransactionLimitMinor, v.MinimumMinor} {
			if n != nil && (*n < 0 || *n > entity.MaxBalance) {
				return bad()
			}
		}
		if v.RiskLevel < 0 || v.RiskLevel > 3 || v.LockDays != nil && (*v.LockDays < 0 || *v.LockDays > 3660) || v.DaysUntil != nil && (*v.DaysUntil < 0 || *v.DaysUntil > 3660) {
			return bad()
		}
		if v.Label != "" && v.Kind != "investment_product" {
			return bad()
		}
		switch v.Kind {
		case "source_account":
			if out.Scenario != "transfer" || v.ID != in.AccountID || v.AmountMinor == nil || v.Currency != "CNY" || v.Status != "active" && v.Status != "frozen" && v.Status != "inactive" && v.Status != "closed" {
				return bad()
			}
		case "saved_recipient_reference":
			if _, e := ID(v.ID); e != nil || out.Scenario != "transfer" || v.ID == out.UserID || v.Status != "requires_explicit_selection" {
				return bad()
			}
		case "card":
			u, e := uuid.Parse(v.ID)
			if e != nil || u.String() != v.ID || out.Scenario != "cards" || v.Status != "active" && v.Status != "unbound" && v.Status != "locked" && v.Status != "lost" {
				return bad()
			}
		case "investment_product":
			if out.Scenario != "investment" || v.Status != "catalog_only" || v.RiskLevel < 1 || v.MinimumMinor == nil || v.Label == "" || v.ID != "sim-cash-v1" && v.ID != "sim-bond-v1" && v.ID != "sim-equity-v1" {
				return bad()
			}
		case "subscription":
			if _, e := ID(v.ID); e != nil || out.Scenario != "subscriptions" || v.Status != "candidate" && v.Status != "active" && v.Status != "cancelled" || v.AmountMinor == nil || v.Version < 1 || v.Currency != "CNY" {
				return bad()
			}
			switch v.BillingInterval {
			case "weekly", "monthly", "quarterly", "annual":
			default:
				return bad()
			}
		case "registered_birthday":
			u, e := uuid.Parse(v.ID)
			day, de := time.Parse("2006-01-02", "2000-"+v.MonthDay)
			if e != nil || u.String() != v.ID || out.Scenario != "birthday" || de != nil || day.Format("01-02") != v.MonthDay || v.Status != "recorded" && v.Status != "disabled" {
				return bad()
			}
			if v.NextDate != "" {
				d, e := time.Parse("2006-01-02", v.NextDate)
				if e != nil || d.Format("2006-01-02") != v.NextDate {
					return bad()
				}
			}
		default:
			return bad()
		}
	}
	return nil
}

// DecodeOutput rejects missing required fields, unknown properties, wrong JSON
// types and trailing values before the executor trusts a remote tool result.
func DecodeOutput(raw []byte, in Input, user int64) (Output, error) {
	var out Output
	var fields map[string]json.RawMessage
	bad := func() (Output, error) {
		return Output{}, entity.Fail("TOOL_OUTPUT_INVALID", "exact owned scenario response required")
	}
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad()
	}
	for _, key := range []string{"scenario", "account_id", "user_id", "is_simulated", "items", "limitations", "truncated"} {
		if v, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.ValidateFor(in, user) != nil {
		return bad()
	}
	return out, nil
}
