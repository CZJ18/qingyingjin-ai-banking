package modelgateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A provider-aborted completion is not a successful business answer. When its
// terminal usage is complete, preserve the supplier report instead of losing
// known usage. Actual financial settlement remains the ledger's separate step.
func TestAbortedCompletionPreservesVerifiedUsage(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			b := &memoryBudget{}
			reply := strings.Replace(validResponse, `"finish_reason":"stop"`, `"finish_reason":"aborted"`, 1)
			body := validBody
			contentType := "application/json"
			if streaming {
				reply = strings.Replace(streamResponse, `"finish_reason":"stop"`, `"finish_reason":"aborted"`, 1)
				body = strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"stream":true`, 1)
				contentType = "text/event-stream"
			}
			h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				io.WriteString(w, reply)
			}, b)
			w := call(h, body, "", "Bearer "+token)
			if w.Code != 200 || len(b.reports) != 1 || b.unknown != 0 || b.reports[0].Usage.TotalTokens != 3 || !strings.Contains(w.Body.String(), `"finish_reason":"aborted"`) {
				t.Fatalf("aborted supplier usage was lost or rewritten: status=%d reports=%d unknown=%d", w.Code, len(b.reports), b.unknown)
			}
		})
	}
}
