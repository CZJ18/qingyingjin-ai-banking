package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

type preflightReport struct {
	ExecutionInstructions *executionInstructions     `json:"execution_instructions,omitempty"`
	ExecutionPlan         *modelspend.Approval       `json:"execution_plan,omitempty"`
	RequestBinding        string                     `json:"request_id_binding,omitempty"`
	ReservationState      string                     `json:"reservation_state,omitempty"`
	SettlementState       string                     `json:"settlement_state,omitempty"`
	Status                string                     `json:"status"`
	Blockers              []string                   `json:"blockers"`
	Checks                map[string]string          `json:"checks"`
	Approval              *modelspend.ApprovalWindow `json:"approval_window,omitempty"`
	ProjectRemaining      string                     `json:"project_remaining_budget,omitempty"`
	UserRemaining         string                     `json:"user_remaining_budget,omitempty"`
	SnapshotOnly          bool                       `json:"snapshot_only_no_reservation"`
	SupplierRequests      int                        `json:"supplier_requests"`
}

func blockedPreflight(code string) error {
	report := preflightReport{Status: "BLOCKED", Blockers: []string{code}, SnapshotOnly: true, Checks: map[string]string{
		"fresh_approval": "missing_or_invalid", "supplier_credentials": "NOT_INSPECTED", "credential_permissions": "NOT_VERIFIED", "published_workflow_scope": "NOT_VERIFIED", "ledger_limits": "NOT_INSPECTED", "supplier_dispatch": "DISABLED"}}
	if code != "FRESH_EXPLICIT_APPROVAL_REQUIRED" {
		report.Checks["fresh_approval"] = "explicit_approval_read; blocked_by_later_check"
		if code != "EXECUTION_PLAN_OR_CONFIRMATION_INVALID" && code != "ACCEPTANCE_INSTRUCTIONS_INVALID" && code != "APPROVAL_POLICY_OR_DATABASE_INVALID_BEFORE_CONFIGURATION" && code != "MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION" {
			report.Checks["supplier_credentials"] = "MAY_HAVE_BEEN_LOADED_FROM_EXPLICIT_CONFIGURATION; NOT_LIVE_VERIFIED"
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	return modelspend.ErrApproval
}

// A separately supplied operator review must bind the actual key fingerprint,
// approval and permission evidence bytes. This is not a provider API probe.
type permissionReview struct {
	ApprovalSHA256   string `json:"approval_sha256"`
	CredentialSHA256 string `json:"credential_sha256"`
	ProviderOrigin   string `json:"provider_origin"`
	Model            string `json:"model"`
	Permission       string `json:"permission"`
	EvidenceSHA256   string `json:"evidence_sha256"`
	ValidUntil       string `json:"valid_until"`
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, modelspend.ErrApproval
	}
	return raw, nil
}

func reviewPermission(a modelspend.Approval, key, path, digest, evidencePath string, approvalSource ...string) error {
	raw, e := readBounded(path, 16384)
	if e != nil || digest == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
		return modelspend.ErrApproval
	}
	var review permissionReview
	evidence, e := readBounded(evidencePath, 16<<20)
	until, deadlineErr := time.Parse(time.RFC3339, a.ValidUntil)
	if e != nil || deadlineErr != nil || modelspend.StrictJSON(raw, &review, 16384) != nil {
		return modelspend.ErrApproval
	}
	approvalBound := review.ApprovalSHA256 == a.Fingerprint()
	if len(approvalSource) != 0 {
		if len(approvalSource) != 2 {
			return modelspend.ErrApproval
		}
		// File-byte hashes can differ from the ledger's canonical fingerprint.
		// Accept the independently confirmed file digest only after re-reading
		// those exact bytes and proving they represent this same approval.
		confirmed, err := modelspend.ReadApproval(approvalSource[0], approvalSource[1], a.RunID, time.Now())
		if err != nil || confirmed.Fingerprint() != a.Fingerprint() {
			return modelspend.ErrApproval
		}
		approvalBound = approvalBound || review.ApprovalSHA256 == approvalSource[1]
	}
	expires, e := time.Parse(time.RFC3339, review.ValidUntil)
	if e != nil || !time.Now().Before(expires) || !expires.Equal(until) || len(key) < 16 || len(key) > 512 || !approvalBound || review.CredentialSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(key))) ||
		review.ProviderOrigin != a.ProviderOrigin || review.Model != a.Model || review.Permission != "chat.completions" || review.EvidenceSHA256 != fmt.Sprintf("%x", sha256.Sum256(evidence)) {
		return modelspend.ErrApproval
	}
	return nil
}

// Probe only the explicitly supplied authenticated Coze scope endpoint. No
// workflow/grant/completion request is sent; proxy and redirects are disabled.
func reviewRuntime(ctx context.Context, a modelspend.Approval, gc modelgateway.Config, envPath, canvasPath, gatewayOrigin string, policy outbound.Policy, expectedCozeOrigin ...string) error {
	raw, e := readBounded(canvasPath, 1<<20)
	if e != nil {
		return modelspend.ErrApproval
	}
	pins := map[string]string{a.WorkflowID: a.WorkflowSHA256}
	for id, digest := range a.AdditionalWorkflows {
		pins[id] = digest
	}
	if len(a.AdditionalWorkflows) == 0 {
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != a.WorkflowSHA256 {
			return modelspend.ErrApproval
		}
	} else {
		// Bundle values preserve the exact exported Canvas bytes as strings.
		var bundle map[string]string
		if modelspend.StrictJSON(raw, &bundle, 1<<20) != nil || len(bundle) != len(pins) {
			return modelspend.ErrApproval
		}
		for id, digest := range pins {
			canvas, ok := bundle[id]
			if !ok || fmt.Sprintf("%x", sha256.Sum256([]byte(canvas))) != digest {
				return modelspend.ErrApproval
			}
		}
	}
	for id, digest := range pins {
		if gc.WorkflowPolicies[id] != digest {
			return modelspend.ErrApproval
		}
	}
	values, e := godotenv.Read(envPath)
	if e != nil {
		return modelspend.ErrApproval
	}
	origin, pat := values["BANKING_COZE_BASE_URL"], values["BANKING_COZE_PAT"]
	if len(expectedCozeOrigin) > 0 && origin != expectedCozeOrigin[0] {
		return modelspend.ErrApproval
	}
	if policy.Validate(origin) != nil || policy.Validate(gatewayOrigin) != nil || len(pat) < 16 || gc.WorkflowPolicies[a.WorkflowID] != a.WorkflowSHA256 {
		return modelspend.ErrApproval
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/v1/banking/model-scope", nil)
	if e != nil {
		return modelspend.ErrApproval
	}
	request.Header.Set("Authorization", "Bearer "+pat)
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 3 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return modelspend.ErrApproval
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 || response.StatusCode != 200 {
		return modelspend.ErrApproval
	}
	var scope struct {
		Mode            string            `json:"mode"`
		WorkflowPins    map[string]string `json:"workflow_pins"`
		GatewayOrigin   string            `json:"gateway_origin"`
		Model           string            `json:"model"`
		MaxOutputTokens int64             `json:"max_output_tokens"`
	}
	if modelspend.StrictJSON(raw, &scope, 65536) != nil || scope.Mode != "banking-task-scope-v1" || scope.WorkflowPins[a.WorkflowID] != a.WorkflowSHA256 || scope.GatewayOrigin != gatewayOrigin || scope.Model != a.Model || scope.MaxOutputTokens != a.MaxOutputTokens {
		return modelspend.ErrApproval
	}
	for id, digest := range pins {
		if scope.WorkflowPins[id] != digest {
			return modelspend.ErrApproval
		}
	}
	return nil
}

func reportPreflight(ctx context.Context, s *modelspend.Service, a modelspend.Approval, permissionOK, runtimeOK bool, instructions ...*executionInstructions) error {
	r := preflightReport{Status: "BLOCKED", Blockers: []string{}, SnapshotOnly: true, Checks: map[string]string{"fresh_approval": "VALID_FOR_EXPLICIT_RUN", "supplier_dispatch": "DISABLED", "credential_permissions": "NOT_VERIFIED", "published_workflow_scope": "NOT_VERIFIED"}}
	r.ExecutionPlan = &a
	r.Checks["execution_confirmation"] = "NOT_GRANTED_BY_PREFLIGHT; SEPARATE_EXACT_PLAN_CONFIRMATION_REQUIRED"
	if len(instructions) > 0 {
		r.ExecutionInstructions = instructions[0]
	}
	if r.ExecutionInstructions.check(a) != nil {
		r.Blockers = append(r.Blockers, "ACCEPTANCE_INSTRUCTIONS_REQUIRED")
	}
	r.RequestBinding = "workflow-" + "SHA256(task_id + NUL + workflow_id); exact request bytes and workflow scope hashed at Reserve"
	r.ReservationState = "NO_RESERVATION_CREATED_BY_PREFLIGHT"
	r.SettlementState = "NOT_SETTLED; requires independently reviewed supplier evidence"
	if !permissionOK {
		r.Blockers = append(r.Blockers, "CREDENTIAL_PERMISSION_EVIDENCE_REQUIRED")
	} else {
		r.Checks["credential_permissions"] = "OPERATOR_REVIEWED_EVIDENCE_BOUND_TO_KEY_AND_APPROVAL"
	}
	if a.TransportMode() == "direct_provider" {
		r.Checks["published_workflow_scope"] = "NOT_APPLICABLE_DIRECT_PROVIDER; COZE_NOT_VALIDATED"
		if !runtimeOK {
			r.Blockers = append(r.Blockers, "ISOLATED_DIRECT_TASK_TRANSPORT_REQUIRED")
		} else {
			r.Checks["direct_task_transport"] = "TEST_ONLY; AUTHORITATIVE_TASK_AND_EXACT_PAYLOAD_RECHECKED_AT_DISPATCH"
		}
	} else if !runtimeOK {
		r.Blockers = append(r.Blockers, "AUTHENTICATED_COZE_SCOPE_AND_REVIEWED_CANVAS_REQUIRED")
	} else {
		r.Checks["published_workflow_scope"] = "LIVE_SCOPE_AND_CANVAS_MATCH; ACTUAL_PUBLICATION_RECHECKED_BEFORE_DISPATCH"
	}
	if a.Simulation {
		r.Checks["dispatch_mode"] = "EXPLICIT_TEST_LOOPBACK_SIMULATION_ONLY; NOT_A_REAL_MODEL"
		if permissionOK {
			r.Checks["credential_permissions"] = "SYNTHETIC_PERMISSION_FIXTURE; NOT_PROVIDER_PERMISSION_EVIDENCE"
		}
		if runtimeOK && a.TransportMode() != "direct_provider" {
			r.Checks["published_workflow_scope"] = "EXPLICIT_SIMULATED_SCOPE_AND_CANVAS; NOT_REAL_PUBLICATION_PROOF"
		}
	}
	window, e := s.ApprovalBudget(ctx)
	if e != nil {
		r.Blockers = append(r.Blockers, "APPROVAL_LEDGER_UNAVAILABLE")
	} else {
		r.Approval = &window
		remaining, _ := modelspend.ParseMoney(window.RemainingBudget)
		risk, _ := modelspend.ParseMoney(window.MaxRequestRisk)
		if window.RemainingRequests < 1 || window.RemainingConcurrent < 1 || remaining < risk {
			r.Blockers = append(r.Blockers, "MODEL_APPROVAL_LIMIT_EXCEEDED")
		}
	}
	pool, e := s.ProjectBudget(ctx)
	if e != nil || pool.Frozen || pool.UsedUnits > pool.LimitUnits || pool.HeldUnits > pool.LimitUnits-pool.UsedUnits {
		r.Blockers = append(r.Blockers, "PROJECT_BUDGET_UNAVAILABLE")
	} else {
		left := pool.LimitUnits - pool.UsedUnits - pool.HeldUnits
		r.ProjectRemaining = modelspend.FormatMoney(left)
		bound, _ := modelspend.ParseMoney(window.MaxRequestRisk)
		if left < bound {
			r.Blockers = append(r.Blockers, "PROJECT_BUDGET_EXCEEDED")
		}
	}
	user, e := s.UserBudget(ctx, a.UserID)
	if e != nil || user.Frozen || user.UsedUnits < 0 || user.HeldUnits < 0 || user.UsedUnits > user.LimitUnits || user.HeldUnits > user.LimitUnits-user.UsedUnits {
		r.Blockers = append(r.Blockers, "USER_BUDGET_UNAVAILABLE")
	} else {
		left := user.LimitUnits - user.UsedUnits - user.HeldUnits
		r.UserRemaining = modelspend.FormatMoney(left)
		bound, _ := modelspend.ParseMoney(window.MaxRequestRisk)
		if left < bound {
			r.Blockers = append(r.Blockers, "USER_BUDGET_EXCEEDED")
		}
	}
	if len(r.Blockers) == 0 {
		r.Status = "READY_SNAPSHOT_ONLY"
		r.Checks["ledger_limits"] = "SQL_REQUEST_COUNT_CONCURRENCY_AND_MONEY_ENFORCED_AT_RESERVE"
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	if len(r.Blockers) != 0 {
		return modelspend.ErrApproval
	}
	return nil
}
