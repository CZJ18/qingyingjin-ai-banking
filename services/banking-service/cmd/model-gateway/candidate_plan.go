package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

// Pure local preparation, deliberately before env/SQL/client initialization.
// A zero exit code means a candidate was serialized, never READY or consent.
func prepareExecutionPlan(manifest, approvalFile, approvalSHA, runID, policy, database, instructionsFile string) error {
	a, err := modelspend.ReadApproval(approvalFile, approvalSHA, runID, time.Now())
	if err != nil {
		return blockedPreflight("FRESH_EXPLICIT_APPROVAL_REQUIRED")
	}
	i, err := readExecutionInstructions(instructionsFile, a)
	if err != nil || i == nil {
		return blockedPreflight("ACCEPTANCE_INSTRUCTIONS_INVALID")
	}
	r, _ := inspectReadinessInputs(manifest, approvalFile, approvalSHA, runID, policy, "", "", "", database, false, i)
	if r.Status != "PARTIAL" {
		_ = json.NewEncoder(os.Stdout).Encode(r)
		return modelspend.ErrApproval
	}
	p := preflightReport{
		Status: "CANDIDATE_NO_EXECUTION", Blockers: []string{}, SnapshotOnly: true,
		ExecutionPlan: &a, ExecutionInstructions: i,
		Checks: map[string]string{
			"local_manifest_approval_policy_instructions": "PASS",
			"external_materials":                          "NOT_INSPECTED",
			"supplier_credentials":                        "NOT_INSPECTED",
			"ledger_limits":                               "NOT_INSPECTED",
			"execution_authorization":                     "NOT_GRANTED",
			"supplier_dispatch":                           "DISABLED",
		},
	}
	// Project only the existing fingerprint field. A candidate has no live
	// ApprovalWindow; serializing that struct would invent zero quota values.
	return json.NewEncoder(os.Stdout).Encode(struct {
		preflightReport
		ApprovalBinding map[string]string `json:"approval_window"`
	}{p, map[string]string{"approval_sha256": a.Fingerprint()}})
}
