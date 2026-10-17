//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type dockerEvidence struct {
	UserID        int64  `json:"user_id,string"`
	AccountID     int64  `json:"account_id,string"`
	GoalAccountID int64  `json:"goal_account_id,string"`
	PlanID        int64  `json:"plan_id,string"`
	Version       int64  `json:"version"`
	BalanceMinor  int64  `json:"balance_minor"`
	SavedAt       int64  `json:"saved_at"`
	Mode          string `json:"mode"`
}

func dockerHarness(t *testing.T) *coreHarness {
	t.Helper()
	base := os.Getenv("BANKING_TEST_BASE_URL")
	if base == "" {
		t.Skip("set BANKING_TEST_BASE_URL only after actual module6 Docker deployment")
	}
	h := harness(t)
	h.server.Close()
	h.server.URL = base
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: base}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestAdaptiveSavingsDocker(t *testing.T) {
	h := dockerHarness(t)
	a := h.account(t, 100000)
	g := parse[entity.SavingsGoal](t, h.call(t, "POST", "/api/v1/savings/goals", uuid.NewString(), service.GoalInput{Name: "Docker simulation only", TargetMinor: 100000, MonthlyMinor: 70000, StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().AddDate(0, 3, 0).UnixMilli()}, 201))
	settings := planning.Settings{AccountIDs: []int64{a.ID}, SavingsGoalIDs: []int64{g.ID}, LifeGoalIDs: []int64{}, LivingRemainingMinor: cents(30000), EssentialRemainingMinor: cents(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000, FutureIncomeMinor: cents(0), Cashflows: []planning.Cashflow{}}
	v := parse[planning.View](t, h.call(t, "POST", "/api/v1/financial-plans", "", planning.CreateInput{Name: "Live Docker fixture", Settings: settings}, 201))
	if v.Result.SavingsCapMinor != 55000 {
		t.Fatal(v.Result)
	}
	h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 10000, Currency: "CNY", Category: "transport", OccurredAt: time.Now().UnixMilli(), EffectMode: "apply_simulated_effect", ConfirmSimulatedEffect: true}, 201)
	path := fmt.Sprintf("/api/v1/financial-plans/%d", v.Plan.ID)
	deadline := time.Now().Add(30 * time.Second)
	for {
		current := parse[planning.View](t, h.call(t, "GET", path, "", nil, 200))
		if current.Result.BalanceMinor == 90000 && current.Version.Version > v.Version.Version {
			v = current
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background event worker did not replan")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if v.Result.SavingsCapMinor != 45000 || v.Result.SuggestedSavingsMinor != 33750 {
		t.Fatal(v.Result)
	}
	h.balance(t, a.ID, 90000)
	h.balance(t, g.AccountID, 0)
	evidence := dockerEvidence{UserID: h.uid, AccountID: a.ID, GoalAccountID: g.AccountID, PlanID: v.Plan.ID, Version: v.Version.Version, BalanceMinor: 90000, SavedAt: time.Now().UnixMilli(), Mode: "persisted local simulated Docker fixture; no real bank operations"}
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	dir := "../../output/module6"
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "live.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestAdaptiveSavingsDockerPersistence(t *testing.T) {
	h := dockerHarness(t)
	raw, e := os.ReadFile("../../output/module6/live.json")
	if e != nil {
		t.Fatal("run Docker fixture before restart", e)
	}
	var evidence dockerEvidence
	if e = json.Unmarshal(raw, &evidence); e != nil {
		t.Fatal(e)
	}
	h.uid = evidence.UserID
	h.token, e = h.auth.IssueUser(fmt.Sprint(h.uid), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	v := parse[planning.View](t, h.call(t, "GET", fmt.Sprintf("/api/v1/financial-plans/%d", evidence.PlanID), "", nil, 200))
	if v.Version.Version < evidence.Version || v.Result.BalanceMinor != evidence.BalanceMinor {
		t.Fatal(v)
	}
	h.balance(t, evidence.AccountID, evidence.BalanceMinor)
	h.balance(t, evidence.GoalAccountID, 0)
}
