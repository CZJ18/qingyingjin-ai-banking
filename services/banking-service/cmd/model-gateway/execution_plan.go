package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/skills"
)

var errExecutionConfirmation = errors.New("execution plan confirmation required")

// Describes a human-reviewed acceptance run. This is not a new tool grant;
// the existing authenticated task, whitelist and SQL approval still govern calls.
type executionInstructions struct {
	TaskRequestSHA256  string           `json:"task_request_sha256"`
	TestInput          string           `json:"test_input"`
	DeterministicTools []skills.Request `json:"deterministic_tools"`
	ExpectedDataScope  string           `json:"expected_data_scope"`
	Verification       []string         `json:"verification"`
	FailureHandling    []string         `json:"failure_handling"`
}

func (v *executionInstructions) check(a modelspend.Approval) error {
	if v == nil || v.TaskRequestSHA256 != a.TaskRequestSHA256 || strings.TrimSpace(v.TestInput) == "" || len(v.TestInput) > 8192 || strings.TrimSpace(v.ExpectedDataScope) == "" || len(v.ExpectedDataScope) > 8192 || len(v.DeterministicTools) > 32 {
		return errExecutionConfirmation
	}
	// A zero-tool plan is only valid for the exact, bounded direct smoke payload.
	// Ordinary planner/Coze plans retain their existing registered-tool requirement.
	smoke := false
	if a.TransportMode() == "direct_provider" {
		raw, err := coze.DirectSmokeRequest(a.Model, a.MaxOutputTokens)
		smoke = err == nil && a.DirectRequestSHA256 == fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	if smoke && (v.TestInput != coze.DirectSmokeInput || len(v.DeterministicTools) != 0) || !smoke && len(v.DeterministicTools) == 0 {
		return errExecutionConfirmation
	}
	seen := map[string]bool{}
	for _, tool := range v.DeterministicTools {
		key := tool.Agent + "/" + tool.Tool
		if tool.SkillID == "" || tool.SkillVersion < 1 || tool.Tool == "model_smoke" || (tool.Mode != "read_only" && tool.Mode != "advice") || seen[key] {
			return errExecutionConfirmation
		}
		if _, err := skills.Resolve(tool); err != nil {
			return errExecutionConfirmation
		}
		seen[key] = true
	}
	for _, items := range [][]string{v.Verification, v.FailureHandling} {
		if len(items) < 1 || len(items) > 16 {
			return errExecutionConfirmation
		}
		for _, text := range items {
			if strings.TrimSpace(text) == "" || len(text) > 2048 {
				return errExecutionConfirmation
			}
		}
	}
	return nil
}

func readExecutionInstructions(path string, a modelspend.Approval) (*executionInstructions, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := readBounded(path, 65536)
	var v executionInstructions
	if err != nil || modelspend.StrictJSON(raw, &v, 65536) != nil || v.check(a) != nil {
		return nil, errExecutionConfirmation
	}
	return &v, nil
}

// The separately supplied confirmation must match the exact preflight plan bytes.
// This gate executes before supplier configuration is read. A READY snapshot is
// never itself execution consent, and all runtime quota checks still run again.
func verifyExecutionConfirmation(a modelspend.Approval, path, expected, confirmed string) error {
	return verifyConfirmedPlan(a, path, expected, confirmed, false)
}

// Only the non-dispatching preflight accepts a separately confirmed candidate.
// It still requires every exact approval/instructions binding. Startup and all
// HTTP calls require a confirmed READY snapshot and never accept this candidate.
func verifyPreflightConfirmation(a modelspend.Approval, path, expected, confirmed string) error {
	return verifyConfirmedPlan(a, path, expected, confirmed, true)
}

func verifyConfirmedPlan(a modelspend.Approval, path, expected, confirmed string, candidateAllowed bool) error {
	if path == "" || len(expected) != 64 || expected != confirmed {
		return errExecutionConfirmation
	}
	raw, err := readBounded(path, 131072)
	var plan preflightReport
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected || modelspend.StrictJSON(raw, &plan, 131072) != nil {
		return errExecutionConfirmation
	}
	statusOK := plan.Status == "READY_SNAPSHOT_ONLY" || candidateAllowed && plan.Status == "CANDIDATE_NO_EXECUTION"
	if !statusOK || len(plan.Blockers) != 0 || !plan.SnapshotOnly || plan.SupplierRequests != 0 || plan.ExecutionPlan == nil || plan.ExecutionPlan.Fingerprint() != a.Fingerprint() || plan.Approval == nil || plan.Approval.ApprovalSHA256 != a.Fingerprint() || plan.ExecutionInstructions.check(a) != nil {
		return errExecutionConfirmation
	}
	// Ensure this remains a JSON plan, not a trailing shell command/document.
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return errExecutionConfirmation
	}
	return nil
}

// A read-only bind mount can still be changed by its host. Recheck before every
// admission/dispatch request so a process cannot keep minting grants under an
// obsolete confirmation. Health is liveness only; readiness fails closed too.
func executionPlanHandler(a modelspend.Approval, path, expected, confirmed string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			if verifyExecutionConfirmation(a, path, expected, confirmed) != nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				if a.Simulation {
					w.Header().Set("X-Banking-Simulation", "synthetic-only-no-real-supplier")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "model_gateway_error", "code": "MODEL_EXECUTION_CONFIRMATION_REQUIRED", "message": "MODEL_EXECUTION_CONFIRMATION_REQUIRED"}})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
