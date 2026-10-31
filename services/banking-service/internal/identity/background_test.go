package identity

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAlreadyDelegatedSessionIsRevokedAndKeepsLeaseBinding(t *testing.T) {
	auth := New(strings.Repeat("s", 32), "bank", "aud", strings.Repeat("t", 32), "42", "")
	version := int64(1)
	auth.SetSessionVersionValidator(func(_ context.Context, _ string, v int64) error {
		if v != version {
			return ErrUnauthenticated
		}
		return nil
	})
	auth.SetAgentOwnerValidator(func(context.Context, string) error { return nil })
	raw, e := auth.IssueSession("42", 1, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.User(raw)
	if e != nil {
		t.Fatal(e)
	}
	actor.TaskVersion = 2
	actor.TaskRequestDigest = strings.Repeat("a", 64)
	actor.LeaseFingerprint = strings.Repeat("b", 64)
	child, e := auth.IssueAgentForActor(actor, "billing", "lg-01234567-89ab-cdef-8123-0123456789ab.billing_1", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	inherited, e := auth.User(child)
	if e != nil || inherited.AuthVersion != 1 || inherited.TaskVersion != 2 || inherited.TaskRequestDigest != actor.TaskRequestDigest || inherited.LeaseFingerprint != actor.LeaseFingerprint {
		t.Fatalf("delegation ancestry lost %+v %v", inherited, e)
	}
	background, e := auth.IssueBackground(actor, "lg-01234567-89ab-cdef-8123-0123456789ab", strings.Repeat("c", 64), 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = auth.BackgroundContext(t.Context(), background); e != nil {
		t.Fatal(e)
	}
	if _, e = auth.User(background); e == nil {
		t.Fatal("background accepted as general user")
	}
	version = 2
	if _, e = auth.User(child); e == nil {
		t.Fatal("already minted delegate survived session revocation")
	}
	if _, e = auth.BackgroundContext(t.Context(), background); e == nil {
		t.Fatal("background survived session revocation")
	}
}
