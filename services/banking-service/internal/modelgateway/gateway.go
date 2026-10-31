// Package modelgateway enforces the project budget before supplier dispatch.
package modelgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Budget interface {
	Reserve(context.Context, string, string) (modelspend.Attempt, error)
	Observe(context.Context, string, modelspend.Report) error
	Unknown(context.Context, string) error
}
type Config struct {
	Approval                           *modelspend.Approval
	RunID, Database                    string
	VerifyIdentity                     IdentityVerifier
	VerifyTask                         TaskIdentityVerifier
	WorkflowPolicies                   map[string]string
	Policy                             modelspend.Policy
	GatewayToken, SupplierKey, BaseURL string
	Timeout                            time.Duration
	Outbound                           outbound.Policy
}

const requestLimit = 1 << 20
const responseLimit = 2 << 20

type gateway struct {
	client     *http.Client
	cfg        Config
	budget     Budget
	policyHash string
}

// New permits the official supplier origin in production. Loopback substitutes
// are available only with the explicit test policy; certificate checks remain on.
func New(cfg Config, b Budget) (http.Handler, error) {
	if cfg.Approval != nil && cfg.Approval.TransportMode() == "direct_provider" && (cfg.Outbound.Environment != "test" || cfg.VerifyTask == nil || cfg.VerifyIdentity == nil || len(cfg.WorkflowPolicies) != 0) {
		return nil, errors.New("direct_provider requires explicit isolated test environment and authoritative task identity; no Coze grants")
	}
	if b == nil || cfg.Policy.Validate(time.Now()) != nil || len(cfg.GatewayToken) < 32 || len(cfg.GatewayToken) > 512 || len(cfg.SupplierKey) < 16 || len(cfg.SupplierKey) > 512 || cfg.GatewayToken == cfg.SupplierKey || strings.ContainsAny(cfg.GatewayToken+cfg.SupplierKey, " \t\r\n") {
		return nil, errors.New("invalid model gateway configuration")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	u, e := url.Parse(cfg.BaseURL)
	if e != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "/v1" {
		return nil, errors.New("invalid model supplier origin")
	}
	test := cfg.Outbound.Environment == "test" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
	if cfg.Approval != nil && cfg.Approval.Simulation && !test {
		return nil, errors.New("simulation approval requires explicit test loopback supplier")
	}
	if !test || cfg.Approval != nil {
		approved, ok := b.(interface{ ApprovalFingerprint() string })
		if cfg.Approval == nil || cfg.Approval.Validate(cfg.Policy, cfg.RunID, cfg.Database, time.Now()) != nil || !ok || approved.ApprovalFingerprint() != cfg.Approval.Fingerprint() {
			return nil, errors.New("fresh bounded model approval required")
		}
	}
	if !test && cfg.VerifyIdentity == nil {
		return nil, errors.New("authenticated user verifier required")
	}
	if !test && cfg.BaseURL != "https://api.deepseek.com/v1" {
		return nil, errors.New("invalid model supplier origin")
	}
	if cfg.Outbound.Validate(u.Scheme+"://"+u.Host) != nil {
		return nil, errors.New("invalid model supplier transport")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Timeout < time.Millisecond || cfg.Timeout > 5*time.Minute {
		return nil, errors.New("invalid model gateway timeout")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: cfg.Timeout, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 20, MaxIdleConnsPerHost: 20, DisableCompression: true, ForceAttemptHTTP2: true}
	for id, digest := range cfg.WorkflowPolicies {
		if id == "" || !hexDigest(digest) || cfg.VerifyTask == nil {
			return nil, errors.New("invalid reviewed workflow policy")
		}
	}
	hash, _ := cfg.Policy.Hash()
	return &gateway{client: &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cfg: cfg, budget: b, policyHash: hash}, nil
}

func failure(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": code, "type": "model_gateway_error", "code": code}})
}
func (g *gateway) authorized(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimPrefix(v, "Bearer ")))
	want := sha256.Sum256([]byte(g.cfg.GatewayToken))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.cfg.Approval != nil && g.cfg.Approval.Simulation {
		w.Header().Set("X-Banking-Simulation", "synthetic-only-no-real-supplier")
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
		if g.cfg.Policy.Validate(time.Now()) != nil || g.cfg.Approval != nil && g.cfg.Approval.Check(g.cfg.RunID, time.Now()) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
		return
	}
	var workflow *workflowCapability
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer bgc1.") {
		if (r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/v1/workflow-admit") || r.Method != http.MethodPost || len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("X-Banking-Authorization")) != 0 {
			failure(w, 401, "WORKFLOW_CAPABILITY_INVALID")
			return
		}
		c, e := g.verifyWorkflow(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if e != nil {
			failure(w, 401, "WORKFLOW_CAPABILITY_INVALID")
			return
		}
		workflow = &c
	} else if !g.authorized(r) {
		failure(w, 401, "UNAUTHORIZED")
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "INVALID_REQUEST")
		return
	}
	if r.URL.Path == "/v1/workflow-grants" {
		if g.cfg.Approval != nil && g.cfg.Approval.TransportMode() == "direct_provider" {
			failure(w, 403, "DIRECT_PROVIDER_COZE_DISABLED")
			return
		}
		g.mintWorkflow(w, r)
		return
	}
	if r.URL.Path == "/v1/workflow-admit" {
		if workflow == nil {
			failure(w, 401, "WORKFLOW_CAPABILITY_REQUIRED")
			return
		}
		var in struct {
			WorkflowID     string `json:"workflow_id"`
			WorkflowSHA256 string `json:"workflow_sha256"`
			InputSHA256    string `json:"input_sha256"`
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, 4097))
		if e != nil || modelspend.StrictJSON(raw, &in, 4096) != nil || in.WorkflowID != workflow.WorkflowID || in.WorkflowSHA256 != workflow.WorkflowSHA256 || in.InputSHA256 != workflow.InputSHA256 {
			failure(w, 403, "WORKFLOW_INPUT_CONFLICT")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var user int64
	direct := g.cfg.Approval != nil && g.cfg.Approval.TransportMode() == "direct_provider" && r.URL.Path == "/v1/chat/completions"
	if direct {
		if workflow != nil || len(r.Header.Values("X-Banking-Authorization")) != 1 {
			failure(w, 401, "TASK_IDENTITY_REQUIRED")
			return
		}
		actor, err := g.cfg.VerifyTask(r.Context(), r.Header.Get("X-Banking-Authorization"))
		a := g.cfg.Approval
		if err != nil || actor.UserID != a.UserID || actor.RequestDigest != a.TaskRequestSHA256 || actor.Version <= 0 || !hexDigest(actor.LeaseFingerprint) || !requestID.MatchString(actor.TaskID) {
			failure(w, 401, "TASK_IDENTITY_INVALID")
			return
		}
		workflow = &workflowCapability{UserID: actor.UserID, TaskID: actor.TaskID, TaskRequestDigest: actor.RequestDigest, TaskVersion: actor.Version, LeaseFingerprint: actor.LeaseFingerprint, WorkflowID: "direct_provider", WorkflowSHA256: a.DirectRequestSHA256, PolicySHA256: a.PolicySHA256, Model: a.Model, PriceVersion: g.cfg.Policy.PriceVersion, ContextTokens: a.ContextTokens, MaxOutputTokens: a.MaxOutputTokens}
	}
	if workflow != nil {
		user = workflow.UserID
		r = r.WithContext(modelspend.WithWorkflowBinding(modelspend.WithUser(r.Context(), user), workflow.binding()))
	}
	if (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/budget") && g.cfg.VerifyIdentity != nil && workflow == nil {
		if len(r.Header.Values("X-Banking-Authorization")) != 1 {
			failure(w, 401, "USER_IDENTITY_REQUIRED")
			return
		}
		var err error
		user, err = g.cfg.VerifyIdentity(r.Context(), r.Header.Get("X-Banking-Authorization"))
		if err != nil || user <= 0 {
			failure(w, 401, "USER_IDENTITY_INVALID")
			return
		}
		r = r.WithContext(modelspend.WithUser(r.Context(), user))
	}
	if r.URL.Path == "/v1/budget" && r.Method == http.MethodGet {
		// A read-only capacity snapshot is not permission to dispatch. Recheck the
		// live window before SQL so stale configuration cannot look available or
		// hide expiration as a generic ledger outage.
		if g.cfg.Policy.Validate(time.Now()) != nil {
			failure(w, 503, "POLICY_UNAVAILABLE")
			return
		}
		if a := g.cfg.Approval; a != nil && (a.UserID != user || a.Check(g.cfg.RunID, time.Now()) != nil) {
			failure(w, 403, "MODEL_APPROVAL_REQUIRED")
			return
		}
		reader, ok := g.budget.(interface {
			UserBudget(context.Context, int64) (modelspend.UserWindow, error)
		})
		if !ok || user <= 0 {
			failure(w, 503, "BUDGET_UNAVAILABLE")
			return
		}
		window, err := reader.UserBudget(r.Context(), user)
		if err != nil {
			failure(w, 503, "BUDGET_UNAVAILABLE")
			return
		}
		var approvalWindow any
		var limits any
		if a := g.cfg.Approval; a != nil {
			if a.UserID != user {
				failure(w, 403, "MODEL_APPROVAL_REQUIRED")
				return
			}
			approved, ok := g.budget.(interface {
				ApprovalBudget(context.Context) (modelspend.ApprovalWindow, error)
			})
			if !ok {
				failure(w, 503, "BUDGET_UNAVAILABLE")
				return
			}
			v, e := approved.ApprovalBudget(r.Context())
			if e != nil || v.ApprovalSHA256 != a.Fingerprint() || v.Currency != a.Currency {
				failure(w, 503, "BUDGET_UNAVAILABLE")
				return
			}
			approvalWindow = v
			limits = map[string]any{"max_requests": a.MaxRequests, "max_concurrent": a.MaxConcurrent, "context_tokens": a.ContextTokens, "max_output_tokens": a.MaxOutputTokens, "simulation": a.Simulation}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"approval_window": approvalWindow, "approval_limits": limits, "period": window.Period, "timezone": "Asia/Shanghai", "currency": window.Currency, "limit": modelspend.FormatMoney(window.LimitUnits), "settled": modelspend.FormatMoney(window.UsedUnits), "reserved": modelspend.FormatMoney(window.HeldUnits), "frozen": window.Frozen, "settlement_basis": "independently_reviewed_supplier_bill"})
		return
	}
	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		if g.cfg.Policy.Validate(time.Now()) != nil {
			failure(w, 503, "POLICY_UNAVAILABLE")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": g.cfg.Policy.Model, "object": "model", "owned_by": "configured-project"}}, "model_spend_enforcement": map[string]any{"mode": "conservative_project_lifetime", "user_monthly_identity_required": g.cfg.VerifyIdentity != nil, "project_id": g.cfg.Policy.ProjectID, "policy_sha256": g.policyHash, "max_output_tokens": g.cfg.Policy.MaxOutputTokens}})
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		failure(w, 404, "NOT_FOUND")
		return
	}
	if r.Method != http.MethodPost {
		failure(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	if g.cfg.Policy.Validate(time.Now()) != nil {
		failure(w, 503, "POLICY_UNAVAILABLE")
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, requestLimit+1))
	if e != nil || len(raw) > requestLimit {
		failure(w, 400, "INVALID_REQUEST")
		return
	}
	req, e := parseRequest(raw, g.cfg.Policy)
	if e != nil {
		if errors.Is(e, inputContextExceeded) {
			failure(w, 400, "MODEL_CONTEXT_LIMIT_EXCEEDED")
			return
		}
		failure(w, 400, "INVALID_REQUEST")
		return
	}
	if direct && fmt.Sprintf("%x", sha256.Sum256(raw)) != g.cfg.Approval.DirectRequestSHA256 {
		failure(w, 403, "DIRECT_REQUEST_BINDING_MISMATCH")
		return
	}
	key := r.Header.Get("X-Model-Request-ID")
	if workflow != nil {
		// A workflow cannot invent another idempotency key, tool loop or fallback.
		if len(r.Header.Values("X-Model-Request-ID")) != 0 || len(req.Tools) > 0 || len(req.ToolChoice) > 0 || req.Stream {
			failure(w, 400, "WORKFLOW_REQUEST_NOT_BOUNDED")
			return
		}
		sum := sha256.Sum256([]byte(workflow.TaskID + "\x00" + workflow.WorkflowID))
		key = "workflow-" + hex.EncodeToString(sum[:])
	}
	if len(r.Header.Values("X-Model-Request-ID")) > 1 || (key != "" && !requestID.MatchString(key)) {
		failure(w, 400, "INVALID_REQUEST_ID")
		return
	}
	if key == "" {
		key = uuid.NewString()
	}
	requestFingerprint := sha256.Sum256([]byte(key))
	w.Header().Set("X-Model-Request-Fingerprint", hex.EncodeToString(requestFingerprint[:]))
	// Hash the exact caller bytes. Reusing a key with changed parameters or even a
	// changed serialization never authorizes another paid attempt.
	digest := sha256.Sum256(raw)
	if workflow != nil {
		binding, _ := json.Marshal(workflow.binding())
		digest = sha256.Sum256(append(append(binding, 0), raw...))
	}
	a, e := g.budget.Reserve(modelspend.WithProviderOrigin(r.Context(), g.cfg.BaseURL), key, hex.EncodeToString(digest[:]))
	if a.ProjectID == g.cfg.Policy.ProjectID && requestID.MatchString(a.ID) && !g.hasSecret([]byte(a.ID)) {
		w.Header().Set("X-Model-Reservation-ID", a.ID)
		w.Header().Set("X-Model-Accounting-State", "reserved_or_unverified")
	}
	if e != nil {
		switch {
		case errors.Is(e, modelspend.ErrApproval):
			failure(w, 403, "MODEL_APPROVAL_REQUIRED")
		case errors.Is(e, modelspend.ErrApprovalQuota):
			failure(w, 429, "MODEL_APPROVAL_LIMIT_EXCEEDED")
		case errors.Is(e, modelspend.ErrUserQuota):
			failure(w, 429, "USER_MONTHLY_BUDGET_EXCEEDED")
		case errors.Is(e, modelspend.ErrQuota):
			failure(w, 429, "BUDGET_EXCEEDED")
		case errors.Is(e, modelspend.ErrConflict):
			failure(w, 409, "REQUEST_CONFLICT")
		case errors.Is(e, modelspend.ErrUnknown):
			failure(w, 409, "OUTCOME_UNKNOWN")
		default:
			failure(w, 503, "BUDGET_UNAVAILABLE")
		}
		return
	}
	if a.ProjectID != g.cfg.Policy.ProjectID || (a.PolicyHash != "" && a.PolicyHash != g.policyHash) {
		failure(w, 503, "BUDGET_UNAVAILABLE")
		return
	}
	if a.ResponseBody != nil {
		if a.Status == "settled" {
			w.Header().Set("X-Model-Accounting-State", "settled")
		} else {
			w.Header().Set("X-Model-Accounting-State", "reported_unsettled")
		}
		cached := []byte(*a.ResponseBody)
		if len(cached) > responseLimit || g.hasSecret(cached) || (a.ContentType != "application/json" && a.ContentType != "text/event-stream") {
			failure(w, 503, "CACHE_UNAVAILABLE")
			return
		}
		if a.ResponseDigest != "" {
			sum := sha256.Sum256(cached)
			if hex.EncodeToString(sum[:]) != a.ResponseDigest {
				failure(w, 503, "CACHE_UNAVAILABLE")
				return
			}
		}
		w.Header().Set("Content-Type", a.ContentType)
		w.Write(cached)
		return
	}
	forward := raw
	if req.Stream {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(raw, &obj)
		obj["stream_options"] = json.RawMessage(`{"include_usage":true}`)
		forward, _ = json.Marshal(obj)
	}
	upstream, e := http.NewRequestWithContext(r.Context(), http.MethodPost, g.cfg.BaseURL+"/chat/completions", bytes.NewReader(forward))
	if e != nil {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+g.cfg.SupplierKey)
	upstream.Header.Set("Content-Type", "application/json")
	if req.Stream {
		upstream.Header.Set("Accept", "text/event-stream")
	} else {
		upstream.Header.Set("Accept", "application/json")
	}
	res, e := g.client.Do(upstream)
	if e != nil {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, responseLimit+1))
	if e != nil || len(body) > responseLimit || g.hasSecret(body) {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	report, e := parseResponse(body, res.Header.Get("Content-Type"), req.Stream, g.cfg.Policy.Model)
	if e != nil {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	// Paid outcomes are recorded independently of downstream cancellation. A DB
	// failure cannot turn a sent request into an automatic retry or refund.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if e = g.budget.Observe(ctx, a.ID, report); e != nil {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	if report.Model != g.cfg.Policy.Model {
		g.unknown(r.Context(), a.ID)
		failure(w, 502, "SUPPLIER_OUTCOME_UNKNOWN")
		return
	}
	w.Header().Set("Content-Type", report.ContentType)
	w.Header().Set("X-Model-Accounting-State", "reported_unsettled")
	w.Write(report.ResponseBody)
}
func (g *gateway) hasSecret(b []byte) bool {
	if g.secretText(string(b)) || g.decodedSecret(b, 0) {
		return true
	}
	// SSE data is JSON too; a literal-byte scan alone misses \uXXXX escapes.
	for _, event := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n\n") {
		var data []string
		for _, line := range strings.Split(event, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if len(data) > 0 && g.decodedSecret([]byte(strings.Join(data, "\n")), 0) {
			return true
		}
	}
	return false
}
func (g *gateway) secretText(s string) bool {
	return strings.Contains(s, g.cfg.SupplierKey) || strings.Contains(s, g.cfg.GatewayToken)
}
func (g *gateway) decodedSecret(raw []byte, depth int) bool {
	if depth > 8 {
		// Do not release an encoded string we could not fully inspect within
		// the bounded scan, even when the visible outer bytes look harmless.
		return true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	for {
		token, e := d.Token()
		if e != nil {
			return false
		}
		if s, ok := token.(string); ok {
			if g.secretText(s) {
				return true
			}
			trimmed := strings.TrimSpace(s)
			if len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"') && g.decodedSecret([]byte(trimmed), depth+1) {
				return true
			}
		}
	}
}
func (g *gateway) unknown(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = g.budget.Unknown(ctx, id)
}

var requestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
