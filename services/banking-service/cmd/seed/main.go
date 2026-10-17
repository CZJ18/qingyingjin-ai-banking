package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"log"
	"os"
	"strconv"
	"time"
)

// Seed invokes the same application/ledger paths as HTTP. It never inserts a
// balance directly. Stable keys make repeated initialization safe.
func main() {
	env := flag.String("env-file", "", "configuration file")
	uid := flag.String("user-id", "42", "trusted simulated user ID")
	confirm := flag.Bool("confirm-simulated-data", false, "explicit operator confirmation to create simulated test data")
	flag.Parse()
	if !*confirm {
		log.Fatal("pass -confirm-simulated-data to explicitly authorize simulated test initialization")
	}
	if *env != "" {
		if err := godotenv.Load(*env); err != nil {
			log.Fatal("cannot load env file")
		}
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if !config.PositiveID(*uid) {
		log.Fatal("invalid user ID")
	}
	user, _ := strconv.ParseInt(*uid, 10, 64)
	conn, err := platform.Open(cfg)
	if err != nil {
		log.Fatal("cannot open database")
	}
	defer conn.Close()
	s := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	m := service.Meta{UserID: user, TraceID: "module2-seed"}
	ctx := context.Background()
	if _, err = s.Register(ctx, m, "轻盈金模拟用户"); err != nil {
		log.Fatal(err)
	}
	a, err := s.CreateAccount(ctx, m, "module2-demo-account-v1", service.AccountInput{DisplayName: "模拟日常账户", InitialBalanceMinor: 500000, Currency: "CNY", ConfirmSimulatedFunds: true})
	if err != nil {
		log.Fatal(err)
	}
	// Fixed historical transaction dates are explicit fixture inputs, not
	// fabricated query results. Queries always read persisted MySQL facts.
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, v := range []struct {
		direction, category, merchant string
		amount                        int64
	}{{"income", "salary", "模拟薪资", 100000}, {"expense", "food", "模拟日常餐食", 18000}, {"expense", "exercise", "模拟运动场馆", 6000}} {
		_, err = s.ImportTransaction(ctx, m, fmt.Sprintf("module2-demo-transaction-v1-%d", i), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: fmt.Sprintf("module2-demo-external-v1-%d", i), Direction: v.direction, AmountMinor: v.amount, Currency: "CNY", Category: v.category, MerchantName: v.merchant, OccurredAt: start.Add(time.Duration(i+1) * 24 * time.Hour).UnixMilli(), EffectMode: "apply_simulated_effect", ConfirmSimulatedEffect: true})
		if err != nil {
			log.Fatal(err)
		}
	}
	b, err := s.CreateBudget(ctx, m, "module2-demo-budget-v1", service.BudgetInput{Month: "2026-10", TotalMinor: 300000, Currency: "CNY", Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 100000, EssentialReserveMinor: 60000}, {Category: "exercise", AllocatedMinor: 40000, EssentialReserveMinor: 10000}, {Category: "housing", AllocatedMinor: 160000, EssentialReserveMinor: 160000}}, EnforceReserve: true})
	if err != nil {
		log.Fatal(err)
	}
	g, err := s.CreateGoal(ctx, m, "module2-demo-savings-v1", service.GoalInput{Name: "规律饮食与合理运动", TargetMinor: 120000, Currency: "CNY", StartsAt: start.UnixMilli(), EndsAt: start.AddDate(1, 0, 0).UnixMilli(), MonthlyMinor: 10000})
	if err != nil {
		log.Fatal(err)
	}
	balance, err := s.Balance(ctx, user, a.ID)
	if err != nil {
		log.Fatal(err)
	}
	result := struct {
		UserID       string `json:"user_id"`
		AccountID    string `json:"account_id"`
		BudgetID     string `json:"budget_id"`
		GoalID       string `json:"goal_id"`
		BalanceMinor int64  `json:"balance_minor"`
		Reconciled   bool   `json:"reconciled"`
	}{*uid, fmt.Sprint(a.ID), fmt.Sprint(b.ID), fmt.Sprint(g.ID), balance.BalanceMinor, balance.Reconciled}
	data, _ := json.Marshal(result)
	fmt.Println(string(data))
}
