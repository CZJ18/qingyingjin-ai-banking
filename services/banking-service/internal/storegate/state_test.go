package storegate

import (
	"context"
	"testing"
)

func TestStateTransitionsRejectLoadingAndUnknownModes(t *testing.T) {
	for _, pair := range [][2]string{{"active", "frozen"}, {"frozen", "active"}, {"standby", "active"}, {"frozen", "standby"}} {
		if !validTransition(pair[0], pair[1]) {
			t.Fatalf("valid transition rejected: %v", pair)
		}
	}
	for _, pair := range [][2]string{{"active", "standby"}, {"active", "loading"}, {"loading", "active"}, {"standby", "loading"}, {"invalid", "active"}} {
		if validTransition(pair[0], pair[1]) {
			t.Fatalf("unsafe transition accepted: %v", pair)
		}
	}
}

func TestChangeRejectsMultipleExpectedGenerations(t *testing.T) {
	if _, err := Change(context.Background(), nil, "postgres", "active", "frozen", "", 1, 2); err == nil {
		t.Fatal("multiple expected generations must be rejected before opening a transaction")
	}
}
