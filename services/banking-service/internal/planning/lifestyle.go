package planning

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"time"
)

// LifestyleSnapshot reuses the deterministic planner within the caller's
// database transaction. Stored past plan amounts are never trusted as cash.
func LifestyleSnapshot(tx *gorm.DB, user, id int64) (Snapshot, Result, error) {
	var p FinancialPlan
	var zero Snapshot
	var result Result
	if e := tx.Where("id=? AND user_id=? AND status='active'", id, user).First(&p).Error; e != nil {
		return zero, result, e
	}
	var settings Settings
	if e := json.Unmarshal(p.SettingsJSON, &settings); e != nil {
		return zero, result, e
	}
	if settings.Period != time.Now().UTC().Format("2006-01") {
		return zero, result, entity.Fail("STATE_CONFLICT", "financial plan must cover current month")
	}
	s, e := snapshot(tx, user, settings, time.Now().UTC())
	if e != nil {
		return s, result, e
	}
	result, e = Calculate(s)
	return s, result, e
}
