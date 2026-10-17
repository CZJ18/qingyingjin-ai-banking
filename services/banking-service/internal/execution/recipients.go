package execution

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"gorm.io/gorm"
)

func recipientPolicy(tx *gorm.DB, op *entity.OperationIntent) error {
	var p recipients.PaymentInput
	if e := strict(op.Payload, &p); e != nil {
		return e
	}
	r, source, target, e := recipients.Resolve(tx, op.UserID, p)
	if e != nil {
		return e
	}
	if recipients.Fingerprint(r) != op.ResourceDigest || source.ID != op.SourceAccountID || target.ID != op.TargetAccountID || op.AmountMinor != p.AmountMinor || op.Currency != "CNY" {
		return entity.Fail("AUTHORIZATION_MISMATCH", "recipient identity or account changed; prepare and confirm again")
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
		return entity.Fail("NECESSARY_RESERVE", "transfer would consume protected living or safety funds")
	}
	return riskEvent(tx, op, "necessary_reserve", "allow", "", map[string]any{"protected_minor": reserve, "amount_minor": op.AmountMinor})
}
