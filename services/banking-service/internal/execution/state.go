package execution

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
)

// Domain intent projections follow gateway terminal decisions in the same
// transaction. This never posts a ledger entry or changes subscription funds.
func projectTerminal(tx *gorm.DB, op *entity.OperationIntent) error {
	if op.ResourceID <= 0 || op.Status != "failed" && op.Status != "expired" && op.Status != "rejected" {
		return nil
	}
	var model any
	status := op.Status
	switch op.Kind {
	case "transfer", "savings_deposit", "savings_withdrawal":
		model = &entity.TransferIntent{}
		if status == "expired" {
			status = "failed"
		}
	case "cancel_subscription":
		model = &entity.SubscriptionActionIntent{}
	default:
		return nil
	}
	return tx.Model(model).Where("id = ? AND user_id = ? AND status = ?", op.ResourceID, op.UserID, "pending_approval").Updates(map[string]any{"status": status, "failure_code": op.ErrorCode}).Error
}
