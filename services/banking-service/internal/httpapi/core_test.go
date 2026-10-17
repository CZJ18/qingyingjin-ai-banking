package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Written before the Core routes: the foundation currently returns 404.
func TestCoreRejectsToolCredentialForUserRegistration(t *testing.T) {
	h, _, _ := testHandler(t, false)
	req := httptest.NewRequest("POST", "/api/v1/users/me", strings.NewReader(`{"display_name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("user registration expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCoreRegistrationRouteExists(t *testing.T) {
	h, a, _ := testHandler(t, false)
	token, _ := a.IssueUser("42", "", time.Hour)
	req := httptest.NewRequest("POST", "/api/v1/users/me", strings.NewReader(`{"display_name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Without a configured Core database, return explicit dependency failure.
	if w.Code != 503 {
		t.Fatalf("expected 503 without Core DB, got %d: %s", w.Code, w.Body.String())
	}
}
