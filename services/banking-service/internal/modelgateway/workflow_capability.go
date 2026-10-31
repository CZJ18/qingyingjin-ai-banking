// Scoped workflow capabilities carry no plaintext Banking credential to Coze.
package modelgateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

type TaskIdentity struct {
	RequestDigest    string
	Version          int64
	LeaseFingerprint string
	UserID           int64
	TaskID           string
}
type TaskIdentityVerifier func(context.Context, string) (TaskIdentity, error)

type workflowCapability struct {
	TaskRequestDigest string `json:"task_request_digest"`
	TaskVersion       int64  `json:"task_version"`
	LeaseFingerprint  string `json:"lease_fingerprint"`
	UserID            int64  `json:"user_id"`
	TaskID            string `json:"task_id"`
	WorkflowID        string `json:"workflow_id"`
	WorkflowSHA256    string `json:"workflow_sha256"`
	InputSHA256       string `json:"input_sha256"`
	PolicySHA256      string `json:"policy_sha256"`
	Model             string `json:"model"`
	PriceVersion      string `json:"price_version"`
	ContextTokens     int64  `json:"context_tokens"`
	MaxOutputTokens   int64  `json:"max_output_tokens"`
	ReserveUnits      int64  `json:"reserve_units"`
	ExpiresAt         int64  `json:"expires_at"`
	// Encrypted authenticated payload, never logged or persisted as audit fields.
	Credential string `json:"credential"`
}

func capCipher(secret string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("banking/workflow-capability/v1\x00" + secret))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func sealCapability(secret string, c workflowCapability) (string, error) {
	a, e := capCipher(secret)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	return "bgc1." + base64.RawURLEncoding.EncodeToString(a.Seal(nonce, nonce, raw, []byte("workflow-capability-v1"))), nil
}
func openCapability(secret, token string) (workflowCapability, error) {
	var c workflowCapability
	denied := errors.New("workflow capability invalid")
	if !strings.HasPrefix(token, "bgc1.") || len(token) > 16384 {
		return c, denied
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "bgc1."))
	if e != nil {
		return c, denied
	}
	a, e := capCipher(secret)
	if e != nil || len(raw) < a.NonceSize()+a.Overhead() {
		return c, denied
	}
	plain, e := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte("workflow-capability-v1"))
	if e != nil || modelspend.StrictJSON(plain, &c, 16384) != nil {
		return c, denied
	}
	return c, nil
}
func hexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, e := hex.DecodeString(value)
	return e == nil && value == strings.ToLower(value)
}
func (g *gateway) mintWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		failure(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	if g.cfg.VerifyTask == nil || len(g.cfg.WorkflowPolicies) == 0 {
		failure(w, 503, "WORKFLOW_BRIDGE_UNAVAILABLE")
		return
	}
	if g.cfg.Policy.Validate(time.Now()) != nil {
		failure(w, 503, "POLICY_UNAVAILABLE")
		return
	}
	if len(r.Header.Values("X-Banking-Authorization")) != 1 {
		failure(w, 401, "TASK_IDENTITY_REQUIRED")
		return
	}
	credential := r.Header.Get("X-Banking-Authorization")
	actor, e := g.cfg.VerifyTask(r.Context(), credential)
	if e != nil || actor.UserID <= 0 || !requestID.MatchString(actor.TaskID) || actor.Version <= 0 || !hexDigest(actor.RequestDigest) || !hexDigest(actor.LeaseFingerprint) {
		failure(w, 401, "TASK_IDENTITY_INVALID")
		return
	}
	var in struct {
		WorkflowID  string `json:"workflow_id"`
		InputSHA256 string `json:"input_sha256"`
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 4097))
	if e != nil || modelspend.StrictJSON(raw, &in, 4096) != nil || !hexDigest(in.InputSHA256) {
		failure(w, 400, "INVALID_WORKFLOW_GRANT")
		return
	}
	graph, ok := g.cfg.WorkflowPolicies[in.WorkflowID]
	if !ok {
		failure(w, 403, "WORKFLOW_NOT_REVIEWED")
		return
	}
	if approval := g.cfg.Approval; approval != nil {
		if approval.Check(g.cfg.RunID, time.Now()) != nil || actor.UserID != approval.UserID || actor.RequestDigest != approval.TaskRequestSHA256 || !approval.AllowsWorkflow(in.WorkflowID, graph) {
			failure(w, 403, "MODEL_APPROVAL_REQUIRED")
			return
		}
		reader, ok := g.budget.(interface {
			ApprovalBudget(context.Context) (modelspend.ApprovalWindow, error)
		})
		if !ok {
			failure(w, 503, "BUDGET_UNAVAILABLE")
			return
		}
		window, e := reader.ApprovalBudget(r.Context())
		remaining, _ := modelspend.ParseMoney(window.RemainingBudget)
		bound, _ := g.cfg.Policy.ReserveUnits()
		if e != nil || window.ApprovalSHA256 != approval.Fingerprint() {
			failure(w, 503, "BUDGET_UNAVAILABLE")
			return
		}
		if window.RemainingRequests < 1 || window.RemainingConcurrent < 1 || remaining < bound {
			failure(w, 429, "MODEL_APPROVAL_LIMIT_EXCEEDED")
			return
		}
	}
	bound, e := g.cfg.Policy.ReserveUnits()
	if e != nil {
		failure(w, 503, "POLICY_UNAVAILABLE")
		return
	}
	reader, ok := g.budget.(interface {
		UserBudget(context.Context, int64) (modelspend.UserWindow, error)
		ProjectBudget(context.Context) (modelspend.Pool, error)
	})
	if !ok {
		failure(w, 503, "BUDGET_UNAVAILABLE")
		return
	}
	project, e := reader.ProjectBudget(r.Context())
	limit, _ := modelspend.ParseMoney(g.cfg.Policy.Limit)
	if e != nil || project.ProjectID != g.cfg.Policy.ProjectID || project.PolicyHash != g.policyHash || project.Currency != g.cfg.Policy.Currency || project.LimitUnits != limit || project.UsedUnits < 0 || project.HeldUnits < 0 {
		failure(w, 503, "BUDGET_UNAVAILABLE")
		return
	}
	if project.Frozen || project.UsedUnits > project.LimitUnits || project.HeldUnits > project.LimitUnits-project.UsedUnits || bound > project.LimitUnits-project.UsedUnits-project.HeldUnits {
		failure(w, 429, "MODEL_SPEND_QUOTA_EXCEEDED")
		return
	}
	window, e := reader.UserBudget(r.Context(), actor.UserID)
	if e != nil {
		failure(w, 503, "BUDGET_UNAVAILABLE")
		return
	}
	if window.Frozen || window.UsedUnits < 0 || window.HeldUnits < 0 || window.LimitUnits < window.UsedUnits || window.HeldUnits > window.LimitUnits-window.UsedUnits || bound > window.LimitUnits-window.UsedUnits-window.HeldUnits {
		failure(w, 429, "USER_MONTHLY_BUDGET_EXCEEDED")
		return
	}
	// This is only a fail-early check; Reserve still commits the atomic hold.
	c := workflowCapability{TaskRequestDigest: actor.RequestDigest, TaskVersion: actor.Version, LeaseFingerprint: actor.LeaseFingerprint, UserID: actor.UserID, TaskID: actor.TaskID, WorkflowID: in.WorkflowID, WorkflowSHA256: graph, InputSHA256: in.InputSHA256, PolicySHA256: g.policyHash, Model: g.cfg.Policy.Model, PriceVersion: g.cfg.Policy.PriceVersion, ContextTokens: g.cfg.Policy.ContextTokens, MaxOutputTokens: g.cfg.Policy.MaxOutputTokens, ReserveUnits: bound, ExpiresAt: time.Now().Add(90 * time.Second).Unix(), Credential: credential}
	cap, e := sealCapability(g.cfg.GatewayToken, c)
	if e != nil {
		failure(w, 503, "WORKFLOW_BRIDGE_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"capability": cap, "workflow_sha256": graph, "expires_at": c.ExpiresAt, "model": c.Model, "price_version": c.PriceVersion, "policy_sha256": c.PolicySHA256, "context_tokens": c.ContextTokens, "max_output_tokens": c.MaxOutputTokens, "reserved_upper_bound_units": c.ReserveUnits, "admission_state": "requires_atomic_model_reservation"})
}
func (g *gateway) verifyWorkflow(ctx context.Context, token string) (workflowCapability, error) {
	c, e := openCapability(g.cfg.GatewayToken, token)
	denied := errors.New("workflow capability invalid")
	if e != nil || g.cfg.VerifyTask == nil || c.UserID <= 0 || !requestID.MatchString(c.TaskID) || !hexDigest(c.InputSHA256) || c.ExpiresAt <= time.Now().Unix() || c.ExpiresAt > time.Now().Add(90*time.Second).Unix() || c.PolicySHA256 != g.policyHash || c.Model != g.cfg.Policy.Model || c.PriceVersion != g.cfg.Policy.PriceVersion || c.ContextTokens != g.cfg.Policy.ContextTokens || c.MaxOutputTokens != g.cfg.Policy.MaxOutputTokens {
		return c, denied
	}
	graph, ok := g.cfg.WorkflowPolicies[c.WorkflowID]
	if !ok || graph != c.WorkflowSHA256 {
		return c, denied
	}
	bound, e := g.cfg.Policy.ReserveUnits()
	if e != nil || bound != c.ReserveUnits {
		return c, denied
	}
	actor, e := g.cfg.VerifyTask(ctx, c.Credential)
	if e != nil || actor.UserID != c.UserID || actor.TaskID != c.TaskID || actor.RequestDigest != c.TaskRequestDigest || actor.Version != c.TaskVersion || actor.LeaseFingerprint != c.LeaseFingerprint {
		return c, denied
	}
	return c, nil
}
func (c workflowCapability) binding() modelspend.WorkflowBinding {
	return modelspend.WorkflowBinding{TaskRequestDigest: c.TaskRequestDigest, TaskVersion: c.TaskVersion, LeaseFingerprint: c.LeaseFingerprint, TaskID: c.TaskID, WorkflowID: c.WorkflowID, WorkflowSHA256: c.WorkflowSHA256, InputSHA256: c.InputSHA256, PolicySHA256: c.PolicySHA256, Model: c.Model, PriceVersion: c.PriceVersion, ContextTokens: c.ContextTokens, MaxOutputTokens: c.MaxOutputTokens, ReserveUnits: c.ReserveUnits}
}
