package repository

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/risk"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func PostRecipientPayment(ctx context.Context, db *gorm.DB, op *entity.OperationIntent, in recipients.PaymentInput, source, target *entity.BankAccount) (map[string]string, error) {
	if e := (risk.TrustedGateway{}).SensitiveCheck(ctx, op.UserID, "recipient_transfer", in); e != nil {
		return nil, e
	}
	if source.UserID != op.UserID || target.UserID != in.RecipientID || source.ID != op.SourceAccountID || target.ID != op.TargetAccountID || source.ID == target.ID || source.UserID == target.UserID || op.AmountMinor != in.AmountMinor || op.AmountMinor <= 0 {
		return nil, entity.Fail("AUTHORIZATION_MISMATCH", "recipient posting does not match authorized transfer")
	}
	return postCrossUserJournals(db, op, source, target, map[string]string{"recipient_id": strconv.FormatInt(in.RecipientID, 10)}, in.Description, "peer_transfer", "peer")
}

// Both AA and recipient transfers share the exact same balanced Post path.
func postCrossUserJournals(db *gorm.DB, op *entity.OperationIntent, source, target *entity.BankAccount, metadataFields map[string]string, description, category, prefix string) (map[string]string, error) {
	result := map[string]string{"operation_id": op.ID}
	metadataFields["operation_id"] = op.ID
	for i, a := range []*entity.BankAccount{source, target} {
		delta := op.AmountMinor
		direction := "income"
		action := prefix + ".receive"
		side1, side2 := "debit", "credit"
		key := "recipient_journal_id"
		if i == 0 {
			delta = -delta
			direction = "expense"
			action = prefix + ".pay"
			side1, side2 = "credit", "debit"
			key = "payer_journal_id"
		}
		merchant := "模拟用户转账"
		if prefix == "aa" {
			merchant = "模拟AA结算"
		}
		journal := uuid.NewString()
		auditID := uuid.NewString()
		external := prefix + ":" + op.ID + ":" + direction
		metadata, _ := json.Marshal(metadataFields)
		row := entity.Transaction{Base: entity.Base{UserID: a.UserID}, BankAccountID: a.ID, ExternalTransactionID: &external, Direction: direction, AmountMinor: op.AmountMinor, Currency: "CNY", Category: category, MerchantName: merchant, Description: description, OccurredAt: time.Now().UnixMilli(), SourceMetadata: metadata, Kind: "internal_transfer", EffectMode: "apply_simulated_effect", JournalID: &journal, TraceID: op.TraceID, TaskID: op.TaskID, AuditID: auditID}
		if e := db.Create(&row).Error; e != nil {
			return nil, e
		}
		legs := []entity.LedgerEntry{{UserID: a.UserID, JournalID: journal, Sequence: 1, AccountID: &a.ID, TransactionID: &row.ID, Currency: "CNY", DeltaMinor: delta, Side: side1}, {UserID: a.UserID, JournalID: journal, Sequence: 2, Currency: "CNY", DeltaMinor: -delta, Side: side2}}
		t := &Tx{db: db, userID: a.UserID}
		if e := t.Post(legs, map[int64]*entity.BankAccount{a.ID: a}); e != nil {
			return nil, e
		}
		if e := db.Create(&entity.Audit{ID: auditID, UserID: a.UserID, Action: action, ResourceID: row.ID, Outcome: "succeeded", TraceID: op.TraceID, TaskID: op.TaskID}).Error; e != nil {
			return nil, e
		}
		result[key] = journal
	}
	return result, nil
}
