package risk

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestTrustedRejectsMissingPermit(t *testing.T) {
	g := TrustedGateway{}
	if g.SensitiveCheck(context.Background(), 42, "open_account", map[string]any{}) == nil {
		t.Fatal("untrusted mutation allowed")
	}
	if g.CheckTransferProposal(context.Background(), 42, &entity.TransferIntent{}) == nil {
		t.Fatal("untrusted proposal allowed")
	}
	if g.CheckFunding(context.Background(), 42, &entity.Transaction{}, &entity.Audit{}) == nil {
		t.Fatal("untrusted funding allowed")
	}
}

func TestTransferBindingRejectsChangedResource(t *testing.T) {
	op := entity.OperationIntent{Kind: "transfer", SourceAccountID: 1, TargetAccountID: 2, AmountMinor: 100, Currency: "CNY"}
	good := entity.TransferIntent{Base: entity.Base{UserID: 42}, Purpose: "transfer", SourceAccountID: 1, TargetAccountID: 2, AmountMinor: 100, Currency: "CNY"}
	if !matchesTransfer(&op, &good, 42) {
		t.Fatal("exact transfer rejected")
	}
	for _, change := range []func(*entity.TransferIntent){func(v *entity.TransferIntent) { v.UserID = 43 }, func(v *entity.TransferIntent) { v.SourceAccountID = 2 }, func(v *entity.TransferIntent) { v.TargetAccountID = 3 }, func(v *entity.TransferIntent) { v.AmountMinor++ }, func(v *entity.TransferIntent) { v.Currency = "USD" }, func(v *entity.TransferIntent) { v.Purpose = "savings_deposit" }} {
		v := good
		change(&v)
		if matchesTransfer(&op, &v, 42) {
			t.Fatal("altered transfer accepted")
		}
	}
}
func TestSubscriptionBindingRejectsChangedVersionPriceAndAccount(t *testing.T) {
	good := entity.Subscription{Base: entity.Base{ID: 1, UserID: 42}, BankAccountID: 2, Version: 1, AmountMinor: 100, Currency: "CNY", BillingInterval: "monthly"}
	raw, _ := json.Marshal(good)
	op := entity.OperationIntent{Kind: "cancel_subscription", Snapshot: raw, SourceAccountID: 2, AmountMinor: 100, Currency: "CNY"}
	if !matchesSubscription(&op, &good) {
		t.Fatal("exact subscription rejected")
	}
	for _, change := range []func(*entity.Subscription){func(s *entity.Subscription) { s.ID++ }, func(s *entity.Subscription) { s.UserID++ }, func(s *entity.Subscription) { s.Version++ }, func(s *entity.Subscription) { s.AmountMinor++ }, func(s *entity.Subscription) { s.BankAccountID++ }, func(s *entity.Subscription) { s.BillingInterval = "yearly" }} {
		v := good
		change(&v)
		if matchesSubscription(&op, &v) {
			t.Fatal("altered subscription accepted")
		}
	}
}
