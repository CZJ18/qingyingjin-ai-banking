package modelspend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service owns one cumulative project pool. A policy change cannot reset or
// silently replace a persisted pool: administrative policy amendments require
// a separate audited workflow.
type Service struct {
	db           *gorm.DB
	policy       Policy
	policyHash   string
	reserveUnits int64
	limitUnits   int64
}

func New(db *gorm.DB, p Policy) (*Service, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	// Historical settlement remains possible after the dispatch price expires.
	if err := p.Check(); err != nil {
		return nil, ErrPolicy
	}
	hash, err := p.Hash()
	if err != nil {
		return nil, ErrPolicy
	}
	reserve, err := p.ReserveUnits()
	if err != nil {
		return nil, ErrPolicy
	}
	limit, err := ParseMoney(p.Limit)
	if err != nil {
		return nil, ErrPolicy
	}
	return &Service{db: db, policy: p, policyHash: hash, reserveUnits: reserve, limitUnits: limit}, nil
}

func hashText(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func digestValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func keyValid(s string) bool {
	return len(s) > 0 && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func ledgerError(err error) error {
	if err == nil {
		return nil
	}
	for _, expected := range []error{ErrPolicy, ErrQuota, ErrConflict, ErrUnknown, ErrUnavailable, ErrBoundExceeded} {
		if errors.Is(err, expected) {
			return expected
		}
	}
	// Driver diagnostics can include credentials, statements, and identifiers.
	return ErrUnavailable
}

func (s *Service) ready() bool { return s != nil && s.db != nil && s.policyHash != "" }

// lockPool is always acquired before attempts/events, including observation and
// settlement. Every process sharing a provider project serializes on this row.
func (s *Service) lockPool(tx *gorm.DB) (Pool, error) {
	var p Pool
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id=?", s.policy.ProjectID).Take(&p).Error
	if err != nil {
		return p, err
	}
	if p.Currency != s.policy.Currency || p.PolicyHash != s.policyHash || p.LimitUnits != s.limitUnits {
		return p, ErrConflict
	}
	if p.LimitUnits <= 0 || p.UsedUnits < 0 || p.HeldUnits < 0 {
		return p, ErrUnavailable
	}
	return p, nil
}

type eventSnapshot struct {
	Policy         Policy      `json:"policy"`
	Before         Pool        `json:"before"`
	After          Pool        `json:"after"`
	PreviousStatus string      `json:"previous_status,omitempty"`
	InputHash      string      `json:"input_hash,omitempty"`
	Usage          *Usage      `json:"usage,omitempty"`
	UsageUnknown   bool        `json:"usage_unknown,omitempty"`
	ResponseDigest string      `json:"response_sha256,omitempty"`
	ReportedModel  string      `json:"reported_model,omitempty"`
	ModelDigest    string      `json:"model_sha256,omitempty"`
	Settlement     *Settlement `json:"settlement,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

func snapshotJSON(snapshot eventSnapshot) (string, error) {
	raw, err := json.Marshal(snapshot)
	return string(raw), err
}

func eventKey(kind, id string) string { return hashText(kind + "\x00" + id) }

// Reserve commits the worst-case monetary hold and its audit before returning.
// Callers may dispatch exactly once only after a fresh successful reservation.
// A same-key in-flight/unknown attempt returns ErrUnknown and must never retry.
func (s *Service) Reserve(ctx context.Context, key, digest string) (out Attempt, err error) {
	if !s.ready() || !keyValid(key) || !digestValid(digest) {
		return out, ErrUnavailable
	}
	if err = s.policy.Validate(time.Now()); err != nil {
		return out, ErrPolicy
	}
	requestKey := hashText(key)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UnixMilli()
		candidate := Pool{ProjectID: s.policy.ProjectID, Currency: s.policy.Currency, PolicyHash: s.policyHash, LimitUnits: s.limitUnits, CreatedAt: now, UpdatedAt: now}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; e != nil {
			return e
		}
		p, e := s.lockPool(tx)
		if e != nil {
			return e
		}
		var existing Attempt
		e = tx.Where("project_id=? AND request_key=?", p.ProjectID, requestKey).Take(&existing).Error
		if e == nil {
			out = existing
			if existing.RequestHash != digest || existing.PolicyHash != s.policyHash {
				return ErrConflict
			}
			if (existing.Status == "reported" || existing.Status == "settled") && existing.ResponseBody != nil {
				return nil
			}
			return ErrUnknown
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if p.Frozen || p.UsedUnits > p.LimitUnits || p.HeldUnits > p.LimitUnits-p.UsedUnits || s.reserveUnits > p.LimitUnits-p.UsedUnits-p.HeldUnits {
			return ErrQuota
		}
		before := p
		p.HeldUnits += s.reserveUnits
		p.UpdatedAt = now
		if e := tx.Save(&p).Error; e != nil {
			return e
		}
		a := Attempt{ID: uuid.NewString(), ProjectID: p.ProjectID, RequestKey: requestKey, RequestHash: digest, PolicyHash: s.policyHash, Status: "reserved", ReservedUnits: s.reserveUnits, CreatedAt: now, UpdatedAt: now}
		if e := tx.Create(&a).Error; e != nil {
			return e
		}
		snapshot, e := snapshotJSON(eventSnapshot{Policy: s.policy, Before: before, After: p})
		if e != nil {
			return e
		}
		event := Event{ID: uuid.NewString(), ProjectID: p.ProjectID, AttemptID: a.ID, Kind: "reserve", DedupeKey: eventKey("reserve", a.ID), RequestHash: digest, AmountUnits: a.ReservedUnits, SnapshotJSON: snapshot, CreatedAt: now}
		if e = tx.Create(&event).Error; e != nil {
			return e
		}
		out = a
		return nil
	})
	if err != nil && !errors.Is(err, ErrUnknown) {
		out = Attempt{}
	}
	return out, ledgerError(err)
}

var supplierID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// validateReport bounds the replay cache and prevents a provider from selecting
// executable content types. The complete response is retained byte-for-byte.
func validateReport(r Report) (digest, contentType string, err error) {
	if !supplierID.MatchString(r.ProviderID) || len(r.ResponseBody) == 0 || len(r.ResponseBody) > 2<<20 || !utf8.Valid(r.ResponseBody) {
		return "", "", ErrUnavailable
	}
	media, params, e := mime.ParseMediaType(r.ContentType)
	if e != nil || (media != "application/json" && media != "text/event-stream") {
		return "", "", ErrUnavailable
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return "", "", ErrUnavailable
		}
	}
	if media == "application/json" && !json.Valid(r.ResponseBody) {
		return "", "", ErrUnavailable
	}
	return hashText(string(r.ResponseBody)), mime.FormatMediaType(media, params), nil
}

func (s *Service) loadAttempt(tx *gorm.DB, id string) (Attempt, error) {
	var a Attempt
	err := tx.Where("id=? AND project_id=?", id, s.policy.ProjectID).Take(&a).Error
	if err == nil && a.PolicyHash != s.policyHash {
		err = ErrConflict
	}
	return a, err
}

// The pool lock serializes provider identity binding without a nullable/empty
// provider unique-index portability compromise. One response belongs to one
// attempt, including after settlement; split invoice components are unsupported.
func (s *Service) providerAvailable(tx *gorm.DB, attemptID, providerID string) error {
	if providerID == "" {
		return nil
	}
	var other Attempt
	err := tx.Select("id").Where("project_id=? AND provider_id=? AND id<>?", s.policy.ProjectID, providerID, attemptID).Take(&other).Error
	if err == nil {
		return ErrConflict
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func evaluateReport(p Policy, r Report) (estimate int64, boundExceeded bool, err error) {
	// The verified price and input/output bound apply only to this exact model.
	// Even malformed/missing usage cannot make a different model safe to price.
	if r.Model != p.Model {
		return 0, true, nil
	}
	if r.UsageUnknown {
		return 0, false, ErrUnavailable
	}
	u := r.Usage
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 || u.PromptTokens > math.MaxInt64-u.CompletionTokens || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		return 0, false, ErrPolicy
	}
	if (u.CacheHitTokens == nil) != (u.CacheMissTokens == nil) {
		return 0, false, ErrPolicy
	}
	if u.CacheHitTokens != nil && (*u.CacheHitTokens < 0 || *u.CacheMissTokens < 0 || *u.CacheHitTokens > u.PromptTokens || *u.CacheMissTokens != u.PromptTokens-*u.CacheHitTokens) {
		return 0, false, ErrPolicy
	}
	// Detect a coherent supplier overrun before Estimate's finite arithmetic
	// safety caps can classify very large counters as an ordinary bad report.
	if u.PromptTokens > p.ContextTokens || u.CompletionTokens > p.MaxOutputTokens || u.TotalTokens > p.ContextTokens {
		return 0, true, nil
	}
	estimate, err = p.Estimate(r.Usage)
	if errors.Is(err, ErrBoundExceeded) {
		return 0, true, nil
	}
	return estimate, false, err
}

func reportModelEvidence(model string) (value, digest string) {
	// A malicious/invalid supplier identity must not make the freeze audit too
	// large to commit. Retain its full fingerprint and only a bounded raw value.
	if len(model) <= 128 && utf8.ValidString(model) {
		value = model
	}
	return value, hashText(model)
}

// Observe records an unverified supplier report without releasing any hold.
// A bound violation commits a freeze audit, then returns ErrBoundExceeded.
func (s *Service) Observe(ctx context.Context, id string, r Report) error {
	if !s.ready() || len(id) == 0 || len(id) > 36 {
		return ErrUnavailable
	}
	digest, contentType, err := validateReport(r)
	if err != nil {
		return err
	}
	estimate, boundExceeded, usageErr := evaluateReport(s.policy, r)
	if usageErr != nil {
		return ledgerError(usageErr)
	}
	var usageJSON []byte
	var usage *Usage
	if !r.UsageUnknown {
		usageJSON, err = json.Marshal(r.Usage)
		if err != nil {
			return ErrUnavailable
		}
		usage = &r.Usage
	}
	boundExceeded = boundExceeded || estimate > s.reserveUnits
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, e := s.lockPool(tx)
		if e != nil {
			return e
		}
		a, e := s.loadAttempt(tx, id)
		if e != nil {
			return e
		}
		if a.ProviderID != "" && a.ProviderID != r.ProviderID {
			return ErrConflict
		}
		if e := s.providerAvailable(tx, a.ID, r.ProviderID); e != nil {
			return e
		}
		if a.ResponseDigest != "" {
			if a.ResponseDigest != digest || a.UsageJSON != string(usageJSON) || a.ContentType != contentType {
				return ErrConflict
			}
			if boundExceeded && a.ResponseBody != nil {
				return ErrConflict
			}
			return nil
		}
		before, previous := p, a.Status
		now := time.Now().UnixMilli()
		a.ProviderID, a.UsageJSON, a.ResponseDigest, a.ContentType = r.ProviderID, string(usageJSON), digest, contentType
		a.UpdatedAt = now
		kind := "report"
		reason := ""
		if boundExceeded {
			kind, reason = "freeze", "supplier_usage_exceeded_reserved_bound"
			if r.Model != s.policy.Model {
				reason = "supplier_model_mismatch"
			}
			p.Frozen, p.UpdatedAt = true, now
			if a.Status != "settled" {
				a.Status = "unknown"
			}
			if e := tx.Save(&p).Error; e != nil {
				return e
			}
		} else {
			body := string(r.ResponseBody)
			a.ResponseBody, a.EstimatedUnits = &body, &estimate
			if a.Status != "settled" {
				a.Status = "reported"
			}
		}
		if e := tx.Save(&a).Error; e != nil {
			return e
		}
		model, modelDigest := reportModelEvidence(r.Model)
		snapshot, e := snapshotJSON(eventSnapshot{Policy: s.policy, Before: before, After: p, PreviousStatus: previous, Usage: usage, UsageUnknown: r.UsageUnknown, ResponseDigest: digest, ReportedModel: model, ModelDigest: modelDigest, Reason: reason})
		if e != nil {
			return e
		}
		return tx.Create(&Event{ID: uuid.NewString(), ProjectID: p.ProjectID, AttemptID: a.ID, Kind: kind, DedupeKey: eventKey(kind, a.ID), RequestHash: hashText(string(usageJSON) + "\x00" + r.ProviderID + "\x00" + digest + "\x00" + contentType), ProviderID: r.ProviderID, AmountUnits: estimate, SnapshotJSON: snapshot, CreatedAt: now}).Error
	})
	if err != nil {
		return ledgerError(err)
	}
	if boundExceeded {
		return ErrBoundExceeded
	}
	return nil
}

// Unknown never refunds or erases a completed report/independent settlement.
func (s *Service) Unknown(ctx context.Context, id string) error {
	if !s.ready() || len(id) == 0 || len(id) > 36 {
		return ErrUnavailable
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, e := s.lockPool(tx)
		if e != nil {
			return e
		}
		a, e := s.loadAttempt(tx, id)
		if e != nil {
			return e
		}
		if a.Status == "unknown" || a.Status == "reported" || a.Status == "settled" {
			return nil
		}
		if a.Status != "reserved" {
			return ErrUnavailable
		}
		previous := a.Status
		a.Status, a.UpdatedAt = "unknown", time.Now().UnixMilli()
		if e := tx.Save(&a).Error; e != nil {
			return e
		}
		snapshot, e := snapshotJSON(eventSnapshot{Policy: s.policy, Before: p, After: p, PreviousStatus: previous})
		if e != nil {
			return e
		}
		return tx.Create(&Event{ID: uuid.NewString(), ProjectID: p.ProjectID, AttemptID: a.ID, Kind: "unknown", DedupeKey: eventKey("unknown", a.ID), RequestHash: a.RequestHash, AmountUnits: a.ReservedUnits, SnapshotJSON: snapshot, CreatedAt: a.UpdatedAt}).Error
	})
	return ledgerError(err)
}
