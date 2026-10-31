package execution

import (
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"time"
)

func cardSnapshot(tx *gorm.DB, user int64, p cards.Input) ([]cards.Card, error) {
	if e := cards.Validate(p); e != nil {
		return nil, e
	}
	portfolio, e := cards.Read(tx, user)
	if e != nil {
		return nil, e
	}
	if p.Action == "bind" || p.Action == "apply" || p.Action == "set_default" {
		id := p.AccountID
		if p.Action == "set_default" {
			for _, c := range portfolio {
				if c.ID == p.CardID {
					id = c.AccountID
					break
				}
			}
			if id == 0 {
				return nil, entity.Fail("NOT_FOUND", "card not found")
			}
		}
		a, e := account(tx, user, id)
		if e != nil {
			return nil, e
		}
		if !a.IsSimulated || a.Currency != "CNY" || (a.AccountType != "checking" && a.AccountType != "savings") {
			return nil, entity.Fail("FORBIDDEN", "simulated CNY account required")
		}
		if a.Status != "active" {
			return nil, entity.Fail("ACCOUNT_FROZEN", "active account required")
		}
	}
	if _, e = cards.Apply(portfolio, p, uuid.NewString(), time.Now().UnixMilli()); e != nil {
		return nil, e
	}
	return portfolio, nil
}

func checkTransferCard(tx *gorm.DB, user int64, p service.TransferInput) error {
	if p.CardID == "" {
		return nil
	}
	portfolio, e := cards.Read(tx, user)
	if e != nil {
		return e
	}
	for _, c := range portfolio {
		if c.ID == p.CardID {
			return cards.CheckPayment(c, p.SourceAccountID, p.AmountMinor)
		}
	}
	return entity.Fail("NOT_FOUND", "owned card not found")
}
func cardPolicy(tx *gorm.DB, op *entity.OperationIntent) error {
	var p cards.Input
	if e := strict(op.Payload, &p); e != nil {
		return e
	}
	portfolio, e := cardSnapshot(tx, op.UserID, p)
	if e != nil {
		return e
	}
	if entity.CanonicalJSONHash(marshal(portfolio)) != op.ResourceDigest {
		return entity.Fail("STATE_CONFLICT", "card portfolio changed; prepare a fresh intent")
	}
	return nil
}
