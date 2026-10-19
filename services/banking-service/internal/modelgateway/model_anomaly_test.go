package modelgateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWrongModelWithUnusableUsageStillReachesFreezeAccounting(t *testing.T) {
	for _, reply := range []string{
		`{"id":"provider-1","model":"unapproved-model","choices":[]}`,
		strings.Replace(strings.Replace(validResponse, `"model":"synthetic-model"`, `"model":"unapproved-model"`, 1), `"prompt_tokens":2,`, "", 1),
		strings.Replace(strings.Replace(validResponse, `"model":"synthetic-model"`, `"model":"unapproved-model"`, 1), `"total_tokens":3`, `"total_tokens":9`, 1),
	} {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, reply)
		}, b)
		w := call(h, validBody, "", "Bearer "+token)
		if w.Code != 502 || len(b.reports) != 1 {
			t.Fatalf("known wrong model not reported: status=%d reports=%d", w.Code, len(b.reports))
		}
		r := b.reports[0]
		if r.Model != "unapproved-model" || r.ProviderID != "provider-1" || !r.UsageUnknown {
			t.Fatal("missing explicit unknown usage flag on model anomaly")
		}
	}
}

func TestWrongModelInCompletedSSEEventFreezesEvenWithoutTerminalUsage(t *testing.T) {
	for _, reply := range []string{
		"data: {\"id\":\"provider-1\",\"model\":\"unapproved-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n",
		strings.Replace(strings.Replace(streamResponse, `"model":"synthetic-model"`, `"model":"unapproved-model"`, 1), `"prompt_tokens":2,`, "", 1),
	} {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, reply)
		}, b)
		body := strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"stream":true`, 1)
		w := call(h, body, "", "Bearer "+token)
		if w.Code != 502 || len(b.reports) != 1 {
			t.Fatalf("known stream model mismatch ignored: status=%d reports=%d", w.Code, len(b.reports))
		}
		if !b.reports[0].UsageUnknown {
			t.Fatal("missing usage unknown flag")
		}
	}
}
