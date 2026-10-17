package aa

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
)

type LineItem struct {
	Name        string `json:"name"`
	AmountMinor int64  `json:"amount_minor"`
}
type Consent struct {
	ID         string `json:"id"`
	Digest     string `json:"digest"`
	Nonce      string `json:"nonce"`
	ExpiresAt  int64  `json:"expires_at"`
	ConsumedAt int64  `json:"consumed_at"`
}
type ExternalRecord struct {
	Report     *Consent `json:"report,omitempty"`
	Receipt    *Consent `json:"receipt,omitempty"`
	ReportedAt int64    `json:"reported_at"`
	ReceivedAt int64    `json:"received_at"`
}
type ReviewInput struct {
	ParticipantID   string `json:"participant_id"`
	Purpose         string `json:"purpose"`
	ExpectedVersion int64  `json:"expected_version"`
}
type ConfirmInput struct {
	ReviewInput
	IntentID string `json:"intent_id"`
	Digest   string `json:"digest"`
	Nonce    string `json:"nonce"`
}
type Review struct {
	Bill          View   `json:"bill"`
	ParticipantID string `json:"participant_id"`
	Purpose       string `json:"purpose"`
	IntentID      string `json:"intent_id"`
	Digest        string `json:"digest"`
	Nonce         string `json:"nonce"`
	ExpiresAt     int64  `json:"expires_at"`
}

func method(value string) string {
	if value == "" {
		return "simulation"
	}
	return value
}

// Empty stored fields retain the historical collaborative mode. Only new input defaults change.
func management(value string) string {
	if value == "" {
		return "collaborative"
	}
	return value
}
func newManagement(in Input) string {
	if in.ManagementMode == "" && method(in.PaymentMethod) == "wechat_manual" {
		return "single_manager"
	}
	return management(in.ManagementMode)
}
func validateDetails(in Input) error {
	bad := func() error {
		return entity.Fail("INVALID_ARGUMENT", "valid settlement method, recipient hint and exact positive item totals required")
	}
	if management(in.ManagementMode) != "collaborative" && management(in.ManagementMode) != "single_manager" {
		return bad()
	}
	if management(in.ManagementMode) == "single_manager" && method(in.PaymentMethod) != "wechat_manual" {
		return bad()
	}
	if method(in.PaymentMethod) != "simulation" && method(in.PaymentMethod) != "wechat_manual" {
		return bad()
	}
	if len(in.Memo) > 400 || len(in.RecipientHint) > 128 || len(in.Items) > 30 {
		return bad()
	}
	if method(in.PaymentMethod) == "wechat_manual" && (strings.TrimSpace(in.RecipientHint) == "" || len(in.Items) == 0 || in.AccountID != 0) {
		return bad()
	}
	if len(in.Items) > 0 {
		sum := int64(0)
		for _, item := range in.Items {
			if strings.TrimSpace(item.Name) == "" || len(item.Name) > 128 || item.AmountMinor <= 0 || item.AmountMinor > in.TotalMinor-sum {
				return bad()
			}
			sum += item.AmountMinor
		}
		if sum != in.TotalMinor {
			return bad()
		}
	}
	return nil
}
func validShareState(b Bill, p Share) bool {
	if management(b.ManagementMode) == "single_manager" {
		if method(b.PaymentMethod) != "wechat_manual" || p.UserID != 0 || p.OperationID != "" || p.PaidAt != 0 {
			return false
		}
		if p.External != nil && (p.External.Report != nil || p.External.ReportedAt != 0) {
			return false
		}
		return p.Status == "awaiting_receipt" && (p.External == nil || p.External.ReceivedAt == 0) || p.Status == "receipt_confirmed" && p.External != nil && p.External.ReceivedAt > 0
	}
	if method(b.PaymentMethod) == "simulation" {
		return p.External == nil && (p.Status == "unclaimed" || p.Status == "joined" || p.Status == "paid")
	}
	if p.OperationID != "" || p.PaidAt != 0 {
		return false
	}
	switch p.Status {
	case "unclaimed":
		return p.External == nil
	case "joined":
		return p.External == nil || p.External.ReportedAt == 0 && p.External.ReceivedAt == 0
	case "payment_reported":
		return p.External != nil && p.External.ReportedAt > 0 && p.External.ReceivedAt == 0
	case "receipt_confirmed":
		return p.External != nil && p.External.ReportedAt > 0 && p.External.ReceivedAt >= p.External.ReportedAt
	}
	return false
}
func reviewDigest(b Bill, p Share, user int64, purpose string) string {
	return entity.CanonicalJSONHash(marshal(struct {
		BillID        string
		OwnerID       int64
		ParticipantID string
		UserID        int64
		Purpose       string
		Method        string
		Management    string
		Title         string
		Total         int64
		Amount        int64
		Recipient     string
		Memo          string
		Items         []LineItem
	}{b.ID, b.OwnerID, p.ID, user, purpose, method(b.PaymentMethod), management(b.ManagementMode), b.Title, b.TotalMinor, p.AmountMinor, b.RecipientHint, b.Memo, b.Items}))
}
func externalShare(b *Bill, user int64, in ReviewInput) (*Share, error) {
	if in.ExpectedVersion < 1 {
		return nil, entity.Fail("INVALID_ARGUMENT", "positive expected_version required")
	}
	if method(b.PaymentMethod) != "wechat_manual" {
		return nil, entity.Fail("STATE_CONFLICT", "not a manual WeChat bill")
	}
	if in.Purpose != "report_paid" && in.Purpose != "confirm_received" {
		return nil, entity.Fail("INVALID_ARGUMENT", "explicit review purpose required")
	}
	if uuid.Validate(in.ParticipantID) != nil {
		return nil, entity.Fail("INVALID_ARGUMENT", "participant UUID required")
	}
	for i := range b.Shares {
		p := &b.Shares[i]
		if p.ID != in.ParticipantID || i == 0 {
			continue
		}
		if management(b.ManagementMode) == "single_manager" {
			if user != b.OwnerID {
				return nil, entity.Fail("FORBIDDEN", "only manager may record receipt")
			}
			if in.Purpose != "confirm_received" {
				return nil, entity.Fail("STATE_CONFLICT", "single-manager mode does not represent payer self-report")
			}
			if b.Status != "open" || p.Status != "awaiting_receipt" {
				return nil, entity.Fail("STATE_CONFLICT", "share is not awaiting manager receipt")
			}
			return p, nil
		}
		if in.Purpose == "report_paid" {
			if p.UserID != user || user == b.OwnerID {
				return nil, entity.Fail("FORBIDDEN", "only payer may report their own payment")
			}
			if p.Status != "joined" || b.Status != "open" {
				return nil, entity.Fail("STATE_CONFLICT", "share is not awaiting a payment report")
			}
		} else {
			if user != b.OwnerID {
				return nil, entity.Fail("FORBIDDEN", "only bill owner may confirm receipt")
			}
			if p.Status != "payment_reported" {
				return nil, entity.Fail("STATE_CONFLICT", "payer must report payment first")
			}
		}
		return p, nil
	}
	return nil, entity.Fail("NOT_FOUND", "participant not found")
}
func (s *Service) manual(ctx context.Context, user, owner int64, id string, fn func(*gorm.DB, *Portfolio, *Bill) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := LockUsers(tx, user, owner); e != nil {
			return e
		}
		p, e := Load(tx, owner)
		if e != nil {
			return e
		}
		b, e := Find(&p, id)
		if e != nil {
			return e
		}
		if !member(*b, user) {
			return entity.Fail("NOT_FOUND", "AA bill not found")
		}
		if e = fn(tx, &p, b); e != nil {
			return e
		}
		return Save(tx, owner, p)
	})
}
func manualAudit(tx *gorm.DB, user int64, id, purpose, digest, state, trace string) error {
	return tx.Create(&entity.AuditLog{ID: uuid.NewString(), UserID: &user, OperationID: id, OperationType: "aa_wechat_" + purpose, Permission: "RECORD", AuthorizationDigest: digest, RiskLevel: "none", State: state, TraceID: trace}).Error
}
func (s *Service) PrepareManual(ctx context.Context, user, owner int64, id string, in ReviewInput, trace string) (out Review, err error) {
	err = s.manual(ctx, user, owner, id, func(tx *gorm.DB, _ *Portfolio, b *Bill) error {
		p, e := externalShare(b, user, in)
		if e != nil {
			return e
		}
		if b.Version != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "AA bill changed; review current details")
		}
		raw := make([]byte, 32)
		if _, e = rand.Read(raw); e != nil {
			return e
		}
		c := &Consent{ID: uuid.NewString(), Digest: reviewDigest(*b, *p, user, in.Purpose), Nonce: hex.EncodeToString(raw), ExpiresAt: time.Now().Add(15 * time.Minute).UnixMilli()}
		if p.External == nil {
			p.External = &ExternalRecord{}
		}
		if in.Purpose == "report_paid" {
			p.External.Report = c
		} else {
			p.External.Receipt = c
		}
		b.Version++
		b.UpdatedAt = time.Now().UnixMilli()
		out = Review{Bill: asView(*b, user), ParticipantID: p.ID, Purpose: in.Purpose, IntentID: c.ID, Digest: c.Digest, Nonce: c.Nonce, ExpiresAt: c.ExpiresAt}
		return manualAudit(tx, user, c.ID, in.Purpose, c.Digest, "review_prepared", trace)
	})
	return
}
func (s *Service) ConfirmManual(ctx context.Context, user, owner int64, id string, in ConfirmInput, trace string) (out View, err error) {
	err = s.manual(ctx, user, owner, id, func(tx *gorm.DB, _ *Portfolio, b *Bill) error {
		p, e := externalShare(b, user, in.ReviewInput)
		if e != nil {
			return e
		}
		if b.Version != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "AA bill changed; review current details")
		}
		var c *Consent
		if p.External != nil {
			if in.Purpose == "report_paid" {
				c = p.External.Report
			} else {
				c = p.External.Receipt
			}
		}
		now := time.Now().UnixMilli()
		if c == nil || c.ConsumedAt != 0 || c.ExpiresAt <= now || c.ID != in.IntentID || c.Digest != in.Digest || c.Digest != reviewDigest(*b, *p, user, in.Purpose) || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(in.Nonce)) != 1 {
			return entity.Fail("STATE_CONFLICT", "review expired, changed or invalid; request a fresh detail review")
		}
		c.ConsumedAt = now
		c.Nonce = ""
		if in.Purpose == "report_paid" {
			p.Status = "payment_reported"
			p.External.ReportedAt = now
		} else {
			p.Status = "receipt_confirmed"
			p.External.ReceivedAt = now
			if b.Status != "cancelled" {
				b.Status = "settled"
				for i, s := range b.Shares {
					if i > 0 && s.Status != "receipt_confirmed" {
						b.Status = "open"
					}
				}
			}
		}
		b.Version++
		b.UpdatedAt = now
		out = asView(*b, user)
		return manualAudit(tx, user, c.ID, in.Purpose, c.Digest, p.Status, trace)
	})
	return
}
