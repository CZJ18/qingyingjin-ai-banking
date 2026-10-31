package httpapi

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/taskproof"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestBackgroundDelegationIsOpaqueTaskScopedRevocableAndCannotExecute(t *testing.T) {
	_, auth, _ := langGraphTestHandler(t, &langGraphRunnerFixture{}, langGraphActive)
	bridge := NewLangGraphBridge(langGraphTestKey, &langGraphRunnerFixture{})
	bridge.ActiveUser = langGraphActive
	bridge.ValidateTask = func(ctx context.Context, user int64, task string) (taskproof.Proof, error) {
		return taskproof.Proof{RequestDigest: strings.Repeat("a", 64), Version: 2, LeaseFingerprint: strings.Repeat("b", 64)}, nil
	}
	h := NewWithLifestyle(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, execution.New(nil, nil), nil, bridge)
	valid := true
	auth.SetAgentOwnerValidator(func(context.Context, string) error { return nil })
	auth.SetLegacyValidator(func(context.Context, string, int64) error {
		if !valid {
			return context.Canceled
		}
		return nil
	})
	user := langGraphToken(t, auth, "user", "")
	root := strings.Split(langGraphTestTask, ".")[0]
	req := `{"task_id":"` + root + `","request_digest":"` + strings.Repeat("a", 64) + `"}`
	w, out := langGraphRequest(t, h, "POST", "/api/v1/langgraph/background-delegations", user, langGraphTestKey, req, nil)
	langGraphWantStatus(t, w, out, 200, "OK")
	var data struct {
		Capability string `json:"capability"`
	}
	if json.Unmarshal(out.Data, &data) != nil || !strings.HasPrefix(data.Capability, "bgt1.") || strings.Contains(data.Capability, user) {
		t.Fatal("opaque scoped capability missing")
	}
	body := `{"task_id":"` + langGraphTestTask + `","agent_type":"billing"}`
	w, out = langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", data.Capability, langGraphTestKey, body, nil)
	langGraphWantStatus(t, w, out, 200, "OK")
	foreign := strings.Replace(body, "0123456789ab.billing_1", "0123456789ac.billing_1", 1)
	w, out = langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", data.Capability, langGraphTestKey, foreign, nil)
	langGraphWantStatus(t, w, out, 403, "FORBIDDEN")
	// This fixture deliberately lacks an audit database: financial denial must
	// itself fail closed (503), while the registered context route proves that
	// this capability is never accepted as an ordinary user identity (401).
	w, out = langGraphRequest(t, h, "GET", "/api/v1/context", data.Capability, langGraphTestKey, "", nil)
	langGraphWantStatus(t, w, out, 401, "UNAUTHENTICATED")
	w, out = langGraphRequest(t, h, "POST", "/api/v1/execution/operations", data.Capability, langGraphTestKey, `{}`, nil)
	langGraphWantStatus(t, w, out, 503, "DEPENDENCY_UNAVAILABLE")
	w, out = langGraphRequest(t, h, "POST", "/api/v1/langgraph/background-delegations", data.Capability, langGraphTestKey, req, nil)
	langGraphWantStatus(t, w, out, 401, "UNAUTHENTICATED")
	valid = false
	w, out = langGraphRequest(t, h, "POST", "/api/v1/langgraph/delegations", data.Capability, langGraphTestKey, body, nil)
	langGraphWantStatus(t, w, out, 401, "UNAUTHENTICATED")
}
