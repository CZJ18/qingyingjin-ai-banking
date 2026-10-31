package statement

import (
	"context"
	"testing"
)

func TestSimulatedAdapterOwnershipRevocationAndSnapshotIsolation(t *testing.T) {
	input := Input{CSV: "id,at,amount,direction,currency,status\na,2026-10-01 00:00:00,1.00,expense,CNY,posted\n", Timezone: "Asia/Shanghai", Columns: map[string]string{"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status"}}
	adapter, err := NewSimulationAdapter("42", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ReadTransactions(context.Background(), "43", 0, 100); err == nil {
		t.Fatal("foreign connection read allowed")
	}
	page, err := adapter.ReadTransactions(context.Background(), "42", 0, 100)
	if err != nil || !page.Connection.IsSimulated || page.Connection.LastSyncedAt == 0 || len(page.Records) != 1 {
		t.Fatal(page, err)
	}
	page.Records[0].Source.RawRecord["amount"] = "999"
	again, err := adapter.ReadTransactions(context.Background(), "42", 0, 100)
	if err != nil || again.Records[0].Source.RawRecord["amount"] != "1.00" {
		t.Fatal("returned snapshot mutated adapter")
	}
	if err := adapter.Revoke(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	state, err := adapter.Status(context.Background(), "42")
	if err != nil || state.State != "revoked" || len(state.Scopes) != 0 {
		t.Fatal(state, err)
	}
	if _, err := adapter.ReadTransactions(context.Background(), "42", 0, 100); err == nil {
		t.Fatal("revoked connection read allowed")
	}
}
