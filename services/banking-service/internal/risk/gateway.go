package risk

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"time"
)

// Gateway is the module8 integration point. Checks occur inside the financial
// transaction using a persisted approval, never an Agent-supplied boolean.
// Implementations must not start external money movements or modify balances.
type Gateway interface {
	Check(context.Context, int64, *entity.TransferIntent, *entity.Approval) error
	CheckFunding(context.Context, int64, *entity.Transaction, *entity.Audit) error
}
type SimulationGateway struct{}

// Opening funds and explicitly applied external facts use the same gateway
// boundary. The service persists consent and the exact transaction before
// invoking this check; the financial transaction consumes it immediately.
func (SimulationGateway) CheckFunding(_ context.Context, user int64, t *entity.Transaction, a *entity.Audit) error {
	if t == nil || a == nil || t.UserID != user || a.UserID != user || a.ResourceID != t.ID || a.Action != "simulation.funding.approve" || a.Outcome != "approved" || t.EffectMode != "apply_simulated_effect" || (t.Kind != "opening" && t.Kind != "external") {
		return entity.Fail("APPROVAL_REQUIRED", "persisted user funding consent required")
	}
	if t.AmountMinor <= 0 || t.AmountMinor > entity.MaxBalance || t.Currency != "CNY" {
		return entity.Fail("RISK_REJECTED", "simulation funding policy rejected")
	}
	return nil
}

func (SimulationGateway) Check(_ context.Context, user int64, v *entity.TransferIntent, a *entity.Approval) error {
	if a == nil || v == nil || a.UserID != user || v.UserID != user || a.TransferID != v.ID || a.Decision != "approve" || a.ApprovalHash != v.ApprovalHash || a.ConsumedAt != 0 || v.Status != "pending_approval" {
		return entity.Fail("APPROVAL_REQUIRED", "valid explicit user approval required")
	}
	now := time.Now().UnixMilli()
	if a.ExpiresAt <= now || v.ExpiresAt <= now {
		return entity.Fail("APPROVAL_EXPIRED", "transfer authorization expired")
	}
	return nil
}
