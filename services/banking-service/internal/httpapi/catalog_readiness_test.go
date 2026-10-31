package httpapi

import (
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/planning"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCatalogRoutesRequireIdentityAndConfiguredStorage(t *testing.T) {
	_, auth, _ := testHandler(t, false)
	h := NewWithLifestyle(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, planning.New(nil), nil, nil, lifestyle.New(nil))
	token, _ := auth.IssueUser("42", "7", time.Minute)
	for _, path := range []string{"/api/v1/financial-plans", "/api/v2/lifestyle-goals"} {
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {token, 503}} {
			req := httptest.NewRequest("GET", path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%s want%d got%d: %s", path, tc.status, w.Code, w.Body.String())
			}
		}
	}
}

func TestReadinessIncludesExecutionPolicies(t *testing.T) {
	_, auth, _ := testHandler(t, false)
	h := NewWithLifestyle(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, execution.New(nil, nil), nil)
	for _, test := range []struct {
		path   string
		status int
	}{{"/healthz", 200}, {"/readyz", 503}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", test.path, nil))
		if w.Code != test.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if test.path == "/readyz" && !strings.Contains(w.Body.String(), `"execution_policy":"unavailable"`) {
			t.Fatal("missing execution policy dependency")
		}
	}
}
