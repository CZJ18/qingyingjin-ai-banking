package modelgateway

import (
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkflowGrantWithoutTrustedTaskVerifierFailsClosed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unadmitted grant dispatched supplier") }))
	defer up.Close()
	h, err := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, BaseURL: up.URL + "/v1", Outbound: outbound.TestPolicy()}, &memoryBudget{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/workflow-grants", strings.NewReader(`{"workflow_id":"123","input_sha256":"untrusted"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("unconfigured task grant must fail closed with 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
