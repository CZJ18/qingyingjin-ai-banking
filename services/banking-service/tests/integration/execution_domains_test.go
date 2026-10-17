//go:build integration

package integration

import (
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestExecutionFinancialDomains(t *testing.T) {
	h := harness(t)
	gateway := execution.New(h.conn.DB, nil)
	r := httpapi.NewWithOrchestration(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, 2*time.Second, h.svc, nil, nil, nil, nil, nil, gateway)
	h.server = httptest.NewServer(r)
	t.Cleanup(h.server.Close)
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: h.server.URL}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	perform := func(kind, key string, p any) execution.View {
		t.Helper()
		raw, _ := json.Marshal(p)
		v := executionView(t, h.call(t, "POST", "/api/v1/execution/operations", key, execution.PrepareInput{Kind: kind, Parameters: raw}, 201))
		id := v.Operation.ID
		v = executionView(t, h.call(t, "POST", "/api/v1/execution/operations/"+id+"/authorize", "", execution.AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce}, 200))
		v = executionView(t, h.call(t, "POST", "/api/v1/execution/operations/"+id+"/execute", key+":exec", execution.ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version}, 200))
		if v.Operation.Status != "succeeded" {
			t.Fatalf("%s failed: %s", kind, v.Operation.ErrorCode)
		}
		return v
	}
	var source entity.BankAccount
	v := perform("open_account", "open", service.AccountInput{DisplayName: "Simulated daily", InitialBalanceMinor: 10000})
	if e = json.Unmarshal(v.Operation.Result, &source); e != nil {
		t.Fatal(e)
	}
	h.balance(t, source.ID, 10000)
	var goal entity.SavingsGoal
	now := time.Now().UnixMilli()
	v = perform("create_savings_goal", "goal", service.GoalInput{Name: "Regular meals and exercise", TargetMinor: 2000, StartsAt: now, EndsAt: now + int64(90*24*time.Hour/time.Millisecond), MonthlyMinor: 1000})
	if e = json.Unmarshal(v.Operation.Result, &goal); e != nil {
		t.Fatal(e)
	}
	perform("savings_deposit", "deposit", execution.SavingsInput{GoalID: goal.ID, Input: service.GoalTransferInput{BankAccountID: source.ID, AmountMinor: 1000}})
	h.balance(t, source.ID, 9000)
	h.balance(t, goal.AccountID, 1000)
	perform("savings_withdrawal", "withdraw", execution.SavingsInput{GoalID: goal.ID, Input: service.GoalTransferInput{BankAccountID: source.ID, AmountMinor: 500}})
	h.balance(t, source.ID, 9500)
	h.balance(t, goal.AccountID, 500)
	var b entity.Budget
	v = perform("create_budget", "budget", service.BudgetInput{Month: time.Now().UTC().Format("2006-01"), TotalMinor: 5000, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 4000, EssentialReserveMinor: 3000}}, EnforceReserve: true})
	if e = json.Unmarshal(v.Operation.Result, &b); e != nil {
		t.Fatal(e)
	}
	v = perform("budget_rule_update", "rule", execution.BudgetRuleInput{BudgetID: b.ID, RuleID: b.Rules[0].ID, ExpectedDigest: entity.CanonicalJSONHash(v.Operation.Result), Enabled: false})
	var updated entity.Budget
	if e = json.Unmarshal(v.Operation.Result, &updated); e != nil || updated.Rules[0].Enabled {
		t.Fatal("budget rule authorization not applied")
	}
	perform("import_transaction", "income", service.TransactionInput{BankAccountID: source.ID, ExternalTransactionID: "gateway-income", Direction: "income", AmountMinor: 200, Category: "salary", MerchantName: "Simulation", OccurredAt: now, EffectMode: "apply_simulated_effect"})
	h.balance(t, source.ID, 9700)
	perform("import_transaction", "expense", service.TransactionInput{BankAccountID: source.ID, ExternalTransactionID: "gateway-expense", Direction: "expense", AmountMinor: 100, Category: "food", MerchantName: "Simulation", OccurredAt: now, EffectMode: "apply_simulated_effect"})
	h.balance(t, source.ID, 9600)
	var current entity.BankAccount
	if e = h.conn.DB.Where("id = ? AND user_id = ?", source.ID, h.uid).First(&current).Error; e != nil {
		t.Fatal(e)
	}
	v = perform("account_status", "freeze", execution.StatusInput{AccountID: source.ID, ExpectedVersion: current.Version, Status: "frozen"})
	if e = json.Unmarshal(v.Operation.Result, &current); e != nil || current.Status != "frozen" {
		t.Fatal("freeze failed")
	}
	perform("account_status", "unfreeze", execution.StatusInput{AccountID: source.ID, ExpectedVersion: current.Version, Status: "active"})
	h.balance(t, source.ID, 9600)
	sub := entity.Subscription{Base: entity.Base{UserID: h.uid}, BankAccountID: source.ID, Fingerprint: "gateway-domain-test", MerchantName: "Simulated sport", Category: "sport", BillingInterval: "monthly", AmountMinor: 100, Currency: "CNY", NextBillingAt: now + int64(24*time.Hour/time.Millisecond), ConfidenceBPS: 9000, Status: "candidate", Version: 1, IsSimulated: true}
	if e = h.conn.DB.Create(&sub).Error; e != nil {
		t.Fatal(e)
	}
	v = perform("subscription_manage", "adopt", execution.ManageInput{SubscriptionID: sub.ID, Input: subscription.ManageInput{ExpectedVersion: 1}})
	if e = json.Unmarshal(v.Operation.Result, &sub); e != nil || sub.Status != "active" {
		t.Fatal("adoption failed")
	}
	perform("cancel_subscription", "cancel", subscription.IntentInput{SubscriptionID: strconv.FormatInt(sub.ID,10), ExpectedVersion: sub.Version})
	if e = h.conn.DB.First(&sub, sub.ID).Error; e != nil || sub.Status != "cancelled" {
		t.Fatal("local cancellation failed")
	}
	h.balance(t, source.ID, 9600)
	var imbalance int64
	if e = h.conn.DB.Raw("SELECT COUNT(*) FROM (SELECT journal_id FROM banking_ledger_entries WHERE user_id = ? GROUP BY journal_id HAVING SUM(delta_minor) <> 0) AS bad", h.uid).Scan(&imbalance).Error; e != nil || imbalance != 0 {
		t.Fatal("unbalanced gateway ledger")
	}
}
