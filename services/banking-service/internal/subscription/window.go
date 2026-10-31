package subscription

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"time"
)

// NormalizeDetection uses RFC3339 instants and a half-open bounded history.
// Three annual observations require more than two years of history.
func NormalizeDetection(in DetectInput, now time.Time) (from, to time.Time, account int64, err error) {
	fail := func() (time.Time, time.Time, int64, error) {
		return from, to, 0, entity.Fail("INVALID_ARGUMENT", "RFC3339 detection window <=1096 days, timezone UTC/Asia/Shanghai and canonical account ID required")
	}
	if in.Timezone != "" && in.Timezone != "UTC" && in.Timezone != "Asia/Shanghai" {
		return fail()
	}
	to = now.UTC()
	if in.To != "" {
		to, err = time.Parse(time.RFC3339, in.To)
		if err != nil {
			return fail()
		}
	}
	from = to.Add(-1096 * 24 * time.Hour)
	if in.From != "" {
		from, err = time.Parse(time.RFC3339, in.From)
		if err != nil {
			return fail()
		}
	}
	if !to.After(from) || to.Sub(from) > 1096*24*time.Hour || from.Year() < 1971 || to.Year() > 2100 {
		return fail()
	}
	if in.AccountID != "" {
		account, err = ID(in.AccountID)
		if err != nil {
			return fail()
		}
	}
	return from, to, account, nil
}
