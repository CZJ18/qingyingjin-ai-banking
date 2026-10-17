package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/identity"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testHandler(t *testing.T, down bool) (http.Handler, *identity.Auth, *bytes.Buffer) {
	t.Helper()
	a := identity.New(strings.Repeat("u", 32), "qingyingjin", "banking-service", strings.Repeat("t", 32), "42", "7")
	var logs bytes.Buffer
	check := func(context.Context) error {
		if down {
			return errors.New("secret database password")
		}
		return nil
	}
	return New(a, slog.New(slog.NewJSONHandler(&logs, nil)), map[string]Checker{"mysql": check, "redis": check}, time.Second), a, &logs
}
func TestHealthAndDependencyFailure(t *testing.T) {
	for _, down := range []bool{false, true} {
		h, _, logs := testHandler(t, down)
		for path, want := range map[string]int{"/healthz": 200, "/readyz": 200} {
			if down && path == "/readyz" {
				want = 503
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != want {
				t.Fatalf("%s: %d", path, w.Code)
			}
			if strings.Contains(w.Body.String(), "password") || strings.Contains(logs.String(), "password") {
				t.Fatal("leaked dependency error")
			}
		}
	}
}
func TestHTTPIdentityCorrelationAndDeniedRoutes(t *testing.T) {
	h, a, logs := testHandler(t, false)
	userToken, _ := a.IssueUser("42", "7", time.Hour)
	cases := []struct {
		path, token string
		status      int
	}{
		{"/api/v1/context", "", 401}, {"/api/v1/context", strings.Repeat("t", 32), 401},
		{"/tools/v1/integration/context", userToken, 401}, {"/tools/v1/integration/context", strings.Repeat("t", 32), 200},
		{"/api/v1/context", userToken, 200}, {"/api/v1/accounts", userToken, 503},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path+"?user_id=999&token=DO_NOT_LOG", nil)
		if strings.HasPrefix(c.path, "/tools/") {
			req.Header.Set("X-Banking-Tool-Key", c.token)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		req.Header.Set("X-Trace-ID", "trace-demo")
		req.Header.Set("X-Task-ID", "task-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.status {
			t.Fatalf("%s: %d: %s", c.path, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Trace-ID") != "trace-demo" || w.Header().Get("X-Request-ID") == "" {
			t.Fatal("missing correlation")
		}
		if c.status == 200 {
			var body struct {
				Data struct {
					Actor identity.Actor `json:"actor"`
				}
				TraceID string `json:"trace_id"`
				TaskID  string `json:"task_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.Actor.UserID != "42" || body.TraceID != "trace-demo" || body.TaskID != "task-123" {
				t.Fatalf("bad identity/correlation: %s", w.Body.String())
			}
		}
	}
	if strings.Contains(logs.String(), "DO_NOT_LOG") || strings.Contains(logs.String(), userToken) || strings.Contains(logs.String(), strings.Repeat("t", 32)) {
		t.Fatal("logged secret")
	}
}
func TestInvalidCorrelationAndMethod(t *testing.T) {
	h, _, _ := testHandler(t, false)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Trace-ID", strings.Repeat("x", 129))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/healthz", nil))
	if w.Code != 405 {
		t.Fatalf("got %d", w.Code)
	}
}
