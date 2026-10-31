package coze

import (
	"context"
	"errors"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"time"
)

var (
	ErrContextLimit                  = errors.New("MODEL_CONTEXT_LIMIT_EXCEEDED")
	ErrExecutionConfirmationRequired = errors.New("MODEL_EXECUTION_CONFIRMATION_REQUIRED")
	ErrApprovalRequired              = errors.New("MODEL_APPROVAL_REQUIRED")
	ErrApprovalLimit                 = errors.New("MODEL_APPROVAL_LIMIT_EXCEEDED")
	ErrMonthlyQuotaExceeded          = errors.New("MODEL_USER_MONTHLY_QUOTA_EXCEEDED")
	ErrSpendQuotaExceeded            = errors.New("MODEL_SPEND_QUOTA_EXCEEDED")
	ErrQuotaExceeded                 = errors.New("MODEL_QUOTA_EXCEEDED")
	ErrOutcomeUnknown                = errors.New("MODEL_OUTCOME_UNKNOWN")
	ErrRequestConflict               = errors.New("MODEL_REQUEST_CONFLICT")
	ErrBudgetUnavailable             = errors.New("MODEL_BUDGET_UNAVAILABLE")
	ErrMonetaryAdmissionRequired     = errors.New("MODEL_MONETARY_ADMISSION_REQUIRED")
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
func NewBudgeted(base, pat string, timeout time.Duration, meter Meter, policies ...outbound.Policy) (*Client, error) {
	if meter == nil {
		return nil, ErrBudgetUnavailable
	}
	c, e := New(base, pat, timeout, policies...)
	if e == nil {
		c.meter = meter
	}
	return c, e
}

// Preserve quota outcomes across existing domain wrappers without disclosing
// database/upstream error details.
func BudgetCode(err error) string {
	if errors.Is(err, ErrContextLimit) {
		return "MODEL_CONTEXT_LIMIT_EXCEEDED"
	}
	if errors.Is(err, ErrExecutionConfirmationRequired) {
		return "MODEL_EXECUTION_CONFIRMATION_REQUIRED"
	}
	if errors.Is(err, ErrApprovalRequired) {
		return "MODEL_APPROVAL_REQUIRED"
	}
	if errors.Is(err, ErrApprovalLimit) {
		return "MODEL_APPROVAL_LIMIT_EXCEEDED"
	}
	for _, item := range []struct {
		e    error
		code string
	}{{ErrSpendQuotaExceeded, "MODEL_SPEND_QUOTA_EXCEEDED"}, {ErrMonthlyQuotaExceeded, "MODEL_USER_MONTHLY_QUOTA_EXCEEDED"}, {ErrQuotaExceeded, "MODEL_QUOTA_EXCEEDED"}, {ErrOutcomeUnknown, "MODEL_OUTCOME_UNKNOWN"}, {ErrRequestConflict, "MODEL_REQUEST_CONFLICT"}, {ErrBudgetUnavailable, "MODEL_BUDGET_UNAVAILABLE"}, {ErrMonetaryAdmissionRequired, "MODEL_MONETARY_ADMISSION_REQUIRED"}} {
		if errors.Is(err, item.e) {
			return item.code
		}
	}
	return ""
}
