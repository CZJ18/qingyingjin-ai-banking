// Package cards manages simulated card metadata. It never posts money or stores PAN/CVV.
package cards

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"regexp"
	"strings"
)

type Card struct {
	ID                    string `json:"id"`
	AccountID             int64  `json:"account_id,string"`
	Issuer                string `json:"issuer"`
	Name                  string `json:"name"`
	LastFour              string `json:"last_four"`
	CardType              string `json:"card_type"`
	Currency              string `json:"currency"`
	Status                string `json:"status"`
	Default               bool   `json:"is_default"`
	CreditLimitMinor      int64  `json:"credit_limit_minor"`
	CreditUsedMinor       int64  `json:"credit_used_minor"`
	CreditAvailableMinor  int64  `json:"credit_available_minor"`
	TransactionLimitMinor *int64 `json:"transaction_limit_minor,omitempty"`
	CreatedAt             int64  `json:"created_at"`
	UpdatedAt             int64  `json:"updated_at"`
	IsSimulated           bool   `json:"is_simulated"`
}
type Input struct {
	Action                string `json:"action"`
	CardID                string `json:"card_id,omitempty"`
	AccountID             int64  `json:"account_id,omitempty,string"`
	Issuer                string `json:"issuer,omitempty"`
	Name                  string `json:"name,omitempty"`
	LastFour              string `json:"last_four,omitempty"`
	CardType              string `json:"card_type,omitempty"`
	CreditLimitMinor      int64  `json:"credit_limit_minor,omitempty"`
	TransactionLimitMinor *int64 `json:"transaction_limit_minor,omitempty"`
}

var suffix = regexp.MustCompile(`^[0-9]{4}$`)

func Validate(p Input) error {
	bad := func() error {
		return entity.Fail("INVALID_ARGUMENT", "invalid simulated card parameters; provide four digits only, never a full card number")
	}
	switch p.Action {
	case "bind", "apply":
		if p.CardID != "" || p.AccountID <= 0 || strings.TrimSpace(p.Issuer) == "" || len(p.Issuer) > 96 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 96 || !suffix.MatchString(p.LastFour) || (p.CardType != "debit" && p.CardType != "credit") || p.CreditLimitMinor < 0 || p.CreditLimitMinor > entity.MaxBalance || (p.CardType == "debit" && p.CreditLimitMinor != 0) {
			return bad()
		}
		if p.TransactionLimitMinor != nil {
			return bad()
		}
	case "unbind", "set_default", "lock", "unlock", "report_lost", "adjust_limit", "set_transaction_limit":
		if p.CardID == "" || len(p.CardID) > 64 || p.AccountID != 0 || p.Issuer != "" || p.Name != "" || p.LastFour != "" || p.CardType != "" {
			return bad()
		}
		if p.Action != "adjust_limit" && p.CreditLimitMinor != 0 {
			return bad()
		}
		if p.CreditLimitMinor < 0 || p.CreditLimitMinor > entity.MaxBalance {
			return bad()
		}
		if p.Action == "adjust_limit" && p.CreditLimitMinor <= 0 {
			return bad()
		}
		if p.Action == "set_transaction_limit" {
			if p.TransactionLimitMinor == nil || *p.TransactionLimitMinor < 0 || *p.TransactionLimitMinor > entity.MaxBalance {
				return bad()
			}
		} else if p.TransactionLimitMinor != nil {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

// Apply is deterministic except for the explicitly supplied new ID and timestamp.
// The caller must hold the owner row lock and persist inside the authorized transaction.
func Apply(current []Card, p Input, newID string, now int64) ([]Card, error) {
	if e := Validate(p); e != nil {
		return nil, e
	}
	out := append([]Card{}, current...)
	if p.Action == "bind" || p.Action == "apply" {
		if len(out) >= 20 {
			return nil, entity.Fail("STATE_CONFLICT", "maximum 20 card records including history")
		}
		for _, c := range out {
			if c.Status != "unbound" && c.AccountID == p.AccountID && c.Issuer == p.Issuer && c.LastFour == p.LastFour {
				return nil, entity.Fail("STATE_CONFLICT", "simulated card already bound")
			}
		}
		return append(out, Card{ID: newID, AccountID: p.AccountID, Issuer: p.Issuer, Name: p.Name, LastFour: p.LastFour, CardType: p.CardType, Currency: "CNY", Status: "active", CreditLimitMinor: p.CreditLimitMinor, CreditAvailableMinor: p.CreditLimitMinor, CreatedAt: now, UpdatedAt: now, IsSimulated: true}), nil
	}
	index := -1
	for i, c := range out {
		if c.ID == p.CardID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, entity.Fail("NOT_FOUND", "card not found")
	}
	if out[index].Status == "unbound" {
		return nil, entity.Fail("STATE_CONFLICT", "card is already unbound")
	}
	if p.Action == "unbind" {
		out[index].Status = "unbound"
		out[index].Default = false
		out[index].UpdatedAt = now
		return out, nil
	}
	if p.Action == "unlock" {
		if out[index].Status != "locked" {
			return nil, entity.Fail("STATE_CONFLICT", "only a locked card may be unlocked; lost cards require a replacement")
		}
		out[index].Status = "active"
		out[index].UpdatedAt = now
		return out, nil
	}
	if out[index].Status != "active" && !(p.Action == "report_lost" && out[index].Status == "locked") {
		return nil, entity.Fail("STATE_CONFLICT", "active card required")
	}
	switch p.Action {
	case "lock", "report_lost":
		out[index].Status = "locked"
		if p.Action == "report_lost" {
			out[index].Status = "lost"
		}
		out[index].Default = false
	case "adjust_limit":
		if out[index].CardType != "credit" || p.CreditLimitMinor < out[index].CreditUsedMinor {
			return nil, entity.Fail("STATE_CONFLICT", "credit limit must cover used credit on a credit card")
		}
		out[index].CreditLimitMinor = p.CreditLimitMinor
		out[index].CreditAvailableMinor = p.CreditLimitMinor - out[index].CreditUsedMinor
	case "set_transaction_limit":
		limit := *p.TransactionLimitMinor
		out[index].TransactionLimitMinor = &limit
	default:
		goto setDefault
	}
	out[index].UpdatedAt = now
	return out, nil
setDefault:
	if out[index].Default {
		return nil, entity.Fail("STATE_CONFLICT", "card is already the default")
	}
	for i := range out {
		if out[i].Default || i == index {
			out[i].Default = i == index
			out[i].UpdatedAt = now
		}
	}
	return out, nil
}

// CheckPayment applies only to an explicitly card-initiated debit transfer.
// Absent legacy card limits retain account policies; zero blocks all spending.
func CheckPayment(c Card, accountID, amount int64) error {
	if c.AccountID != accountID || c.Status != "active" || !c.IsSimulated {
		return entity.Fail("CARD_UNAVAILABLE", "active owned simulated card required")
	}
	if c.CardType != "debit" {
		return entity.Fail("INVALID_ARGUMENT", "credit spending and repayment are not supported")
	}
	if amount <= 0 || amount > entity.MaxBalance {
		return entity.Fail("INVALID_ARGUMENT", "valid integer-cent amount required")
	}
	if c.TransactionLimitMinor != nil && amount > *c.TransactionLimitMinor {
		return entity.Fail("CARD_LIMIT_EXCEEDED", "card single-transaction limit exceeded")
	}
	return nil
}
