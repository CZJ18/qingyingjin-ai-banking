package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

const token = "synthetic-gateway-credential-000000000000"
const supplier = "synthetic-supplier-credential-00000000000"
const validBody = `{"model":"synthetic-model","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`
const validResponse = `{"id":"provider-1","model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":2}}`

type memoryBudget struct {
	mu         sync.Mutex
	attempts   map[string]modelspend.Attempt
	reports    []modelspend.Report
	unknown    int
	limit      int
	observeErr error
}

func (b *memoryBudget) Reserve(_ context.Context, key, digest string) (modelspend.Attempt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.attempts[key]; ok {
		if a.RequestHash != digest {
			return a, modelspend.ErrConflict
		}
		if a.ResponseBody == nil {
			return a, modelspend.ErrUnknown
		}
		return a, nil
	}
	if b.limit > 0 && len(b.attempts) >= b.limit {
		return modelspend.Attempt{}, modelspend.ErrQuota
	}
	a := modelspend.Attempt{ID: key, ProjectID: "project", RequestKey: key, RequestHash: digest, Status: "reserved"}
	b.attempts[key] = a
	return a, nil
}
func (b *memoryBudget) Observe(_ context.Context, id string, r modelspend.Report) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reports = append(b.reports, r)
	if b.observeErr != nil {
		return b.observeErr
	}
	a := b.attempts[id]
	v := string(r.ResponseBody)
	a.ResponseBody = &v
	a.ContentType = r.ContentType
	a.Status = "observed"
	b.attempts[id] = a
	return nil
}
func (b *memoryBudget) Unknown(_ context.Context, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unknown++
	return nil
}
func policy() modelspend.Policy {
	return modelspend.Policy{ProjectID: "project", Currency: "CNY", Model: "synthetic-model", PriceVersion: "test-v1", PriceSource: "synthetic:test", ValidFrom: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), PriceEvidenceSHA256: strings.Repeat("a", 64), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Limit: "100", InputPrice: "2", CacheHitPrice: "1", OutputPrice: "3", ContextTokens: 100, MaxOutputTokens: 8}
}
func setup(t *testing.T, provider http.HandlerFunc, b *memoryBudget) (http.Handler, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(provider)
	t.Cleanup(s.Close)
	if b.attempts == nil {
		b.attempts = map[string]modelspend.Attempt{}
	}
	h, e := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, BaseURL: s.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, b)
	if e != nil {
		t.Fatal(e)
	}
	return h, s
}
func call(h http.Handler, body, key, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", auth)
	if key != "" {
		r.Header.Set("X-Model-Request-ID", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthenticatedDispatchRecordsUsageAndNeverForwardsGatewayCredential(t *testing.T) {
	b := &memoryBudget{}
	var paid atomic.Int64
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+supplier {
			t.Errorf("supplier auth not replaced")
		}
		if r.Header.Get("X-Model-Request-ID") != "" {
			t.Errorf("private client headers forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	w := call(h, validBody, "request-1", "Bearer "+token)
	if w.Code != 200 || w.Body.String() != validResponse {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if paid.Load() != 1 || len(b.reports) != 1 || b.reports[0].Usage.TotalTokens != 3 {
		t.Fatal("not observed")
	}
	if strings.Contains(w.Body.String(), supplier) || strings.Contains(w.Header().Get("Authorization"), supplier) {
		t.Fatal("credential leaked")
	}
}
func TestUnauthorizedRequestsDoNotReserveOrDispatch(t *testing.T) {
	b := &memoryBudget{}
	var paid atomic.Int64
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { paid.Add(1) }, b)
	for _, auth := range []string{"", "Bearer wrong", supplier, "Bearer " + supplier} {
		w := call(h, validBody, "", auth)
		if w.Code != 401 {
			t.Errorf("status=%d", w.Code)
		}
	}
	if paid.Load() != 0 || len(b.attempts) != 0 {
		t.Fatal("unauthorized paid dispatch")
	}
}
func TestModelsAreAuthenticatedAndLocal(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("models contacted provider") }, b)
	for _, auth := range []string{"", "Bearer " + token} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if auth != "" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
		if want == 200 && !strings.Contains(w.Body.String(), `"id":"synthetic-model"`) {
			t.Fatal(w.Body.String())
		}
	}
}
func TestRejectsUnsupportedOrUnboundedRequestsBeforeReserve(t *testing.T) {
	bad := []string{
		`{"model":"other","messages":[{"role":"user","content":"x"}],"max_tokens":8}`,
		`{"model":"synthetic-model","messages":[{"role":"user","content":"x"}]}`,
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":9`, 1),
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":0`, 1),
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"n":2`, 1),
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"search":true`, 1),
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"enable_thinking":true`, 1),
		strings.Replace(validBody, `"content":"hello"`, `"content":[{"type":"image_url","image_url":{"url":"https://example.com/a"}}]`, 1),
		strings.Replace(validBody, `"role":"user"`, `"role":"developer"`, 1),
		strings.Replace(validBody, `"model":"synthetic-model"`, `"model":"wrong","model":"synthetic-model"`, 1),
		validBody + ` {}`, `null`, strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"tools":[{"type":"web_search"}]`, 1),
	}
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("rejected request dispatched") }, b)
	for i, body := range bad {
		w := call(h, body, "", "Bearer "+token)
		if w.Code != 400 {
			t.Errorf("case %d status %d", i, w.Code)
		}
	}
	if len(b.attempts) != 0 {
		t.Fatal("invalid request reserved")
	}
}
func TestTextFunctionToolsAndSamplingAreCompatible(t *testing.T) {
	body := `{"model":"synthetic-model","messages":[{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","content":"result","tool_call_id":"call_1"}],"max_tokens":8,"temperature":0.3,"top_p":0.8,"frequency_penalty":0,"presence_penalty":0,"response_format":{"type":"json_object"},"tools":[{"type":"function","function":{"name":"lookup","description":"local tool","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}],"tool_choice":"auto","n":1}`
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Fatal(e)
		}
		if got["tools"] == nil {
			t.Error("tools lost")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	w := call(h, body, "", "Bearer "+token)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestIdempotencyCachesExactReplayAndRejectsChangedBody(t *testing.T) {
	b := &memoryBudget{}
	var paid atomic.Int64
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	for i := 0; i < 2; i++ {
		w := call(h, validBody, "stable-id", "Bearer "+token)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	w := call(h, strings.Replace(validBody, "hello", "other", 1), "stable-id", "Bearer "+token)
	if w.Code != 409 || paid.Load() != 1 {
		t.Fatalf("status=%d paid=%d", w.Code, paid.Load())
	}
}
func TestIndependentRetriesReserveIndependentAttempts(t *testing.T) {
	b := &memoryBudget{limit: 2}
	var paid atomic.Int64
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	for i := 0; i < 3; i++ {
		w := call(h, validBody, "", "Bearer "+token)
		want := 200
		if i == 2 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("%d != %d", w.Code, want)
		}
	}
	if paid.Load() != 2 {
		t.Fatal(paid.Load())
	}
}
func TestConcurrentBudgetDenyHasZeroExtraDispatch(t *testing.T) {
	b := &memoryBudget{limit: 1}
	var paid atomic.Int64
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); call(h, validBody, "", "Bearer "+token) }()
	}
	wg.Wait()
	if paid.Load() != 1 || len(b.attempts) != 1 {
		t.Fatal("budget bypass")
	}
}
func TestMalformedOrMissingUsageIsUnknownAndRawErrorsStayPrivate(t *testing.T) {
	for _, reply := range []string{`{"id":"x","model":"synthetic-model","choices":[]}`, strings.Replace(validResponse, `"prompt_tokens":2,`, "", 1), strings.Replace(validResponse, `"total_tokens":3`, `"total_tokens":4`, 1), `{"error":{"message":"` + supplier + `"}}`} {
		t.Run(reply[:min(24, len(reply))], func(t *testing.T) {
			b := &memoryBudget{}
			h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, reply)
			}, b)
			w := call(h, validBody, "", "Bearer "+token)
			if w.Code != 502 || b.unknown != 1 || len(b.reports) != 0 {
				t.Fatalf("status=%d unknown=%d reports=%d", w.Code, b.unknown, len(b.reports))
			}
			if strings.Contains(w.Body.String(), supplier) {
				t.Fatal("supplier error leaked")
			}
		})
	}
}
func TestExplicitZeroUsageIsAccepted(t *testing.T) {
	b := &memoryBudget{}
	reply := `{"id":"zero","model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	}, b)
	if w := call(h, validBody, "", "Bearer "+token); w.Code != 200 || len(b.reports) != 1 {
		t.Fatal(w.Code)
	}
}
func TestOverboundUsageReachesLedgerForFreeze(t *testing.T) {
	b := &memoryBudget{observeErr: modelspend.ErrBoundExceeded}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Replace(strings.Replace(validResponse, `"completion_tokens":1`, `"completion_tokens":9`, 1), `"total_tokens":3`, `"total_tokens":11`, 1))
	}, b)
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || len(b.reports) != 1 || b.reports[0].Usage.CompletionTokens != 9 {
		t.Fatal("overbound report lost")
	}
}
func TestTimeoutAndProviderFailureKeepReservation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "timeout"}[timeout], func(t *testing.T) {
			b := &memoryBudget{}
			var paid atomic.Int64
			h, s := setup(t, func(w http.ResponseWriter, r *http.Request) {
				paid.Add(1)
				if timeout {
					io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(100 * time.Millisecond):
					}
					return
				}
				w.WriteHeader(429)
				io.WriteString(w, supplier)
			}, b)
			_ = s
			if timeout {
				h.(*gateway).client.Timeout = 15 * time.Millisecond
			}
			w := call(h, validBody, "", "Bearer "+token)
			if w.Code != 502 || b.unknown != 1 || paid.Load() != 1 || len(b.attempts) != 1 {
				t.Fatalf("status=%d paid=%d unknown=%d", w.Code, paid.Load(), b.unknown)
			}
			if strings.Contains(w.Body.String(), supplier) {
				t.Fatal("key leaked")
			}
		})
	}
}
func TestConfigRequiresSeparateCredentialAndOfficialTLSOrigin(t *testing.T) {
	b := &memoryBudget{}
	base := Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, Timeout: time.Second}
	for _, mut := range []func(*Config){func(c *Config) { c.GatewayToken = "short" }, func(c *Config) { c.SupplierKey = token }, func(c *Config) { c.BaseURL = "http://api.deepseek.com/v1" }, func(c *Config) { c.BaseURL = "https://evil.example/v1" }, func(c *Config) { c.BaseURL = "https://api.deepseek.com/v1?x=1" }, func(c *Config) { c.BaseURL = "https://api.deepseek.com/beta" }} {
		c := base
		mut(&c)
		if _, e := New(c, b); e == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	if _, e := New(base, b); e != nil {
		t.Fatal(e)
	}
}
func TestObserveFailureDoesNotExposeResponse(t *testing.T) {
	b := &memoryBudget{observeErr: errors.New("db detail " + supplier)}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || strings.Contains(w.Body.String(), supplier) || strings.Contains(w.Body.String(), "hello") {
		t.Fatal("unrecorded response escaped")
	}
}
