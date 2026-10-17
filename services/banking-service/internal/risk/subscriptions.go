package risk

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"time"
)

// Narrow module8 boundary: tool proposal and explicitly confirmed execution are
// separate checks. Implementations receive server-owned persisted state only.
type SubscriptionGateway interface {
	CheckSubscriptionProposal(context.Context, int64, *entity.Subscription) error
	CheckSubscriptionCancel(context.Context, int64, *entity.Subscription, *entity.SubscriptionActionIntent, *entity.SubscriptionApproval) error
}

func (SimulationGateway) CheckSubscriptionProposal(_ context.Context, user int64, s *entity.Subscription) error {
	if s == nil || s.UserID != user || !s.IsSimulated || s.Currency != "CNY" || s.Status != entity.SubscriptionActive {
		return entity.Fail("RISK_REJECTED", "only user-adopted active local simulated subscriptions can be cancelled")
	}
	return nil
}
func (g SimulationGateway) CheckSubscriptionCancel(ctx context.Context, user int64, s *entity.Subscription, i *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) error {
	if e := g.CheckSubscriptionProposal(ctx, user, s); e != nil {
		return e
	}
	if i == nil || a == nil || i.ID <= 0 || a.ID <= 0 || i.UserID != user || a.UserID != user || i.SubscriptionID != s.ID || i.SubscriptionVersion != s.Version || i.Status != "pending_approval" || i.Action != "cancel" || a.IntentID != i.ID || a.Decision != "approve" || a.ConsumedAt != nil || a.ConfirmationHash != i.ConfirmationHash || entity.SubscriptionDigest(i) != i.ConfirmationHash {
		return entity.Fail("APPROVAL_REQUIRED", "persisted explicit authorization matching current subscription required")
	}
	if i.ExpiresAt <= time.Now().UnixMilli() {
		return entity.Fail("APPROVAL_EXPIRED", "subscription confirmation expired")
	}
	return nil
}
