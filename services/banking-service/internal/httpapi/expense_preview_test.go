package httpapi

import (
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExpensePreviewRemainsUserOnlyAndRequiresStorage(t *testing.T) {
	_, a, _ := testHandler(t, false)
	h := NewWithLifestyle(a, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, nil, lifestyle.New(nil))
	user, _ := a.IssueUser("42", "7", time.Minute)
	agent, _ := a.IssueAgent("42", "lifestyle", "synthetic-goal-test", time.Minute)
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {agent, 403}, {user, 503}} {
		req := httptest.NewRequest("POST", "http://localhost:8090/api/v2/lifestyle-goals/expense-reduction-preview", strings.NewReader(`{}`))
		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != test.status {
			t.Fatalf("expected %d got %d: %s", test.status, rec.Code, rec.Body.String())
		}
	}
}

func TestExpenseTrialEndpointsRemainUserOnly(t *testing.T) {
	_, a, _ := testHandler(t, false)
	h := NewWithLifestyle(a, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, nil, lifestyle.New(nil))
	user, _ := a.IssueUser("42", "7", time.Minute)
	agent, _ := a.IssueAgent("42", "lifestyle", "synthetic-trial", time.Minute)
	for _, endpoint := range []struct{ method, path string }{{"POST", "/api/v2/expense-trials"}, {"GET", "/api/v2/expense-trials?account_id=7"}, {"GET", "/api/v2/expense-trials/1"}, {"POST", "/api/v2/expense-trials/1/reviews"}} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {agent, 403}, {user, 503}} {
			req := httptest.NewRequest(endpoint.method, "http://localhost:8090"+endpoint.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			if actor.token != "" {
				req.Header.Set("Authorization", "Bearer "+actor.token)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != actor.status {
				t.Fatalf("%s %s expected %d got %d", endpoint.method, endpoint.path, actor.status, rec.Code)
			}
		}
	}
}
