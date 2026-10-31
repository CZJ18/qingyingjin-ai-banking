package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

const productRequestLimit int64 = 1 << 20
const productResponseLimit int64 = 8 << 20

var productSegment = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type productProxy struct {
	next    http.Handler
	auth    *identity.Auth
	origin  *url.URL
	client  *http.Client
	timeout time.Duration
}

// NewProductProxy is opt-in. The origin is administrator configured, never read
// from a request. Python independently authenticates the original user token.
func NewProductProxy(next http.Handler, auth *identity.Auth, origin string, policy outbound.Policy) (http.Handler, error) {
	if err := policy.Validate(origin); err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	transport.DisableCompression = true
	return &productProxy{next: next, auth: auth, origin: u, timeout: 180 * time.Second, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// productMethods enumerates the existing user-facing Python API only. Internal
// context, health endpoints and arbitrary descendants are intentionally absent.
func productMethods(path string) string {
	switch path {
	case "/api/v1/me":
		return "GET"
	case "/api/v1/profile":
		return "GET PUT"
	case "/api/v1/recommendations":
		return "GET"
	case "/api/v1/projections/refresh":
		return "POST"
	case "/api/v1/agent-tasks":
		return "GET POST"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if !strings.HasPrefix(path, "/api/v1/") {
		return ""
	}
	for _, part := range parts {
		if !productSegment.MatchString(part) {
			return ""
		}
	}
	if len(parts) == 2 && parts[0] == "agent-tasks" {
		return "GET"
	}
	if len(parts) == 3 && parts[0] == "agent-tasks" {
		switch parts[2] {
		case "run", "resume", "cancel":
			return "POST"
		case "events":
			return "GET"
		}
	}
	if len(parts) == 4 && parts[0] == "agent-tasks" && parts[2] == "events" && parts[3] == "stream" {
		return "GET"
	}
	if len(parts) == 3 && parts[0] == "recommendations" && parts[2] == "evidence" {
		return "GET"
	}
	if parts[0] == "projections" && (len(parts) == 2 || len(parts) == 3) {
		return "GET"
	}
	return ""
}

func productError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": "product request unavailable", "data": nil})
}

func (p *productProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/product" && !strings.HasPrefix(r.URL.Path, "/product/") {
		p.next.ServeHTTP(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/product")
	methods := productMethods(path)
	if methods == "" || r.URL.RawPath != "" {
		productError(w, 404, "NOT_FOUND")
		return
	}
	if !strings.Contains(" "+methods+" ", " "+r.Method+" ") {
		w.Header().Set("Allow", strings.ReplaceAll(methods, " ", ", "))
		productError(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") || p.auth == nil {
		productError(w, 401, "UNAUTHENTICATED")
		return
	}
	actor, err := p.auth.UserContext(r.Context(), strings.TrimPrefix(raw, "Bearer "))
	if err != nil || actor.Kind != "user" {
		productError(w, 401, "UNAUTHENTICATED")
		return
	}
	// Identity and routing are never taken from user-supplied query parameters.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		productError(w, 400, "INVALID_ARGUMENT")
		return
	}
	for key := range query {
		if key != "after" && key != "limit" && key != "offset" {
			productError(w, 400, "INVALID_ARGUMENT")
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, productRequestLimit+1))
	if err != nil {
		productError(w, 400, "INVALID_ARGUMENT")
		return
	}
	if int64(len(body)) > productRequestLimit {
		productError(w, 413, "REQUEST_TOO_LARGE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()
	target := *p.origin
	target.Path = path
	target.RawPath = ""
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		productError(w, 400, "INVALID_ARGUMENT")
		return
	}
	// Explicit allowlist removes cookies, proxy/identity and service credentials,
	// including Connection-nominated headers supplied by an untrusted browser.
	for _, key := range []string{"Authorization", "Content-Type", "Accept", "Idempotency-Key", "Last-Event-ID", "X-Trace-ID", "X-Task-ID"} {
		if value := r.Header.Get(key); value != "" {
			req.Header.Set(key, value)
		}
	}
	response, err := p.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			productError(w, 504, "UPSTREAM_TIMEOUT")
		} else {
			productError(w, 502, "UPSTREAM_UNAVAILABLE")
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		productError(w, 502, "UPSTREAM_REDIRECT_DENIED")
		return
	}
	stream := response.StatusCode == 200 && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream")
	if !stream {
		data, err := io.ReadAll(io.LimitReader(response.Body, productResponseLimit+1))
		if err != nil || int64(len(data)) > productResponseLimit {
			status := 502
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = 504
			}
			productError(w, status, "UPSTREAM_RESPONSE_INVALID")
			return
		}
		copyProductHeaders(w, response)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		return
	}
	copyProductHeaders(w, response)
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(response.StatusCode)
	var total int64
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		total += int64(n)
		if total > productResponseLimit {
			readErr = errors.New("stream limit")
		}
		if total <= productResponseLimit && n > 0 {
			if _, err = w.Write(buffer[:n]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				_, _ = io.WriteString(w, "\nevent: error\ndata: {\"code\":\"UPSTREAM_STREAM_FAILED\"}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			return
		}
	}
}

func copyProductHeaders(w http.ResponseWriter, response *http.Response) {
	for _, key := range []string{"Content-Type", "X-Request-ID", "X-Trace-ID", "Retry-After"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
}
