package execution

import (
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
)

func TestPermissionIndependentOfModelClaims(t *testing.T) {
	m := service.Meta{UserID: 42}
	for _, kind := range []string{"agent", "tool"} {
		a := identity.Actor{Kind: kind, UserID: "42", TokenID: "fake-model-confirmation", Scopes: []string{"execution:read", "execution:plan", "execution:prepare", "execution:authorize", "execution:execute"}}
		for _, p := range []string{"read", "plan", "prepare"} {
			if permission(a, m, p) != nil {
				t.Fatal("allowed planning blocked")
			}
		}
		for _, p := range []string{"authorize", "execute"} {
			if permission(a, m, p) == nil {
				t.Fatal("model elevated execution scope")
			}
		}
	}
	a := identity.Actor{Kind: "user", UserID: "42", TokenID: "signed-jti", Scopes: []string{"execution:authorize", "execution:execute"}}
	if permission(a, m, "execute") != nil {
		t.Fatal("human rejected")
	}
	a.UserID = "43"
	if permission(a, m, "execute") == nil {
		t.Fatal("owner mismatch accepted")
	}
	a.UserID = "42"
	a.TokenID = ""
	if permission(a, m, "authorize") == nil {
		t.Fatal("unbound human token accepted")
	}
}

func TestIntentIdempotencyKeys(t *testing.T) {
	for _, key := range []string{"", " x", "x\n", "模型", string(make([]byte, 129))} {
		if idempotency(key) == nil {
			t.Fatal("invalid idempotency key accepted")
		}
	}
	if idempotency("prepare:simulation-42") != nil {
		t.Fatal("valid key rejected")
	}
}
