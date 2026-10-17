//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecutionReviewEssentialExpense(t *testing.T) {
	for _, tc := range []struct {
		name, category string
		previousMonth  bool
		want           string
	}{
		{"NormalFood", "food", false, "succeeded"},
		{"ShoppingCannotSpendFoodReserve", "shopping", false, "failed"},
		{"PastFoodCannotReleaseCurrentReserve", "food", true, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := harness(t)
			a := h.account(t, 10000)
			now := time.Now().UTC()
			_, err := h.svc.CreateBudget(context.Background(), service.Meta{UserID: h.uid}, uuid.NewString(), service.BudgetInput{Month: now.Format("2006-01"), TotalMinor: 10000, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 10000, EssentialReserveMinor: 10000}}, EnforceReserve: true})
			if err != nil {
				t.Fatal(err)
			}
			installExecutionHTTP(t, h, subscription.LocalSimulator{})
			if os.Getenv("BANKING_TEST_BASE_URL") != "" {
				useExecutionLive(t, h)
			}
			occurred := now.UnixMilli()
			if tc.previousMonth {
				occurred = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Hour).UnixMilli()
			}
			v := execAuthorize(t, h, execPrepare(t, h, "import_transaction", service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 100, Category: tc.category, MerchantName: "Local simulation", OccurredAt: occurred, EffectMode: "apply_simulated_effect"}))
			out := execRun(t, h, v, uuid.NewString())
			if out.Operation.Status != tc.want {
				t.Fatalf("status=%s code=%s, want=%s", out.Operation.Status, out.Operation.ErrorCode, tc.want)
			}
			balance := int64(10000)
			if tc.want == "succeeded" {
				balance -= 100
			} else if out.Operation.ErrorCode != "NECESSARY_RESERVE" {
				t.Fatal(out.Operation.ErrorCode)
			}
			h.balance(t, a.ID, balance)
			var count int64
			if err = h.conn.DB.Model(&entity.Transaction{}).Where("user_id = ? AND direction = ?", h.uid, "expense").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			expected := int64(0)
			if tc.want == "succeeded" {
				expected = 1
			}
			if count != expected {
				t.Fatalf("expense rows=%d want=%d", count, expected)
			}
			if tc.want == "failed" {
				var denied int64
				if err = h.conn.DB.Model(&entity.RiskEvent{}).Where("operation_id = ? AND decision = ? AND error_code = ?", out.Operation.ID, "deny", "NECESSARY_RESERVE").Count(&denied).Error; err != nil || denied == 0 {
					t.Fatalf("missing reserve refusal event: %v", err)
				}
			}
		})
	}
}

func TestExecutionReviewDeniedAuditBinding(t *testing.T) {
	h := harness(t)
	installExecutionHTTP(t, h, subscription.LocalSimulator{})
	if os.Getenv("BANKING_TEST_BASE_URL") != "" {
		useExecutionLive(t, h)
	}
	v := execPrepare(t, h, "open_account", service.AccountInput{DisplayName: "Audited intent"})
	path := "/api/v1/execution/operations/" + v.Operation.ID + "/authorize"
	h.call(t, "POST", path, "", map[string]any{"confirmed": true}, 400)
	taskID := uuid.NewString()
	agent, err := h.auth.IssueAgent(fmt.Sprint(h.uid), "savings", taskID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := h.raw("POST", path, "", map[string]any{}, agent)
	if err != nil || status != 403 {
		t.Fatalf("agent denial %d %v", status, err)
	}
	var agentAudit entity.AuditLog
	if err = h.conn.DB.Where("operation_id = ? AND agent = ? AND error_code = ?", v.Operation.ID, "savings", "FORBIDDEN").First(&agentAudit).Error; err != nil || agentAudit.TaskID != taskID {
		t.Fatalf("unsigned task correlation: %s %v", agentAudit.TaskID, err)
	}
	for _, code := range []string{"INVALID_ARGUMENT", "FORBIDDEN"} {
		var count int64
		if err = h.conn.DB.Model(&entity.AuditLog{}).Where("user_id = ? AND operation_id = ? AND error_code = ?", h.uid, v.Operation.ID, code).Count(&count).Error; err != nil || count == 0 {
			t.Fatalf("missing scoped audit %s: %v", code, err)
		}
	}
}

func TestExecutionReviewPrepareValidation(t *testing.T) {
	h := harness(t)
	a := h.account(t, 1000)
	installExecutionHTTP(t, h, subscription.LocalSimulator{})
	if os.Getenv("BANKING_TEST_BASE_URL") != "" {
		useExecutionLive(t, h)
	}
	now := time.Now().UnixMilli()
	cases := []struct {
		kind string
		p    any
	}{
		{"create_budget", service.BudgetInput{Month: "0001-01", TotalMinor: 100, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 100}}}},
		{"create_budget", service.BudgetInput{Month: "2026-10", TotalMinor: 100, Categories: []service.CategoryInput{{Category: strings.Repeat("x", 65), AllocatedMinor: 100}}}},
		{"create_savings_goal", service.GoalInput{Name: strings.Repeat("x", 129), TargetMinor: 100, MonthlyMinor: 10, StartsAt: now, EndsAt: now + 1000}},
		{"create_savings_goal", service.GoalInput{Name: "Too long", TargetMinor: 100, MonthlyMinor: 10, StartsAt: now, EndsAt: now + int64(11*365*24*time.Hour/time.Millisecond)}},
		{"import_transaction", service.TransactionInput{BankAccountID: a.ID, Direction: "income", AmountMinor: 100, Category: "salary", OccurredAt: now, EffectMode: "record_only"}},
	}
	for _, tc := range cases {
		h.call(t, "POST", "/api/v1/execution/operations", uuid.NewString(), map[string]any{"kind": tc.kind, "parameters": tc.p}, 400)
	}
	var count int64
	if err := h.conn.DB.Model(&entity.OperationIntent{}).Where("user_id = ?", h.uid).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid intents persisted: %d %v", count, err)
	}
}
