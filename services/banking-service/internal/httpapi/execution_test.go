package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestExecutionIdentifiersAreCanonical(t *testing.T) {
	for _, raw := range []string{"", "7", "../../other", "00000000000000000000000000000000", "A987FBC9-4BED-4078-8F07-9141BA07C9F3"} {
		if _, e := executionID(raw); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, e := executionID("a987fbc9-4bed-4078-8f07-9141ba07c9f3"); e != nil {
		t.Fatal(e)
	}
}
func TestSensitiveLegacyMutationsCannotBypassGateway(t *testing.T) {
	for _, path := range []string{"/api/v1/simulation/accounts", "/api/v1/transactions/import", "/api/v1/transfers", "/api/v1/transfers/7/approvals", "/api/v1/budgets", "/api/v1/savings/goals", "/api/v1/savings/goals/7/deposits", "/api/v1/savings/goals/7/withdrawals", "/api/v1/subscription-intents/7/confirm", "/api/v1/subscriptions/7/manage"} {
		if !sensitiveExecutionPath("POST", path) {
			t.Fatalf("unprotected %s", path)
		}
	}
	if !sensitiveExecutionPath("PATCH", "/api/v1/accounts/7/status") {
		t.Fatal("unprotected account status")
	}
	for _, path := range []string{"/api/v1/budgets", "/api/v1/accounts/7/status", "/api/v1/transfers/7"} {
		if sensitiveExecutionPath("GET", path) {
			t.Fatalf("blocked read %s", path)
		}
	}
}
func TestAgentMutationAllowlistUsesRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	paths := []struct {
		path    string
		allowed bool
	}{
		{"/api/v1/orchestrations", true}, {"/api/v1/goals", true}, {"/api/v1/financial-plans", true},
		{"/api/v1/financial-plans/:id/replan", true}, {"/api/v1/financial-plans/:id/decisions", false}, {"/api/v1/financial-plans/future-mutation", false}, {"/api/v1/billing/future-mutation", false},
		{"/api/v1/orchestrations/:id/confirm", false}, {"/api/v1/orchestrations/:id/inputs", false}, {"/api/v1/orchestrations/:id/resume", false}, {"/api/v1/users/me", false}, {"/api/v1/unknown", false},
	}
	for _, item := range paths {
		want := item.allowed
		r.POST(item.path, func(c *gin.Context) {
			if agentPlanAllowed(c) != want {
				t.Errorf("%s permission mismatch", c.FullPath())
			}
			c.Status(204)
		})
	}
	for _, item := range paths {
		path := item.path
		for i := 0; i < len(path); i++ {
			if path[i] == ':' {
				path = path[:i] + "7" + path[i+3:]
				break
			}
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 204 {
			t.Fatalf("fixture %s: %d", path, w.Code)
		}
	}
}
