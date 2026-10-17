package userauth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
)

func TestAuthenticationRejectsInvalidInputsAndFailsClosedWithoutDependencies(t *testing.T) {
	s := New(nil, identity.New(strings.Repeat("u", 32), "i", "a", "t", "42", ""), nil)
	ctx := context.Background()
	if _, err := s.Register(ctx, RegisterInput{Phone: "123", Password: "12345678"}, "127.0.0.1"); entity.Code(err) != "INVALID_ARGUMENT" {
		t.Fatalf("invalid phone: %v", err)
	}
	if _, err := s.Login(ctx, Credentials{Phone: "13800138000", Password: "12345678"}, "127.0.0.1"); entity.Code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("missing redis/db: %v", err)
	}
	if _, err := s.Bind(ctx, identity.Actor{Kind: "agent", UserID: "42"}, Credentials{Phone: "13800138000", Password: "12345678"}, "127.0.0.1"); entity.Code(err) != "FORBIDDEN" {
		t.Fatalf("agent bind: %v", err)
	}
	if _, err := s.Refresh(ctx, "invalid"); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("bad refresh: %v", err)
	}
	if err := s.Logout(ctx, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshCredentialNeverAppearsInJSON(t *testing.T) {
	raw, err := json.Marshal(Session{AccessToken: "access", RefreshToken: "secret-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-refresh") || strings.Contains(string(raw), "refresh_token") {
		t.Fatal("refresh token serialized")
	}
}
