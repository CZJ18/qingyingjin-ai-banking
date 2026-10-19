// Package modelspend accounts for supplier model expenses, never bank balances.
package modelspend

import "errors"

var (
	ErrPolicy        = errors.New("MODEL_SPEND_POLICY_INVALID")
	ErrQuota         = errors.New("MODEL_SPEND_QUOTA_EXCEEDED")
	ErrConflict      = errors.New("MODEL_SPEND_CONFLICT")
	ErrUnknown       = errors.New("MODEL_SPEND_OUTCOME_UNKNOWN")
	ErrUnavailable   = errors.New("MODEL_SPEND_UNAVAILABLE")
	ErrBoundExceeded = errors.New("MODEL_SPEND_BOUND_EXCEEDED")
)

const CurrencyScale int64 = 100_000_000

type Policy struct {
	ProjectID           string `json:"project_id"`
	Currency            string `json:"currency"`
	Model               string `json:"model"`
	PriceVersion        string `json:"price_version"`
	PriceSource         string `json:"price_source"`
	PriceEvidenceSHA256 string `json:"price_evidence_sha256"`
	ValidFrom           string `json:"valid_from"`
	ValidUntil          string `json:"valid_until"`
	Limit               string `json:"lifetime_limit"`
	InputPrice          string `json:"input_price_per_million"`
	CacheHitPrice       string `json:"cache_hit_price_per_million"`
	OutputPrice         string `json:"output_price_per_million"`
	ContextTokens       int64  `json:"provider_context_tokens"`
	MaxOutputTokens     int64  `json:"max_output_tokens"`
}

type Usage struct {
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CacheHitTokens   *int64 `json:"prompt_cache_hit_tokens,omitempty"`
	CacheMissTokens  *int64 `json:"prompt_cache_miss_tokens,omitempty"`
}

type Pool struct {
	ProjectID                        string `gorm:"primaryKey"`
	Currency, PolicyHash             string
	LimitUnits, UsedUnits, HeldUnits int64
	Frozen                           bool
	CreatedAt                        int64 `gorm:"autoCreateTime:milli"`
	UpdatedAt                        int64 `gorm:"autoUpdateTime:milli"`
}

func (Pool) TableName() string { return "banking_model_spend_pools" }

type Attempt struct {
	ID                                                     string `gorm:"primaryKey"`
	ProjectID, RequestKey, RequestHash, PolicyHash, Status string
	ReservedUnits                                          int64
	EstimatedUnits, ActualUnits                            *int64
	ProviderID, UsageJSON                                  string
	ResponseBody                                           *string
	ContentType, ResponseDigest                            string
	CreatedAt                                              int64 `gorm:"autoCreateTime:milli"`
	UpdatedAt                                              int64 `gorm:"autoUpdateTime:milli"`
}

func (Attempt) TableName() string { return "banking_model_spend_attempts" }

type Event struct {
	ID                                                                   string `gorm:"primaryKey"`
	ProjectID, AttemptID, Kind, DedupeKey, RequestHash                   string
	AmountUnits                                                          int64
	ProviderID, BillID, LineID, EvidenceDigest, OperatorID, SnapshotJSON string
	CreatedAt                                                            int64 `gorm:"autoCreateTime:milli"`
}

func (Event) TableName() string { return "banking_model_spend_events" }

type Report struct {
	UsageUnknown bool
	Model        string
	ProviderID   string
	Usage        Usage
	ResponseBody []byte
	ContentType  string
}

type Settlement struct {
	AttemptID          string `json:"attempt_id"`
	ProjectID          string `json:"project_id"`
	Currency           string `json:"currency"`
	BillID             string `json:"bill_id"`
	LineID             string `json:"line_id"`
	ProviderID         string `json:"provider_id"`
	ActualCost         string `json:"actual_cost"`
	EvidenceDigest     string `json:"evidence_sha256"`
	ConfirmNoActiveRun bool   `json:"confirm_no_active_run"`
}
type SettlementResult struct {
	Replay  bool   `json:"replay"`
	Frozen  bool   `json:"frozen"`
	EventID string `json:"event_id"`
}
