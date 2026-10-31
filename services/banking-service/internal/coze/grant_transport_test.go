package coze

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestGrantTransportFailureIsUnavailableBeforeAnyWorkflowOrHold(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		t.Run(map[bool]string{false: "connection refused", true: "grant response lost"}[lostResponse], func(t *testing.T) {
			grants, workflowRequests := 0, 0
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				grants++
				if r.URL.Path != "/v1/workflow-grants" {
					t.Error("unexpected paid route")
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			defer gateway.Close()
			if !lostResponse {
				gateway.Close()
			}
			workflow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				workflowRequests++
			}))
			defer workflow.Close()
			meter := &testMeter{}
			policy := outbound.Policy{Environment: "development"}
			client, err := NewBudgeted(workflow.URL, "synthetic-Coze-only-PAT", time.Second, meter, policy)
			if err != nil {
				t.Fatal(err)
			}
			if err = client.ConfigureWorkflowBridge(gateway.URL, strings.Repeat("g", 32), policy); err != nil {
				t.Fatal(err)
			}
			ctx := WithDelegation(WithIdentity(context.Background(), 42, "new-test-task"), "synthetic-task-delegation")
			_, err = client.RunSyncWorkflow(ctx, RunRequest{WorkflowID: "123"})
			if BudgetCode(err) != "MODEL_BUDGET_UNAVAILABLE" || workflowRequests != 0 || meter.owner != 0 || meter.unknown != 0 || meter.completed != 0 {
				t.Fatalf("pre-dispatch failure misclassified: err=%v workflow_requests=%d meter=%+v", err, workflowRequests, meter)
			}
			if lostResponse && grants != 1 {
				t.Fatalf("grant request retried: %d", grants)
			}
		})
	}
}
