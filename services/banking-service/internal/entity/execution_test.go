package entity

import (
	"encoding/json"
	"testing"
)

func TestExecutionDigestBindsMoneyIdentityAndIntent(t *testing.T) {
	op := OperationIntent{ID: "fixture-id", UserID: 42, Kind: "transfer", SourceAccountID: 1, TargetAccountID: 2, AmountMinor: 100, Currency: "CNY", Payload: json.RawMessage(`{"amount_minor":100}`), Snapshot: json.RawMessage(`{}`), Nonce: "challenge", ExpiresAt: 1000}
	base := OperationDigest(&op)
	for _, change := range []func(*OperationIntent){func(v *OperationIntent) { v.AmountMinor++ }, func(v *OperationIntent) { v.TargetAccountID++ }, func(v *OperationIntent) { v.UserID++ }, func(v *OperationIntent) { v.ID = "different" }, func(v *OperationIntent) { v.Nonce = "new" }, func(v *OperationIntent) { v.Payload = json.RawMessage(`{"amount_minor":200}`) }} {
		copy := op
		change(&copy)
		if OperationDigest(&copy) == base {
			t.Fatal("authorization binding failed")
		}
	}
}
