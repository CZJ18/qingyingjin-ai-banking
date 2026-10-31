package coze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	bridge                 *workflowBridge
	protocolTestDispatch   bool
	costCurrency, costUnit string
	base, pat              string
	http                   *http.Client
	meter                  Meter
}

// ConfigureReportedCost declares operator-verified field metadata, not trust in
// reported amounts. Empty metadata explicitly leaves currency/unit unknown.
func (c *Client) ConfigureReportedCost(currency, unit string) error {
	if !ValidCostMetadata(currency, unit) {
		return errors.New("invalid reported cost metadata")
	}
	c.costCurrency, c.costUnit = currency, unit
	return nil
}

type RunRequest struct {
	WorkflowID string
	Parameters map[string]any
	TraceID    string
}
type RunResponse struct {
	Code      *int64 `json:"code"`
	ExecuteID string `json:"execute_id"`
	Data      string `json:"data"`
	Token     *int64 `json:"token,omitempty"`
	Cost      string `json:"cost,omitempty"`
}

func New(base, pat string, timeout time.Duration, policies ...outbound.Policy) (*Client, error) {
	p := outbound.Policy{}
	if len(policies) > 1 {
		return nil, errors.New("invalid transport policy")
	}
	if len(policies) == 1 {
		p = policies[0]
	}
	if e := p.Validate(base); e != nil {
		return nil, e
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(pat) == "" || timeout <= 0 {
		return nil, errors.New("invalid Coze client configuration")
	}
	ip := net.ParseIP(u.Hostname())
	return &Client{protocolTestDispatch: p.Environment == "test" && ip != nil && ip.IsLoopback(), base: strings.TrimRight(base, "/"), pat: pat, http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// RunWorkflow has no automatic retry: retrying a POST can create a duplicate run.
func (c *Client) RunWorkflow(ctx context.Context, in RunRequest) (RunResponse, error) {
	return c.run(ctx, in, true)
}

// RunSyncWorkflow uses the synchronous branch verified in the local Coze
// OpenAPIRun handler. End-node variables are returned as a JSON string in data.
func (c *Client) RunSyncWorkflow(ctx context.Context, in RunRequest) (RunResponse, error) {
	return c.run(ctx, in, false)
}

func (c *Client) run(ctx context.Context, in RunRequest, async bool) (out RunResponse, err error) {
	id, idErr := strconv.ParseInt(in.WorkflowID, 10, 64)
	if idErr != nil || id <= 0 || strconv.FormatInt(id, 10) != in.WorkflowID {
		return out, errors.New("invalid workflow ID")
	}
	if in.TraceID != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`).MatchString(in.TraceID) {
		return out, errors.New("invalid trace ID")
	}
	parameters, err := json.Marshal(in.Parameters)
	if err != nil {
		return out, errors.New("invalid workflow parameters")
	}
	body, err := json.Marshal(struct {
		WorkflowID string `json:"workflow_id"`
		Parameters string `json:"parameters"`
		IsAsync    bool   `json:"is_async"`
	}{in.WorkflowID, string(parameters), async})
	if err != nil {
		return out, err
	}
	if len(body) > 1024*1024 {
		return out, errors.New("workflow request too large")
	}
	// Only reviewed synchronous scopes may use the monetary bridge. Ordinary
	// workflows, async runs and missing runtime proof remain closed before POST.
	if !c.protocolTestDispatch && (c.bridge == nil || async) {
		return out, ErrMonetaryAdmissionRequired
	}
	var capability, graphDigest string
	if !c.protocolTestDispatch {
		grant, grantError := c.bridge.grant(ctx, in.WorkflowID, parameters)
		if grantError != nil {
			return out, grantError
		}
		if e := c.verifyRuntime(ctx, in.WorkflowID, grant); e != nil {
			return out, e
		}
		capability, graphDigest = grant.Capability, grant.WorkflowSHA256
	}
	var reservation string
	identity, _ := ctx.Value(identityKey{}).(modelIdentity)
	settled := false
	if c.meter != nil {
		if identity.owner <= 0 || identity.key == "" {
			return out, ErrBudgetUnavailable
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		var cache []byte
		reservation, cache, err = c.meter.Begin(ctx, identity.owner, identity.key, in.WorkflowID, digest)
		if err != nil {
			return out, err
		}
		if len(cache) > 0 {
			if len(cache) > 1<<20 || json.Unmarshal(cache, &out) != nil || out.Code == nil || *out.Code != 0 || out.ExecuteID == "" || !async && out.Data == "" || out.Token != nil && (*out.Token < 0 || *out.Token > 1_000_000_000_000) || out.Cost != "" && !ValidReportedCost(out.Cost) {
				return RunResponse{}, ErrBudgetUnavailable
			}
			return out, nil
		}
		defer func() {
			if !settled {
				accounting, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				defer stop()
				if e := c.meter.Complete(accounting, identity.owner, reservation, nil, nil, "", false); e != nil {
					slog.Warn("model_unknown_accounting_unavailable", "reservation_id", reservation, "owner_id", identity.owner)
				}
				slog.Warn("model_outcome_unknown_reserved", "reservation_id", reservation, "owner_id", identity.owner)
				// Never expose a partial response or a retryable availability
				// error while persistent accounting still marks this call unknown.
				out = RunResponse{}
				err = errors.Join(ErrOutcomeUnknown, err)
			}
		}()
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/workflow/run", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Content-Type", "application/json")
	if capability != "" {
		req.Header.Set("X-Banking-Model-Capability", capability)
		req.Header.Set("X-Banking-Workflow-SHA256", graphDigest)
	}
	if in.TraceID != "" {
		req.Header.Set("X-Trace-ID", in.TraceID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if c.meter != nil {
			// A dispatched workflow may already have reached its supplier. The
			// deferred accounting retains the hold; expose the same uncertainty
			// to the task and UI rather than a retryable availability error.
			return out, ErrOutcomeUnknown
		}
		return out, errors.New("Coze transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// Correlate an upstream rejection without logging credentials, request
		// parameters or an untrusted error body. Accounting remains unknown.
		slog.Warn("coze_workflow_rejected", "workflow_id", in.WorkflowID, "trace_id", in.TraceID, "reservation_id", reservation, "http_status", resp.StatusCode)
		if c.meter != nil {
			return out, ErrOutcomeUnknown
		}
		return out, fmt.Errorf("Coze HTTP status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return out, errors.New("invalid Coze response size")
	}
	decodeError := json.Unmarshal(raw, &out)
	if recorder, ok := c.meter.(CostReporter); ok {
		report := CostReport{Raw: out.Cost, ExecuteID: out.ExecuteID, Currency: c.costCurrency, Unit: c.costUnit, ResponseDigest: fmt.Sprintf("%x", sha256.Sum256(raw))}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			report.InvalidFormat = true
		} else {
			if value, ok := fields["cost"]; ok && string(value) != "null" {
				if json.Unmarshal(value, &report.Raw) != nil {
					report.Raw = string(value)
					report.InvalidFormat = true
				}
			}
			var tokens int64
			if v, ok := fields["token"]; ok && string(v) != "null" && json.Unmarshal(v, &tokens) == nil {
				report.Tokens = &tokens
			}
		}
		accounting, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		recordError := recorder.RecordReport(accounting, identity.owner, reservation, report)
		stop()
		if recordError != nil {
			return RunResponse{}, ErrBudgetUnavailable
		}
	}
	if decodeError != nil || out.Code == nil {
		return RunResponse{}, errors.New("invalid Coze response")
	}
	if *out.Code != 0 {
		slog.Warn("coze_workflow_rejected", "workflow_id", in.WorkflowID, "trace_id", in.TraceID, "reservation_id", reservation, "http_status", resp.StatusCode, "business_code", *out.Code, "response_sha256", fmt.Sprintf("%x", sha256.Sum256(raw)))
		return RunResponse{}, fmt.Errorf("Coze business code %d", *out.Code)
	}
	if out.ExecuteID == "" {
		return RunResponse{}, errors.New("Coze response missing execute_id")
	}
	if !async && out.Data == "" {
		return RunResponse{}, errors.New("Coze synchronous response missing data")
	}
	if out.Cost != "" && !ValidReportedCost(out.Cost) {
		return RunResponse{}, errors.New("invalid reported Coze cost")
	}
	if out.Token != nil && (*out.Token < 0 || *out.Token > 1_000_000_000_000) || len(out.Cost) > 64 {
		return RunResponse{}, errors.New("invalid Coze usage")
	}
	if c.meter != nil {
		accounting, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		err = c.meter.Complete(accounting, identity.owner, reservation, raw, out.Token, out.Cost, true)
		stop()
		if err != nil {
			slog.Warn("model_settlement_unavailable", "reservation_id", reservation, "owner_id", identity.owner)
			return RunResponse{}, ErrBudgetUnavailable
		}
		settled = true
	}
	return out, nil
}
