package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/skills"
)

func captureReadiness(t *testing.T, args []string) (map[string]any, error) {
	t.Helper()
	old := os.Stdout
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	os.Stdout = w
	result := make(chan []byte, 1)
	go func() { raw, _ := io.ReadAll(r); result <- raw }()
	err := run(args)
	w.Close()
	os.Stdout = old
	raw := <-result
	r.Close()
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		t.Fatalf("missing structured offline report: %s (%v)", raw, err)
	}
	return v, err
}

func readinessFixture(t *testing.T) (readinessManifest, []string, string) {
	t.Helper()
	a, approvalFile, _ := newRealApprovalForStartup(t)
	p := modelspend.Policy{ProjectID: a.ProjectID, Currency: a.Currency, Model: a.Model, PriceVersion: "unit-test-v1", PriceSource: "review:unit-test-not-provider-evidence", PriceEvidenceSHA256: strings.Repeat("a", 64), ValidFrom: a.ValidFrom, ValidUntil: a.ValidUntil, Limit: a.Amount, InputPrice: "1", CacheHitPrice: "0.1", OutputPrice: "2", ContextTokens: a.ContextTokens, MaxOutputTokens: a.MaxOutputTokens}
	a.PolicySHA256, _ = p.Hash()
	write := func(file string, v any) string {
		raw, _ := json.Marshal(v)
		if e := os.WriteFile(file, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	digest := write(approvalFile, a)
	dir := t.TempDir()
	policyFile := filepath.Join(dir, "policy.json")
	write(policyFile, p)
	i := &executionInstructions{TaskRequestSHA256: a.TaskRequestSHA256, TestInput: "UNIT TEST ONLY; no real model", DeterministicTools: []skills.Request{{SkillID: "billing_analysis", SkillVersion: 1, Agent: "billing", Tool: "generate_billing_report", Mode: "read_only"}}, ExpectedDataScope: a.Scope, Verification: []string{"verify isolated evidence only"}, FailureHandling: []string{"preserve holds and do not replay"}}
	plan := preflightReport{Status: "READY_SNAPSHOT_ONLY", SnapshotOnly: true, ExecutionPlan: &a, ExecutionInstructions: i, Approval: &modelspend.ApprovalWindow{ApprovalSHA256: a.Fingerprint()}}
	planFile := filepath.Join(dir, "plan.json")
	planSHA := write(planFile, plan)
	m := readinessManifest{SchemaVersion: 1, Mode: "real", Supplier: "UNIT-TEST-NOT-A-SUPPLIER-SELECTION", SupplierOrigin: a.ProviderOrigin, Model: a.Model, ModelVersion: "unit-test-not-live-version", CozeOrigin: "https://coze.invalid", GatewayOrigin: "https://gateway.invalid", ProtectedScopeMode: "banking-task-scope-v1", AllowedOrigins: []string{"https://api.deepseek.com", "https://coze.invalid", "https://gateway.invalid"}, Workflows: map[string]readinessWorkflow{"supervisor": {Enabled: true, ID: a.WorkflowID, Version: "unit-test-publication-claim", CanvasSHA256: a.WorkflowSHA256}, "billing": {}, "subscription": {}, "savings": {}, "lifestyle": {}}, PriceVersion: p.PriceVersion, PriceSource: p.PriceSource, PriceEvidenceSHA256: p.PriceEvidenceSHA256, Currency: a.Currency, BudgetAmount: a.Amount, MaxRequests: a.MaxRequests, MaxConcurrent: a.MaxConcurrent, ContextTokens: a.ContextTokens, MaxOutputTokens: a.MaxOutputTokens, UserID: a.UserID, TaskRequestSHA256: a.TaskRequestSHA256, Database: a.Database, DataScope: a.Scope, ValidFrom: a.ValidFrom, ValidUntil: a.ValidUntil, ExternalEvidence: map[string]readinessEvidence{}}
	for _, key := range []string{"publication", "permission", "egress", "price", "usage", "billing"} {
		m.ExternalEvidence[key] = readinessEvidence{Kind: "official_reference", Reference: "UNIT-TEST-UNVERIFIED-CLAIM"}
	}
	args := []string{"--offline-preflight", "--approval-file", approvalFile, "--approval-sha256", digest, "--run-id", a.RunID, "--expected-database", a.Database, "--policy-file", policyFile, "--execution-plan-file", planFile, "--execution-plan-sha256", planSHA, "--confirm-execution-plan-sha256", planSHA, "--env-file", "must-not-load.env", "--allow-runtime-scope-probe"}
	return m, args, filepath.Join(dir, "manifest.json")
}

func TestOfflineReadinessNeverUpgradesExternalClaimsToPass(t *testing.T) {
	m, args, file := readinessFixture(t)
	raw, _ := json.Marshal(m)
	os.WriteFile(file, raw, 0600)
	r, err := captureReadiness(t, append(args, "--readiness-manifest-file", file))
	if err == nil || r["status"] != "PARTIAL" || r["execution_authorized"] != false || r["candidate_plan_generated"] != false || r["remaining_budget"] != nil {
		t.Fatal(r, err)
	}
	for _, item := range r["checks"].([]any) {
		c := item.(map[string]any)
		if strings.HasPrefix(c["name"].(string), "external_") && c["status"] != "NOT_INSPECTED" {
			t.Fatal(c)
		}
	}
}

func TestOfflineReadinessRejectsMismatchesAndSyntheticEvidenceForRealMode(t *testing.T) {
	for _, kind := range []string{"model", "user", "task", "database", "workflow", "budget", "tokens", "price", "synthetic_evidence", "synthetic_model", "plan_changed", "confirmation"} {
		t.Run(kind, func(t *testing.T) {
			m, args, file := readinessFixture(t)
			switch kind {
			case "model":
				m.Model = "changed"
			case "user":
				m.UserID++
			case "task":
				m.TaskRequestSHA256 = strings.Repeat("f", 64)
			case "database":
				m.Database = "changed_test"
			case "workflow":
				w := m.Workflows["supervisor"]
				w.CanvasSHA256 = strings.Repeat("f", 64)
				m.Workflows["supervisor"] = w
			case "budget":
				m.BudgetAmount = "0.5"
			case "tokens":
				m.MaxOutputTokens++
			case "price":
				m.PriceVersion = "different"
			case "synthetic_evidence":
				m.ExternalEvidence["permission"] = readinessEvidence{Kind: "synthetic", Reference: "NOT_PROVIDER_PERMISSION"}
			case "synthetic_model":
				m.Model = "synthetic-not-real"
			case "plan_changed":
				for n, v := range args {
					if v == "--execution-plan-file" {
						f := args[n+1]
						b, _ := os.ReadFile(f)
						os.WriteFile(f, append(b, ' '), 0600)
					}
				}
			case "confirmation":
				for n, v := range args {
					if v == "--confirm-execution-plan-sha256" {
						args[n+1] = strings.Repeat("f", 64)
					}
				}
			}
			raw, _ := json.Marshal(m)
			os.WriteFile(file, raw, 0600)
			r, err := captureReadiness(t, append(args, "--readiness-manifest-file", file))
			if err == nil || r["status"] != "FAIL" || r["supplier_credentials"] != "NOT_INSPECTED" || r["network_requests"] != float64(0) {
				t.Fatal(r, err)
			}
		})
	}
}

func TestOfflineManifestRejectsCredentialFieldsWithoutEchoingValues(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(file, []byte(`{"schema_version":1,"mode":"real","supplier_api_key":"SYNTHETIC-CANARY-DO-NOT-ECHO"}`), 0600)
	r, err := captureReadiness(t, []string{"--offline-preflight", "--readiness-manifest-file", file})
	b, _ := json.Marshal(r)
	if err == nil || r["status"] != "FAIL" || strings.Contains(string(b), "SYNTHETIC-CANARY") {
		t.Fatal(r, err)
	}
}

func TestOfflineReadinessRejectsExplicitDifferentTargetDatabase(t *testing.T) {
	m, args, file := readinessFixture(t)
	raw, _ := json.Marshal(m)
	os.WriteFile(file, raw, 0600)
	r, err := captureReadiness(t, append(args, "--readiness-manifest-file", file, "--expected-database", "different_test"))
	if err == nil || r["status"] != "FAIL" {
		t.Fatalf("explicit target database ignored: %v %v", r, err)
	}
}

func TestRuntimePreflightRejectsUnboundPolicyBeforeSupplierConfiguration(t *testing.T) {
	_, args, _ := readinessFixture(t)
	for n, v := range args {
		if v == "--offline-preflight" {
			args[n] = "--preflight"
		}
		if v == "--policy-file" {
			args[n+1] = "missing-non-secret-policy.json"
		}
	}
	r, err := captureReadiness(t, args)
	checks := r["checks"].(map[string]any)
	if err == nil || checks["supplier_credentials"] != "NOT_INSPECTED" || r["blockers"].([]any)[0] != "APPROVAL_POLICY_OR_DATABASE_INVALID_BEFORE_CONFIGURATION" {
		t.Fatal(r, err)
	}
}

func TestOrdinaryPreflightRequiresConfirmationBeforeConfiguration(t *testing.T) {
	_, args, _ := readinessFixture(t)
	filtered := []string{"--preflight"}
	for n := 0; n < len(args); n++ {
		if args[n] == "--offline-preflight" || args[n] == "--allow-runtime-scope-probe" {
			continue
		}
		if args[n] == "--execution-plan-file" || args[n] == "--execution-plan-sha256" || args[n] == "--confirm-execution-plan-sha256" {
			n++
			continue
		}
		filtered = append(filtered, args[n])
	}
	r, err := captureReadiness(t, filtered)
	if err == nil || r["checks"].(map[string]any)["supplier_credentials"] != "NOT_INSPECTED" || r["blockers"].([]any)[0] != "EXECUTION_PLAN_OR_CONFIRMATION_INVALID" {
		t.Fatal(r, err)
	}
}

func TestConfirmedCandidateIsPreflightOnlyAndCannotStartOrDispatch(t *testing.T) {
	_, args, _ := readinessFixture(t)
	var path string
	for i, v := range args {
		if v == "--execution-plan-file" {
			path = args[i+1]
		}
	}
	raw, _ := os.ReadFile(path)
	var p preflightReport
	json.Unmarshal(raw, &p)
	p.Status = "CANDIDATE_NO_EXECUTION"
	raw, _ = json.Marshal(p)
	os.WriteFile(path, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if verifyPreflightConfirmation(*p.ExecutionPlan, path, digest, digest) != nil {
		t.Fatal("confirmed candidate refused by non-dispatching preflight")
	}
	if verifyExecutionConfirmation(*p.ExecutionPlan, path, digest, digest) == nil {
		t.Fatal("candidate authorized startup")
	}
	if verifyPreflightConfirmation(*p.ExecutionPlan, path, digest, "") == nil {
		t.Fatal("candidate silently confirmed")
	}
}

func TestOfflineReadinessNeverLoadsCredentialConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "public-manifest.json")
	os.WriteFile(file, []byte(`{"schema_version":1,"mode":"real"}`), 0600)
	report, err := captureReadiness(t, []string{"--offline-preflight", "--readiness-manifest-file", file, "--env-file", "must-not-open-credentials.env"})
	if err == nil || report["status"] != "BLOCKED" || report["supplier_credentials"] != "NOT_INSPECTED" || report["network_requests"] != float64(0) || report["execution_authorized"] != false {
		t.Fatal(report, err)
	}
}
