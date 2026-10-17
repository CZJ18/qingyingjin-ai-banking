package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestLegacyValidatorCanRevokeUnversionedTokensAfterPasswordReset(t *testing.T) {
	secret := strings.Repeat("u", 32)
	a := New(secret, "issuer", "audience", "tool", "42", "")
	changed := int64(0)
	a.SetLegacyValidator(func(ctx context.Context, user string, issued int64) error {
		if user != "42" {
			return errors.New("wrong user")
		}
		if changed > 0 && issued <= changed {
			return errors.New("password changed")
		}
		return nil
	})
	now := time.Now().Unix()
	issue := func(iat int64) string {
		claims := Claims{TokenUse: "user", RegisteredClaims: jwt.RegisteredClaims{Subject: "42", Issuer: "issuer", Audience: jwt.ClaimStrings{"audience"}, IssuedAt: jwt.NewNumericDate(time.Unix(iat, 0)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	before := issue(now - 2)
	if _, err := a.User(before); err != nil {
		t.Fatal(err)
	}
	changed = now - 1
	if _, err := a.User(before); err == nil {
		t.Fatal("pre-reset operator token remained valid")
	}
	if _, err := a.User(issue(changed)); err == nil {
		t.Fatal("token issued in reset second remained valid")
	}
	if _, err := a.User(issue(now)); err != nil {
		t.Fatal("post-reset operator token rejected", err)
	}
}

func TestLegacyValidatorReceivesMissingIssuedAtAndCannotApproveAgentScope(t *testing.T) {
	secret := strings.Repeat("u", 32)
	a := New(secret, "issuer", "audience", "tool", "42", "")
	calls := 0
	a.SetLegacyValidator(func(ctx context.Context, user string, issued int64) error {
		calls++
		if issued == 0 {
			return errors.New("password changed")
		}
		return nil
	})
	claims := Claims{TokenUse: "user", RegisteredClaims: jwt.RegisteredClaims{Subject: "42", Issuer: "issuer", Audience: jwt.ClaimStrings{"audience"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(raw); err == nil || calls != 1 {
		t.Fatal("missing-issued-at legacy token bypassed persistence check")
	}
	agent, err := a.IssueAgent("42", "billing", "reset-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := a.User(agent)
	if err != nil || actor.Kind != "agent" || calls != 1 {
		t.Fatal("legacy validator changed agent identity semantics")
	}
	for _, scope := range actor.Scopes {
		if scope == "execution:execute" || scope == "execution:authorize" {
			t.Fatal("agent gained execution scope")
		}
	}
}
