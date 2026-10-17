package aa

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"time"
)

func marshal(v any) []byte { b, _ := json.Marshal(v); return b }

// Payment resolves immutable amount/recipient from stored shares, never from caller amounts.
func Payment(tx *gorm.DB, user int64, in PaymentInput) (Portfolio, *Bill, *Share, entity.BankAccount, entity.BankAccount, error) {
	p, e := Load(tx, in.OwnerID)
	if e != nil {
		return p, nil, nil, entity.BankAccount{}, entity.BankAccount{}, e
	}
	b, e := Find(&p, in.BillID)
	if e != nil {
		return p, nil, nil, entity.BankAccount{}, entity.BankAccount{}, e
	}
	bad := func(code, msg string) (Portfolio, *Bill, *Share, entity.BankAccount, entity.BankAccount, error) {
		return p, nil, nil, entity.BankAccount{}, entity.BankAccount{}, entity.Fail(code, msg)
	}
	if in.OwnerID == user || b.Status != "open" || method(b.PaymentMethod) != "simulation" {
		return bad("STATE_CONFLICT", "AA bill is not payable")
	}
	var share *Share
	for i := range b.Shares {
		if b.Shares[i].ID == in.ParticipantID && b.Shares[i].UserID == user {
			share = &b.Shares[i]
		}
	}
	if share == nil {
		return bad("NOT_FOUND", "owned AA share not found")
	}
	if share.Status != "joined" {
		return bad("STATE_CONFLICT", "AA share already paid or not joined")
	}
	source, e := receiving(tx, user, in.SourceAccountID)
	if e != nil {
		return p, b, share, source, entity.BankAccount{}, e
	}
	target, e := receiving(tx, in.OwnerID, b.AccountID)
	return p, b, share, source, target, e
}
func MarkPaid(tx *gorm.DB, owner int64, p Portfolio, b *Bill, share *Share, op string) error {
	share.Status = "paid"
	share.OperationID = op
	share.PaidAt = time.Now().UnixMilli()
	b.Version++
	b.UpdatedAt = share.PaidAt
	b.Status = "settled"
	for i, s := range b.Shares {
		if i > 0 && s.Status != "paid" {
			b.Status = "open"
		}
	}
	return Save(tx, owner, p)
}
