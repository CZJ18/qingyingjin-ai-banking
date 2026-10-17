package coze

import (
	"context"
	"errors"
	"time"
)

var (
	ErrQuotaExceeded     = errors.New("MODEL_QUOTA_EXCEEDED")
	ErrOutcomeUnknown    = errors.New("MODEL_OUTCOME_UNKNOWN")
	ErrRequestConflict   = errors.New("MODEL_REQUEST_CONFLICT")
	ErrBudgetUnavailable = errors.New("MODEL_BUDGET_UNAVAILABLE")
)

// Meter persists reservations before dispatch. Unknown outcomes retain their
// allocation; completion is idempotent and the cached response is owner-bound.
type Meter interface {
	Begin(context.Context, int64, string, string, string) (string, []byte, error)
	Complete(context.Context, int64, string, []byte, *int64, string, bool) error
}
type modelIdentity struct {
	owner int64
	key   string
}
type identityKey struct{}

// WithIdentity accepts only server-selected ownership and invocation keys.
// It never sends either field to Coze or derives ownership from prompt content.
func WithIdentity(ctx context.Context, owner int64, key string) context.Context {
	return context.WithValue(ctx, identityKey{}, modelIdentity{owner, key})
}
func NewBudgeted(base, pat string, timeout time.Duration, meter Meter) (*Client, error) {
	if meter == nil {
		return nil, ErrBudgetUnavailable
	}
	c, e := New(base, pat, timeout)
	if e == nil {
		c.meter = meter
	}
	return c, e
}

// Preserve quota outcomes across existing domain wrappers without disclosing
// database/upstream error details.
func BudgetCode(err error) string {
	for _, item := range []struct {
		e    error
		code string
	}{{ErrQuotaExceeded, "MODEL_QUOTA_EXCEEDED"}, {ErrOutcomeUnknown, "MODEL_OUTCOME_UNKNOWN"}, {ErrRequestConflict, "MODEL_REQUEST_CONFLICT"}, {ErrBudgetUnavailable, "MODEL_BUDGET_UNAVAILABLE"}} {
		if errors.Is(err, item.e) {
			return item.code
		}
	}
	return ""
}
