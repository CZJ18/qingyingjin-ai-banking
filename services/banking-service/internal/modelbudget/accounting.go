package modelbudget

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/coze"
	"gorm.io/gorm"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// RecordReport preserves untrusted supplier evidence independently of whether
// model output is accepted. Missing currency/unit never means CNY or zero cost.
func (s *Service) RecordReport(ctx context.Context, user int64, id string, r coze.CostReport) error {
	if user <= 0 || id == "" || len(r.ExecuteID) > 128 || len(r.ResponseDigest) != 64 || !coze.ValidCostMetadata(r.Currency, r.Unit) {
		return coze.ErrBudgetUnavailable
	}
	return safe(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := owner(tx, user, false); e != nil {
			return e
		}
		var call Call
		if e := tx.Where("id=? AND user_id=?", id, user).Take(&call).Error; e != nil {
			return e
		}
		if call.ReportDigest != "" {
			if call.ReportDigest == r.ResponseDigest {
				return nil
			}
			return coze.ErrRequestConflict
		}
		raw := r.Raw
		if len(raw) > 4096 {
			raw = raw[:4096]
			for !utf8.ValidString(raw) {
				raw = raw[:len(raw)-1]
			}
		} // Full response fingerprint remains available.
		call.RawReportedCost = &raw
		call.CostCurrency = r.Currency
		call.CostUnit = r.Unit
		call.ProviderExecuteID = r.ExecuteID
		call.ReportDigest = r.ResponseDigest
		call.CostStatus = "missing"
		if r.Raw != "" || r.InvalidFormat {
			call.CostStatus = "invalid"
			if !r.InvalidFormat && coze.ValidReportedCost(r.Raw) {
				value := r.Raw
				call.ValidatedReportedCost = &value
				call.CostStatus = "reported_unverified"
			}
		}
		if r.Tokens != nil && *r.Tokens >= 0 && *r.Tokens <= 1_000_000_000_000 {
			call.ReportedTokens = r.Tokens
		}
		return tx.Save(&call).Error
	}))
}

type Reconciliation struct {
	ID                                               string `gorm:"primaryKey"`
	CallID                                           string
	UserID                                           int64
	OperatorID, RequestKey, RequestHash              string
	Tokens                                           *int64
	ActualCost                                       *string
	Currency, Unit, EvidenceRef, PreviousUsageStatus string
	CreatedAt                                        int64
}

func (Reconciliation) TableName() string { return "banking_model_reconciliations" }

type ReconcileInput struct {
	UserID                      int64
	CallID                      string
	Tokens                      *int64
	ActualCost                  *string
	Currency, Unit, EvidenceRef string
	ConfirmNoActiveRun          bool
}
type Reconciler struct {
	db         *gorm.DB
	secretHash [32]byte
	operator   string
}

var operatorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
var evidencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/#-]{0,255}$`)

func NewReconciler(db *gorm.DB, secret, operator string) (*Reconciler, error) {
	if db == nil || len(secret) < 32 || !operatorPattern.MatchString(operator) {
		return nil, coze.ErrBudgetUnavailable
	}
	return &Reconciler{db: db, secretHash: sha256.Sum256([]byte(secret)), operator: operator}, nil
}
func (r *Reconciler) authorized(token string) bool {
	h := sha256.Sum256([]byte(token))
	return len(token) >= 32 && subtle.ConstantTimeCompare(h[:], r.secretHash[:]) == 1
}

type PendingCall struct {
	WorkflowID                                                                                string
	ID                                                                                        string
	UserID                                                                                    int64
	Status, UsageStatus, CostStatus, ProviderExecuteID, ReportDigest, RequestKey, RequestHash string
	ReservedTokens                                                                            int64
	ReportedTokens                                                                            *int64
	CreatedAt, UpdatedAt                                                                      int64
}

func (r *Reconciler) Pending(ctx context.Context, token string) ([]PendingCall, error) {
	if !r.authorized(token) {
		return nil, coze.ErrBudgetUnavailable
	}
	var rows []PendingCall
	e := r.db.WithContext(ctx).Model(&Call{}).Select("id", "user_id", "status", "usage_status", "cost_status", "provider_execute_id", "report_digest", "request_key", "request_hash", "reserved_tokens", "reported_tokens", "created_at", "updated_at", "workflow_id").Where("usage_status='pending' OR (usage_status='legacy' AND actual_tokens IS NULL)").Order("created_at,id").Limit(100).Find(&rows).Error
	return rows, safe(e)
}

// Reconcile never dispatches/retries a model or grants financial authorization.
// The offline operator must provide independent settlement evidence, not a prompt.
func (r *Reconciler) Reconcile(ctx context.Context, token, key string, in ReconcileInput) error {
	if !r.authorized(token) || len(key) == 0 || len(key) > 128 || in.UserID <= 0 || in.CallID == "" || !evidencePattern.MatchString(in.EvidenceRef) || in.Tokens == nil && in.ActualCost == nil {
		return coze.ErrBudgetUnavailable
	}
	if in.Tokens != nil && (*in.Tokens < 0 || *in.Tokens > 1_000_000_000_000) {
		return coze.ErrBudgetUnavailable
	}
	if in.ActualCost != nil && (!coze.ValidReportedCost(*in.ActualCost) || in.Currency == "" || !coze.ValidCostMetadata(in.Currency, in.Unit)) {
		return coze.ErrBudgetUnavailable
	}
	if in.ActualCost == nil && (in.Currency != "" || in.Unit != "") {
		return coze.ErrBudgetUnavailable
	}
	body, _ := json.Marshal(in)
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	requestKey := fmt.Sprintf("%x", sha256.Sum256([]byte(r.operator+"\x00"+strconv.FormatInt(in.UserID, 10)+"\x00"+key)))
	return safe(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := owner(tx, in.UserID, false); e != nil {
			return e
		}
		var old Reconciliation
		e := tx.Where("request_key=?", requestKey).Take(&old).Error
		if e == nil {
			if old.RequestHash != hash {
				return coze.ErrRequestConflict
			}
			return nil
		}
		if e != gorm.ErrRecordNotFound {
			return e
		}
		var call Call
		if e = tx.Where("id=? AND user_id=?", in.CallID, in.UserID).Take(&call).Error; e != nil {
			return e
		}
		previous := call.UsageStatus
		// A reserved request may still be in flight. Only completed/unknown outcomes
		// can be reviewed; crash-left reserved records require independent cancellation.
		if call.Status == "reserved" {
			if !in.ConfirmNoActiveRun {
				return coze.ErrOutcomeUnknown
			}
			call.Status = "unknown"
		}
		if in.Tokens != nil {
			if call.ActualTokens != nil {
				return coze.ErrRequestConflict
			}
			var w Window
			if e = tx.Where("id=? AND user_id=?", call.WindowID, in.UserID).Take(&w).Error; e != nil {
				return e
			}
			if w.TokensReserved < call.ReservedTokens || w.TokensUsed > 1_000_000_000_000-*in.Tokens {
				return coze.ErrBudgetUnavailable
			}
			w.TokensReserved -= call.ReservedTokens
			w.TokensUsed += *in.Tokens
			if e = tx.Save(&w).Error; e != nil {
				return e
			}
			call.ReservedTokens = 0
			call.ActualTokens = in.Tokens
			call.UsageStatus = "reconciled"
		}
		if in.ActualCost != nil {
			if call.ConfirmedCost != nil {
				return coze.ErrRequestConflict
			}
			call.ConfirmedCost = in.ActualCost
			call.ConfirmedCurrency = in.Currency
			call.ConfirmedUnit = in.Unit
			call.CostStatus = "reconciled"
		}
		if e = tx.Save(&call).Error; e != nil {
			return e
		}
		record := Reconciliation{ID: uuid.NewString(), CallID: call.ID, UserID: in.UserID, OperatorID: r.operator, RequestKey: requestKey, RequestHash: hash, Tokens: in.Tokens, ActualCost: in.ActualCost, Currency: in.Currency, Unit: in.Unit, EvidenceRef: in.EvidenceRef, PreviousUsageStatus: previous, CreatedAt: time.Now().UnixMilli()}
		return tx.Create(&record).Error
	}))
}
