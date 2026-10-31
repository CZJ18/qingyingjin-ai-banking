package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

// This manifest is declarative public metadata, never a credential container or
// an additional source of authority. Only existing Approval and plan validators
// establish bindings. External references are never treated as verified facts.
type readinessWorkflow struct {
	Enabled      bool   `json:"enabled"`
	ID           string `json:"id"`
	Version      string `json:"published_version"`
	CanvasSHA256 string `json:"canvas_sha256"`
}

func (w *readinessWorkflow) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || (string(fields["enabled"]) != "true" && string(fields["enabled"]) != "false") {
		return modelspend.ErrApproval
	}
	type plain readinessWorkflow
	return modelspend.StrictJSON(raw, (*plain)(w), 65536)
}

type readinessEvidence struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}
type readinessManifest struct {
	IntegrationMode     string                       `json:"integration_mode,omitempty"`
	DirectRequestSHA256 string                       `json:"direct_request_sha256,omitempty"`
	SchemaVersion       int                          `json:"schema_version"`
	Mode                string                       `json:"mode"`
	Supplier            string                       `json:"supplier"`
	SupplierOrigin      string                       `json:"supplier_origin"`
	Model               string                       `json:"model"`
	ModelVersion        string                       `json:"model_version"`
	CozeOrigin          string                       `json:"coze_origin"`
	ProtectedScopeMode  string                       `json:"protected_scope_mode"`
	GatewayOrigin       string                       `json:"gateway_origin"`
	AllowedOrigins      []string                     `json:"allowed_origins"`
	Workflows           map[string]readinessWorkflow `json:"workflows"`
	PriceVersion        string                       `json:"price_version"`
	PriceSource         string                       `json:"price_source"`
	PriceEvidenceSHA256 string                       `json:"price_evidence_sha256"`
	Currency            string                       `json:"currency"`
	BudgetAmount        string                       `json:"budget_amount"`
	MaxRequests         int64                        `json:"max_requests"`
	MaxConcurrent       int64                        `json:"max_concurrent"`
	ContextTokens       int64                        `json:"context_tokens"`
	MaxOutputTokens     int64                        `json:"max_output_tokens"`
	UserID              int64                        `json:"user_id"`
	TaskRequestSHA256   string                       `json:"task_request_sha256"`
	Database            string                       `json:"database"`
	DataScope           string                       `json:"data_scope"`
	ValidFrom           string                       `json:"valid_from"`
	ValidUntil          string                       `json:"valid_until"`
	ExternalEvidence    map[string]readinessEvidence `json:"external_evidence"`
}
type readinessCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type offlineReport struct {
	ManifestSHA256         string           `json:"manifest_sha256,omitempty"`
	Status                 string           `json:"status"`
	Checks                 []readinessCheck `json:"checks"`
	SupplierCredentials    string           `json:"supplier_credentials"`
	NetworkRequests        int              `json:"network_requests"`
	DatabaseConnections    int              `json:"database_connections"`
	ReservationsCreated    int              `json:"reservations_created"`
	ExecutionAuthorized    bool             `json:"execution_authorized"`
	CandidatePlanGenerated bool             `json:"candidate_plan_generated"`
	DeclaredMaxCost        *string          `json:"declared_price_maximum_cost,omitempty"`
	DeclaredCurrency       *string          `json:"declared_price_currency,omitempty"`
	DeclaredPriceVerified  bool             `json:"declared_price_independently_verified"`
	RemainingBudget        *string          `json:"remaining_budget"`
	RemainingRequests      *int64           `json:"remaining_requests"`
	RemainingConcurrent    *int64           `json:"remaining_concurrent"`
}

func metadataDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func metadataOrigin(s string, supplier bool) bool {
	u, e := url.Parse(s)
	if e != nil || u.String() != s || u.ForceQuery || u.RawFragment != "" || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || u.Host != strings.ToLower(u.Host) || u.Hostname() == "" || u.Path != "" && !(supplier && u.Path == "/v1") {
		return false
	}
	base := *u
	base.Path = ""
	return outbound.TestPolicy().Validate(base.String()) == nil && base.String() == u.Scheme+"://"+u.Host
}
func approvedPolicyBeforeConfiguration(a modelspend.Approval, path, runID, database string) error {
	raw, err := readBounded(path, 16384)
	if err != nil {
		return modelspend.ErrApproval
	}
	p, err := modelspend.ParsePolicy(raw)
	if err != nil || a.Validate(p, runID, database, time.Now()) != nil {
		return modelspend.ErrApproval
	}
	return nil
}

func offlineReadiness(manifestPath, approvalPath, approvalSHA, runID, policyPath, planPath, planSHA, confirmedSHA, expectedDatabase string) error {
	r, _ := inspectReadiness(manifestPath, approvalPath, approvalSHA, runID, policyPath, planPath, planSHA, confirmedSHA, expectedDatabase, false)
	_ = json.NewEncoder(os.Stdout).Encode(r)
	return modelspend.ErrApproval
}

// Shared local structural/binding checks; no output, env, SQL or network. The
// ordinary entry must not silently ignore a supplied manifest. PARTIAL here is
// only local consistency; all existing price/permission/scope gates still run.
func inspectReadiness(manifestPath, approvalPath, approvalSHA, runID, policyPath, planPath, planSHA, confirmedSHA, expectedDatabase string, candidateAllowed bool) (offlineReport, readinessManifest) {
	return inspectReadinessInputs(manifestPath, approvalPath, approvalSHA, runID, policyPath, planPath, planSHA, confirmedSHA, expectedDatabase, candidateAllowed, nil)
}

// The optional instructions are used ONLY to prepare an unconfirmed candidate.
// Normal offline/preflight/startup callers always verify the exact plan instead.
func inspectReadinessInputs(manifestPath, approvalPath, approvalSHA, runID, policyPath, planPath, planSHA, confirmedSHA, expectedDatabase string, candidateAllowed bool, candidateInstructions *executionInstructions) (offlineReport, readinessManifest) {
	r := offlineReport{Status: "PARTIAL", SupplierCredentials: "NOT_INSPECTED", Checks: []readinessCheck{}}
	add := func(name, status, detail string) { r.Checks = append(r.Checks, readinessCheck{name, status, detail}) }
	var m readinessManifest
	raw, e := readBounded(manifestPath, 65536)
	valid := e == nil && modelspend.StrictJSON(raw, &m, 65536) == nil
	if !valid {
		status := "FAIL"
		if manifestPath == "" {
			status = "BLOCKED"
		}
		add("non_secret_manifest", status, "explicit bounded strict JSON required; unknown fields including credentials are rejected; references are never opened")
	} else {
		direct := m.IntegrationMode == "direct_provider"
		if m.IntegrationMode != "" && m.IntegrationMode != "coze" && !direct {
			add("integration_mode", "FAIL", "unknown integration mode")
		}
		if direct && (!metadataDigest(m.DirectRequestSHA256) || m.CozeOrigin != "" || m.ProtectedScopeMode != "") {
			add("direct_binding", "FAIL", "direct mode requires exact request digest and no Coze authority")
		}
		if !direct && m.DirectRequestSHA256 != "" {
			add("direct_binding", "FAIL", "direct request pin is not a Coze binding")
		}
		r.ManifestSHA256 = readinessManifestSHA(raw)
		add("non_secret_manifest", "PASS", "strict schema parsed; no env or referenced evidence/secret files opened")
		if m.SchemaVersion != 1 || (m.Mode != "real" && m.Mode != "simulation") {
			add("schema_and_mode", "FAIL", "schema_version=1 and explicit real/simulation mode required")
		} else {
			add("schema_and_mode", "PASS", "declared mode only; not runtime mode verification")
		}
		for _, v := range []struct{ name, value string }{{"supplier", m.Supplier}, {"model", m.Model}, {"model_version", m.ModelVersion}, {"price_version", m.PriceVersion}, {"price_source", m.PriceSource}, {"database", m.Database}, {"data_scope", m.DataScope}} {
			if strings.TrimSpace(v.value) == "" {
				add(v.name, "BLOCKED", "required public metadata not supplied")
			} else {
				add(v.name, "PASS", "declared non-empty metadata; actual external identity not verified")
			}
		}
		for _, v := range []struct {
			name, value string
			supplier    bool
		}{{"supplier_origin", m.SupplierOrigin, true}, {"coze_origin", m.CozeOrigin, false}, {"gateway_origin", m.GatewayOrigin, false}} {
			if direct && v.name == "coze_origin" {
				continue
			}
			if v.value == "" {
				add(v.name, "BLOCKED", "origin not supplied")
			} else if !metadataOrigin(v.value, v.supplier) || m.Mode == "real" && !strings.HasPrefix(v.value, "https://") {
				add(v.name, "FAIL", "canonical origin required; no userinfo, query or fragment")
			} else {
				add(v.name, "PASS", "origin syntax only; no DNS or network probe")
			}
		}
		if direct {
			add("scope_contract", "PARTIAL", "direct mode; no Coze publication or scope attestation")
		} else if m.ProtectedScopeMode != "banking-task-scope-v1" {
			add("scope_contract", "BLOCKED", "protected scope mode must be explicitly specified")
		} else {
			add("scope_contract", "PASS", "declared scope contract only")
		}
		if len(m.AllowedOrigins) == 0 {
			add("declared_egress", "BLOCKED", "explicit fixed origin allowlist required")
		} else {
			allowed := map[string]bool{}
			ok := true
			for _, origin := range m.AllowedOrigins {
				if !metadataOrigin(origin, false) || allowed[origin] {
					ok = false
				}
				allowed[origin] = true
			}
			u, _ := url.Parse(m.SupplierOrigin)
			supplierOrigin := ""
			if u != nil {
				supplierOrigin = u.Scheme + "://" + u.Host
			}
			if (!direct && !allowed[m.CozeOrigin]) || !allowed[m.GatewayOrigin] || !allowed[supplierOrigin] {
				ok = false
			}
			for origin := range allowed {
				if (direct || origin != m.CozeOrigin) && origin != m.GatewayOrigin && origin != supplierOrigin {
					ok = false
				}
			}
			if ok {
				add("declared_egress", "PASS", "declared origins cover supplier, Coze and gateway; actual network not inspected")
			} else {
				add("declared_egress", "FAIL", "allowlist invalid or does not cover declared origins")
			}
		}
		if !metadataDigest(m.PriceEvidenceSHA256) || !metadataDigest(m.TaskRequestSHA256) || m.UserID <= 0 {
			add("evidence_and_task_digests", "BLOCKED", "explicit price/task SHA256 and positive user required")
		} else {
			add("evidence_and_task_digests", "PASS", "syntax only; account binding still governed by persisted task and Go tools")
		}
		amount, err := modelspend.ParseMoney(m.BudgetAmount)
		if err != nil || amount <= 0 || len(m.Currency) != 3 || strings.Trim(m.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" || m.MaxRequests < 1 || m.MaxConcurrent < 1 || m.MaxConcurrent > m.MaxRequests || m.ContextTokens < 1 || m.MaxOutputTokens < 1 || m.MaxOutputTokens > m.ContextTokens || m.MaxOutputTokens > bankingscope.MaxConfiguredOutputTokens {
			add("explicit_limits", "BLOCKED", "positive budget, currency, request/concurrency and token bounds required")
		} else {
			add("explicit_limits", "PASS", "declared bounds; no SQL quota snapshot or reservation")
		}
		from, e1 := time.Parse(time.RFC3339, m.ValidFrom)
		until, e2 := time.Parse(time.RFC3339, m.ValidUntil)
		if e1 != nil || e2 != nil || !from.Before(until) || time.Now().Before(from) || !time.Now().Before(until) {
			add("validity_window", "BLOCKED", "explicit currently valid time window required")
		} else {
			add("validity_window", "PASS", "declared validity window")
		}
	}
	a, approvalErr := modelspend.ReadApproval(approvalPath, approvalSHA, runID, time.Now())
	if approvalErr != nil {
		add("new_approval", "BLOCKED", "fresh explicitly supplied approval and independently confirmed digest required; no historical discovery")
	} else {
		add("new_approval", "PASS", "existing approval parser and validity checks passed; not execution consent")
		if expectedDatabase == "" {
			add("target_database_binding", "BLOCKED", "explicit --expected-database required for the intended target")
		} else if expectedDatabase != a.Database {
			add("target_database_binding", "FAIL", "explicit target database differs from approval")
		} else {
			add("target_database_binding", "PASS", "explicit target database matches approval; no database connection")
		}
		if valid {
			manifestMode := m.IntegrationMode
			if manifestMode == "" {
				manifestMode = "coze"
			}
			if manifestMode != a.TransportMode() || m.DirectRequestSHA256 != a.DirectRequestSHA256 || a.Simulation != (m.Mode == "simulation") || m.Model != a.Model || m.SupplierOrigin != a.ProviderOrigin || m.UserID != a.UserID || m.TaskRequestSHA256 != a.TaskRequestSHA256 || m.Database != a.Database || m.DataScope != a.Scope || m.Currency != a.Currency || m.BudgetAmount != a.Amount || m.MaxRequests != a.MaxRequests || m.MaxConcurrent != a.MaxConcurrent || m.ContextTokens != a.ContextTokens || m.MaxOutputTokens != a.MaxOutputTokens || m.ValidFrom != a.ValidFrom || m.ValidUntil != a.ValidUntil {
				add("manifest_approval_binding", "FAIL", "mode, supplier/model, user/task/data/database, time or monetary/token bounds differ")
			} else {
				add("manifest_approval_binding", "PASS", "exact public scope matches existing approval")
			}
		}
	}
	if candidateInstructions != nil {
		if approvalErr != nil || candidateInstructions.check(a) != nil {
			add("candidate_instructions", "FAIL", "instructions must bind the new approval and registered read/advice tools")
		} else {
			add("plan_and_independent_confirmation", "NOT_INSPECTED", "preparing an unconfirmed candidate only; no execution or preflight authority granted")
		}
	} else if planPath == "" || planSHA == "" || confirmedSHA == "" {
		add("plan_and_independent_confirmation", "BLOCKED", "explicit exact plan bytes, digest and separately supplied confirmation required; never generated here")
	} else if approvalErr != nil {
		add("plan_and_independent_confirmation", "NOT_INSPECTED", "cannot bind plan without valid new approval")
	} else if verifyConfirmedPlan(a, planPath, planSHA, confirmedSHA, candidateAllowed) != nil {
		add("plan_and_independent_confirmation", "FAIL", "existing exact plan/approval/instructions/confirmation validator rejected supplied files")
	} else {
		add("plan_and_independent_confirmation", "PASS", "local exact binding only; does not start gateway or authorize execution")
	}
	var p modelspend.Policy
	policyRaw, policyErr := readBounded(policyPath, 16384)
	if policyErr != nil {
		add("price_policy", "BLOCKED", "explicit non-secret existing policy file required")
	} else if p, policyErr = modelspend.ParsePolicy(policyRaw); policyErr != nil || p.Validate(time.Now()) != nil {
		add("price_policy", "FAIL", "existing policy parser/validity rejected file")
	} else if approvalErr != nil {
		add("price_policy_binding", "NOT_INSPECTED", "valid policy structure; cannot bind without approval")
	} else if a.Validate(p, runID, a.Database, time.Now()) != nil {
		add("price_policy_binding", "FAIL", "existing approval-policy validator rejected policy")
	} else {
		if valid && (p.PriceVersion != m.PriceVersion || p.PriceSource != m.PriceSource || p.PriceEvidenceSHA256 != m.PriceEvidenceSHA256) {
			add("price_policy_binding", "FAIL", "declared price metadata differs from approved policy")
		} else {
			add("price_policy_binding", "PASS", "approved policy binding; provider prices not independently verified")
		}
		// Offline real evidence is never independently reviewed here. Do not
		// present a number derived from declared prices as real fee risk.
		if valid && m.Mode == "simulation" {
			units, _ := p.ReserveUnits()
			v := modelspend.FormatMoney(units)
			r.DeclaredMaxCost = &v
			r.DeclaredCurrency = &p.Currency
		}
	}
	if valid {
		direct := m.IntegrationMode == "direct_provider"
		pins := map[string]string{}
		if approvalErr == nil && !direct {
			pins[a.WorkflowID] = a.WorkflowSHA256
			for id, h := range a.AdditionalWorkflows {
				pins[id] = h
			}
		}
		declared := map[string]string{}
		known := map[string]bool{"supervisor": true, "billing": true, "subscription": true, "savings": true, "lifestyle": true}
		for role := range m.Workflows {
			if !known[role] {
				add("workflow_roles", "FAIL", "unknown role in manifest")
			}
		}
		for _, role := range []string{"supervisor", "billing", "subscription", "savings", "lifestyle"} {
			w, exists := m.Workflows[role]
			if !exists {
				add("workflow_"+role, "BLOCKED", "supply workflow metadata or explicitly disable unused specialist")
				continue
			}
			if direct {
				if w.Enabled || w.ID != "" || w.Version != "" || w.CanvasSHA256 != "" {
					add("workflow_"+role, "FAIL", "direct mode cannot carry Coze workflow authority")
				}
				continue
			}
			if !w.Enabled {
				if role == "supervisor" || w.ID != "" || w.Version != "" || w.CanvasSHA256 != "" {
					add("workflow_"+role, "FAIL", "supervisor cannot be disabled; disabled specialist must not carry workflow authority")
				} else {
					add("workflow_"+role, "PARTIAL", "explicitly disabled specialist; runtime configuration still needs independent inspection")
				}
				continue
			}
			if w.ID == "" || strings.TrimSpace(w.Version) == "" || !metadataDigest(w.CanvasSHA256) {
				add("workflow_"+role, "BLOCKED", "enabled role requires ID, published version and exact raw Canvas SHA256")
				continue
			}
			if _, ok := declared[w.ID]; ok {
				add("workflow_"+role, "FAIL", "role aliases cannot reuse a workflow ID; enable/disable must identify distinct reviewed workflows")
			}
			declared[w.ID] = w.CanvasSHA256
			if approvalErr != nil {
				add("workflow_"+role, "NOT_INSPECTED", "metadata present; no valid approval to bind")
			} else if !a.AllowsWorkflow(w.ID, w.CanvasSHA256) || role == "supervisor" && w.ID != a.WorkflowID {
				add("workflow_"+role, "FAIL", "workflow not in exact approved pins")
			} else {
				add("workflow_"+role, "PASS", "declared pin matches approval; actual published Canvas bytes/status not inspected")
			}
		}
		if approvalErr == nil {
			if len(pins) != len(declared) {
				add("workflow_set", "FAIL", "declared enabled workflow set differs from approval; never expand authorization automatically")
			} else {
				for id, h := range pins {
					if declared[id] != h {
						add("workflow_set", "FAIL", "workflow pins differ")
					}
				}
			}
		}
		if m.Mode == "real" && (strings.HasPrefix(m.Model, "synthetic-") || strings.HasPrefix(strings.ToLower(m.PriceSource), "synthetic:")) {
			add("real_fixture_separation", "FAIL", "real manifest cannot use synthetic model or price metadata")
		}
		for _, name := range []string{"publication", "permission", "egress", "price", "usage", "billing"} {
			v, exists := m.ExternalEvidence[name]
			if !exists || v.Reference == "" || v.Kind == "" {
				add("external_"+name, "BLOCKED", "reviewable non-secret evidence reference required; do not include credentials")
			} else if m.Mode == "real" && v.Kind != "official_reference" {
				add("external_"+name, "FAIL", "synthetic or unknown evidence kind cannot attest real capability")
			} else {
				add("external_"+name, "NOT_INSPECTED", "reference recorded structurally only; bytes, authority, permissions and current external state not verified")
			}
		}
	}
	add("live_scope_publication_permissions_prices_egress_billing", "NOT_INSPECTED", "offline mode never reads credentials, follows references, resolves DNS or invokes external interfaces")
	add("persistent_ledger_and_remaining_limits", "NOT_INSPECTED", "no database connection; remaining amounts/counts/concurrency are null, not zero or available")
	add("execution_authorization", "NOT_INSPECTED", "offline result cannot create confirmation, grant, reservation or financial execution intent")
	for _, c := range r.Checks {
		if c.Status == "BLOCKED" {
			r.Status = "BLOCKED"
		}
	}
	for _, c := range r.Checks {
		if c.Status == "FAIL" {
			r.Status = "FAIL"
		}
	}
	// Even structurally complete metadata remains PARTIAL without independent
	// external/ledger checks. No READY execution document is emitted here.
	return r, m
}

// Keep the manifest hash available for audit without copying its values to logs.
func readinessManifestSHA(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
