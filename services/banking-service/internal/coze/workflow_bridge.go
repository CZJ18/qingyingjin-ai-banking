package coze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type delegationKey struct{}

// WithDelegation carries only a Go-issued task specialist token, never a front-end user ID.
func WithDelegation(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, delegationKey{}, token)
}

type workflowBridge struct {
	base, token string
	client      *http.Client
}

// ConfigureWorkflowBridge does not authorize a workflow. The gateway must still
// admit its reviewed graph, authenticated task and monetary policy on each run.
func (c *Client) ConfigureWorkflowBridge(base, token string, p outbound.Policy) error {
	u, e := url.Parse(base)
	if e != nil || p.Validate(base) != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \r\n\t") || c.meter == nil {
		return ErrMonetaryAdmissionRequired
	}
	c.bridge = &workflowBridge{base: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return nil
}

type workflowGrant struct {
	Capability      string `json:"capability"`
	WorkflowSHA256  string `json:"workflow_sha256"`
	ExpiresAt       int64  `json:"expires_at"`
	Model           string `json:"model"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
}

func (b *workflowBridge) grant(ctx context.Context, workflow string, parameters []byte) (workflowGrant, error) {
	var grant workflowGrant
	credential, _ := ctx.Value(delegationKey{}).(string)
	if credential == "" || len(credential) > 8192 || strings.ContainsAny(credential, " \r\n\t") {
		return grant, ErrMonetaryAdmissionRequired
	}
	sum := sha256.Sum256(parameters)
	raw, _ := json.Marshal(map[string]string{"workflow_id": workflow, "input_sha256": hex.EncodeToString(sum[:])})
	req, e := http.NewRequestWithContext(ctx, "POST", b.base+"/v1/workflow-grants", bytes.NewReader(raw))
	if e != nil {
		return grant, ErrMonetaryAdmissionRequired
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("X-Banking-Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	res, e := b.client.Do(req)
	if e != nil {
		// This endpoint only mints a capability. No workflow POST, daily
		// reservation or supplier dispatch has occurred, even if its response
		// was lost. Preserve availability instead of inventing model uncertainty.
		return grant, ErrBudgetUnavailable
	}
	defer res.Body.Close()
	data, e := io.ReadAll(io.LimitReader(res.Body, 32769))
	if e != nil || len(data) > 32768 {
		return grant, ErrMonetaryAdmissionRequired
	}
	if res.StatusCode == http.StatusServiceUnavailable && gatewayQuotaCode(data) == "MODEL_EXECUTION_CONFIRMATION_REQUIRED" {
		return grant, ErrExecutionConfirmationRequired
	}
	if res.StatusCode == http.StatusServiceUnavailable {
		switch gatewayQuotaCode(data) {
		case "MODEL_BUDGET_UNAVAILABLE", "BUDGET_UNAVAILABLE":
			return grant, ErrBudgetUnavailable
		case "POLICY_UNAVAILABLE":
			return grant, ErrApprovalRequired
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		switch gatewayQuotaCode(data) {
		case "MODEL_APPROVAL_LIMIT_EXCEEDED":
			return grant, ErrApprovalLimit
		case "USER_MONTHLY_BUDGET_EXCEEDED":
			return grant, ErrMonthlyQuotaExceeded
		case "MODEL_SPEND_QUOTA_EXCEEDED":
			return grant, ErrSpendQuotaExceeded
		}
		return grant, ErrMonetaryAdmissionRequired
	}
	if res.StatusCode == http.StatusForbidden && gatewayQuotaCode(data) == "MODEL_APPROVAL_REQUIRED" {
		return grant, ErrApprovalRequired
	}
	if res.StatusCode != 200 {
		return grant, ErrMonetaryAdmissionRequired
	}
	if json.Unmarshal(data, &grant) != nil || !strings.HasPrefix(grant.Capability, "bgc1.") || len(grant.Capability) > 16384 || len(grant.WorkflowSHA256) != 64 || grant.ExpiresAt <= time.Now().Unix() || grant.Model == "" || grant.MaxOutputTokens < 1 {
		return workflowGrant{}, ErrMonetaryAdmissionRequired
	}
	if _, e = hex.DecodeString(grant.WorkflowSHA256); e != nil {
		return workflowGrant{}, ErrMonetaryAdmissionRequired
	}
	return grant, nil
}

// Parse only the gateway's small error envelope, rejecting duplicate keys,
// unknown fields, non-string values and trailing JSON before classifying it.
func gatewayQuotaCode(raw []byte) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	expect := func(want any) bool { got, err := d.Token(); return err == nil && got == want }
	if !expect(json.Delim('{')) || !expect("error") || !expect(json.Delim('{')) {
		return ""
	}
	seen := map[string]bool{}
	code := ""
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || (name != "code" && name != "message" && name != "type") {
			return ""
		}
		seen[name] = true
		value, err := d.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return ""
		}
		if name == "code" {
			code = text
		}
	}
	if !expect(json.Delim('}')) || !expect(json.Delim('}')) {
		return ""
	}
	if _, err := d.Token(); err != io.EOF {
		return ""
	}
	return code
}

// A configured URL cannot opt an unpatched Coze runtime into paid dispatch.
// This read-only proof precedes workflow POST and daily/money reservations.
func (c *Client) verifyRuntime(ctx context.Context, workflow string, grant workflowGrant) error {
	req, e := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/banking/model-scope", nil)
	if e != nil {
		return ErrMonetaryAdmissionRequired
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	res, e := c.http.Do(req)
	if e != nil {
		return ErrMonetaryAdmissionRequired
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || res.StatusCode != 200 || len(raw) > 65536 {
		return ErrMonetaryAdmissionRequired
	}
	var view struct {
		Mode            string            `json:"mode"`
		WorkflowPins    map[string]string `json:"workflow_pins"`
		GatewayOrigin   string            `json:"gateway_origin"`
		Model           string            `json:"model"`
		MaxOutputTokens int64             `json:"max_output_tokens"`
	}
	if json.Unmarshal(raw, &view) != nil || view.Mode != "banking-task-scope-v1" || view.WorkflowPins[workflow] != grant.WorkflowSHA256 || view.GatewayOrigin != c.bridge.base || view.Model != grant.Model || view.MaxOutputTokens != grant.MaxOutputTokens {
		return ErrMonetaryAdmissionRequired
	}
	return nil
}

func HasDelegation(ctx context.Context) bool {
	token, _ := ctx.Value(delegationKey{}).(string)
	return token != ""
}
