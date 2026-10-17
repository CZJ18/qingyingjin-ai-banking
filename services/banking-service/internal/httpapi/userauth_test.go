package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhoneAuthRoutesRejectCrossOriginAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		path, origin, contentType, cookie string
		status                            int
	}{
		{"login", "http://evil.test", "application/json", "", 403},
		{"login", "https://localhost:8090", "application/json", "", 403},
		{"register", "http://evil.test", "application/json", "", 403},
		{"refresh", "", "application/json", "qingrongjin_refresh=unit-test-only", 403},
		{"logout", "http://evil.test", "application/json", "qingrongjin_refresh=unit-test-only", 403},
		{"login", "http://localhost:8090", "text/plain", "", 400},
		{"login", "http://localhost:8090", "application/json", "", 503},
		{"refresh", "http://localhost:8090", "application/json", "", 503},
		{"bind", "http://localhost:8090", "application/json", "", 401},
	} {
		t.Run(tc.path+tc.origin+tc.contentType, func(t *testing.T) {
			h, _, _ := testHandler(t, false)
			req := httptest.NewRequest("POST", "http://localhost:8090/api/v1/auth/"+tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("auth route %s returned %d want %d: %s", tc.path, w.Code, tc.status, w.Body.String())
			}
		})
	}
}

func TestSMSAuthRoutesFailClosed(t *testing.T) {
	h, _, _ := testHandler(t, false)
	for path, status := range map[string]int{"sms/registration": 503, "sms/password-reset": 503, "sms/phone-verification": 401, "phone/verify": 401, "password/reset": 503} {
		req := httptest.NewRequest("POST", "http://localhost:8090/api/v1/auth/"+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:8090")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != status {
			t.Errorf("%s got %d, want %d", path, w.Code, status)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8090/api/v1/auth/capabilities", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_mode":"disabled"`) {
		t.Fatal("capabilities must disclose disabled status without credentials")
	}
}
