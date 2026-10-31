package bankingscope

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type gatewayTransport struct {
	ctx       context.Context
	config    GatewayConfig
	transport *http.Transport
}

// NewClient has no environment proxy, redirect, supplier key or SDK fallback.
func NewClient(ctx context.Context) (*http.Client, error) {
	cfg, err := Gateway(ctx)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Timeout: 90 * time.Second, Transport: &gatewayTransport{ctx, cfg, transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrDenied }}, nil
}

func (t *gatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "POST" || req.URL.Scheme+"://"+req.URL.Host != t.config.Origin || req.URL.Path != "/v1/chat/completions" || req.URL.RawQuery != "" || req.URL.User != nil {
		return nil, ErrDenied
	}
	if req.Body == nil {
		return nil, ErrDenied
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1048577))
	req.Body.Close()
	if err != nil || len(body) > 1048576 {
		return nil, ErrDenied
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return nil, ErrDenied
	}
	var model string
	var max int
	var stream bool
	if _, a := payload["max_tokens"]; a {
		if _, b := payload["max_completion_tokens"]; b {
			return nil, ErrDenied
		}
	}
	if json.Unmarshal(payload["model"], &model) != nil || model != t.config.Model {
		return nil, ErrDenied
	}
	if json.Unmarshal(payload["max_tokens"], &max) != nil {
		if json.Unmarshal(payload["max_completion_tokens"], &max) != nil {
			return nil, ErrDenied
		}
	}
	if max < 1 || max > t.config.MaxOutputTokens {
		return nil, ErrDenied
	}
	if raw, ok := payload["stream"]; ok {
		if json.Unmarshal(raw, &stream) != nil || stream {
			return nil, ErrDenied
		}
	}
	for _, k := range []string{"tools", "tool_choice", "functions", "function_call", "n"} {
		if _, ok := payload[k]; ok {
			return nil, ErrDenied
		}
	}
	// DeepSeek enables thinking by default. This bounded JSON planning/explanation
	// path must not spend its reviewed output allowance on implicit reasoning.
	// Coze SDK options cannot enable a different mode or change the JSON contract.
	if t.config.Model == "deepseek-flash" {
		if _, ok := payload["reasoning_effort"]; ok {
			return nil, ErrDenied
		}
		for field, expected := range map[string]string{"thinking": "disabled", "response_format": "json_object"} {
			if raw, exists := payload[field]; exists {
				var control map[string]string
				if json.Unmarshal(raw, &control) != nil || len(control) != 1 || control["type"] != expected {
					return nil, ErrDenied
				}
			}
			raw, _ := json.Marshal(map[string]string{"type": expected})
			payload[field] = raw
		}
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, ErrDenied
		}
	}
	if err := ClaimCall(t.ctx); err != nil {
		return nil, err
	}
	copy := req.Clone(t.ctx)
	copy.Body = io.NopCloser(bytes.NewReader(body))
	copy.ContentLength = int64(len(body))
	copy.TransferEncoding = nil
	copy.GetBody = nil
	copy.Header = make(http.Header)
	copy.Header.Set("Content-Type", "application/json")
	copy.Header.Set("Authorization", "Bearer "+t.config.Capability)
	// Suppress net/http's automatic retry of an idempotent replayable request.
	return t.transport.RoundTrip(copy)
}
