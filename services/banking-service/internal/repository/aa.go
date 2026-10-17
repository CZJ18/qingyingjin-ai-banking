package repository

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/risk"
	"gorm.io/gorm"
)

func (r *Core) AA() *aa.Service { return aa.New(r.db) }

// PostAAPayment uses the sole Post balance path, with one balanced clearing journal per owner.
// The caller holds sorted owner locks and both account locks inside the gateway transaction.
func PostAAPayment(ctx context.Context, db *gorm.DB, op *entity.OperationIntent, in aa.PaymentInput, source, target *entity.BankAccount) (map[string]string, error) {
	if e := (risk.TrustedGateway{}).SensitiveCheck(ctx, op.UserID, "aa_payment", in); e != nil {
		return nil, e
	}
	if source.UserID != op.UserID || target.UserID != in.OwnerID || source.ID != op.SourceAccountID || target.ID != op.TargetAccountID || source.ID == target.ID || op.AmountMinor <= 0 {
		return nil, entity.Fail("AUTHORIZATION_MISMATCH", "AA posting does not match authorized accounts")
	}
	result, e := postCrossUserJournals(db, op, source, target, map[string]string{"bill_id": in.BillID, "participant_id": in.ParticipantID}, "AA分摊结算", "aa_settlement", "aa")
	if e == nil {
		result["bill_id"] = in.BillID
		result["participant_id"] = in.ParticipantID
	}
	return result, e
}
