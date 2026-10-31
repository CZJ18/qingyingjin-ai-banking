//go:build integration

package modelgateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/taskproof"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The supplier is explicitly a local protocol fixture. Identity, Banking HTTP,
// reservation, restart reads and audit rows are real SQL, not supplier billing.
func TestSQLWorkflowCapabilityBindsSignedTaskAndRetainsUnknown(t *testing.T) {
	_, p := fixture(t)
	p.Limit = "10"
	env, e := godotenv.Read(os.Getenv("BANKING_SPEND_TEST_ENV"))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ledger, e := modelspend.New(conn.DB, p)
	if e != nil {
		t.Fatal(e)
	}
	uid := time.Now().UnixMicro()
	for _, id := range []int64{uid, uid + 1} {
		if e = conn.DB.Create(&entity.User{ID: id, DisplayName: "Explicit workflow protocol fixture", Status: "active"}).Error; e != nil {
			t.Fatal(e)
		}
		if e = ledger.ProvisionUserLimit(t.Context(), id, "0.00240000"); e != nil {
			t.Fatal(e)
		}
	}
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, "42", "")
	// Explicit task-proof protocol fixture; real persisted task/lease behavior is
	// exercised separately against the Python endpoint and live processes.
	auth.SetLegacyValidator(func(context.Context, string, int64) error { return nil })
	issue := func(user int64, task string) (string, error) {
		return auth.IssueAgentForActor(identity.Actor{Kind: "user", UserID: fmt.Sprint(user), ParentIssuedAt: time.Now().Unix(), TaskVersion: 2, TaskRequestDigest: strings.Repeat("c", 64), LeaseFingerprint: strings.Repeat("d", 64)}, "billing", task, 2*time.Minute)
	}
	core := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	bank := httptest.NewServer(httpapi.New(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, core))
	defer bank.Close()
	taskVerifier, e := modelgateway.NewTaskIdentityVerifier(bank.URL, outbound.TestPolicy(), func(ctx context.Context, user int64, task string) (taskproof.Proof, error) {
		return taskproof.Proof{OwnerID: fmt.Sprint(user), TaskID: task, Version: 2, RequestDigest: strings.Repeat("c", 64), LeaseFingerprint: strings.Repeat("d", 64)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	userVerifier, e := modelgateway.NewIdentityVerifier(bank.URL, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	task := "lg-" + uuid.NewString() + ".billing_1"
	signed, e := issue(uid, task)
	if e != nil {
		t.Fatal(e)
	}
	ordinary, _ := auth.IssueUser(fmt.Sprint(uid), "", time.Minute)
	foreign, _ := issue(uid+1, task)
	var calls atomic.Int64
	var slow atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+supplier || r.Header.Get("X-Banking-Authorization") != "" || r.Header.Get("X-Banking-Model-Capability") != "" {
			t.Error("credentials crossed supplier boundary")
		}
		if slow.Load() {
			time.Sleep(100 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"workflow-protocol-%d","model":"synthetic-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"protocol fixture"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, n)
	}))
	defer up.Close()
	create := func(s *modelspend.Service) http.Handler {
		h, e := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: access, SupplierKey: supplier, BaseURL: up.URL + "/v1", Timeout: 30 * time.Millisecond, Outbound: outbound.TestPolicy(), VerifyIdentity: userVerifier, VerifyTask: taskVerifier, WorkflowPolicies: map[string]string{"123": strings.Repeat("b", 64)}}, s)
		if e != nil {
			t.Fatal(e)
		}
		return h
	}
	h := create(ledger)
	send := func(path, credential, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	mint := func(bearer, input string) string {
		w := send("/v1/workflow-grants", access, `{"workflow_id":"123","input_sha256":"`+input+`"}`, map[string]string{"X-Banking-Authorization": "Bearer " + bearer})
		if w.Code != 200 {
			t.Fatalf("mint %d %s", w.Code, w.Body.String())
		}
		var g struct {
			Capability string `json:"capability"`
		}
		if json.Unmarshal(w.Body.Bytes(), &g) != nil || !strings.HasPrefix(g.Capability, "bgc1.") || strings.Contains(g.Capability, bearer) {
			t.Fatal("opaque capability")
		}
		return g.Capability
	}
	if w := send("/v1/workflow-grants", access, `{"workflow_id":"123","input_sha256":"`+strings.Repeat("a", 64)+`"}`, map[string]string{"X-Banking-Authorization": "Bearer " + ordinary}); w.Code != 401 {
		t.Fatalf("ordinary user minted task %d", w.Code)
	}
	cap := mint(signed, strings.Repeat("a", 64))
	if w := send("/v1/workflow-admit", cap, `{"workflow_id":"123","workflow_sha256":"`+strings.Repeat("b", 64)+`","input_sha256":"`+strings.Repeat("c", 64)+`"}`, nil); w.Code != 403 || calls.Load() != 0 {
		t.Fatal("changed parameters admitted")
	}
	if w := send("/v1/workflow-admit", cap, `{"workflow_id":"123","workflow_sha256":"`+strings.Repeat("b", 64)+`","input_sha256":"`+strings.Repeat("a", 64)+`"}`, nil); w.Code != 204 || calls.Load() != 0 {
		t.Fatal("valid parameters admission sent model")
	}

	if w := send("/v1/chat/completions", cap+"x", prompt, nil); w.Code != 401 || calls.Load() != 0 {
		t.Fatal("tamper dispatched")
	}
	if w := send("/v1/chat/completions", cap, prompt, map[string]string{"X-Model-Request-ID": "new-key"}); w.Code != 400 || calls.Load() != 0 {
		t.Fatal("caller key bypass")
	}
	if w := send("/v1/chat/completions", cap, prompt, nil); w.Code != 200 {
		t.Fatalf("model admission %d %s", w.Code, w.Body.String())
	}
	if w := send("/v1/chat/completions", cap, prompt, nil); w.Code != 200 || calls.Load() != 1 {
		t.Fatal("replay dispatched")
	}
	if w := send("/v1/chat/completions", mint(signed, strings.Repeat("c", 64)), prompt, nil); w.Code != 409 || calls.Load() != 1 {
		t.Fatal("changed workflow input replayed")
	}
	foreignCap := mint(foreign, strings.Repeat("a", 64))
	if w := send("/v1/chat/completions", foreignCap, prompt, nil); w.Code != 200 || calls.Load() != 2 {
		t.Fatal("cross-user collision")
	}
	// A second task times out after real dispatch. Its reservation cannot be freed
	// or retried after constructing new gateway+ledger instances.
	signed, _ = issue(uid, "lg-"+uuid.NewString()+".billing_1")
	unknownCap := mint(signed, strings.Repeat("a", 64))
	slow.Store(true)
	if w := send("/v1/chat/completions", unknownCap, prompt, nil); w.Code != 502 {
		t.Fatalf("timeout %d %s", w.Code, w.Body.String())
	}
	restarted, e := modelspend.New(conn.DB, p)
	if e != nil {
		t.Fatal(e)
	}
	h = create(restarted)
	if w := send("/v1/chat/completions", unknownCap, prompt, nil); w.Code != 409 || calls.Load() != 3 {
		t.Fatal("unknown repeated provider dispatch")
	}
	window, e := restarted.UserBudget(context.Background(), uid)
	if e != nil || window.HeldUnits != 240000 || window.UsedUnits != 0 {
		t.Fatalf("unknown/observed hold incorrectly settled %+v %v", window, e)
	}
	var events []modelspend.Event
	if e = conn.DB.Where("project_id=? AND kind=?", p.ProjectID, "reserve").Find(&events).Error; e != nil || len(events) != 3 {
		t.Fatal("reserve audit", e, len(events))
	}
	for _, event := range events {
		if !strings.Contains(event.SnapshotJSON, `"workflow_sha256":"`+strings.Repeat("b", 64)) || strings.Contains(event.SnapshotJSON, "bgc1.") || strings.Contains(event.SnapshotJSON, "credential") {
			t.Fatal("binding absent or credential in audit")
		}
	}
	t.Logf("SQL workflow evidence: project=%s owner=%d held=%d used=%d provider_fixture_calls=%d reserve_audits=%d", p.ProjectID, uid, window.HeldUnits, window.UsedUnits, calls.Load(), len(events))
}
