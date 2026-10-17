package coze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base, pat string
	http      *http.Client
	meter     Meter
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

func New(base, pat string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(pat) == "" || timeout <= 0 {
		return nil, errors.New("invalid Coze client configuration")
	}
	return &Client{base: strings.TrimRight(base, "/"), pat: pat, http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
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

func (c *Client) run(ctx context.Context, in RunRequest, async bool) (RunResponse, error) {
	var out RunResponse
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
			if len(cache) > 1<<20 || json.Unmarshal(cache, &out) != nil || out.Code == nil || *out.Code != 0 || out.ExecuteID == "" || !async && out.Data == "" || out.Token != nil && (*out.Token < 0 || *out.Token > 1_000_000_000_000) || len(out.Cost) > 64 {
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
			}
		}()
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/workflow/run", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Content-Type", "application/json")
	if in.TraceID != "" {
		req.Header.Set("X-Trace-ID", in.TraceID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, errors.New("Coze transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("Coze HTTP status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return out, errors.New("invalid Coze response size")
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Code == nil {
		return RunResponse{}, errors.New("invalid Coze response")
	}
	if *out.Code != 0 {
		return RunResponse{}, fmt.Errorf("Coze business code %d", *out.Code)
	}
	if out.ExecuteID == "" {
		return RunResponse{}, errors.New("Coze response missing execute_id")
	}
	if !async && out.Data == "" {
		return RunResponse{}, errors.New("Coze synchronous response missing data")
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
