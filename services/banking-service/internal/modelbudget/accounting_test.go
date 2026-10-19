package modelbudget

import (
	"context"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestUnauthorizedReconciliationNeverTouchesDatabase(t *testing.T) {
	r, e := NewReconciler(&gorm.DB{}, strings.Repeat("z", 32), "operator")
	if e != nil {
		t.Fatal(e)
	}
	// This empty DB would panic if an unauthenticated operation reached GORM.
	if _, e = r.Pending(context.Background(), "wrong"); e == nil {
		t.Fatal("unauthorized pending read")
	}
	if e = r.Reconcile(context.Background(), "wrong", "key", ReconcileInput{UserID: 1, CallID: "id", EvidenceRef: "invoice:1"}); e == nil {
		t.Fatal("unauthorized write")
	}
}
