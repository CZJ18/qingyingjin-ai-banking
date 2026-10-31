package coze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestRejectedWorkflowLogsSafeDiagnosticAndKeepsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   int64
	}{
		{"native-business-error", http.StatusOK, `{"code":777777772,"msg":"synthetic-sensitive-upstream-detail","data":null}`, 777777772},
		{"http-error", http.StatusBadGateway, `synthetic-sensitive-upstream-detail`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prior) })
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			meter := &testMeter{}
			c, err := NewBudgeted(s.URL, "synthetic-private-pat", time.Second, meter, outbound.TestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.RunSyncWorkflow(WithIdentity(context.Background(), 42, "synthetic-key"), RunRequest{
				WorkflowID: "123", TraceID: "synthetic-trace", Parameters: map[string]any{"private_input": "synthetic-private-financial-input"},
			})
			if !errors.Is(err, ErrOutcomeUnknown) || out.ExecuteID != "" || calls != 1 || meter.unknown != 1 || meter.completed != 0 {
				t.Fatalf("diagnosis altered failure/unknown/no-retry semantics: error=%v calls=%d meter=%+v", err, calls, meter)
			}
			found := false
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var fields map[string]any
				if json.Unmarshal([]byte(line), &fields) != nil {
					t.Fatal("invalid structured log")
				}
				if fields["msg"] != "coze_workflow_rejected" {
					continue
				}
				found = true
				if fields["workflow_id"] != "123" || fields["trace_id"] != "synthetic-trace" || fields["reservation_id"] != "reservation" || fields["http_status"] != float64(tc.status) {
					t.Fatalf("missing correlation metadata: %+v", fields)
				}
				if tc.code != 0 && (fields["business_code"] != float64(tc.code) || fields["response_sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.body)))) {
					t.Fatalf("native error code/digest lost: %+v", fields)
				}
			}
			if !found {
				t.Fatal("upstream rejection has no safe diagnostic")
			}
			for _, secret := range []string{"synthetic-sensitive-upstream-detail", "synthetic-private-pat", "synthetic-private-financial-input", "synthetic-key"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("diagnostic leaked private input or credential")
				}
			}
		})
	}
}
