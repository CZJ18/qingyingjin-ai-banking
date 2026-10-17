package planning

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"time"
)

// ExecutionReserve reuses the authoritative snapshot and deterministic engine
// inside the gateway's owner-locked transaction. It performs no plan mutation.
func ExecutionReserve(ctx context.Context, tx *gorm.DB, user int64, settings Settings, now time.Time) (int64, error) {
	s, e := snapshot(tx.WithContext(ctx), user, settings, now)
	if e != nil {
		return 0, e
	}
	r, e := Calculate(s)
	if e != nil {
		return 0, e
	}
	if len(r.Blockers) > 0 {
		return 0, entity.Fail("INPUT_REQUIRED", "financial plan constraints need clarification before execution")
	}
	return r.LivingReserveMinor + r.AdditionalObligationsMinor + r.SafetyReserveMinor + r.CashflowBufferMinor + r.IncomeRiskReserveMinor, nil
}
