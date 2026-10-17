package planning

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"time"
)

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit])
	}
	return s
}

// Reserve observed recurring obligations without claiming that an overdue
// estimate was actually debited. All overdue scheduled charges are protected
// until evidence/user review resolves them; no forecast becomes cash.
func subscriptionReserve(amount, next int64, interval string, start, end, now time.Time) (int64, bool, error) {
	if !bounded(amount) || next <= 0 {
		return 0, false, entity.Fail("INVALID_ARGUMENT", "invalid subscription schedule")
	}
	var total int64
	due := time.UnixMilli(next).UTC()
	overdue := due.Before(now)
	for i := 0; due.Before(end); i++ {
		if i >= 10000 {
			return 0, overdue, entity.Fail("ANALYSIS_LIMIT", "subscription schedule exceeds bound")
		}
		total += amount
		if total > 1000*entity.MaxBalance {
			return 0, overdue, entity.Fail("ANALYSIS_LIMIT", "subscription obligation total exceeds planning bound")
		}
		following := subscription.NextCharge(due, interval)
		if !following.After(due) {
			return 0, overdue, entity.Fail("INVALID_ARGUMENT", "invalid subscription interval")
		}
		due = following
	}
	return total, overdue, nil
}
