// model-gateway owns the supplier credential and monetary admission boundary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/spendconfig"
	"github.com/qingyingjin/banking-service/internal/taskproof"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		if errors.Is(e, errExecutionConfirmation) {
			fmt.Fprintln(os.Stderr, "MODEL_EXECUTION_CONFIRMATION_REQUIRED: separately confirm the exact reviewed execution plan before starting real dispatch")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "model gateway refused; verify protected configuration, budget policy and database schema")
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("model-gateway", flag.ContinueOnError)
	env := f.String("env-file", "", "protected gateway-only configuration")
	policy := f.String("policy-file", "", "independently verified model price and cumulative limit policy")
	database := f.String("expected-database", "", "explicit exact database scope")
	workflowPolicy := f.String("workflow-policy-file", "", "reviewed workflowID to exact published Canvas SHA256 map; empty disables workflows")
	addr := f.String("listen", "127.0.0.1:18160", "private listener")
	cert, key := f.String("tls-cert", "", "listener certificate"), f.String("tls-key", "", "listener private key")
	probe := f.String("probe", "", "probe private loopback readiness without loading credentials")
	priceEvidence := f.String("price-evidence-file", "", "independent reviewed provider price/bounds evidence")
	priceReviewed := f.Bool("confirm-price-review", false, "operator has independently confirmed provider price and bounds")
	preflight := f.Bool("preflight", false, "read-only admission report; never dispatch or reserve")
	integrationMode := f.String("integration-mode", "coze", "coze or isolated-test direct_provider; must match new approval")
	approvalFile := f.String("approval-file", "", "explicit NEW approval for this run; no historical discovery")
	approvalSHA := f.String("approval-sha256", "", "independently confirmed digest of the new approval file")
	runID := f.String("run-id", "", "explicit current run identifier bound by the new approval")
	permissionFile := f.String("permission-review-file", "", "new operator-reviewed supplier permission evidence binding")
	permissionSHA := f.String("permission-review-sha256", "", "independently confirmed permission review digest")
	permissionEvidence := f.String("permission-evidence-file", "", "supplier permission evidence bytes; never synthetic")
	cozeEnv := f.String("coze-scope-env-file", "", "explicit Coze scope-only credentials; read only after approval")
	canvas := f.String("published-canvas-file", "", "reviewed exported published Canvas matching the approved digest")
	gatewayOrigin := f.String("gateway-origin", "", "gateway origin confirmed by the authenticated Coze scope")
	probeScope := f.Bool("allow-runtime-scope-probe", false, "explicitly allow one authenticated scope GET; never a workflow/model call")
	instructionsFile := f.String("acceptance-instructions-file", "", "non-secret test input, task digest, tool scope, verification and failure handling")
	executionPlanFile := f.String("execution-plan-file", "", "exact independently reviewed READY preflight JSON; not itself execution consent")
	executionPlanSHA := f.String("execution-plan-sha256", "", "digest of the exact proposed execution plan")
	confirmedPlanSHA := f.String("confirm-execution-plan-sha256", "", "explicit separately confirmed plan digest; never infer from approval or preflight success")
	offline := f.Bool("offline-preflight", false, "non-secret manifest checks only; never load env, connect SQL, probe scope, reserve or dispatch")
	preparePlan := f.Bool("prepare-execution-plan", false, "offline candidate JSON to stdout; requires explicit new approval/policy/manifest/instructions; never confirms or authorizes execution")
	manifest := f.String("readiness-manifest-file", "", "explicit non-secret real integration manifest; no credential values or automatic file discovery")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return errors.New("invalid gateway flags")
	}
	if *preparePlan {
		if *offline || *preflight || *probe != "" || *executionPlanFile != "" || *executionPlanSHA != "" || *confirmedPlanSHA != "" {
			return errors.New("candidate preparation cannot be combined with preflight, probe or execution confirmation")
		}
		return prepareExecutionPlan(*manifest, *approvalFile, *approvalSHA, *runID, *policy, *database, *instructionsFile)
	}
	if *offline {
		return offlineReadiness(*manifest, *approvalFile, *approvalSHA, *runID, *policy, *executionPlanFile, *executionPlanSHA, *confirmedPlanSHA, *database)
	}
	if *probe != "" {
		return checkProbe(*probe)
	}
	approval, approvalError := modelspend.ReadApproval(*approvalFile, *approvalSHA, *runID, time.Now())
	if approvalError != nil {
		if *preflight {
			return blockedPreflight("FRESH_EXPLICIT_APPROVAL_REQUIRED")
		}
		return modelspend.ErrApproval
	}
	if approval.TransportMode() != *integrationMode {
		return modelspend.ErrApproval
	}
	if !*preflight {
		if e := verifyExecutionConfirmation(approval, *executionPlanFile, *executionPlanSHA, *confirmedPlanSHA); e != nil {
			return e
		}
	}
	if *preflight {
		if verifyPreflightConfirmation(approval, *executionPlanFile, *executionPlanSHA, *confirmedPlanSHA) != nil {
			return blockedPreflight("EXECUTION_PLAN_OR_CONFIRMATION_INVALID")
		}
	}
	instructions, instructionsError := readExecutionInstructions(*instructionsFile, approval)
	if instructionsError != nil {
		if *preflight {
			return blockedPreflight("ACCEPTANCE_INSTRUCTIONS_INVALID")
		}
		return instructionsError
	}
	if *preflight && approvedPolicyBeforeConfiguration(approval, *policy, *runID, *database) != nil {
		return blockedPreflight("APPROVAL_POLICY_OR_DATABASE_INVALID_BEFORE_CONFIGURATION")
	}
	var integration *readinessManifest
	if *manifest != "" || !approval.Simulation {
		r, m := inspectReadiness(*manifest, *approvalFile, *approvalSHA, *runID, *policy, *executionPlanFile, *executionPlanSHA, *confirmedPlanSHA, *database, *preflight)
		if r.Status == "FAIL" || r.Status == "BLOCKED" || m.GatewayOrigin != *gatewayOrigin {
			if *preflight {
				return blockedPreflight("MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION")
			}
			return modelspend.ErrApproval
		}
		integration = &m // Frozen local declaration; never reload as new authority.
	}
	var reviewedWorkflows map[string]string
	if *workflowPolicy != "" {
		raw, err := readBounded(*workflowPolicy, 65536)
		if err != nil || modelspend.StrictJSON(raw, &reviewedWorkflows, 65536) != nil || len(reviewedWorkflows) == 0 {
			if *preflight {
				return blockedPreflight("MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION")
			}
			return modelspend.ErrApproval
		}
	}
	if integration != nil {
		if approval.TransportMode() == "direct_provider" && len(reviewedWorkflows) != 0 {
			return modelspend.ErrApproval
		}
		pins := map[string]string{approval.WorkflowID: approval.WorkflowSHA256}
		if approval.TransportMode() == "direct_provider" {
			pins = map[string]string{}
		}
		for id, h := range approval.AdditionalWorkflows {
			pins[id] = h
		}
		validPins := len(reviewedWorkflows) == len(pins)
		for id, h := range pins {
			if reviewedWorkflows[id] != h {
				validPins = false
			}
		}
		if !validPins {
			if *preflight {
				return blockedPreflight("MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION")
			}
			return modelspend.ErrApproval
		}
	}
	c, e := spendconfig.Load(*env, *policy, *database, true)
	if e != nil {
		if *preflight {
			return blockedPreflight("GATEWAY_POLICY_OR_DATABASE_CONFIGURATION_INVALID")
		}
		return e
	}
	if approval.Validate(c.Policy, *runID, *database, time.Now()) != nil {
		if *preflight {
			return blockedPreflight("APPROVAL_SCOPE_OR_POLICY_MISMATCH")
		}
		return modelspend.ErrApproval
	}
	if approval.Simulation && c.Outbound.Environment != "test" {
		if *preflight {
			return blockedPreflight("SIMULATION_REQUIRES_EXPLICIT_TEST_ENVIRONMENT")
		}
		return modelspend.ErrApproval
	}
	if c.Env["BANKING_MODEL_SPEND_ADMIN_TOKEN"] != "" || c.Env["BANKING_MODEL_SPEND_OPERATOR"] != "" {
		return errors.New("operator authority must not enter model gateway")
	}
	if e = spendconfig.ValidateListener(c.Outbound.Environment, *addr, *cert, *key); e != nil {
		return e
	}
	timeout := 90 * time.Second
	if raw := c.Env["BANKING_MODEL_GATEWAY_TIMEOUT"]; raw != "" {
		timeout, e = time.ParseDuration(raw)
		if e != nil || timeout < time.Second || timeout > 3*time.Minute {
			return errors.New("invalid supplier timeout")
		}
	}
	gc := modelgateway.Config{Policy: c.Policy, GatewayToken: c.Env["BANKING_MODEL_GATEWAY_TOKEN"], SupplierKey: c.Env["BANKING_MODEL_SUPPLIER_API_KEY"], BaseURL: c.Env["BANKING_MODEL_SUPPLIER_BASE_URL"], Timeout: timeout, Outbound: c.Outbound}
	if approval.TransportMode() == "direct_provider" {
		if c.Outbound.Environment != "test" {
			return errors.New("direct_provider is isolated-test only")
		}
	}
	gc.Approval, gc.RunID, gc.Database = &approval, *runID, *database
	if gc.BaseURL != "" && gc.BaseURL != approval.ProviderOrigin {
		if *preflight {
			return blockedPreflight("APPROVED_SUPPLIER_ORIGIN_MISMATCH")
		}
		return modelspend.ErrApproval
	}
	gc.SupplierKey, e = supplierCredential(approval, c.Env, os.Getenv, readBounded)
	if e != nil {
		return e
	}
	if origin := c.Env["BANKING_MODEL_IDENTITY_BASE_URL"]; origin != "" {
		gc.VerifyIdentity, e = modelgateway.NewIdentityVerifier(origin, c.Outbound)
		if e != nil {
			return e
		}
	}
	if *workflowPolicy != "" || approval.TransportMode() == "direct_provider" {
		gc.WorkflowPolicies = reviewedWorkflows
		proof, e := taskproof.New(c.Env["BANKING_MODEL_TASK_STATE_URL"], c.Env["BANKING_MODEL_TASK_SERVICE_KEY"], c.Outbound)
		if e != nil {
			return e
		}
		gc.VerifyTask, e = modelgateway.NewTaskIdentityVerifier(c.Env["BANKING_MODEL_IDENTITY_BASE_URL"], c.Outbound, proof)
		if e != nil {
			return e
		}
	}
	// Only explicit loopback fake suppliers in test mode skip paid-price review.
	parsed, parseError := url.Parse(gc.BaseURL)
	if parseError != nil {
		return errors.New("invalid supplier origin")
	}
	testIP := net.ParseIP(parsed.Hostname())
	if !(c.Outbound.Environment == "test" && testIP != nil && testIP.IsLoopback()) {
		file, e := os.Open(*priceEvidence)
		if e != nil {
			if *preflight {
				return blockedPreflight("REVIEWED_PRICE_EVIDENCE_REQUIRED")
			}
			return errors.New("reviewed price evidence required")
		}
		raw, e := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		file.Close()
		if e != nil || spendconfig.ReviewPrice(c.Policy, raw, *priceReviewed) != nil {
			if *preflight {
				return blockedPreflight("PRICE_EVIDENCE_OR_REVIEW_INVALID")
			}
			return errors.New("reviewed price evidence required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, e := spendconfig.Open(ctx, c)
	if e != nil {
		if *preflight {
			return blockedPreflight("DATABASE_SCOPE_SCHEMA_OR_STORAGE_AUTHORITY_UNAVAILABLE")
		}
		return e
	}
	defer conn.Close()
	ledger, e := modelspend.New(conn.DB, c.Policy)
	if e != nil {
		return e
	}
	if e = ledger.UseApproval(approval, *runID, *database); e != nil {
		return e
	}
	h, e := modelgateway.New(gc, ledger)
	if e != nil {
		if *preflight {
			return blockedPreflight("GATEWAY_CREDENTIAL_OR_IDENTITY_CONFIGURATION_INVALID")
		}
		return e
	}
	permissionOK := reviewPermission(approval, gc.SupplierKey, *permissionFile, *permissionSHA, *permissionEvidence, *approvalFile, *approvalSHA) == nil
	runtimeOK := false
	if approval.TransportMode() == "direct_provider" {
		runtimeOK = c.Outbound.Environment == "test" && gc.VerifyTask != nil && len(gc.WorkflowPolicies) == 0
	}
	if *probeScope && approval.TransportMode() != "direct_provider" {
		if integration != nil {
			runtimeOK = reviewRuntime(ctx, approval, gc, *cozeEnv, *canvas, *gatewayOrigin, c.Outbound, integration.CozeOrigin) == nil
		} else {
			runtimeOK = reviewRuntime(ctx, approval, gc, *cozeEnv, *canvas, *gatewayOrigin, c.Outbound) == nil
		}
	}
	if *preflight {
		return reportPreflight(ctx, ledger, approval, permissionOK, runtimeOK, instructions)
	}
	if !permissionOK || !runtimeOK {
		return errors.New("fresh permission review and explicit runtime scope verification required")
	}
	baseHandler := executionPlanHandler(approval, *executionPlanFile, *executionPlanSHA, *confirmedPlanSHA, h)
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
			check, done := context.WithTimeout(r.Context(), time.Second)
			defer done()
			if spendconfig.Check(check, c, conn) != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		baseHandler.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: timeout + 10*time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	go func() {
		<-stop.Done()
		end, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(end)
	}()
	if *cert != "" {
		e = server.ListenAndServeTLS(*cert, *key)
	} else {
		e = server.ListenAndServe()
	}
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func checkProbe(target string) error {
	u, e := url.Parse(target)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/readyz" || u.RawPath != "" || u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("invalid readiness target")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("readiness must be loopback")
	}
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Get(target)
	if e != nil {
		return errors.New("gateway unready")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("gateway unready")
	}
	return nil
}
