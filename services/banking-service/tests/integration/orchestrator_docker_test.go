//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type orchestrationDockerEvidence struct {
	UserID       int64  `json:"user_id,string"`
	JobID        string `json:"job_id"`
	AccountID    int64  `json:"account_id,string"`
	Executions   int    `json:"executions"`
	BalanceMinor int64  `json:"balance_minor"`
	SavedAt      int64  `json:"saved_at"`
}

func orchestrationEvidencePath() string {
	return filepath.Join("..", "..", "output", "module7", "docker-evidence.json")
}
func TestOrchestratorDockerWorker(t *testing.T) {
	h := dockerHarness(t)
	account := h.account(t, 50000)
	period := json.RawMessage(fmt.Sprintf(`{"from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z","account_id":"%d"}`, account.ID))
	v := parse[orchestrator.View](t, h.call(t, "POST", "/api/v1/orchestrations", uuid.NewString(), orchestrator.SubmitInput{Tasks: []orchestrator.TaskSpec{{Key: "bill", AgentType: "billing", Tool: "analyze_spending", Parameters: period, Dependencies: []string{}}, {Key: "subscription", AgentType: "subscription", Tool: "analyze_subscription_cost", Parameters: json.RawMessage(`{}`), Dependencies: []string{}}}}, 201))
	path := "/api/v1/orchestrations/" + v.Job.ID
	deadline := time.Now().Add(15 * time.Second)
	for {
		v = parse[orchestrator.View](t, h.call(t, "GET", path, "", nil, 200))
		if v.Job.Status == "succeeded" {
			break
		}
		if v.Job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("worker status %s", v.Job.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	result := parse[orchestrator.Result](t, h.call(t, "GET", path+"/result", "", nil, 200))
	if len(result.Outputs) != 2 || result.FinancialExecutionEnabled {
		t.Fatal("worker final output invalid")
	}
	tr := parse[orchestrator.TraceView](t, h.call(t, "GET", path+"/trace", "", nil, 200))
	if len(tr.Executions) != 2 || len(tr.Invocations) != 2 {
		t.Fatal("worker records missing")
	}
	h.balance(t, account.ID, 50000)
	evidence := orchestrationDockerEvidence{UserID: h.uid, JobID: v.Job.ID, AccountID: account.ID, Executions: len(tr.Executions), BalanceMinor: 50000, SavedAt: time.Now().UnixMilli()}
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	if e := os.MkdirAll(filepath.Dir(orchestrationEvidencePath()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(orchestrationEvidencePath(), raw, 0600); e != nil {
		t.Fatal(e)
	}
	// Local cancellation proposal requires explicit task authorization, then blocks
	// at the absent module8 gateway. No simulated subscription is cancelled.
	intent := parse[orchestrator.View](t, h.call(t, "POST", "/api/v1/orchestrations", uuid.NewString(), orchestrator.SubmitInput{Tasks: []orchestrator.TaskSpec{{Key: "cancel", AgentType: "subscription", Tool: "create_cancel_intent", Parameters: json.RawMessage(`{"subscription_id":"1","expected_version":1}`), Dependencies: []string{}}}}, 201))
	intentPath := "/api/v1/orchestrations/" + intent.Job.ID
	deadline = time.Now().Add(10 * time.Second)
	for {
		intent = parse[orchestrator.View](t, h.call(t, "GET", intentPath, "", nil, 200))
		if intent.Job.Status == "awaiting_confirmation" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker didn't stop for confirmation")
		}
		time.Sleep(100 * time.Millisecond)
	}
	intent = parse[orchestrator.View](t, h.call(t, "POST", intentPath+"/confirm", "", orchestrator.ConfirmInput{TaskKey: "cancel", ExpectedVersion: intent.Job.Version, Decision: "approve"}, 200))
	deadline = time.Now().Add(10 * time.Second)
	for {
		intent = parse[orchestrator.View](t, h.call(t, "GET", intentPath, "", nil, 200))
		if intent.Job.Status == "awaiting_gateway" {
			break
		}
		if time.Now().After(deadline) || intent.Job.Status == "failed" {
			t.Fatalf("gateway isolation %s", intent.Job.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var count int64
	h.conn.DB.Model(&entity.SubscriptionActionIntent{}).Where("user_id=?", h.uid).Count(&count)
	if count != 0 {
		t.Fatal("module8 gateway bypassed")
	}
	h.call(t, "POST", intentPath+"/cancel", "", map[string]any{}, 200)
}
func TestOrchestratorDockerPersistence(t *testing.T) {
	if os.Getenv("BANKING_TEST_BASE_URL") == "" {
		t.Skip("requires module7 Docker deployment and a real service restart")
	}
	if os.Getenv("BANKING_TEST_ORCHESTRATOR_RESTARTED") != "1" {
		t.Skip("explicitly set BANKING_TEST_ORCHESTRATOR_RESTARTED=1 after restarting the service")
	}
	raw, e := os.ReadFile(orchestrationEvidencePath())
	if e != nil {
		t.Fatal(e)
	}
	var saved orchestrationDockerEvidence
	if e = json.Unmarshal(raw, &saved); e != nil {
		t.Fatal(e)
	}
	h := dockerHarness(t)
	h.uid = saved.UserID
	h.token, e = h.auth.IssueUser(fmt.Sprint(saved.UserID), "", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/orchestrations/" + saved.JobID
	v := parse[orchestrator.View](t, h.call(t, "GET", path, "", nil, 200))
	if v.Job.Status != "succeeded" {
		t.Fatal("completed task lost on restart")
	}
	tr := parse[orchestrator.TraceView](t, h.call(t, "GET", path+"/trace", "", nil, 200))
	if len(tr.Executions) != saved.Executions {
		t.Fatal("restart repeated completed invocation")
	}
	h.balance(t, saved.AccountID, saved.BalanceMinor)
}
