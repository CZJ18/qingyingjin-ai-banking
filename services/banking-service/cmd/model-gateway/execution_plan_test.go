package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/skills"
)

func newRealApprovalForStartup(t *testing.T) (modelspend.Approval, string, string) {
	t.Helper()
	a := modelspend.Approval{ID: "new-test-only", RunID: "new-test-only", ProviderOrigin: "https://api.deepseek.com/v1", Model: "test-no-dispatch", ProjectID: "test-project", Database: "isolated_test", Currency: "USD", Amount: "1", PolicySHA256: strings.Repeat("a", 64), Scope: "test only; no supplier request permitted", UserID: 42, TaskRequestSHA256: strings.Repeat("b", 64), WorkflowID: "123", WorkflowSHA256: strings.Repeat("c", 64), ValidFrom: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MaxRequests: 1, MaxConcurrent: 1, ContextTokens: 1000, MaxOutputTokens: 100}
	raw, _ := json.Marshal(a)
	file := filepath.Join(t.TempDir(), "approval.json")
	os.WriteFile(file, raw, 0600)
	return a, file, fmt.Sprintf("%x", sha256.Sum256(raw))
}

func TestRealStartupRequiresSeparateExecutionConfirmationBeforeSecrets(t *testing.T) {
	a, file, digest := newRealApprovalForStartup(t)
	err := run([]string{"--approval-file", file, "--approval-sha256", digest, "--run-id", a.RunID, "--env-file", "must-not-load-supplier-credentials.env"})
	if err == nil || err.Error() != "execution plan confirmation required" {
		t.Fatalf("approval alone reached credential/configuration loading: %v", err)
	}
}

func TestSimulationStartupAlsoRequiresExplicitExecutionConfirmation(t *testing.T) {
	a, file, _ := newRealApprovalForStartup(t)
	a.Simulation = true
	a.ProviderOrigin = "http://127.0.0.1:29489/v1"
	a.Model = "synthetic-confirmation-test"
	a.Database = "isolated_test"
	raw, _ := json.Marshal(a)
	os.WriteFile(file, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	err := run([]string{"--approval-file", file, "--approval-sha256", digest, "--run-id", a.RunID, "--env-file", "must-not-load-supplier-credentials.env"})
	if err != errExecutionConfirmation {
		t.Fatalf("simulation skipped independent confirmation and reached configuration: %v", err)
	}
}

func TestPreflightChecksSuppliedConfirmationBeforeConfiguration(t *testing.T) {
	a, file, digest := newRealApprovalForStartup(t)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := run([]string{"--preflight", "--approval-file", file, "--approval-sha256", digest, "--run-id", a.RunID, "--execution-plan-file", "missing-reviewed-plan.json", "--execution-plan-sha256", strings.Repeat("a", 64), "--confirm-execution-plan-sha256", strings.Repeat("b", 64), "--env-file", "must-not-read.env"})
	w.Close()
	os.Stdout = old
	raw, _ := io.ReadAll(r)
	r.Close()
	if err == nil || !strings.Contains(string(raw), "EXECUTION_PLAN_OR_CONFIRMATION_INVALID") || !strings.Contains(string(raw), "NOT_INSPECTED") {
		t.Fatalf("supplied plan was ignored: %v %s", err, raw)
	}
}

func TestExecutionConfirmationBindsExactReadyPlanAndReadOnlyTools(t *testing.T) {
	a, _, _ := newRealApprovalForStartup(t)
	i := &executionInstructions{TaskRequestSHA256: a.TaskRequestSHA256, TestInput: "analyze the explicitly selected isolated account", DeterministicTools: []skills.Request{{SkillID: "billing_analysis", SkillVersion: 1, Agent: "billing", Tool: "generate_billing_report", Mode: "read_only"}}, ExpectedDataScope: "approved isolated database and account bindings only", Verification: []string{"compare model plan, Go tool evidence, SQL rows and browser result"}, FailureHandling: []string{"stop on timeout/unknown; preserve holds; no automatic replay"}}
	p := preflightReport{Status: "READY_SNAPSHOT_ONLY", Blockers: []string{}, SnapshotOnly: true, ExecutionPlan: &a, ExecutionInstructions: i, Approval: &modelspend.ApprovalWindow{ApprovalSHA256: a.Fingerprint()}}
	raw, _ := json.Marshal(p)
	file := filepath.Join(t.TempDir(), "reviewed-plan.json")
	os.WriteFile(file, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if verifyExecutionConfirmation(a, file, digest, "") == nil {
		t.Fatal("preflight success became execution consent")
	}
	if err := verifyExecutionConfirmation(a, file, digest, digest); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := executionPlanHandler(a, file, digest, digest, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/v1/workflow-grants", nil))
	if accepted.Code != http.StatusNoContent || calls != 1 {
		t.Fatal("confirmed request did not reach existing admission")
	}
	if verifyExecutionConfirmation(a, file, digest, strings.Repeat("d", 64)) == nil {
		t.Fatal("unconfirmed plan accepted")
	}
	changed := a
	changed.WorkflowSHA256 = strings.Repeat("d", 64)
	if verifyExecutionConfirmation(changed, file, digest, digest) == nil {
		t.Fatal("different workflow approval accepted")
	}
	os.WriteFile(file, append(raw, ' '), 0600)
	if verifyExecutionConfirmation(a, file, digest, digest) == nil {
		t.Fatal("modified exact plan bytes accepted")
	}
	for _, path := range []string{"/v1/workflow-grants", "/v1/workflow-admit", "/v1/chat/completions", "/v1/budget", "/readyz"} {
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, path, nil))
		if denied.Code != http.StatusServiceUnavailable || !strings.Contains(denied.Body.String(), "MODEL_EXECUTION_CONFIRMATION_REQUIRED") || calls != 1 {
			t.Fatalf("obsolete confirmation reached %s: %d", path, denied.Code)
		}
	}
	i.DeterministicTools = []skills.Request{{SkillID: "operation_preparation", SkillVersion: 1, Agent: "subscription", Tool: "create_cancel_intent", Mode: "intent"}}
	if i.check(a) == nil {
		t.Fatal("financial intent included in read/advice acceptance plan")
	}
	i.DeterministicTools = []skills.Request{{SkillID: "billing_analysis", SkillVersion: 1, Agent: "billing", Tool: "invented_tool", Mode: "read_only"}}
	if i.check(a) == nil {
		t.Fatal("unregistered tool accepted")
	}
}
