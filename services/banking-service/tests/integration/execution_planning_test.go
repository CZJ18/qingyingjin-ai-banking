//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
	"time"
)

func TestExecutionPlanningProtection(t *testing.T) {
	h := harness(t)
	src := h.account(t, 5000)
	dst := h.account(t, 0)
	m := service.Meta{UserID: h.uid, TraceID: "planning-execution"}
	ctx := context.Background()
	now := time.Now().UnixMilli()
	g, e := h.svc.CreateGoal(ctx, m, "execution-plan-goal", service.GoalInput{Name: "Sustainable habits", TargetMinor: 1000, StartsAt: now, EndsAt: now + int64(90*24*time.Hour/time.Millisecond), MonthlyMinor: 500})
	if e != nil {
		t.Fatal(e)
	}
	living := int64(2000)
	essential := int64(1000)
	future := int64(1000000000)
	_, e = planning.New(h.conn.DB).Create(ctx, m, planning.CreateInput{Name: "Protected current cash", Settings: planning.Settings{AccountIDs: []int64{src.ID}, SavingsGoalIDs: []int64{g.ID}, LivingRemainingMinor: &living, EssentialRemainingMinor: &essential, SafetyReserveMinor: 500, CashBufferMinor: 500, FutureIncomeMinor: &future, Cashflows: []planning.Cashflow{{Name: "Necessary upcoming expense", Kind: "necessary_expense", AmountMinor: 500, DueAt: now + 1000}}}})
	if e != nil {
		t.Fatal(e)
	}
	gateway := execution.New(h.conn.DB, nil)
	human, e := h.auth.User(h.token)
	if e != nil {
		t.Fatal(e)
	}
	v, e := gateway.Prepare(ctx, human, m, "plan-protected-transfer", execution.PrepareInput{Kind: "transfer", Parameters: json.RawMessage(fmt.Sprintf(`{"source_account_id":"%d","target_account_id":"%d","amount_minor":2000}`, src.ID, dst.ID))})
	if e != nil {
		t.Fatal(e)
	}
	v, e = gateway.Authorize(ctx, human, m, v.Operation.ID, execution.AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce})
	if e != nil {
		t.Fatal(e)
	}
	out, e := gateway.Execute(ctx, human, m, v.Operation.ID, "plan-execution", execution.ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version})
	if e != nil || out.Operation.ErrorCode != "NECESSARY_RESERVE" {
		t.Fatalf("future income or life floor bypassed: %s / %v", out.Operation.ErrorCode, e)
	}
	h.balance(t, src.ID, 5000)
	h.balance(t, dst.ID, 0)
	var journalCount int64
	if e = h.conn.DB.Model(&entity.Transaction{}).Where("user_id = ? AND transfer_id = ?", h.uid, v.Operation.ResourceID).Count(&journalCount).Error; e != nil || journalCount != 0 {
		t.Fatal("blocked planning transfer posted")
	}
}
