package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func candidateFixture(t *testing.T) ([]string, string) {
	t.Helper()
	m, flags, manifest := readinessFixture(t)
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--prepare-execution-plan", "--readiness-manifest-file", manifest}
	var planPath string
	for n := 0; n < len(flags); n++ {
		switch flags[n] {
		case "--offline-preflight", "--allow-runtime-scope-probe":
		case "--execution-plan-file":
			planPath = flags[n+1]
			n++
		case "--execution-plan-sha256", "--confirm-execution-plan-sha256":
			n++
		default:
			args = append(args, flags[n], flags[n+1])
			n++
		}
	}
	data, _ := os.ReadFile(planPath)
	var p preflightReport
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	i, _ := json.Marshal(p.ExecutionInstructions)
	file := filepath.Join(t.TempDir(), "instructions.json")
	if err := os.WriteFile(file, i, 0600); err != nil {
		t.Fatal(err)
	}
	return append(args, "--acceptance-instructions-file", file), manifest
}

func TestPrepareExecutionPlanIsOfflineAndCannotAuthorizeDispatch(t *testing.T) {
	args, _ := candidateFixture(t)
	// Invalid protected paths and an explicit probe flag must remain unused.
	args = append(args, "--coze-scope-env-file", "must-not-open.env", "--allow-runtime-scope-probe")
	r, err := captureReadiness(t, args)
	if err != nil || r["status"] != "CANDIDATE_NO_EXECUTION" || r["supplier_requests"] != float64(0) || r["snapshot_only_no_reservation"] != true {
		t.Fatalf("candidate generation failed: %v %v", r, err)
	}
	if r["project_remaining_budget"] != nil || r["user_remaining_budget"] != nil {
		t.Fatal("fabricated live balances", r)
	}
	window := r["approval_window"].(map[string]any)
	if len(window) != 1 || window["approval_sha256"] == nil {
		t.Fatal("offline candidate must contain only approval binding, never zero-valued live quota", window)
	}
	raw, _ := json.Marshal(r)
	var plan preflightReport
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "candidate.json")
	os.WriteFile(file, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if verifyPreflightConfirmation(*plan.ExecutionPlan, file, digest, digest) != nil {
		t.Fatal("candidate cannot enter independently confirmed preflight")
	}
	if verifyPreflightConfirmation(*plan.ExecutionPlan, file, digest, "") == nil {
		t.Fatal("candidate confirmed itself")
	}
	if verifyExecutionConfirmation(*plan.ExecutionPlan, file, digest, digest) == nil {
		t.Fatal("candidate authorized dispatch")
	}
	if plan.Checks["external_materials"] != "NOT_INSPECTED" || plan.Checks["supplier_credentials"] != "NOT_INSPECTED" || plan.Checks["execution_authorization"] != "NOT_GRANTED" {
		t.Fatal("candidate overstated readiness", plan.Checks)
	}
}

func TestPrepareExecutionPlanRejectsIncompleteOrMismatchedInputs(t *testing.T) {
	for _, kind := range []string{"missing_approval", "wrong_database", "workflow", "instructions", "mixed_mode"} {
		t.Run(kind, func(t *testing.T) {
			args, manifest := candidateFixture(t)
			switch kind {
			case "missing_approval":
				args = append(args, "--approval-file", "missing-approval.json")
			case "wrong_database":
				args = append(args, "--expected-database", "other_test")
			case "instructions":
				args = append(args, "--acceptance-instructions-file", "missing-instructions.json")
			case "mixed_mode":
				args = append(args, "--preflight")
			case "workflow":
				raw, _ := os.ReadFile(manifest)
				var m readinessManifest
				json.Unmarshal(raw, &m)
				w := m.Workflows["supervisor"]
				w.CanvasSHA256 = fmt.Sprintf("%064x", 1)
				m.Workflows["supervisor"] = w
				raw, _ = json.Marshal(m)
				os.WriteFile(manifest, raw, 0600)
			}
			// Mixed modes may reject flags before JSON; all other cases report blockers.
			if kind == "mixed_mode" {
				if run(args) == nil {
					t.Fatal("ambiguous action accepted")
				}
				return
			}
			r, err := captureReadiness(t, args)
			if err == nil || r["status"] == "CANDIDATE_NO_EXECUTION" || r["execution_plan"] != nil {
				t.Fatalf("invalid inputs produced plan: %v %v", r, err)
			}
		})
	}
}
