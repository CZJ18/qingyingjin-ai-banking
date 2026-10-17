package risk

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func TestSimulationGatewayRejectsUnboundExpiredAndConsumedApproval(t *testing.T) {
	now := time.Now().UnixMilli()
	v := entity.TransferIntent{Base: entity.Base{ID: 1, UserID: 42}, ApprovalHash: "hash", Status: "pending_approval", ExpiresAt: now + 60000}
	good := entity.Approval{TransferID: 1, UserID: 42, ApprovalHash: "hash", Decision: "approve", ExpiresAt: v.ExpiresAt}
	g := SimulationGateway{}
	if err := g.Check(context.Background(), 42, &v, &good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*entity.Approval){func(a *entity.Approval) { a.UserID = 43 }, func(a *entity.Approval) { a.ApprovalHash = "other" }, func(a *entity.Approval) { a.ExpiresAt = now - 1 }, func(a *entity.Approval) { a.ConsumedAt = now }, func(a *entity.Approval) { a.Decision = "reject" }} {
		a := good
		change(&a)
		if err := g.Check(context.Background(), 42, &v, &a); err == nil {
			t.Fatal("invalid approval accepted")
		}
	}
}

func TestFundingRequiresPersistedMatchingConsent(t *testing.T) {
	g := SimulationGateway{}
	tr := entity.Transaction{Base: entity.Base{ID: 1, UserID: 42}, AmountMinor: 100, Currency: "CNY", Kind: "opening", EffectMode: "apply_simulated_effect"}
	a := entity.Audit{UserID: 42, ResourceID: 1, Action: "simulation.funding.approve", Outcome: "approved"}
	if err := g.CheckFunding(context.Background(), 42, &tr, &a); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*entity.Audit){func(a *entity.Audit) { a.UserID = 43 }, func(a *entity.Audit) { a.ResourceID = 2 }, func(a *entity.Audit) { a.Outcome = "proposed" }} {
		bad := a
		change(&bad)
		if err := g.CheckFunding(context.Background(), 42, &tr, &bad); err == nil {
			t.Fatal("invalid funding authorization accepted")
		}
	}
}
