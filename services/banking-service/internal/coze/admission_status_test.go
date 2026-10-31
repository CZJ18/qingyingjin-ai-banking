package coze

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdmissionSnapshotPreservesKnownRefusalWithoutGrant(t *testing.T) {
	for _, tc := range []struct {
		status     int
		code, want string
	}{
		{403, "MODEL_APPROVAL_REQUIRED", "MODEL_APPROVAL_REQUIRED"},
		{429, "MODEL_APPROVAL_LIMIT_EXCEEDED", "MODEL_APPROVAL_LIMIT_EXCEEDED"},
		{429, "USER_MONTHLY_BUDGET_EXCEEDED", "MODEL_USER_MONTHLY_QUOTA_EXCEEDED"},
		{429, "MODEL_SPEND_QUOTA_EXCEEDED", "MODEL_SPEND_QUOTA_EXCEEDED"},
		{503, "MODEL_EXECUTION_CONFIRMATION_REQUIRED", "MODEL_EXECUTION_CONFIRMATION_REQUIRED"},
		{503, "MODEL_BUDGET_UNAVAILABLE", "MODEL_BUDGET_UNAVAILABLE"},
		{503, "BUDGET_UNAVAILABLE", "MODEL_BUDGET_UNAVAILABLE"},
		{503, "POLICY_UNAVAILABLE", "MODEL_APPROVAL_REQUIRED"},
		{500, "unknown", "MODEL_MONETARY_ADMISSION_REQUIRED"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/budget" {
					t.Error("preflight generated a model request or grant")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"error":{"code":"` + tc.code + `"}}`))
			}))
			defer server.Close()
			c := Client{bridge: &workflowBridge{base: server.URL, token: "synthetic-only", client: server.Client()}}
			snapshot, e := c.AdmissionSnapshot(context.Background(), "Bearer synthetic-user")
			if BudgetCode(e) != tc.want || snapshot != nil || calls != 1 {
				t.Fatalf("wrong refusal: %v %v calls%d", snapshot, e, calls)
			}
		})
	}
}
