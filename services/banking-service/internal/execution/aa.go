package execution

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"gorm.io/gorm"
)

func lockPrepareUsers(tx *gorm.DB, user int64, in PrepareInput) error {
	if in.Kind == "recipient_transfer" {
		var p recipients.PaymentInput
		if e := strict(in.Parameters, &p); e != nil {
			return e
		}
		return aa.LockUsers(tx, user, p.RecipientID)
	}
	if in.Kind != "aa_payment" {
		return lockUser(tx, user)
	}
	var p aa.PaymentInput
	if e := strict(in.Parameters, &p); e != nil {
		return e
	}
	return aa.LockUsers(tx, user, p.OwnerID)
}

// Discover the immutable lock set BEFORE beginning the financial transaction.
// A non-locking SELECT inside MySQL REPEATABLE READ would pin an old bill snapshot.
func (s *Service) operationCounterparty(ctx context.Context, user int64, id string) (int64, error) {
	var op entity.OperationIntent
	e := s.db.WithContext(ctx).Where("id=? AND user_id=?", id, user).First(&op).Error
	if e == gorm.ErrRecordNotFound {
		return 0, entity.Fail("NOT_FOUND", "operation not found")
	}
	if e != nil {
		return 0, e
	}
	if op.Kind == "recipient_transfer" {
		var p recipients.PaymentInput
		if e := strict(op.Payload, &p); e != nil {
			return 0, e
		}
		return p.RecipientID, nil
	}
	if op.Kind != "aa_payment" {
		return 0, nil
	}
	var p aa.PaymentInput
	if e := strict(op.Payload, &p); e != nil {
		return 0, e
	}
	return p.OwnerID, nil
}
func lockOperationUsers(tx *gorm.DB, user, owner int64) error {
	if owner == 0 {
		return lockUser(tx, user)
	}
	return aa.LockUsers(tx, user, owner)
}
func aaPolicy(tx *gorm.DB, op *entity.OperationIntent) error {
	var p aa.PaymentInput
	if e := strict(op.Payload, &p); e != nil {
		return e
	}
	_, _, share, source, target, e := aa.Payment(tx, op.UserID, p)
	if e != nil {
		return e
	}
	if op.SourceAccountID != source.ID || op.TargetAccountID != target.ID || op.AmountMinor != share.AmountMinor || op.Currency != "CNY" {
		return entity.Fail("AUTHORIZATION_MISMATCH", "AA amount or recipient differs from stored share")
	}
	if source.BalanceMinor < op.AmountMinor {
		return entity.Fail("INSUFFICIENT_FUNDS", "available balance insufficient")
	}
	if _, e := entity.CheckedBalance(target.BalanceMinor, op.AmountMinor); e != nil {
		return e
	}
	reserve, e := protected(tx, op.UserID, source.ID)
	if e != nil {
		return e
	}
	if source.BalanceMinor-op.AmountMinor < reserve {
		return entity.Fail("NECESSARY_RESERVE", "AA payment would consume protected funds")
	}
	return riskEvent(tx, op, "necessary_reserve", "allow", "", map[string]any{"protected_minor": reserve, "amount_minor": op.AmountMinor})
}
