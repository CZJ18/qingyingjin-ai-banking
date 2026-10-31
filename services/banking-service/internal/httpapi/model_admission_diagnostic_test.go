package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUnconfiguredModelAdmissionReportsNonSensitiveReasonWithoutGrant(t *testing.T) {
	h, a, _ := langGraphTestHandler(t, nil, langGraphActive)
	token, e := a.IssueUser("42", "7", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "http://localhost:8090/api/v1/model-admission", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct {
		Code string `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil || rec.Code != 503 || body.Code != "MODEL_MONETARY_ADMISSION_REQUIRED" || body.Data.Reason != "MODEL_ADMISSION_NOT_CONFIGURED" {
		t.Fatalf("unconfigured status lost: %d %s", rec.Code, rec.Body.String())
	}
}
