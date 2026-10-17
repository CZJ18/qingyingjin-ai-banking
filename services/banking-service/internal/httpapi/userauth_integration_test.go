//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/userauth"
)

type livePhoneAuth struct {
	http   http.Handler
	auth   *identity.Auth
	core   *service.Core
	logs   *bytes.Buffer
	origin string
	remote string
}

func newLivePhoneAuth(t *testing.T, secure bool) *livePhoneAuth {
	t.Helper()
	path := os.Getenv("BANKING_AUTH_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_AUTH_TEST_ENV is not configured")
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
		t.Fatal("phone HTTP acceptance requires an isolated *_test database")
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	var version int
	if err = conn.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil || version < 12 {
		t.Fatal("phone HTTP acceptance requires migration 012")
	}
	if err = conn.CheckRedis(context.Background()); err != nil {
		t.Fatal("Redis must be available for actual authentication acceptance")
	}
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID)
	accounts := userauth.New(conn.DB, auth, conn.Redis)
	core := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	logs := new(bytes.Buffer)
	h := NewWithPhoneAuthentication(auth, slog.New(slog.NewJSONHandler(logs, nil)), nil, time.Second, core, nil, nil, nil, nil, nil, execution.New(conn.DB, nil), nil, accounts, secure)
	origin := "http://banking.test:8088"
	if secure {
		origin = "https://banking.test:8443"
	}
	return &livePhoneAuth{http: h, auth: auth, core: core, logs: logs, origin: origin, remote: fmt.Sprintf("198.51.100.%d:54321", time.Now().UnixNano()%200+1)}
}

func (h *livePhoneAuth) request(t *testing.T, method, path, body, token, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, h.origin+path, strings.NewReader(body))
	req.RemoteAddr = h.remote
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
	w := httptest.NewRecorder()
	h.http.ServeHTTP(w, req)
	return w
}

func phoneHTTPNumber() string { return fmt.Sprintf("136%08d", time.Now().UnixNano()%100000000) }
func phoneHTTPJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func phoneHTTPSession(t *testing.T, w *httptest.ResponseRecorder, status int) (userauth.Session, *http.Cookie) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("auth response status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var envelope struct {
		Code string           `json:"code"`
		Data userauth.Session `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Code != "OK" || envelope.Data.AccessToken == "" || envelope.Data.User.ID <= 0 {
		t.Fatal("invalid authentication envelope")
	}
	var cookie *http.Cookie
	for _, v := range w.Result().Cookies() {
		if v.Name == refreshCookieName {
			cookie = v
		}
	}
	return envelope.Data, cookie
}

func TestPhoneAuthHTTPRealLifecycleAndCookieIsolation(t *testing.T) {
	h := newLivePhoneAuth(t, false)
	phone := phoneHTTPNumber()
	password := "真实网页登录密码 123"
	body := phoneHTTPJSON(t, map[string]string{"phone": phone, "password": password, "display_name": "网页登录验收"})
	w := h.request(t, "POST", "/api/v1/auth/register", body, "", "", nil)
	registered, cookie := phoneHTTPSession(t, w, 201)
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != refreshCookiePath || cookie.Domain != "" || cookie.MaxAge != 86400 {
		t.Fatal("unsafe local refresh cookie attributes")
	}
	if strings.Contains(w.Body.String(), cookie.Value) || strings.Contains(w.Body.String(), password) || strings.Contains(w.Body.String(), phone) || strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "refresh_hash") {
		t.Fatal("credential material leaked in authentication JSON")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authentication response may be cached")
	}
	if got := h.request(t, "GET", "/api/v1/users/me", "", registered.AccessToken, "", nil); got.Code != 200 {
		t.Fatalf("registered identity was not usable: %d", got.Code)
	}
	// Cookies alone never authenticate a financial API.
	if got := h.request(t, "GET", "/api/v1/accounts", "", "", "", cookie); got.Code != 401 {
		t.Fatal("financial API accepted refresh cookie as authorization")
	}
	for _, tc := range []struct {
		path, origin string
		cookie       *http.Cookie
	}{
		{"refresh", "", cookie}, {"refresh", "http://evil.test", cookie}, {"refresh", "https://banking.test:8088", cookie}, {"logout", "http://banking.test:9999", cookie}, {"login", "", cookie},
	} {
		if got := h.request(t, "POST", "/api/v1/auth/"+tc.path, `{}`, "", tc.origin, tc.cookie); got.Code != 403 {
			t.Fatalf("unsafe origin accepted for %s: %d", tc.path, got.Code)
		}
	}
	refreshed, _ := phoneHTTPSession(t, h.request(t, "POST", "/api/v1/auth/refresh", `{}`, "", h.origin, cookie), 200)
	if refreshed.User.ID != registered.User.ID {
		t.Fatal("refresh changed user identity")
	}
	wrong := h.request(t, "POST", "/api/v1/auth/login", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": "wrong-password"}), "", h.origin, nil)
	if wrong.Code != 401 || !strings.Contains(wrong.Body.String(), "AUTH_INVALID_CREDENTIALS") {
		t.Fatalf("wrong password accepted: %d", wrong.Code)
	}
	unknown := h.request(t, "POST", "/api/v1/auth/login", phoneHTTPJSON(t, map[string]string{"phone": phoneHTTPNumber(), "password": "wrong-password"}), "", h.origin, nil)
	if unknown.Code != 401 {
		t.Fatalf("unknown phone did not use the credential error: %d", unknown.Code)
	}
	var wrongError, unknownError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(wrong.Body.Bytes(), &wrongError); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(unknown.Body.Bytes(), &unknownError); err != nil {
		t.Fatal(err)
	}
	if wrongError != unknownError {
		t.Fatal("authentication error reveals whether a phone exists")
	}
	loggedIn, newCookie := phoneHTTPSession(t, h.request(t, "POST", "/api/v1/auth/login", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": password}), "", h.origin, nil), 200)
	if newCookie == nil || newCookie.Value == cookie.Value {
		t.Fatal("login did not replace refresh credentials")
	}
	if got := h.request(t, "GET", "/api/v1/users/me", "", registered.AccessToken, "", nil); got.Code != 401 {
		t.Fatal("new login did not revoke old access")
	}
	if got := h.request(t, "POST", "/api/v1/auth/logout", `{}`, "", h.origin, cookie); got.Code != 200 {
		t.Fatal("stale-cookie logout should be idempotent")
	}
	if got := h.request(t, "GET", "/api/v1/users/me", "", loggedIn.AccessToken, "", nil); got.Code != 200 {
		t.Fatal("stale cookie revoked a newer login")
	}
	logout := h.request(t, "POST", "/api/v1/auth/logout", `{}`, "", h.origin, newCookie)
	if logout.Code != 200 {
		t.Fatalf("logout failed: %d", logout.Code)
	}
	cleared := false
	for _, v := range logout.Result().Cookies() {
		if v.Name == refreshCookieName && v.MaxAge < 0 && v.Value == "" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear browser refresh cookie")
	}
	if got := h.request(t, "GET", "/api/v1/users/me", "", loggedIn.AccessToken, "", nil); got.Code != 401 {
		t.Fatal("logout did not revoke access token")
	}
	if got := h.request(t, "POST", "/api/v1/auth/refresh", `{}`, "", h.origin, newCookie); got.Code != 401 {
		t.Fatal("logged out cookie recovered session")
	}
	for _, secret := range []string{phone, password, cookie.Value, newCookie.Value, registered.AccessToken, loggedIn.AccessToken} {
		if strings.Contains(h.logs.String(), secret) {
			t.Fatal("authentication secret appeared in HTTP logs")
		}
	}
}

func TestPhoneAuthHTTPSCookieAndBodyValidation(t *testing.T) {
	h := newLivePhoneAuth(t, true)
	phone := phoneHTTPNumber()
	password := "secure-password-123"
	w := h.request(t, "POST", "/api/v1/auth/register", phoneHTTPJSON(t, map[string]string{"phone": phone, "password": password}), "", h.origin, nil)
	_, cookie := phoneHTTPSession(t, w, 201)
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("production cookie is not secure")
	}
	if got := h.request(t, "POST", "/api/v1/auth/refresh", `{}`, "", strings.Replace(h.origin, "https:", "http:", 1), cookie); got.Code != 403 {
		t.Fatal("production refresh accepted insecure origin")
	}
	for _, body := range []string{`{"phone":"` + phone + `","password":"` + password + `","user_id":"42"}`, `{} {}`, strings.Repeat("x", 4100)} {
		if got := h.request(t, "POST", "/api/v1/auth/login", body, "", h.origin, nil); got.Code != 400 {
			t.Fatalf("invalid authentication body accepted: %d", got.Code)
		}
	}
}

func TestPhoneAuthHTTPBindingAndAccountOwnership(t *testing.T) {
	h := newLivePhoneAuth(t, false)
	ctx := context.Background()
	uid := time.Now().UnixNano() / 10
	if _, err := h.core.Register(ctx, service.Meta{UserID: uid}, "历史账户本人"); err != nil {
		t.Fatal(err)
	}
	legacy, err := h.auth.IssueUser(fmt.Sprint(uid), "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := h.auth.IssueAgent(fmt.Sprint(uid), "supervisor", "phone-binding-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	phone := phoneHTTPNumber()
	body := phoneHTTPJSON(t, map[string]string{"phone": phone, "password": "owner-password-123"})
	if got := h.request(t, "POST", "/api/v1/auth/bind", body, "", h.origin, nil); got.Code != 401 {
		t.Fatal("anonymous binding accepted")
	}
	if got := h.request(t, "POST", "/api/v1/auth/bind", body, agent, h.origin, nil); got.Code != 403 {
		t.Fatal("agent bound browser credentials")
	}
	bound, _ := phoneHTTPSession(t, h.request(t, "POST", "/api/v1/auth/bind", body, legacy, h.origin, nil), 200)
	if bound.User.ID != uid {
		t.Fatal("binding changed existing user identity")
	}
	if got := h.request(t, "GET", "/api/v1/users/me", "", legacy, "", nil); got.Code != 200 {
		t.Fatal("operator token compatibility was broken")
	}
	// The fixture creates an empty simulated account through the core service;
	// authorization/financial mutation flows are covered by their existing suite.
	account, err := h.core.CreateAccount(ctx, service.Meta{UserID: uid, TraceID: "phone-http-ownership"}, fmt.Sprint(time.Now().UnixNano()), service.AccountInput{DisplayName: "授权空账户", Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.request(t, "GET", "/api/v1/accounts/"+fmt.Sprint(account.ID), "", bound.AccessToken, "", nil); got.Code != 200 {
		t.Fatalf("owner could not read account: %d", got.Code)
	}
	other, _ := phoneHTTPSession(t, h.request(t, "POST", "/api/v1/auth/register", phoneHTTPJSON(t, map[string]string{"phone": phoneHTTPNumber(), "password": "other-password-123"}), "", h.origin, nil), 201)
	if other.User.ID == uid {
		t.Fatal("anonymous registration claimed a historical account")
	}
	if got := h.request(t, "GET", "/api/v1/accounts/"+fmt.Sprint(account.ID)+"?user_id="+fmt.Sprint(uid), "", other.AccessToken, "", nil); got.Code != 404 {
		t.Fatalf("cross-account access was not denied: %d", got.Code)
	}
	var own entity.User
	w := h.request(t, "GET", "/api/v1/users/me?user_id="+fmt.Sprint(uid), "", other.AccessToken, "", nil)
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(envelope.Data, &own); err != nil || own.ID != other.User.ID {
		t.Fatal("query string changed authenticated identity")
	}
}
