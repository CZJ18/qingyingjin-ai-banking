package modelspend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

var ErrApproval = errors.New("MODEL_APPROVAL_REQUIRED")
var ErrApprovalQuota = errors.New("MODEL_APPROVAL_LIMIT_EXCEEDED")

// Approval is an operator-supplied, newly approved run scope, never generated
// from prior policies or accepted from the model/HTTP request body.
type Approval struct {
	IntegrationMode     string            `json:"integration_mode,omitempty"`
	DirectRequestSHA256 string            `json:"direct_request_sha256,omitempty"`
	ID                  string            `json:"approval_id"`
	Simulation          bool              `json:"simulation,omitempty"`
	RunID               string            `json:"run_id"`
	ProviderOrigin      string            `json:"provider_origin"`
	Model               string            `json:"model"`
	ProjectID           string            `json:"project_id"`
	Database            string            `json:"database"`
	Currency            string            `json:"currency"`
	Amount              string            `json:"budget_amount"`
	PolicySHA256        string            `json:"policy_sha256"`
	Scope               string            `json:"scope"`
	UserID              int64             `json:"user_id"`
	TaskRequestSHA256   string            `json:"task_request_sha256"`
	WorkflowID          string            `json:"workflow_id"`
	WorkflowSHA256      string            `json:"workflow_sha256"`
	AdditionalWorkflows map[string]string `json:"additional_workflows,omitempty"`
	ValidFrom           string            `json:"valid_from"`
	ValidUntil          string            `json:"valid_until"`
	MaxRequests         int64             `json:"max_requests"`
	MaxConcurrent       int64             `json:"max_concurrent"`
	ContextTokens       int64             `json:"context_tokens"`
	MaxOutputTokens     int64             `json:"max_output_tokens"`
}

func (a Approval) Check(runID string, now time.Time) error {
	originOK := a.ProviderOrigin == "https://api.deepseek.com/v1"
	if a.Simulation {
		u, err := url.Parse(a.ProviderOrigin)
		originOK = err == nil && u.Scheme == "http" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && u.Path == "/v1" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() && strings.HasSuffix(a.Database, "_test") && strings.HasPrefix(a.Model, "synthetic-")
	}
	if len(a.AdditionalWorkflows) > 31 {
		return ErrApproval
	}
	for id, digest := range a.AdditionalWorkflows {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id || id == a.WorkflowID || !digestValid(digest) {
			return ErrApproval
		}
	}
	workflow, workflowError := strconv.ParseInt(a.WorkflowID, 10, 64)
	workflowOK := workflowError == nil && workflow > 0 && strconv.FormatInt(workflow, 10) == a.WorkflowID && digestValid(a.WorkflowSHA256) && a.DirectRequestSHA256 == ""
	if a.TransportMode() == "direct_provider" {
		workflowOK = a.WorkflowID == "" && a.WorkflowSHA256 == "" && len(a.AdditionalWorkflows) == 0 && digestValid(a.DirectRequestSHA256)
	}
	from, e1 := time.Parse(time.RFC3339, a.ValidFrom)
	until, e2 := time.Parse(time.RFC3339, a.ValidUntil)
	amount, e3 := ParseMoney(a.Amount)
	if !identifier.MatchString(a.ID) || !identifier.MatchString(runID) || a.RunID != runID || !originOK ||
		!modelName.MatchString(a.Model) || !identifier.MatchString(a.ProjectID) || !identifier.MatchString(a.Database) || len(strings.TrimSpace(a.Scope)) == 0 || len(a.Scope) > 1024 || len(a.Currency) != 3 || strings.Trim(a.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" ||
		a.UserID <= 0 || !digestValid(a.PolicySHA256) || !digestValid(a.TaskRequestSHA256) || !workflowOK || (a.TransportMode() != "coze" && a.TransportMode() != "direct_provider") ||
		e1 != nil || e2 != nil || !from.Before(until) || now.Before(from) || !now.Before(until) || e3 != nil || amount <= 0 ||
		a.MaxRequests < 1 || a.MaxConcurrent < 1 || a.MaxConcurrent > a.MaxRequests || a.ContextTokens < 1 || a.MaxOutputTokens < 1 || a.MaxOutputTokens > a.ContextTokens {
		return ErrApproval
	}
	return nil
}

func (a Approval) TransportMode() string {
	if a.IntegrationMode == "" {
		return "coze"
	}
	return a.IntegrationMode
}

// Additional workflows are explicit approval entries and share all run limits.
func (a Approval) AllowsWorkflow(id, digest string) bool {
	if a.TransportMode() == "direct_provider" {
		return id == "direct_provider" && digest == a.DirectRequestSHA256
	}
	if id == a.WorkflowID {
		return digest == a.WorkflowSHA256
	}
	approved, ok := a.AdditionalWorkflows[id]
	return ok && approved == digest
}

func (a Approval) Validate(p Policy, runID, database string, now time.Time) error {
	hash, err := p.Hash()
	cap, e := ParseMoney(a.Amount)
	policyCap, _ := ParseMoney(p.Limit)
	bound, _ := p.ReserveUnits()
	if a.Check(runID, now) != nil || err != nil || e != nil || p.Validate(now) != nil || a.PolicySHA256 != hash || a.Model != p.Model || a.ProjectID != p.ProjectID ||
		a.Simulation && !strings.HasPrefix(p.PriceSource, "synthetic:") ||
		a.Database != database || a.Currency != p.Currency || cap > policyCap || cap < bound || a.ContextTokens != p.ContextTokens || a.MaxOutputTokens != p.MaxOutputTokens {
		return ErrApproval
	}
	return nil
}

func (a Approval) Fingerprint() string {
	raw, _ := json.Marshal(a)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Only explicitly named files are read. Missing approval is handled before
// reading the gateway environment (which can contain a supplier credential).
func ReadApproval(path, expectedSHA256, runID string, now time.Time) (Approval, error) {
	var a Approval
	if path == "" || !digestValid(expectedSHA256) || !identifier.MatchString(runID) {
		return a, ErrApproval
	}
	f, err := os.Open(path)
	if err != nil {
		return a, ErrApproval
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(raw) == 0 || len(raw) > 16384 {
		return a, ErrApproval
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != expectedSHA256 || StrictJSON(raw, &a, 16384) != nil || a.Check(runID, now) != nil {
		return Approval{}, ErrApproval
	}
	return a, nil
}

func (s *Service) UseApproval(a Approval, runID, database string) error {
	if !s.ready() || a.Validate(s.policy, runID, database, time.Now()) != nil {
		return ErrApproval
	}
	s.approval = &a
	return nil
}

func (s *Service) ApprovalFingerprint() string {
	if s == nil || s.approval == nil {
		return ""
	}
	return s.approval.Fingerprint()
}

type ApprovalWindow struct {
	ApprovalSHA256      string `json:"approval_sha256"`
	Currency            string `json:"currency"`
	MaxRequestRisk      string `json:"next_request_maximum_cost"`
	RemainingBudget     string `json:"remaining_approval_budget"`
	RemainingRequests   int64  `json:"remaining_requests"`
	RemainingConcurrent int64  `json:"remaining_concurrent_slots"`
	SnapshotOnly        bool   `json:"snapshot_only_not_a_reservation"`
}

// approvalWindow is read-only. Reserve calls it under the existing project row
// lock so every gateway process shares the same limits. Unknown outcomes keep
// both their request slot and concurrency slot until explicitly reconciled.
func (s *Service) approvalWindow(tx *gorm.DB) (ApprovalWindow, error) {
	var w ApprovalWindow
	if s.approval == nil {
		return w, ErrApproval
	}
	a := s.approval
	if a.Validate(s.policy, a.RunID, a.Database, time.Now()) != nil {
		return w, ErrApproval
	}
	var changed int64
	if e := tx.Model(&Event{}).Where("project_id=? AND dedupe_key=? AND evidence_digest<>?", a.ProjectID, approvalBindingKey(*a), a.Fingerprint()).Count(&changed).Error; e != nil {
		return w, ledgerError(e)
	}
	if changed != 0 {
		return w, ErrConflict
	}
	var rows []struct {
		Status      string
		AmountUnits int64
	}
	if e := tx.Table("banking_model_spend_events AS e").Select("a.status,e.amount_units").Joins("JOIN banking_model_spend_attempts AS a ON a.id=e.attempt_id AND a.project_id=e.project_id").
		Where("e.project_id=? AND e.kind='reserve' AND e.evidence_digest=?", a.ProjectID, a.Fingerprint()).Scan(&rows).Error; e != nil {
		return w, ledgerError(e)
	}
	amount, _ := ParseMoney(a.Amount)
	remaining := amount
	concurrent := a.MaxConcurrent
	for _, row := range rows {
		if row.AmountUnits <= 0 || row.AmountUnits > remaining {
			return w, ErrApprovalQuota
		}
		remaining -= row.AmountUnits
		if row.Status == "reserved" || row.Status == "unknown" {
			concurrent--
		}
	}
	w = ApprovalWindow{a.Fingerprint(), a.Currency, FormatMoney(s.reserveUnits), FormatMoney(remaining), a.MaxRequests - int64(len(rows)), concurrent, true}
	if w.RemainingRequests < 0 || concurrent < 0 {
		return w, ErrApprovalQuota
	}
	return w, nil
}

func approvalBindingKey(a Approval) string {
	return hashText("approval_binding\x00" + a.ProjectID + "\x00" + a.ID)
}

func (s *Service) ApprovalBudget(ctx context.Context) (ApprovalWindow, error) {
	if !s.ready() {
		return ApprovalWindow{}, ErrUnavailable
	}
	return s.approvalWindow(s.db.WithContext(ctx))
}

func (s *Service) checkApprovalContext(ctx context.Context) error {
	if s.approval == nil {
		return nil
	} // Legacy/test ledger use; paid gateway requires an approved ledger.
	a := s.approval
	b := workflowBindingFrom(ctx)
	origin, _ := ctx.Value(providerKey{}).(string)
	if a.Validate(s.policy, a.RunID, a.Database, time.Now()) != nil || origin != a.ProviderOrigin || userFrom(ctx) != a.UserID || b == nil ||
		b.TaskRequestDigest != a.TaskRequestSHA256 || !a.AllowsWorkflow(b.WorkflowID, b.WorkflowSHA256) || b.PolicySHA256 != a.PolicySHA256 ||
		b.Model != a.Model || b.ContextTokens != a.ContextTokens || b.MaxOutputTokens != a.MaxOutputTokens {
		return ErrApproval
	}
	return nil
}
