package risk

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func TestSubscriptionGatewayBindsPersistedAuthorization(t *testing.T) {
	s := &entity.Subscription{Base: entity.Base{ID: 2, UserID: 42}, Status: "active", Currency: "CNY", IsSimulated: true, Version: 3}
	i := &entity.SubscriptionActionIntent{Base: entity.Base{ID: 4, UserID: 42}, SubscriptionID: 2, SubscriptionVersion: 3, Status: "pending_approval", Action: "cancel", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	i.ConfirmationHash = entity.SubscriptionDigest(i)
	a := &entity.SubscriptionApproval{Base: entity.Base{ID: 9, UserID: 42}, IntentID: 4, Decision: "approve", ConfirmationHash: i.ConfirmationHash}
	g := SimulationGateway{}
	if e := g.CheckSubscriptionCancel(context.Background(), 42, s, i, a); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		name   string
		mutate func(*entity.Subscription, *entity.SubscriptionActionIntent, *entity.SubscriptionApproval)
	}{
		{"candidate", func(s *entity.Subscription, _ *entity.SubscriptionActionIntent, _ *entity.SubscriptionApproval) {
			s.Status = "candidate"
		}},
		{"foreign", func(_ *entity.Subscription, _ *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) {
			a.UserID = 43
		}},
		{"stale version", func(s *entity.Subscription, _ *entity.SubscriptionActionIntent, _ *entity.SubscriptionApproval) {
			s.Version++
		}},
		{"forged digest", func(_ *entity.Subscription, i *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) {
			i.SubscriptionVersion++
			i.ConfirmationHash = entity.SubscriptionDigest(i)
			a.ConfirmationHash = i.ConfirmationHash
		}},
		{"unpersisted", func(_ *entity.Subscription, _ *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) {
			a.ID = 0
		}},
		{"expired", func(_ *entity.Subscription, i *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) {
			i.ExpiresAt = 1
			i.ConfirmationHash = entity.SubscriptionDigest(i)
			a.ConfirmationHash = i.ConfirmationHash
		}},
		{"consumed", func(_ *entity.Subscription, _ *entity.SubscriptionActionIntent, a *entity.SubscriptionApproval) {
			n := int64(1)
			a.ConsumedAt = &n
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ss, ii, aa := *s, *i, *a
			test.mutate(&ss, &ii, &aa)
			if g.CheckSubscriptionCancel(context.Background(), 42, &ss, &ii, &aa) == nil {
				t.Fatal("unsafe authorization accepted")
			}
		})
	}
}
