package modelgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

const streamResponse = "data: {\"id\":\"provider-1\",\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"provider-1\",\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"id\":\"provider-1\",\"model\":\"synthetic-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
	"data: [DONE]\n\n"

func TestStreamForcesUsageAndReturnsOnlyAfterObservation(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"include_usage":true`) {
			t.Error("SDK false usage flag not replaced")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		io.WriteString(w, streamResponse)
	}, b)
	body := strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"stream":true,"stream_options":{"include_usage":false}`, 1)
	w := call(h, body, "stream-id", "Bearer "+token)
	if w.Code != 200 || w.Body.String() != streamResponse || len(b.reports) != 1 || b.reports[0].ContentType != "text/event-stream" {
		t.Fatalf("status=%d reports=%d body=%s", w.Code, len(b.reports), w.Body.String())
	}
	if w = call(h, body, "stream-id", "Bearer "+token); w.Code != 200 || w.Body.String() != streamResponse {
		t.Fatal("stream cache replay failed")
	}
}
func TestStreamTruncationMalformedUsageOrMismatchedIdentityStayUnknown(t *testing.T) {
	cases := []string{strings.Replace(streamResponse, "data: [DONE]\n\n", "", 1), strings.TrimSuffix(streamResponse, "\n"), strings.Replace(streamResponse, `"prompt_tokens":2,`, "", 1), strings.Replace(streamResponse, `"total_tokens":3`, `"total_tokens":9`, 1), strings.Replace(streamResponse, `"provider-1"`, `"provider-other"`, 1), strings.Replace(streamResponse, `"finish_reason":"stop"`, `"finish_reason":null`, 1), streamResponse + "data: {}\n\n", strings.Replace(streamResponse, `"choices":[],"usage"`, `"choices":[],"usagex"`, 1)}
	for i, reply := range cases {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, reply)
		}, b)
		body := strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"stream":true`, 1)
		w := call(h, body, "", "Bearer "+token)
		if w.Code != 502 || b.unknown != 1 || len(b.reports) != 0 {
			t.Fatalf("case=%d status=%d unknown=%d reports=%d", i, w.Code, b.unknown, len(b.reports))
		}
	}
}
func TestWrongModelReportReachesLedgerBeforeResponseIsRejected(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Replace(validResponse, `"model":"synthetic-model"`, `"model":"unapproved-model"`, 1))
	}, b)
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || len(b.reports) != 1 || b.reports[0].Model != "unapproved-model" {
		t.Fatal("model mismatch not recorded for freeze")
	}
}
func TestRedirectIsNeverFollowed(t *testing.T) {
	var diverted atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { diverted.Add(1) }))
	defer target.Close()
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}, b)
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || diverted.Load() != 0 || b.unknown != 1 {
		t.Fatal("redirect followed")
	}
}
func TestUntrustedTLSFailsWithoutHTTPFallback(t *testing.T) {
	var paid atomic.Int64
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paid.Add(1); io.WriteString(w, validResponse) }))
	defer s.Close()
	b := &memoryBudget{attempts: map[string]modelspend.Attempt{}}
	h, e := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, BaseURL: s.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, b)
	if e != nil {
		t.Fatal(e)
	}
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || paid.Load() != 0 || b.unknown != 1 {
		t.Fatal("TLS trust bypass")
	}
}
func TestRequestAndResponseSizeBounds(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Repeat("x", responseLimit+1))
	}, b)
	if w := call(h, strings.Repeat("x", requestLimit+1), "", "Bearer "+token); w.Code != 400 || len(b.attempts) != 0 {
		t.Fatal("oversized request reserved")
	}
	if w := call(h, validBody, "", "Bearer "+token); w.Code != 502 || b.unknown != 1 {
		t.Fatal("oversized response accepted")
	}
}
func TestPrivateSupplierHeaderAndBodyAreNeverLeaked(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Authorization", "Bearer "+supplier)
		w.Header().Set("X-Debug-Secret", supplier)
		io.WriteString(w, strings.Replace(validResponse, `"content":"hello"`, `"content":"`+supplier+`"`, 1))
	}, b)
	w := call(h, validBody, "", "Bearer "+token)
	if w.Code != 502 || strings.Contains(w.Body.String(), supplier) || strings.Contains(w.Header().Get("X-Debug-Secret"), supplier) || len(b.reports) != 0 {
		t.Fatal("credential reflected")
	}
}
func TestReadinessHasNoConfigurationDetails(t *testing.T) {
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("readiness contacted provider") }, &memoryBudget{})
	r := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestCachedReplayChecksProjectPolicyAndDigest(t *testing.T) {
	for _, kind := range []string{"project", "policy", "digest"} {
		b := &memoryBudget{attempts: map[string]modelspend.Attempt{}}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid cache triggered dispatch") }, b)
		body := validResponse
		sum := sha256.Sum256([]byte(validBody))
		a := modelspend.Attempt{ID: "cached", ProjectID: "project", RequestKey: "cached", RequestHash: hex.EncodeToString(sum[:]), ResponseBody: &body, ContentType: "application/json"}
		switch kind {
		case "project":
			a.ProjectID = "other"
		case "policy":
			a.PolicyHash = "wrong"
		case "digest":
			a.ResponseDigest = "wrong"
		}
		b.attempts["cached"] = a
		if w := call(h, validBody, "cached", "Bearer "+token); w.Code != 503 {
			t.Fatal(kind, w.Code)
		}
	}
}

type cancelBudget struct {
	memoryBudget
	observedCanceled bool
	unknownCanceled  bool
}

func (b *cancelBudget) Observe(ctx context.Context, id string, r modelspend.Report) error {
	b.observedCanceled = ctx.Err() != nil
	return b.memoryBudget.Observe(ctx, id, r)
}
func (b *cancelBudget) Unknown(ctx context.Context, id string) error {
	b.unknownCanceled = ctx.Err() != nil
	return b.memoryBudget.Unknown(ctx, id)
}
func TestOutcomeAccountingSurvivesClientCancellation(t *testing.T) {
	b := &cancelBudget{memoryBudget: memoryBudget{attempts: map[string]modelspend.Attempt{}}}
	ctx, cancel := context.WithCancel(context.Background())
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}))
	defer s.Close()
	h, e := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, BaseURL: s.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, b)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(validBody)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if b.unknown != 1 || b.unknownCanceled {
		t.Fatal("canceled accounting context")
	}
}
