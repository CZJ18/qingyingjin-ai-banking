package repository

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/risk"
	"gorm.io/gorm"
	"time"
)

// PostScenarioFunds uses the existing balanced Post path inside the original
// execution transaction. It cannot be called without the exact persisted permit.
func PostScenarioFunds(ctx context.Context, db *gorm.DB, op *entity.OperationIntent, payload any, source, target *entity.BankAccount) (any, error) {
	if op.Kind != "scheduled_transfer" && op.Kind != "investment_order" && op.Kind != "occasion_plan" && op.Kind != "occasion_order" && op.Kind != "occasion_release" {
		return nil, entity.Fail("FORBIDDEN", "unsupported scenario posting")
	}
	if err := (risk.TrustedGateway{}).SensitiveCheck(ctx, op.UserID, op.Kind, payload); err != nil {
		return nil, err
	}
	if source == nil || source.UserID != op.UserID || !source.IsSimulated || source.Status != "active" || source.Currency != "CNY" || op.AmountMinor <= 0 || source.BalanceMinor < op.AmountMinor || op.SourceAccountID != source.ID {
		return nil, entity.Fail("AUTHORIZATION_MISMATCH", "scenario source and amount must match authorization")
	}
	if target != nil {
		if target.UserID != op.UserID || !target.IsSimulated || target.Status != "active" || target.Currency != "CNY" || source.ID == target.ID || (op.TargetAccountID != 0 && op.TargetAccountID != target.ID) {
			return nil, entity.Fail("AUTHORIZATION_MISMATCH", "owned scenario target required")
		}
		return postCrossUserJournals(db, op, source, target, map[string]string{"simulation": "true", "scenario": op.Kind}, "模拟场景内部资金划转", "scenario_transfer", "scenario")
	}
	if op.Kind != "occasion_order" {
		return nil, entity.Fail("FORBIDDEN", "only an approved simulated order may post an expense")
	}
	journal, audit := uuid.NewString(), uuid.NewString()
	external := "occasion:" + op.ID
	meta, _ := json.Marshal(map[string]string{"operation_id": op.ID, "simulation": "true", "merchant_status": "local_simulator_only"})
	row := entity.Transaction{Base: entity.Base{UserID: op.UserID}, BankAccountID: source.ID, ExternalTransactionID: &external, Direction: "expense", AmountMinor: op.AmountMinor, Currency: "CNY", Category: "celebration", MerchantName: "本地模拟庆祝商城", Description: "模拟鲜花/蛋糕订单，未联系真实商户", OccurredAt: time.Now().UnixMilli(), SourceMetadata: meta, Kind: "external", EffectMode: "apply_simulated_effect", JournalID: &journal, TraceID: op.TraceID, AuditID: audit}
	if err := db.Create(&row).Error; err != nil {
		return nil, err
	}
	legs := []entity.LedgerEntry{{UserID: op.UserID, JournalID: journal, Sequence: 1, AccountID: &source.ID, TransactionID: &row.ID, Currency: "CNY", DeltaMinor: -op.AmountMinor, Side: "credit"}, {UserID: op.UserID, JournalID: journal, Sequence: 2, Currency: "CNY", DeltaMinor: op.AmountMinor, Side: "debit"}}
	if err := (&Tx{db: db, userID: op.UserID}).Post(legs, map[int64]*entity.BankAccount{source.ID: source}); err != nil {
		return nil, err
	}
	if err := db.Create(&entity.Audit{ID: audit, UserID: op.UserID, Action: "occasion.simulated_purchase", ResourceID: row.ID, Outcome: "succeeded", TraceID: op.TraceID}).Error; err != nil {
		return nil, err
	}
	return map[string]any{"order_id": op.ID, "journal_id": journal, "transaction_id": row.ID, "is_simulated": true, "merchant_contacted": false, "settlement": "local_simulation_only"}, nil
}
