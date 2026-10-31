package coze

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfiguredBridgeRefusesUnpatchedCozeBeforeWorkflowOrDailyHold(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"capability": "bgc1.explicit-protocol-fixture", "workflow_sha256": strings.Repeat("a", 64), "model": "reviewed-model", "max_output_tokens": 8, "expires_at": time.Now().Add(time.Minute).Unix()})
	}))
	defer gateway.Close()
	posts := 0
	coze := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"execute_id":"protocol-only","data":"{}","token":1}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer coze.Close()
	meter := &testMeter{}
	c, e := NewBudgeted(coze.URL, "explicit-protocol-pat", time.Second, meter, outbound.Policy{Environment: "development"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ConfigureWorkflowBridge(gateway.URL, strings.Repeat("g", 32), outbound.Policy{Environment: "development"}); e != nil {
		t.Fatal(e)
	}
	ctx := WithDelegation(WithIdentity(context.Background(), 42, "real-task-fixture"), "restricted-task-fixture")
	_, e = c.RunSyncWorkflow(ctx, RunRequest{WorkflowID: "123", Parameters: map[string]any{}})
	if !errors.Is(e, ErrMonetaryAdmissionRequired) || posts != 0 || meter.owner != 0 {
		t.Fatalf("unpatched runtime admitted: err=%v posts=%d dailyOwner=%d", e, posts, meter.owner)
	}
}

func TestWorkflowGrantClassifiesOnlyKnownGatewayQuotaCodes(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"project", `{"error":{"code":"MODEL_SPEND_QUOTA_EXCEEDED","message":"MODEL_SPEND_QUOTA_EXCEEDED","type":"model_gateway_error"}}`, "MODEL_SPEND_QUOTA_EXCEEDED"},
		{"new_approval", `{"error":{"code":"MODEL_APPROVAL_LIMIT_EXCEEDED","message":"MODEL_APPROVAL_LIMIT_EXCEEDED","type":"model_gateway_error"}}`, "MODEL_APPROVAL_LIMIT_EXCEEDED"},
		{"monthly", `{"error":{"code":"USER_MONTHLY_BUDGET_EXCEEDED","message":"USER_MONTHLY_BUDGET_EXCEEDED","type":"model_gateway_error"}}`, "MODEL_USER_MONTHLY_QUOTA_EXCEEDED"},
		{"unknown", `{"error":{"code":"UNKNOWN"}}`, "MODEL_MONETARY_ADMISSION_REQUIRED"},
		{"malformed", `not json`, "MODEL_MONETARY_ADMISSION_REQUIRED"},
		{"duplicate", `{"error":{"code":"USER_MONTHLY_BUDGET_EXCEEDED","code":"MODEL_SPEND_QUOTA_EXCEEDED"}}`, "MODEL_MONETARY_ADMISSION_REQUIRED"},
		{"oversize", strings.Repeat("x", 32769), "MODEL_MONETARY_ADMISSION_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); w.Write([]byte(tc.body)) }))
			defer s.Close()
			b := workflowBridge{base: s.URL, token: strings.Repeat("g", 32), client: s.Client()}
			_, err := b.grant(WithDelegation(context.Background(), "synthetic-task"), "123", []byte(`{}`))
			if got := BudgetCode(err); got != tc.want {
				t.Fatalf("code=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestExecutionConfirmationRefusalPreservesCodeThroughBridgeAndSnapshot(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"error":{"code":"MODEL_EXECUTION_CONFIRMATION_REQUIRED","message":"MODEL_EXECUTION_CONFIRMATION_REQUIRED","type":"model_gateway_error"}}`))
	}))
	defer s.Close()
	b := &workflowBridge{base: s.URL, token: strings.Repeat("g", 32), client: s.Client()}
	_, err := b.grant(WithDelegation(context.Background(), "synthetic-task"), "1", []byte(`{}`))
	if BudgetCode(err) != "MODEL_EXECUTION_CONFIRMATION_REQUIRED" {
		t.Fatalf("grant lost confirmation refusal: %v", err)
	}
	c := &Client{bridge: b}
	_, err = c.AdmissionSnapshot(context.Background(), "Bearer synthetic-user")
	if BudgetCode(err) != "MODEL_EXECUTION_CONFIRMATION_REQUIRED" {
		t.Fatalf("snapshot lost confirmation refusal: %v", err)
	}
}

func TestWorkflowGrantPreservesUnavailableBudgetAndExpiredPolicy(t *testing.T) {
	for _, tc := range []struct{ upstream, want string }{
		{"BUDGET_UNAVAILABLE", "MODEL_BUDGET_UNAVAILABLE"},
		{"POLICY_UNAVAILABLE", "MODEL_APPROVAL_REQUIRED"},
	} {
		t.Run(tc.upstream, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1/workflow-grants" {
					t.Fatal("unexpected paid/workflow request")
				}
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": tc.upstream}})
			}))
			defer s.Close()
			bridge := workflowBridge{base: s.URL, token: strings.Repeat("g", 32), client: s.Client()}
			grant, err := bridge.grant(WithDelegation(context.Background(), "synthetic-task"), "123", []byte(`{}`))
			if BudgetCode(err) != tc.want || grant.Capability != "" || calls != 1 {
				t.Fatalf("upstream refusal hidden: %v, grant=%v calls=%d", err, grant, calls)
			}
		})
	}
}
