//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/smsprovider"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"github.com/qingyingjin/banking-service/internal/userauth"
)

type liveSMSHTTP struct {
	handler   http.Handler
	auth      *identity.Auth
	conn      *platform.Connections
	inbox     *smsprovider.Development
	accounts  *userauth.Service
	core      *service.Core
	contract  routers.Router
	logs      bytes.Buffer
	responses []string
}

const smsHTTPOrigin = "http://sms-banking.test:8088"

var smsHTTPPhoneSequence atomic.Int64
var smsHTTPPhoneEpoch = time.Now().UnixNano() % 100000000

func smsHTTPPhone() string {
	return fmt.Sprintf("137%08d", (smsHTTPPhoneEpoch+smsHTTPPhoneSequence.Add(1))%100000000)
}

func newSMSHTTP(t *testing.T) *liveSMSHTTP {
	t.Helper()
	path := os.Getenv("BANKING_AUTH_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_AUTH_TEST_ENV not configured")
	}
	env, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	dbName := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		dbName = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatal("SMS HTTP acceptance requires an isolated *_test database")
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	var version int
	if err = conn.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil || version < 13 {
		t.Fatal("SMS HTTP acceptance requires schema 013")
	}
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	// The contract server URL is substituted for this isolated in-process host;
	// all path, request and response schemas remain the published definitions.
	doc.Servers = openapi3.Servers{&openapi3.Server{URL: smsHTTPOrigin}}
	contract, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	h := &liveSMSHTTP{auth: identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID), conn: conn, core: service.New(repository.New(conn.DB), risk.SimulationGateway{}), contract: contract}
	h.freshVerifier(t)
	return h
}
func (h *liveSMSHTTP) freshVerifier(t *testing.T) {
	t.Helper()
	secret := "sms-http-test-" + uuid.NewString() + uuid.NewString()
	inbox, err := smsprovider.NewDevelopment(h.conn.Redis, secret)
	if err != nil {
		t.Fatal(err)
	}
	h.inbox = inbox
	h.accounts = userauth.New(h.conn.DB, h.auth, h.conn.Redis, smsverify.New(h.conn.Redis, inbox, secret))
	h.handler = NewWithPhoneAuthentication(h.auth, slog.New(slog.NewJSONHandler(&h.logs, nil)), nil, time.Second, h.core, nil, nil, nil, nil, nil, execution.New(h.conn.DB, nil), nil, h.accounts, false)
}
func (h *liveSMSHTTP) do(t *testing.T, method, path, body, token, origin string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, smsHTTPOrigin+path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.243:45678"
	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	route, params, err := h.contract.FindRoute(req)
	if err != nil {
		t.Fatal("SMS route missing from OpenAPI", err)
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if err = openapi3filter.ValidateRequest(context.Background(), input); err != nil {
		t.Fatal("SMS request violates OpenAPI", err)
	}
	req.Body = io.NopCloser(strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	if w.Code != status {
		var safe struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &safe)
		t.Fatalf("%s %s status=%d want=%d code=%s", method, path, w.Code, status, safe.Code)
	}
	if status == 200 && (path == "/api/v1/auth/phone/verify" || path == "/api/v1/auth/password/reset" || path == "/api/v1/auth/logout") {
		var envelope map[string]json.RawMessage
		if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal("invalid success envelope")
		}
		data, present := envelope["data"]
		if !present || string(data) != "null" {
			t.Fatal("empty authentication success must contain explicit data:null")
		}
	}
	if err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String()))}); err != nil {
		t.Fatal("SMS response violates OpenAPI", err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("SMS response may be cached")
	}
	h.responses = append(h.responses, w.Body.String())
	return w
}
func (h *liveSMSHTTP) challenge(t *testing.T, path, phone, token string) smsverify.Challenge {
	t.Helper()
	body := `{}`
	if phone != "" {
		body = phoneHTTPJSON(t, map[string]string{"phone": phone})
	}
	w := h.do(t, "POST", path, body, token, smsHTTPOrigin, nil, 200)
	var envelope struct {
		Data smsverify.Challenge `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Data.ChallengeID == "" || envelope.Data.ExpiresIn != 300 {
		t.Fatal("invalid challenge envelope")
	}
	return envelope.Data
}
func (h *liveSMSHTTP) code(t *testing.T, phone string, p smsverify.Purpose) string {
	t.Helper()
	msg, err := h.inbox.Read(context.Background(), "+86"+strings.TrimPrefix(phone, "+86"), string(p))
	if err != nil {
		t.Fatal("local SMS inbox unavailable")
	}
	return msg.Code
}
func (h *liveSMSHTTP) noLeaks(t *testing.T, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(h.logs.String(), secret) {
			t.Fatal("credential or OTP leaked in logs")
		}
		for _, response := range h.responses {
			if strings.Contains(response, `"`+secret+`"`) {
				t.Fatal("credential or OTP leaked in response JSON")
			}
		}
	}
}

func TestSMSHTTPVerifiedRegistrationAndPasswordReset(t *testing.T) {
	h := newSMSHTTP(t)
	phone := smsHTTPPhone()
	old := "http-old-password-123"
	next := "http-new-password-123"
	capabilities := h.do(t, "GET", "/api/v1/auth/capabilities", "", "", "", nil, 200)
	if !strings.Contains(capabilities.Body.String(), `"sms_mode":"development"`) {
		t.Fatal("development mode is not explicit")
	}
	h.do(t, "POST", "/api/v1/auth/register", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": old}), "", smsHTTPOrigin, nil, 401)
	ch := h.challenge(t, "/api/v1/auth/sms/registration", phone, "")
	code := h.code(t, phone, smsverify.RegisterPhone)
	w := h.do(t, "POST", "/api/v1/auth/register", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": old, "challenge_id": ch.ChallengeID, "code": code}), "", smsHTTPOrigin, nil, 201)
	registered, cookie := phoneHTTPSession(t, w, 201)
	if !registered.PhoneVerified || registered.PhoneVerificationMethod != "development" {
		t.Fatal("verified session metadata missing")
	}
	legacyToken, err := h.auth.IssueUser(fmt.Sprint(registered.User.ID), "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h.freshVerifier(t) // Separate test installation avoids bypassing production resend limits.
	reset := h.challenge(t, "/api/v1/auth/sms/password-reset", phone, "")
	resetCode := h.code(t, phone, smsverify.ResetPassword)
	body := phoneHTTPJSON(t, map[string]string{"phone": phone, "challenge_id": reset.ChallengeID, "code": resetCode, "new_password": next})
	result := h.do(t, "POST", "/api/v1/auth/password/reset", body, "", smsHTTPOrigin, cookie, 200)
	if strings.Contains(result.Body.String(), "access_token") {
		t.Fatal("password reset automatically logged in")
	}
	h.do(t, "GET", "/api/v1/users/me", "", registered.AccessToken, "", nil, 401)
	h.do(t, "GET", "/api/v1/users/me", "", legacyToken, "", nil, 401)
	h.do(t, "POST", "/api/v1/auth/refresh", `{}`, "", smsHTTPOrigin, cookie, 401)
	h.do(t, "POST", "/api/v1/auth/password/reset", body, "", smsHTTPOrigin, nil, 401)
	h.do(t, "POST", "/api/v1/auth/login", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": old}), "", smsHTTPOrigin, nil, 401)
	_, latestCookie := phoneHTTPSession(t, h.do(t, "POST", "/api/v1/auth/login", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": next}), "", smsHTTPOrigin, nil, 200), 200)
	h.do(t, "POST", "/api/v1/auth/logout", `{}`, "", smsHTTPOrigin, latestCookie, 200)
	h.noLeaks(t, phone, old, next, code, resetCode, cookie.Value, latestCookie.Value)
}

func TestSMSHTTPExistingPhoneVerificationAndPermissionBoundaries(t *testing.T) {
	h := newSMSHTTP(t)
	phone := smsHTTPPhone()
	password := "verify-current-password-123"
	plain := userauth.New(h.conn.DB, h.auth, h.conn.Redis)
	v, err := plain.Register(context.Background(), userauth.RegisterInput{Phone: phone, Password: password}, "sms-http-fixture-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h.freshVerifier(t)
	agent, err := h.auth.IssueAgent(fmt.Sprint(v.User.ID), "billing", "sms-http-agent", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h.do(t, "POST", "/api/v1/auth/sms/phone-verification", `{}`, "", smsHTTPOrigin, nil, 401)
	h.do(t, "POST", "/api/v1/auth/sms/phone-verification", `{}`, agent, smsHTTPOrigin, nil, 403)
	h.do(t, "POST", "/api/v1/auth/sms/phone-verification", `{}`, v.AccessToken, "http://evil.test", nil, 403)
	ch := h.challenge(t, "/api/v1/auth/sms/phone-verification", "", v.AccessToken)
	code := h.code(t, phone, smsverify.VerifyPhone)
	input := map[string]string{"challenge_id": ch.ChallengeID, "code": code, "current_password": "wrong-current-password"}
	h.do(t, "POST", "/api/v1/auth/phone/verify", phoneHTTPJSON(t, input), agent, smsHTTPOrigin, nil, 403)
	h.do(t, "POST", "/api/v1/auth/phone/verify", phoneHTTPJSON(t, input), v.AccessToken, smsHTTPOrigin, nil, 401)
	input["current_password"] = password
	h.do(t, "POST", "/api/v1/auth/phone/verify", phoneHTTPJSON(t, input), v.AccessToken, smsHTTPOrigin, nil, 200)
	h.do(t, "GET", "/api/v1/users/me", "", v.AccessToken, "", nil, 200)
	h.do(t, "POST", "/api/v1/auth/phone/verify", phoneHTTPJSON(t, input), v.AccessToken, smsHTTPOrigin, nil, 401)
	h.noLeaks(t, phone, password, code)
}

func TestSMSHTTPUnknownAndUnverifiedResetResponsesAreUniform(t *testing.T) {
	h := newSMSHTTP(t)
	unknown := smsHTTPPhone()
	unverified := smsHTTPPhone()
	password := "unverified-original-123"
	plain := userauth.New(h.conn.DB, h.auth, h.conn.Redis)
	if _, err := plain.Register(context.Background(), userauth.RegisterInput{Phone: unverified, Password: password}, "sms-http-fixture-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	h.freshVerifier(t)
	var errors []string
	for _, phone := range []string{unknown, unverified} {
		ch := h.challenge(t, "/api/v1/auth/sms/password-reset", phone, "")
		code := h.code(t, phone, smsverify.ResetPassword)
		w := h.do(t, "POST", "/api/v1/auth/password/reset", phoneHTTPJSON(t, map[string]string{"phone": phone, "challenge_id": ch.ChallengeID, "code": code, "new_password": "not-allowed-to-claim"}), "", smsHTTPOrigin, nil, 401)
		var envelope struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		errors = append(errors, envelope.Code+":"+envelope.Message)
		h.noLeaks(t, phone, code)
	}
	if errors[0] != errors[1] {
		t.Fatal("reset error enumerates existing accounts")
	}
}
