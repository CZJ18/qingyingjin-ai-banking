package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSkillRoleIsolationWithoutGateway(t *testing.T) {
	h, a, _ := testHandler(t, false)
	token, err := a.IssueAgent("42", "billing", "signed-task", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/api/v1/subscriptions/detect_subscriptions", 403},
 {"POST", "/api/v1/transfers", 403},
		{"POST", "/api/v1/billing/get_transactions", 503},
		{"GET", "/api/v1/skills", 200},
	} {
		q := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		q.Header.Set("Authorization", "Bearer "+token)
		q.Header.Set("X-Task-ID", "forged-task")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, q)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"task_id":"signed-task"`) {
			t.Fatal("signed task correlation lost", w.Body.String())
		}
		if tc.path == "/api/v1/skills" && (strings.Contains(w.Body.String(), "subscription_analysis") || !strings.Contains(w.Body.String(), "billing_analysis")) {
			t.Fatal("catalog scope", w.Body.String())
		}
	}
}
