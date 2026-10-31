package modelgateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

type directBudget struct {
	memoryBudget
	approval modelspend.Approval
}

func (b *directBudget) ApprovalFingerprint() string { return b.approval.Fingerprint() }

func TestDirectProviderRequiresExactPayloadTaskAndExistingAccounting(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(validResponse))
	}))
	defer provider.Close()
	p := policy()
	h, _ := p.Hash()
	value := map[string]any{"integration_mode": "direct_provider", "direct_request_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(validBody))), "approval_id": "direct-test", "simulation": true, "run_id": "direct-test", "provider_origin": provider.URL + "/v1", "model": p.Model, "project_id": p.ProjectID, "database": "direct_test", "currency": p.Currency, "budget_amount": "1", "policy_sha256": h, "scope": "synthetic read only", "user_id": 42, "task_request_sha256": strings.Repeat("b", 64), "valid_from": p.ValidFrom, "valid_until": p.ValidUntil, "max_requests": 1, "max_concurrent": 1, "context_tokens": p.ContextTokens, "max_output_tokens": p.MaxOutputTokens}
	raw, _ := json.Marshal(value)
	var a modelspend.Approval
	json.Unmarshal(raw, &a)
	b := &directBudget{approval: a, memoryBudget: memoryBudget{attempts: map[string]modelspend.Attempt{}, limit: 1}}
	identity := func(context.Context, string) (int64, error) { return 42, nil }
	task := func(_ context.Context, credential string) (TaskIdentity, error) {
		if credential != "Bearer synthetic-task" {
			return TaskIdentity{}, fmt.Errorf("not signed")
		}
		return TaskIdentity{UserID: 42, TaskID: "lg-synthetic.supervisor_plan", RequestDigest: strings.Repeat("b", 64), Version: 1, LeaseFingerprint: strings.Repeat("c", 64)}, nil
	}
	g, e := New(Config{Approval: &a, RunID: a.RunID, Database: a.Database, Policy: p, GatewayToken: token, SupplierKey: supplier, BaseURL: provider.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy(), VerifyIdentity: identity, VerifyTask: task}, b)
	if e != nil {
		t.Fatal("direct approval refused", e)
	}
	send := func(body, taskToken, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Banking-Authorization", taskToken)
		if key != "" {
			r.Header.Set("X-Model-Request-ID", key)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	if w := send(validBody, "Bearer wrong", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(strings.Replace(validBody, "hello", "changed", 1), "Bearer synthetic-task", ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(validBody, "Bearer synthetic-task", "caller-controlled"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	for n := 0; n < 2; n++ {
		if w := send(validBody, "Bearer synthetic-task", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 || len(b.reports) != 1 || len(b.attempts) != 1 {
		t.Fatal("direct path bypassed accounting/idempotency", calls, len(b.reports), len(b.attempts))
	}
	r := httptest.NewRequest("POST", "/v1/workflow-grants", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("direct mode minted Coze grant", w.Code)
	}
}
