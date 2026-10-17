package risk_test

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
)

// A successful trusted guard means persisted consent was validated. The nil
// repository stops the test at the subsequent persistence boundary.
type authorizedGuard struct{ risk.SimulationGateway }

func (authorizedGuard) SensitiveCheck(context.Context, int64, string, any) error { return nil }
func TestPersistedConsentDoesNotRequireLegacyBoolean(t *testing.T) {
	core := service.New(nil, authorizedGuard{})
	reachedRepository := false
	func() {
		defer func() {
			if recover() != nil {
				reachedRepository = true
			}
		}()
		_, err := core.CreateAccount(context.Background(), service.Meta{UserID: 42}, "key", service.AccountInput{DisplayName: "approved", InitialBalanceMinor: 100})
		if err != nil {
			t.Errorf("trusted consent rejected by legacy boolean: %v", err)
		}
	}()
	if !reachedRepository {
		t.Error("persisted consent did not reach account persistence")
	}
}

func TestSensitiveServiceRejectsBeforeRepositoryAccess(t *testing.T) {
	core := service.New(nil, risk.TrustedGateway{})
	ctx := context.Background()
	m := service.Meta{UserID: 42}
	for name, call := range map[string]func() error{
		"open_account": func() error {
			_, e := core.CreateAccount(ctx, m, "key", service.AccountInput{DisplayName: "test"})
			return e
		},
		"import_transaction":  func() error { _, e := core.ImportTransaction(ctx, m, "key", service.TransactionInput{}); return e },
		"account_status":      func() error { return core.SetAccountStatus(ctx, m, 1, "active") },
		"create_budget":       func() error { _, e := core.CreateBudget(ctx, m, "key", service.BudgetInput{}); return e },
		"create_savings_goal": func() error { _, e := core.CreateGoal(ctx, m, "key", service.GoalInput{}); return e },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() != nil {
					t.Error("guard reached repository")
				}
			}()
			if e := call(); entity.Code(e) != "APPROVAL_REQUIRED" {
				t.Errorf("missing authorization rejection: %v", e)
			}
		})
	}
}
