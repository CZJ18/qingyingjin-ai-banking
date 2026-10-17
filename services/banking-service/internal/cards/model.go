// Package cards manages simulated card metadata. It never posts money or stores PAN/CVV.
package cards

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"regexp"
	"strings"
)

type Card struct {
	ID                   string `json:"id"`
	AccountID            int64  `json:"account_id,string"`
	Issuer               string `json:"issuer"`
	Name                 string `json:"name"`
	LastFour             string `json:"last_four"`
	CardType             string `json:"card_type"`
	Currency             string `json:"currency"`
	Status               string `json:"status"`
	Default              bool   `json:"is_default"`
	CreditLimitMinor     int64  `json:"credit_limit_minor"`
	CreditUsedMinor      int64  `json:"credit_used_minor"`
	CreditAvailableMinor int64  `json:"credit_available_minor"`
	CreatedAt            int64  `json:"created_at"`
	UpdatedAt            int64  `json:"updated_at"`
	IsSimulated          bool   `json:"is_simulated"`
}
type Input struct {
	Action           string `json:"action"`
	CardID           string `json:"card_id,omitempty"`
	AccountID        int64  `json:"account_id,omitempty,string"`
	Issuer           string `json:"issuer,omitempty"`
	Name             string `json:"name,omitempty"`
	LastFour         string `json:"last_four,omitempty"`
	CardType         string `json:"card_type,omitempty"`
	CreditLimitMinor int64  `json:"credit_limit_minor,omitempty"`
}

var suffix = regexp.MustCompile(`^[0-9]{4}$`)

func Validate(p Input) error {
	bad := func() error {
		return entity.Fail("INVALID_ARGUMENT", "invalid simulated card parameters; provide four digits only, never a full card number")
	}
	switch p.Action {
	case "bind":
		if p.CardID != "" || p.AccountID <= 0 || strings.TrimSpace(p.Issuer) == "" || len(p.Issuer) > 96 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 96 || !suffix.MatchString(p.LastFour) || (p.CardType != "debit" && p.CardType != "credit") || p.CreditLimitMinor < 0 || p.CreditLimitMinor > entity.MaxBalance || (p.CardType == "debit" && p.CreditLimitMinor != 0) {
			return bad()
		}
	case "unbind", "set_default":
		if p.CardID == "" || len(p.CardID) > 64 || p.AccountID != 0 || p.Issuer != "" || p.Name != "" || p.LastFour != "" || p.CardType != "" || p.CreditLimitMinor != 0 {
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
	if p.Action == "bind" {
		if len(out) >= 20 {
			return nil, entity.Fail("STATE_CONFLICT", "maximum 20 card records including history")
		}
		for _, c := range out {
			if c.Status == "active" && c.AccountID == p.AccountID && c.Issuer == p.Issuer && c.LastFour == p.LastFour {
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
	if out[index].Status != "active" {
		return nil, entity.Fail("STATE_CONFLICT", "card is already unbound")
	}
	if p.Action == "unbind" {
		out[index].Status = "unbound"
		out[index].Default = false
		out[index].UpdatedAt = now
		return out, nil
	}
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
