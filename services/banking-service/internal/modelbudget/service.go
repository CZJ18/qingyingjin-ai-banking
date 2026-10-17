// Package modelbudget owns per-user model quotas, not financial account funds.
package modelbudget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Policy struct{ DailyCalls, DailyTokens, ReservationTokens int64 }

func (p Policy) Valid() bool {
	return p.DailyCalls > 0 && p.DailyCalls <= 1000 && p.ReservationTokens > 0 && p.ReservationTokens <= 1_000_000_000 && p.DailyTokens >= p.ReservationTokens && p.DailyTokens <= 1_000_000_000_000
}

type Service struct {
	db     *gorm.DB
	policy Policy
}

func New(db *gorm.DB, policy Policy) (*Service, error) {
	if db == nil || !policy.Valid() {
		return nil, coze.ErrBudgetUnavailable
	}
	return &Service{db: db, policy: policy}, nil
}

type Window struct {
	ID                                    string `gorm:"primaryKey"`
	UserID                                int64
	Period                                string
	CallsUsed, TokensUsed, TokensReserved int64
}

func (Window) TableName() string { return "banking_model_budget_windows" }

type Call struct {
	ID                                                    string `gorm:"primaryKey"`
	UserID                                                int64
	WindowID, RequestKey, RequestHash, WorkflowID, Status string
	ReservedTokens                                        int64
	ActualTokens                                          *int64
	ReportedCost                                          string
	ResponseJSON                                          *string
	CreatedAt, UpdatedAt                                  int64
}

func (Call) TableName() string { return "banking_model_calls" }
func owner(tx *gorm.DB, user int64, active bool) error {
	var row entity.User
	if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").Where("id=?", user).Take(&row).Error; e != nil {
		return e
	}
	if active && row.Status != "active" {
		return coze.ErrBudgetUnavailable
	}
	return nil
}
func safe(e error) error {
	if e == nil {
		return nil
	}
	for _, v := range []error{coze.ErrQuotaExceeded, coze.ErrOutcomeUnknown, coze.ErrRequestConflict} {
		if errors.Is(e, v) {
			return v
		}
	}
	return coze.ErrBudgetUnavailable
}
func (s *Service) Begin(ctx context.Context, user int64, key, workflow, digest string) (id string, cached []byte, err error) {
	if user <= 0 || len(key) == 0 || len(key) > 256 || len(digest) != 64 {
		return "", nil, coze.ErrBudgetUnavailable
	}
	if _, e := hex.DecodeString(digest); e != nil {
		return "", nil, coze.ErrBudgetUnavailable
	}
	k := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := owner(tx, user, true); e != nil {
			return e
		}
		var existing Call
		e := tx.Where("user_id=? AND request_key=?", user, k).Take(&existing).Error
		if e == nil {
			if existing.RequestHash != digest || existing.WorkflowID != workflow {
				return coze.ErrRequestConflict
			}
			if existing.Status == "completed" && existing.ResponseJSON != nil {
				id = existing.ID
				cached = []byte(*existing.ResponseJSON)
				return nil
			}
			return coze.ErrOutcomeUnknown
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		period := time.Now().UTC().Format("2006-01-02")
		window := Window{ID: fmt.Sprintf("%d:%s", user, period), UserID: user, Period: period}
		if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&window).Error; e != nil {
			return e
		}
		if e = tx.Where("id=?", window.ID).Take(&window).Error; e != nil {
			return e
		}
		p := s.policy
		if window.CallsUsed >= p.DailyCalls || window.TokensUsed > p.DailyTokens || window.TokensReserved > p.DailyTokens-window.TokensUsed || p.ReservationTokens > p.DailyTokens-window.TokensUsed-window.TokensReserved {
			return coze.ErrQuotaExceeded
		}
		window.CallsUsed++
		window.TokensReserved += p.ReservationTokens
		if e = tx.Save(&window).Error; e != nil {
			return e
		}
		call := Call{ID: uuid.NewString(), UserID: user, WindowID: window.ID, RequestKey: k, RequestHash: digest, WorkflowID: workflow, Status: "reserved", ReservedTokens: p.ReservationTokens}
		if e = tx.Create(&call).Error; e != nil {
			return e
		}
		id = call.ID
		return nil
	})
	return id, cached, safe(err)
}
func (s *Service) Complete(ctx context.Context, user int64, id string, raw []byte, tokens *int64, cost string, complete bool) error {
	if user <= 0 || id == "" || len(raw) > 1<<20 || len(cost) > 64 || tokens != nil && (*tokens < 0 || *tokens > 1_000_000_000_000) {
		return coze.ErrBudgetUnavailable
	}
	if complete && !json.Valid(raw) {
		return coze.ErrBudgetUnavailable
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := owner(tx, user, false); e != nil {
			return e
		}
		var call Call
		if e := tx.Where("id=? AND user_id=?", id, user).Take(&call).Error; e != nil {
			return e
		}
		if call.Status == "completed" {
			if complete && call.ResponseJSON != nil && *call.ResponseJSON == string(raw) {
				return nil
			}
			if !complete {
				return nil
			}
			return coze.ErrRequestConflict
		}
		if !complete {
			call.Status = "unknown"
			return tx.Save(&call).Error
		}
		var window Window
		if e := tx.Where("id=? AND user_id=?", call.WindowID, user).Take(&window).Error; e != nil {
			return e
		}
		if tokens != nil {
			if window.TokensReserved < call.ReservedTokens || window.TokensUsed > 1_000_000_000_000-*tokens {
				return coze.ErrBudgetUnavailable
			}
			window.TokensReserved -= call.ReservedTokens
			window.TokensUsed += *tokens
			if e := tx.Save(&window).Error; e != nil {
				return e
			}
			call.ReservedTokens = 0
			call.ActualTokens = tokens
		}
		response := string(raw)
		call.ResponseJSON = &response
		call.ReportedCost = cost
		call.Status = "completed"
		return tx.Save(&call).Error
	})
	return safe(err)
}
