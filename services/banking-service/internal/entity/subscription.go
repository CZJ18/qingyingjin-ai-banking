package entity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
)

// Active/cancelled reuse the source Coze Banking status vocabulary. Candidates
// are observations, not proof of a merchant contract or permission to cancel.
const SubscriptionActive = string(legacy.SubscriptionStatusActive)
const SubscriptionCancelled = string(legacy.SubscriptionStatusCancelled)

type Subscription struct {
	Base
	BankAccountID   int64  `json:"bank_account_id,string"`
	Fingerprint     string `json:"-"`
	MerchantName    string `json:"merchant_name"`
	Category        string `json:"category"`
	BillingInterval string `json:"billing_interval"`
	AmountMinor     int64  `json:"amount_minor"`
	Currency        string `json:"currency"`
	NextBillingAt   int64  `json:"next_billing_at"`
	ConfidenceBPS   int64  `json:"confidence_bps"`
	Status          string `json:"status"`
	Version         int64  `json:"version"`
	IsSimulated     bool   `json:"is_simulated"`
	LastUsedAt      *int64 `json:"last_used_at,omitempty"`
	CancelledAt     *int64 `json:"cancelled_at,omitempty"`
}

func (Subscription) TableName() string { return "banking_subscriptions" }

type SubscriptionPayment struct {
	Base
	SubscriptionID      int64 `json:"subscription_id,string"`
	TransactionID       int64 `json:"transaction_id,string"`
	AmountMinor         int64 `json:"amount_minor"`
	OccurredAt          int64 `json:"occurred_at"`
	IsPossibleDuplicate bool  `json:"is_possible_duplicate"`
}

func (SubscriptionPayment) TableName() string { return "banking_subscription_payments" }

type SubscriptionAnalysis struct {
	Base
	Payload []byte `json:"-"`
	TraceID string `json:"trace_id"`
	TaskID  string `json:"task_id"`
	AuditID string `json:"audit_id"`
}

func (SubscriptionAnalysis) TableName() string { return "banking_subscription_analyses" }

type SubscriptionActionIntent struct {
	Base
	SubscriptionID      int64  `json:"subscription_id,string"`
	SubscriptionVersion int64  `json:"subscription_version"`
	Action              string `json:"action"`
	Status              string `json:"status"`
	ConfirmationHash    string `json:"confirmation_hash"`
	ExpiresAt           int64  `json:"expires_at"`
	ExecutedAt          *int64 `json:"executed_at,omitempty"`
	FailureCode         string `json:"failure_code"`
	TraceID             string `json:"trace_id"`
	TaskID              string `json:"task_id"`
	AuditID             string `json:"audit_id"`
}

func (SubscriptionActionIntent) TableName() string { return "banking_subscription_action_intents" }

type SubscriptionApproval struct {
	Base
	IntentID         int64  `json:"intent_id,string"`
	Decision         string `json:"decision"`
	ConfirmationHash string `json:"confirmation_hash"`
	ConsumedAt       *int64 `json:"consumed_at"`
}

func (SubscriptionApproval) TableName() string { return "banking_subscription_approvals" }
func SubscriptionDigest(i *SubscriptionActionIntent) string {
	// The version binds the immutable proposal to the price, cadence and status
	// snapshot; all subscription updates increment that version under row lock.
	raw, _ := json.Marshal([]any{i.ID, i.UserID, i.SubscriptionID, i.SubscriptionVersion, i.Action, i.ExpiresAt, "local_simulation_only"})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
