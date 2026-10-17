package identity

import (
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"testing"
	"time"
)

func TestJWTValidationAndToolIsolation(t *testing.T) {
	a := New(strings.Repeat("u", 32), "qingyingjin", "banking-service", strings.Repeat("t", 32), "42", "7")
	token, err := a.IssueUser("42", "7", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := a.User(token)
	if err != nil || actor.UserID != "42" || actor.Kind != "user" {
		t.Fatalf("user = %#v, %v", actor, err)
	}
	tool, err := a.Tool(strings.Repeat("t", 32))
	if err != nil || tool.UserID != "42" || tool.Kind != "tool" {
		t.Fatalf("tool = %#v, %v", tool, err)
	}
	if _, err := a.User(strings.Repeat("t", 32)); err == nil {
		t.Fatal("tool approved as user")
	}
	if _, err := a.Tool(token); err == nil {
		t.Fatal("user approved as tool")
	}
	if _, err := a.IssueUser("42", "7", 25*time.Hour); err == nil {
		t.Fatal("unbounded token TTL")
	}
}
func TestUnknownTokenUseRejected(t *testing.T) {
	a := New(strings.Repeat("u", 32), "i", "a", "t", "42", "")
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "i", "aud": "a", "sub": "42", "exp": time.Now().Add(time.Minute).Unix(), "token_use": "admin"}).SignedString([]byte(strings.Repeat("u", 32)))
	if _, err := a.User(raw); err == nil {
		t.Fatal("unknown token use accepted")
	}
}
func TestAgentTokenIsTaskBoundAndCannotExecute(t *testing.T) {
	a := New(strings.Repeat("u", 32), "i", "a", "t", "42", "")
	raw, e := a.IssueAgent("42", "supervisor", "task-42", 2*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	actor, e := a.User(raw)
	if e != nil || actor.Kind != "agent" || actor.TokenID == "" || actor.TaskID != "task-42" || actor.AgentType != "supervisor" {
		t.Fatalf("actor=%+v err=%v", actor, e)
	}
	for _, scope := range actor.Scopes {
		if scope == "execution:execute" || scope == "execution:authorize" || scope == "transfer:approve" || scope == "simulation:write" {
			t.Fatalf("agent has privileged scope %s", scope)
		}
	}
	for _, role := range []string{"admin", "", "financial"} {
		if _, e := a.IssueAgent("42", role, "task", time.Minute); e == nil {
			t.Fatalf("invalid role %s", role)
		}
	}
	if _, e := a.IssueAgent("42", "billing", "task", 3*time.Minute); e == nil {
		t.Fatal("unbounded TTL")
	}
}
func TestJWTRejectsMissingExpiryWrongIssuerAndAlgorithm(t *testing.T) {
	secret := strings.Repeat("u", 32)
	a := New(secret, "qingyingjin", "banking-service", strings.Repeat("t", 32), "42", "7")
	cases := []struct {
		claims jwt.RegisteredClaims
		method jwt.SigningMethod
	}{
		{jwt.RegisteredClaims{Subject: "42", Issuer: "qingyingjin", Audience: jwt.ClaimStrings{"banking-service"}}, jwt.SigningMethodHS256},
		{jwt.RegisteredClaims{Subject: "42", Issuer: "wrong", Audience: jwt.ClaimStrings{"banking-service"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, jwt.SigningMethodHS256},
		{jwt.RegisteredClaims{Subject: "42", Issuer: "qingyingjin", Audience: jwt.ClaimStrings{"wrong"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, jwt.SigningMethodHS256},
		{jwt.RegisteredClaims{Subject: "42", Issuer: "qingyingjin", Audience: jwt.ClaimStrings{"banking-service"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}, jwt.SigningMethodHS256},
		{jwt.RegisteredClaims{Subject: "42", Issuer: "qingyingjin", Audience: jwt.ClaimStrings{"banking-service"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, jwt.SigningMethodHS384},
	}
	for i, c := range cases {
		token, err := jwt.NewWithClaims(c.method, c.claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.User(token); err == nil {
			t.Fatalf("accepted case %d", i)
		}
	}
}
