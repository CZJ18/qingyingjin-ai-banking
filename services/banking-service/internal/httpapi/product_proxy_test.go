package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestProductHistoryMethodBoundary(t *testing.T) {
	if got := productMethods("/api/v1/agent-tasks"); got != "GET POST" {
		t.Fatalf("persisted history must be available through the authenticated proxy: %q", got)
	}
	if productMethods("/internal/model-task-context") != "" {
		t.Fatal("history must not expose internal endpoints")
	}
}

func TestProductProxyBoundary(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/agent-tasks" || r.Header.Get("Authorization") == "" || r.Header.Get("Idempotency-Key") != "submit-1" {
			t.Error("lost product request")
		}
		for _, h := range []string{"X-Agent-Service-Key", "X-Forwarded-For", "X-User-ID", "Cookie", "Forwarded", "X-Banking-Tool-Key"} {
			if r.Header.Get(h) != "" {
				t.Errorf("forwarded %s", h)
			}
		}
		w.Header().Set("Set-Cookie", "secret=value")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"task_id":"task-1"}}`))
	}))
	defer upstream.Close()
	auth := identity.New(strings.Repeat("u", 32), "i", "a", "tool", "42", "")
	active := true
	auth.SetSessionVersionValidator(func(context.Context, string, int64) error {
		if !active {
			return errors.New("inactive")
		}
		return nil
	})
	token, _ := auth.IssueSession("42", 1, time.Minute)
	agentToken, _ := auth.IssueAgent("42", "billing", langGraphTestTask, time.Minute)
	proxy, err := NewProductProxy(http.NotFoundHandler(), auth, upstream.URL, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", "/product/api/v1/agent-tasks", "", 401},
		{"POST", "/product/api/v1/agent-tasks", agentToken, 401},
		{"POST", "/product/internal/model-task-context", token, 404},
		{"DELETE", "/product/api/v1/agent-tasks", token, 405},
		{"GET", "/product/api/v1/agent-tasks/x/unknown", token, 404},
		{"POST", "/product/api/v1/agent-tasks", token, 202},
	} {
		r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"user_id":99}`))
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		for _, h := range []string{"X-Agent-Service-Key", "X-Forwarded-For", "X-User-ID", "Cookie", "Forwarded", "X-Banking-Tool-Key"} {
			r.Header.Set(h, "attacker")
		}
		r.Header.Set("Idempotency-Key", "submit-1")
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s %s = %d want %d", tt.method, tt.path, w.Code, tt.want)
		}
		if w.Header().Get("Set-Cookie") != "" {
			t.Error("forwarded cookie")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
	active = false
	r := httptest.NewRequest("POST", "/product/api/v1/agent-tasks", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	if w.Code != 401 || calls.Load() != 1 {
		t.Fatal("inactive session reached upstream")
	}
}

func TestProductProxyTimeoutAndRedirect(t *testing.T) {
	auth := identity.New(strings.Repeat("u", 32), "i", "a", "tool", "42", "")
	token, _ := auth.IssueUser("42", "", time.Minute)
	for _, mode := range []string{"timeout", "redirect", "oversize", "sse-error", "stream-timeout"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "stream-timeout":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "http://127.0.0.1:1/internal", 302)
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat("a", int(productResponseLimit)+1)))
				case "sse-error":
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"code":"FORBIDDEN"}`))
				}
			}))
			defer upstream.Close()
			h, e := NewProductProxy(http.NotFoundHandler(), auth, upstream.URL, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			h.(*productProxy).timeout = time.Second
			if mode == "timeout" || mode == "stream-timeout" {
				h.(*productProxy).timeout = 20 * time.Millisecond
			}
			r := httptest.NewRequest("GET", "/product/api/v1/agent-tasks/task-1/events/stream", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 502
			if mode == "timeout" {
				want = 504
			}
			if mode == "sse-error" {
				want = 403
			}
			if mode == "stream-timeout" {
				want = 200
				if !strings.Contains(w.Body.String(), "UPSTREAM_STREAM_FAILED") {
					t.Fatal("stream truncation lost error event")
				}
			}
			if w.Code != want {
				t.Fatalf("status %d want %d", w.Code, want)
			}
		})
	}
}

func TestProductProxyLimitsOriginAndCompatibility(t *testing.T) {
	auth := identity.New(strings.Repeat("u", 32), "i", "a", "tool", "42", "")
	for _, origin := range []string{"http://evil.example", "http://127.0.0.1/a", "http://user:pass@127.0.0.1", "http://127.0.0.1?target=http://evil.example"} {
		if _, err := NewProductProxy(http.NotFoundHandler(), auth, origin, outbound.TestPolicy()); err == nil {
			t.Fatalf("accepted origin %s", origin)
		}
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	h, err := NewProductProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), auth, upstream.URL, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	token, _ := auth.IssueUser("42", "", time.Minute)
	for _, tt := range []struct {
		path string
		body string
		want int
	}{
		{"/healthz", "", 204},
		{"/product/api/v1/agent-tasks?user_id=99", "{}", 400},
		{"/product/api/v1/agent-tasks", strings.Repeat("x", int(productRequestLimit)+1), 413},
		{"/product/api/v1/agent-tasks/%2e%2e/internal", "", 404},
	} {
		r := httptest.NewRequest("POST", tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s: %d want %d", tt.path, w.Code, tt.want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("denied request reached Python")
	}
}
