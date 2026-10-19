package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEscapedSupplierCredentialNeverReachesClientOrReplayCache(t *testing.T) {
	encoded := strings.ReplaceAll(supplier, "-", `\u002d`)
	nested, _ := json.Marshal(`{"private_key":"` + encoded + `"}`)
	deep := string(nested)
	for i := 0; i < 10; i++ {
		wrapped, _ := json.Marshal(deep)
		deep = string(wrapped)
	}
	for _, reply := range []string{strings.Replace(validResponse, `"content":"hello"`, `"content":"`+encoded+`"`, 1), strings.Replace(validResponse, `"content":"hello"`, `"content":`+string(nested), 1), strings.Replace(validResponse, `"content":"hello"`, `"content":`+deep, 1)} {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, reply)
		}, b)
		w := call(h, validBody, "encoded-json", "Bearer "+token)
		if w.Code != 502 || len(b.reports) != 0 || b.unknown != 1 {
			t.Errorf("encoded supplier credential escaped: status=%d reports=%d", w.Code, len(b.reports))
		}
	}
}
func TestEscapedSupplierCredentialInSSEIsPrivate(t *testing.T) {
	reply := strings.Replace(streamResponse, "hello", strings.ReplaceAll(supplier, "-", `\u002d`), 1)
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, reply)
	}, b)
	body := strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"stream":true`, 1)
	w := call(h, body, "encoded-stream", "Bearer "+token)
	if w.Code != 502 || len(b.reports) != 0 || b.unknown != 1 {
		t.Fatalf("encoded SSE secret leaked: status=%d reports=%d", w.Code, len(b.reports))
	}
}
func TestCredentialReflectionGuardOnlyInspectsProviderOutput(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validResponse)
	}, b)
	if w := call(h, strings.Replace(validBody, "hello", supplier, 1), "request-text", "Bearer "+token); w.Code != 200 {
		t.Fatalf("unrelated request text incorrectly blocked: %d", w.Code)
	}
}
