//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"testing"
	"time"
)

func TestExecutionOrchestratorPersistedApproval(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	db := h.conn.DB
	gateway := execution.New(db, subscription.LocalSimulator{})
	account := h.account(t, 100000)
	sub := entity.Subscription{Base: entity.Base{UserID: h.uid}, BankAccountID: account.ID, Fingerprint: uuid.NewString(), MerchantName: "Module8 local subscription", Category: "subscription", BillingInterval: "monthly", AmountMinor: 1200, Currency: "CNY", NextBillingAt: time.Now().Add(24 * time.Hour).UnixMilli(), ConfidenceBPS: 9000, Status: entity.SubscriptionActive, Version: 1, IsSimulated: true}
	if e := db.Create(&sub).Error; e != nil {
		t.Fatal(e)
	}
	parameters, _ := json.Marshal(subscription.IntentInput{SubscriptionID: fmt.Sprint(sub.ID), ExpectedVersion: sub.Version})
	call := orchestrator.Call{Meta: service.Meta{UserID: h.uid, TraceID: "module8-orchestration"}, JobID: uuid.NewString(), TaskID: uuid.NewString(), AgentType: "subscription", Tool: "create_cancel_intent", Parameters: parameters, Confirmed: true}
	call.InvocationKey = "orchestrator:" + call.TaskID
	reject := func(c orchestrator.Call) {
		t.Helper()
		if _, e := gateway.Propose(ctx, c); entity.Code(e) != "FORBIDDEN" {
			t.Fatalf("untrusted scheduler state accepted: %v", e)
		}
	}
	reject(call) // A caller-controlled confirmation flag has no authority.
	call.LeaseToken = uuid.NewString()
	reject(call) // Even a supplied lease token must exist in persisted job state.
	job := orchestrator.Job{ID: call.JobID, UserID: h.uid, Status: "running", Strategy: "sequential", Version: 1, MaxAttempts: 1, TimeoutMS: 60000, IdempotencyKey: uuid.NewString(), RequestHash: uuid.NewString(), LeaseToken: call.LeaseToken, LeaseUntil: time.Now().Add(time.Minute).UnixMilli(), TraceID: call.Meta.TraceID}
	task := orchestrator.AgentTask{ID: call.TaskID, UserID: h.uid, JobID: call.JobID, Key: "cancel", AgentType: call.AgentType, Tool: call.Tool, Mode: "intent", Status: "running", Parameters: parameters, Output: json.RawMessage(`{}`), Attempts: 1, Confirmation: "pending"}
	run := orchestrator.AgentExecution{ID: uuid.NewString(), UserID: h.uid, JobID: call.JobID, TaskID: call.TaskID, AgentType: call.AgentType, Attempt: 1, Status: "running", StartedAt: time.Now().UnixMilli(), Input: parameters, Output: json.RawMessage(`{}`), LeaseToken: call.LeaseToken}
	for _, row := range []any{&job, &task, &run} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	reject(call) // Persisted approval is required even with Confirmed=true.
	if e := db.Model(&task).Update("confirmation", "approve").Error; e != nil {
		t.Fatal(e)
	}
	if e := db.Model(&task).Update("mode", "execute").Error; e != nil {
		t.Fatal(e)
	}
	reject(call) // Approval cannot upgrade a persisted task into execution mode.
	if e := db.Model(&task).Update("mode", "intent").Error; e != nil {
		t.Fatal(e)
	}
	call.Confirmed = false // Persisted approval, rather than the boolean, controls PREPARE.
	raw, e := gateway.Propose(ctx, call)
	if e != nil {
		t.Fatal(e)
	}
	var intent entity.SubscriptionActionIntent
	if e = json.Unmarshal(raw, &intent); e != nil {
		t.Fatal(e)
	}
	if intent.Status != "pending_approval" || intent.ExecutedAt != nil {
		t.Fatalf("proposal executed: %+v", intent)
	}
	var current entity.Subscription
	if e = db.First(&current, sub.ID).Error; e != nil {
		t.Fatal(e)
	}
	if current.Status != entity.SubscriptionActive || current.Version != sub.Version || current.CancelledAt != nil {
		t.Fatal("prepare mutated subscription")
	}
	var balance entity.BankAccount
	if e = db.First(&balance, account.ID).Error; e != nil {
		t.Fatal(e)
	}
	if balance.BalanceMinor != account.BalanceMinor {
		t.Fatal("prepare moved funds")
	}
	tampered := call
	tampered.Parameters = json.RawMessage(fmt.Sprintf(`{"subscription_id":"%d","expected_version":2}`, sub.ID))
	reject(tampered)
	if e = db.Model(&job).Update("lease_until", time.Now().Add(-time.Second).UnixMilli()).Error; e != nil {
		t.Fatal(e)
	}
	reject(call)
	if e = db.Model(&job).Updates(map[string]any{"lease_until": time.Now().Add(time.Minute).UnixMilli(), "status": "cancelled"}).Error; e != nil {
		t.Fatal(e)
	}
	reject(call)
	var operations int64
	if e = db.Model(&entity.OperationIntent{}).Where("user_id=?", h.uid).Count(&operations).Error; e != nil {
		t.Fatal(e)
	}
	if operations != 1 {
		t.Fatalf("denied calls created operations: %d", operations)
	}
	var approvals int64
	if e = db.Model(&entity.UserAuthorization{}).Where("user_id=?", h.uid).Count(&approvals).Error; e != nil {
		t.Fatal(e)
	}
	if approvals != 0 {
		t.Fatal("scheduler approval granted financial authorization")
	}
}
