package modelgateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestInputContextRejectedBeforeReservationAndDispatch(t *testing.T) {
	for _, content := range []string{strings.Repeat("x", 101), strings.Repeat("财", 40)} {
		b := &memoryBudget{}
		calls := 0
		h, _ := setup(t, func(http.ResponseWriter, *http.Request) { calls++ }, b)
		w := call(h, strings.Replace(validBody, "hello", content, 1), "bounded-input", "Bearer "+token)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "MODEL_CONTEXT_LIMIT_EXCEEDED") || calls != 0 || len(b.attempts) != 0 || len(b.reports) != 0 {
			t.Fatalf("context admission: status=%d body=%s calls=%d attempts=%d", w.Code, w.Body.String(), calls, len(b.attempts))
		}
	}
}

func TestInputContextIncludesHistoryAndToolSchemas(t *testing.T) {
	p := policy()
	for _, body := range []string{
		strings.Replace(validBody, `"messages":[`, `"messages":[{"role":"system","content":"`+strings.Repeat("s", 90)+`"},`, 1),
		strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"tools":[{"type":"function","function":{"name":"read_only","description":"`+strings.Repeat("d", 90)+`","parameters":{"type":"object"}}}]`, 1),
	} {
		if _, err := parseRequest([]byte(body), p); err == nil {
			t.Fatal("history or tool schema bypassed the input limit")
		}
	}
	if _, err := parseRequest([]byte(validBody), p); err != nil {
		t.Fatal("bounded input rejected", err)
	}
}
