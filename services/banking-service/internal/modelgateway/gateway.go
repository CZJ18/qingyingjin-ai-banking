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
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
		if g.cfg.Policy.Validate(time.Now()) != nil {
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
	if !g.authorized(r) {
		failure(w, 401, "UNAUTHORIZED")
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "INVALID_REQUEST")
		return
	}
	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		if g.cfg.Policy.Validate(time.Now()) != nil {
			failure(w, 503, "POLICY_UNAVAILABLE")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": g.cfg.Policy.Model, "object": "model", "owned_by": "configured-project"}}, "model_spend_enforcement": map[string]any{"mode": "conservative_project_lifetime", "project_id": g.cfg.Policy.ProjectID, "policy_sha256": g.policyHash, "max_output_tokens": g.cfg.Policy.MaxOutputTokens}})
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
		failure(w, 400, "INVALID_REQUEST")
		return
	}
	key := r.Header.Get("X-Model-Request-ID")
	if len(r.Header.Values("X-Model-Request-ID")) > 1 || (key != "" && !requestID.MatchString(key)) {
		failure(w, 400, "INVALID_REQUEST_ID")
		return
	}
	if key == "" {
		key = uuid.NewString()
	}
	// Hash the exact caller bytes. Reusing a key with changed parameters or even a
	// changed serialization never authorizes another paid attempt.
	digest := sha256.Sum256(raw)
	a, e := g.budget.Reserve(r.Context(), key, hex.EncodeToString(digest[:]))
	if e != nil {
		switch {
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
