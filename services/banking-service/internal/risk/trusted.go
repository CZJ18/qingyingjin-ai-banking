package risk

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type permitKey struct{}
type permit struct {
	tx                                  *gorm.DB
	operationID, authorizationID, stage string
}

// WithPermit carries only identifiers. Every guard reads authorization from
// the caller's financial transaction; a context value cannot grant consent.
func WithPermit(ctx context.Context, tx *gorm.DB, operationID, authorizationID, stage string) context.Context {
	return context.WithValue(ctx, permitKey{}, permit{tx, operationID, authorizationID, stage})
}

type TrustedGateway struct{}
type SensitiveGuard interface {
	SensitiveCheck(context.Context, int64, string, any) error
}

func CheckSensitive(ctx context.Context, gateway any, user int64, kind string, payload any) error {
	if gateway == nil {
		return entity.Fail("RISK_REJECTED", "safety gateway required")
	}
	if g, ok := gateway.(SensitiveGuard); ok {
		return g.SensitiveCheck(ctx, user, kind, payload)
	}
	return nil
}

type TransferProposalGuard interface {
	CheckTransferProposal(context.Context, int64, *entity.TransferIntent) error
}

func CheckTransferProposal(ctx context.Context, gateway any, user int64, v *entity.TransferIntent) error {
	if gateway == nil {
		return entity.Fail("RISK_REJECTED", "safety gateway required")
	}
	if g, ok := gateway.(TransferProposalGuard); ok {
		return g.CheckTransferProposal(ctx, user, v)
	}
	return nil
}
func refusal() error {
	return entity.Fail("APPROVAL_REQUIRED", "trusted persisted operation authorization required")
}
func operation(ctx context.Context, user int64, stage string) (*entity.OperationIntent, error) {
	p, ok := ctx.Value(permitKey{}).(permit)
	if !ok || p.tx == nil || p.operationID == "" || p.stage != stage || (stage != "prepare" && stage != "execute") {
		return nil, refusal()
	}
	var owner entity.User
	if err := p.tx.WithContext(ctx).Where("id = ? AND status = ?", user, "active").First(&owner).Error; err != nil {
		return nil, refusal()
	}
	var op entity.OperationIntent
	if err := p.tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", p.operationID, user).First(&op).Error; err != nil {
		return nil, refusal()
	}
	now := time.Now().UnixMilli()
	if op.ExpiresAt <= now {
		return nil, entity.Fail("APPROVAL_EXPIRED", "operation expired")
	}
	if op.Nonce == "" || op.IntentDigest == "" || entity.OperationDigest(&op) != op.IntentDigest {
		return nil, refusal()
	}
	if stage == "prepare" {
		if op.Status != "pending_authorization" {
			return nil, refusal()
		}
		return &op, nil
	}
	if op.Status != "executing" || p.authorizationID == "" || op.AuthorizationID != p.authorizationID {
		return nil, refusal()
	}
	var a entity.UserAuthorization
	if err := p.tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", p.authorizationID, user).First(&a).Error; err != nil {
		return nil, refusal()
	}
	if a.OperationID != op.ID || a.IntentDigest != op.IntentDigest || a.Decision != "approve" || a.TokenID == "" || a.ConsumedAt != 0 {
		return nil, refusal()
	}
	if a.ExpiresAt <= now {
		return nil, entity.Fail("APPROVAL_EXPIRED", "user authorization expired")
	}
	return &op, nil
}
func (TrustedGateway) SensitiveCheck(ctx context.Context, user int64, kind string, payload any) error {
	op, e := operation(ctx, user, "execute")
	if e != nil {
		return e
	}
	if op.Kind != kind {
		return refusal()
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return refusal()
	}
	expected := []byte(op.Payload)
	if kind == "account_status" {
		var v map[string]json.RawMessage
		if json.Unmarshal(expected, &v) != nil {
			return refusal()
		}
		expected, _ = json.Marshal(map[string]json.RawMessage{"account_id": v["account_id"], "status": v["status"]})
	}
	if entity.CanonicalJSONHash(raw) == "" || entity.CanonicalJSONHash(raw) != entity.CanonicalJSONHash(expected) {
		return refusal()
	}
	return nil
}
func purpose(kind string) string {
	switch kind {
	case "transfer":
		return "transfer"
	case "savings_deposit":
		return "savings_deposit"
	case "savings_withdrawal":
		return "savings_withdrawal"
	}
	return ""
}
func matchesTransfer(op *entity.OperationIntent, v *entity.TransferIntent, user int64) bool {
	return v != nil && v.UserID == user && purpose(op.Kind) != "" && v.Purpose == purpose(op.Kind) && v.SourceAccountID == op.SourceAccountID && v.TargetAccountID == op.TargetAccountID && v.AmountMinor == op.AmountMinor && v.Currency == op.Currency
}
func (TrustedGateway) CheckTransferProposal(ctx context.Context, user int64, v *entity.TransferIntent) error {
	op, e := operation(ctx, user, "prepare")
	if e != nil {
		return e
	}
	if !matchesTransfer(op, v, user) || (op.ResourceID > 0 && op.ResourceID != v.ID) {
		return refusal()
	}
	return nil
}
func (TrustedGateway) Check(ctx context.Context, user int64, v *entity.TransferIntent, a *entity.Approval) error {
	op, e := operation(ctx, user, "execute")
	if e != nil {
		return e
	}
	var saved entity.TransferIntent
	if !matchesTransfer(op, v, user) || json.Unmarshal(op.Snapshot, &saved) != nil || op.ResourceID != v.ID || op.ResourceDigest != v.ApprovalHash || saved.ID != v.ID || entity.TransferDigest(&saved) != entity.TransferDigest(v) || v.ApprovalHash != entity.TransferDigest(v) {
		return refusal()
	}
	return (SimulationGateway{}).Check(ctx, user, v, a)
}
func (TrustedGateway) CheckFunding(ctx context.Context, user int64, t *entity.Transaction, a *entity.Audit) error {
	op, e := operation(ctx, user, "execute")
	if e != nil {
		return e
	}
	if t == nil {
		return refusal()
	}
	if op.Kind == "open_account" {
		var in struct {
			Amount   int64  `json:"initial_balance_minor"`
			Currency string `json:"currency"`
		}
		if json.Unmarshal(op.Payload, &in) != nil || t.Kind != "opening" || t.Direction != "income" || in.Amount != t.AmountMinor {
			return refusal()
		}
		if in.Currency != "" && in.Currency != t.Currency {
			return refusal()
		}
	} else if op.Kind == "import_transaction" {
		var in struct {
			Account   int64  `json:"bank_account_id,string"`
			Amount    int64  `json:"amount_minor"`
			Direction string `json:"direction"`
			Currency  string `json:"currency"`
			Effect    string `json:"effect_mode"`
		}
		if json.Unmarshal(op.Payload, &in) != nil || t.Kind != "external" || t.BankAccountID != in.Account || t.AmountMinor != in.Amount || t.Direction != in.Direction || t.EffectMode != in.Effect || (in.Currency != "" && t.Currency != in.Currency) {
			return refusal()
		}
	} else {
		return refusal()
	}
	return (SimulationGateway{}).CheckFunding(ctx, user, t, a)
}
func matchesSubscription(op *entity.OperationIntent, s *entity.Subscription) bool {
	var saved entity.Subscription
	return s != nil && op.Kind == "cancel_subscription" && json.Unmarshal(op.Snapshot, &saved) == nil && saved.ID == s.ID && saved.UserID == s.UserID && saved.Version == s.Version && saved.BankAccountID == s.BankAccountID && saved.AmountMinor == s.AmountMinor && saved.Currency == s.Currency && saved.BillingInterval == s.BillingInterval && op.SourceAccountID == s.BankAccountID && op.AmountMinor == s.AmountMinor && op.Currency == s.Currency
}
func (TrustedGateway) CheckSubscriptionProposal(ctx context.Context, user int64, s *entity.Subscription) error {
	op, e := operation(ctx, user, "prepare")
	if e != nil {
		return e
	}
	if !matchesSubscription(op, s) {
		return refusal()
	}
	return (SimulationGateway{}).CheckSubscriptionProposal(ctx, user, s)
}
func (TrustedGateway) CheckSubscriptionCancel(ctx context.Context, user int64, s *entity.Subscription, i *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) error {
	op, e := operation(ctx, user, "execute")
	if e != nil {
		return e
	}
	if !matchesSubscription(op, s) || i == nil || op.ResourceID != i.ID || op.ResourceDigest != i.ConfirmationHash {
		return refusal()
	}
	return (SimulationGateway{}).CheckSubscriptionCancel(ctx, user, s, i, a)
}
