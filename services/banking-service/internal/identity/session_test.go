package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVersionedSessionTokenRejectsRevocationAndKeepsOperatorCompatibility(t *testing.T) {
	a := New(strings.Repeat("u", 32), "i", "a", "t", "42", "")
	version := int64(3)
	a.SetSessionVersionValidator(func(ctx context.Context, user string, v int64) error {
		if user != "42" || v != version {
			return errors.New("revoked")
		}
		return nil
	})
	raw, err := a.IssueSession("42", version, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := a.UserContext(context.Background(), raw)
	if err != nil || actor.AuthVersion != 3 || actor.Kind != "user" {
		t.Fatalf("invalid session: %+v %v", actor, err)
	}
	version++
	if _, err = a.User(raw); err == nil {
		t.Fatal("revoked session accepted")
	}
	legacy, err := a.IssueUser("42", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(legacy); err != nil {
		t.Fatal("operator token rejected", err)
	}
}

func TestVersionedSessionTokensFailClosedWithoutValidator(t *testing.T) {
	a := New(strings.Repeat("u", 32), "i", "a", "t", "42", "")
	raw, err := a.IssueSession("42", 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(raw); err == nil {
		t.Fatal("session token accepted without persistence check")
	}
	for _, v := range []int64{0, -1} {
		if _, err = a.IssueSession("42", v, time.Minute); err == nil {
			t.Fatal("invalid session version accepted")
		}
	}
	if _, err = a.IssueSession("42", 1, 16*time.Minute); err == nil {
		t.Fatal("long session access token accepted")
	}
}
