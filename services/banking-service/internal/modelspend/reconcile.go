package modelspend

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Reconciler struct {
	service    *Service
	secretHash [32]byte
	operator   string
}

func NewReconciler(s *Service, secret, operator string) (*Reconciler, error) {
	if !s.ready() || len(secret) < 32 || len(secret) > 4096 || !identifier.MatchString(operator) {
		return nil, ErrUnavailable
	}
	return &Reconciler{service: s, secretHash: sha256.Sum256([]byte(secret)), operator: operator}, nil
}

func (r *Reconciler) authorized(token string) bool {
	if r == nil || len(token) > 4096 {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(hash[:], r.secretHash[:]) == 1 && len(token) >= 32
}

var billIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var currencyIdentity = regexp.MustCompile(`^[A-Z]{3}$`)

func validateSettlement(in Settlement) (int64, error) {
	if in.AttemptID == "" || len(in.AttemptID) > 36 || !identifier.MatchString(in.ProjectID) || !currencyIdentity.MatchString(in.Currency) || !billIdentity.MatchString(in.BillID) || !billIdentity.MatchString(in.LineID) || !supplierID.MatchString(in.ProviderID) || !digestValid(in.EvidenceDigest) {
		return 0, ErrUnavailable
	}
	amount, err := ParseMoney(in.ActualCost)
	if err != nil {
		return 0, ErrUnavailable
	}
	return amount, nil
}

// Pending exposes at most 100 oldest unresolved attempts from this project.
// Response bodies and prompts are deliberately absent from the operator queue.
func (r *Reconciler) Pending(ctx context.Context, token string) ([]Attempt, error) {
	if !r.authorized(token) || !r.service.ready() {
		return nil, ErrUnavailable
	}
	var rows []Attempt
	err := r.service.db.WithContext(ctx).Select("id", "project_id", "request_key", "request_hash", "policy_hash", "status", "reserved_units", "estimated_units", "actual_units", "provider_id", "usage_json", "response_digest", "created_at", "updated_at").Where("project_id=? AND actual_units IS NULL", r.service.policy.ProjectID).Order("created_at ASC,id ASC").Limit(100).Find(&rows).Error
	return rows, ledgerError(err)
}

// Reconcile uses two independent identities: one supplier bill row across all
// operators, and one operator request key. Neither can be rebound to new input.
// Event.RequestHash on settle events stores the operator/key fingerprint;
// SnapshotJSON.InputHash stores the exact settlement payload fingerprint.
func (r *Reconciler) Reconcile(ctx context.Context, token, key string, in Settlement) (out SettlementResult, err error) {
	if !r.authorized(token) || !r.service.ready() || !keyValid(key) {
		return out, ErrUnavailable
	}
	actual, err := validateSettlement(in)
	if err != nil {
		return out, err
	}
	s := r.service
	if in.ProjectID != s.policy.ProjectID || in.Currency != s.policy.Currency {
		return out, ErrConflict
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, ErrUnavailable
	}
	inputHash := hashText(string(raw))
	billKey := hashText(in.ProjectID + "\x00" + in.BillID + "\x00" + in.LineID)
	requestKey := hashText(r.operator + "\x00" + key)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, e := s.lockPool(tx)
		if e != nil {
			return e
		}
		var prior Event
		e = tx.Where("project_id=? AND dedupe_key=?", p.ProjectID, billKey).Take(&prior).Error
		if e == nil {
			var snapshot eventSnapshot
			if prior.Kind != "settle" || prior.AttemptID != in.AttemptID || json.Unmarshal([]byte(prior.SnapshotJSON), &snapshot) != nil || snapshot.InputHash != inputHash {
				return ErrConflict
			}
			// A new operator/key replay receives a zero-money alias binding.
			// Subsequent exact key replays remain read-only, while this alias
			// cannot later be reused to settle a different supplier bill row.
			var reused Event
			e := tx.Where("project_id=? AND kind IN ? AND request_hash=?", p.ProjectID, []string{"settle", "settle_request"}, requestKey).Take(&reused).Error
			if e == nil {
				var binding eventSnapshot
				if reused.AttemptID != in.AttemptID || reused.BillID != in.BillID || reused.LineID != in.LineID || json.Unmarshal([]byte(reused.SnapshotJSON), &binding) != nil || binding.InputHash != inputHash {
					return ErrConflict
				}
			} else if errors.Is(e, gorm.ErrRecordNotFound) {
				aliasSnapshot, e := snapshotJSON(eventSnapshot{Policy: s.policy, Before: p, After: p, InputHash: inputHash, Settlement: &in, Reason: "bill_replay_request_binding"})
				if e != nil {
					return e
				}
				alias := Event{ID: uuid.NewString(), ProjectID: p.ProjectID, AttemptID: prior.AttemptID, Kind: "settle_request", DedupeKey: eventKey("settle_request", p.ProjectID+"\x00"+requestKey), RequestHash: requestKey, AmountUnits: 0, ProviderID: in.ProviderID, BillID: in.BillID, LineID: in.LineID, EvidenceDigest: in.EvidenceDigest, OperatorID: r.operator, SnapshotJSON: aliasSnapshot, CreatedAt: time.Now().UnixMilli()}
				if e := tx.Create(&alias).Error; e != nil {
					return e
				}
			} else {
				return e
			}
			out = SettlementResult{Replay: true, Frozen: p.Frozen, EventID: prior.ID}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		var keyBinding Event
		e = tx.Where("project_id=? AND kind IN ? AND request_hash=?", p.ProjectID, []string{"settle", "settle_request"}, requestKey).Take(&keyBinding).Error
		if e == nil {
			return ErrConflict
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		a, e := s.loadAttempt(tx, in.AttemptID)
		if e != nil {
			return e
		}
		if a.ActualUnits != nil || a.Status == "settled" {
			// Corrections require a future append-only adjustment operation.
			return ErrConflict
		}
		if a.ProviderID != "" && a.ProviderID != in.ProviderID {
			return ErrConflict
		}
		if e := s.providerAvailable(tx, a.ID, in.ProviderID); e != nil {
			return e
		}
		if a.Status == "reserved" && !in.ConfirmNoActiveRun {
			return ErrUnknown
		}
		if a.Status != "reserved" && a.Status != "unknown" && a.Status != "reported" {
			return ErrUnavailable
		}
		if a.ReservedUnits < 0 || p.HeldUnits < a.ReservedUnits || p.UsedUnits > math.MaxInt64-actual {
			return ErrUnavailable
		}
		before, previous := p, a.Status
		now := time.Now().UnixMilli()
		p.HeldUnits -= a.ReservedUnits
		p.UsedUnits += actual
		// Preserve the entire independently evidenced debt, even beyond limit.
		if actual > a.ReservedUnits || p.UsedUnits > p.LimitUnits || p.HeldUnits > p.LimitUnits-p.UsedUnits {
			p.Frozen = true
		}
		p.UpdatedAt = now
		a.ActualUnits, a.ProviderID, a.Status, a.UpdatedAt = &actual, in.ProviderID, "settled", now
		// ReservedUnits is the immutable original bound; pool.HeldUnits records
		// the remaining monetary hold. ActualUnits prevents a second release.
		if e := tx.Save(&p).Error; e != nil {
			return e
		}
		if e := tx.Save(&a).Error; e != nil {
			return e
		}
		reason := ""
		if actual > a.ReservedUnits {
			reason = "supplier_bill_exceeded_reserved_bound"
		}
		snapshot, e := snapshotJSON(eventSnapshot{Policy: s.policy, Before: before, After: p, PreviousStatus: previous, InputHash: inputHash, Settlement: &in, Reason: reason})
		if e != nil {
			return e
		}
		event := Event{ID: uuid.NewString(), ProjectID: p.ProjectID, AttemptID: a.ID, Kind: "settle", DedupeKey: billKey, RequestHash: requestKey, AmountUnits: actual, ProviderID: in.ProviderID, BillID: in.BillID, LineID: in.LineID, EvidenceDigest: in.EvidenceDigest, OperatorID: r.operator, SnapshotJSON: snapshot, CreatedAt: now}
		if e = tx.Create(&event).Error; e != nil {
			return e
		}
		out = SettlementResult{Frozen: p.Frozen, EventID: event.ID}
		return nil
	})
	if err != nil {
		out = SettlementResult{}
	}
	return out, ledgerError(err)
}
