package main

import (
	"context"
	"errors"
	"flag"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/modelbudget"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/reminders"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/smsprovider"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"github.com/qingyingjin/banking-service/internal/storegate"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"github.com/qingyingjin/banking-service/internal/taskproof"
	"github.com/qingyingjin/banking-service/internal/userauth"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	envFile := flag.String("env-file", "", "optional env file; process environment takes precedence")
	probe := flag.String("probe", "", "HTTP probe URL for Docker healthcheck")
	flag.Parse()
	if *probe != "" {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(*probe)
		if err != nil {
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if *envFile != "" {
		if err := godotenv.Load(*envFile); err != nil {
			slog.Error("env_file_load_failed")
			os.Exit(1)
		}
	}
	if err := run(); err != nil {
		slog.Error("service_stopped", "reason", err.Error())
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	connections, err := platform.Open(cfg)
	if err != nil {
		return errors.New("connection configuration failed")
	}
	defer connections.Close()
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID)
	var verifier *smsverify.Verifier
	if cfg.SMS.Provider == "development" {
		sender, e := smsprovider.NewDevelopment(connections.Redis, cfg.SMS.OTPSecret)
		if e != nil {
			return errors.New("development verification configuration failed")
		}
		verifier = smsverify.New(connections.Redis, sender, cfg.SMS.OTPSecret)
	}
	accounts := userauth.New(connections.DB, auth, connections.Redis, verifier)
	core := service.New(repository.New(connections.DB), risk.TrustedGateway{})
	parser := &goal.Parser{WorkflowID: cfg.GoalWorkflowID}
	if cfg.CozeBaseURL != "" {
		meter, e := modelbudget.New(connections.DB, modelbudget.Policy{DailyCalls: cfg.ModelDailyCalls, DailyTokens: cfg.ModelDailyTokens, ReservationTokens: cfg.ModelReservationTokens})
		if e != nil {
			return e
		}
		client, e := coze.NewBudgeted(cfg.CozeBaseURL, cfg.CozePAT, cfg.CozeTimeout, meter, cfg.Outbound)
		if e != nil {
			return e
		}
		if e = client.ConfigureReportedCost(cfg.ModelCostCurrency, cfg.ModelCostUnit); e != nil {
			return e
		}
		parser.Runner = client
	}
	goals := goal.NewService(goal.NewRepository(connections.DB), parser, core)
	billingParser := &billing.QueryParser{Runner: parser.Runner, WorkflowID: cfg.BillingWorkflowID}
	bills := billing.New(billing.NewRepository(connections.DB), billingParser)
	subs := subscription.New(repository.New(connections.DB), risk.TrustedGateway{}, subscription.LocalSimulator{})
	if origin := os.Getenv("BANKING_COZE_MODEL_GATEWAY_URL"); origin != "" {
		runner, ok := parser.Runner.(*coze.Client)
		if !ok || runner.ConfigureWorkflowBridge(origin, os.Getenv("BANKING_COZE_MODEL_GATEWAY_TOKEN"), cfg.Outbound) != nil {
			return errors.New("invalid task monetary bridge configuration")
		}
	}
	executionGateway := execution.New(connections.DB, subscription.LocalSimulator{})
	plans := planning.New(connections.DB)
	lives := lifestyle.New(connections.DB)
	lives.Parser = &lifestyle.Parser{Runner: parser.Runner, WorkflowID: cfg.LifestyleGoalWorkflowID}
	var taskRunner orchestrator.WorkflowRunner = parser.Runner
	var directRunner *coze.DirectRunner
	if mode := os.Getenv("BANKING_MODEL_INTEGRATION_MODE"); mode != "" && mode != "coze" {
		if mode != "direct_provider" || cfg.CozeBaseURL != "" || cfg.Outbound.Environment != "test" {
			return errors.New("direct_provider requires isolated test mode without Coze")
		}
		max, e := strconv.ParseInt(os.Getenv("BANKING_DIRECT_MAX_OUTPUT_TOKENS"), 10, 64)
		if e != nil {
			return errors.New("explicit direct token limit required")
		}
		directRunner, e = coze.NewDirectRunner(os.Getenv("BANKING_DIRECT_GATEWAY_ORIGIN"), os.Getenv("BANKING_DIRECT_GATEWAY_TOKEN"), os.Getenv("BANKING_DIRECT_MODEL"), max, cfg.Outbound)
		if e != nil {
			return e
		}
		taskRunner = directRunner
		cfg.SupervisorWorkflowID, e = directSupervisorWorkflow(os.Getenv("BANKING_DIRECT_PLANNER_PROFILE"))
		if e != nil {
			return e
		}
		if cfg.BillingAgentWorkflowID != "" || cfg.SubscriptionAgentWorkflowID != "" || cfg.SavingsAgentWorkflowID != "" || cfg.LifestyleAgentWorkflowID != "" {
			return errors.New("published specialist workflow IDs cannot be used in direct_provider")
		}
		cfg.BillingAgentWorkflowID, e = directBillingWorkflow(os.Getenv("BANKING_DIRECT_BILLING_EXPLANATION"))
		if e != nil {
			return e
		}
		cfg.LifestyleAgentWorkflowID, e = directLifestyleWorkflow(os.Getenv("BANKING_DIRECT_LIFESTYLE_EXPLANATION"))
		if e != nil {
			return e
		}
	}
	executor, e := orchestrator.NewHTTPExecutor(cfg.OrchestratorBaseURL, auth, taskRunner, map[string]string{"lifestyle": cfg.LifestyleAgentWorkflowID, "supervisor": cfg.SupervisorWorkflowID, "billing": cfg.BillingAgentWorkflowID, "subscription": cfg.SubscriptionAgentWorkflowID, "savings": cfg.SavingsAgentWorkflowID}, executionGateway, cfg.Outbound)
	if e != nil {
		return e
	}
	jobs := orchestrator.New(connections.DB, executor, executor)
	bridge := httpapi.NewLangGraphBridge(cfg.LangGraphServiceToken, executor)
	if directRunner != nil {
		bridge.AdmissionSnapshot = directRunner.AdmissionSnapshot
	}
	if client, ok := parser.Runner.(*coze.Client); ok {
		bridge.AdmissionSnapshot = client.AdmissionSnapshot
	}
	if origin := os.Getenv("BANKING_LANGGRAPH_TASK_STATE_URL"); origin != "" {
		bridge.ValidateTask, e = taskproof.New(origin, cfg.LangGraphServiceToken, cfg.Outbound)
		if e != nil {
			return errors.New("invalid task state validation configuration")
		}
	}
	handler := httpapi.NewWithPhoneAuthentication(auth, log, map[string]httpapi.Checker{cfg.DatabaseDriver: connections.CheckDatabase, "redis": connections.CheckRedis, "storage": func(ctx context.Context) error { return storegate.CheckActive(ctx, connections.SQL) }, "schema": func(ctx context.Context) error {
		var version int
		if err := connections.SQL.QueryRowContext(ctx, "SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version != migration.CurrentVersion {
			return errors.New("financial schema version mismatch")
		}
		return nil
	}}, cfg.CheckTimeout, core, goals, bills, subs, plans, jobs, executionGateway, lives, accounts, cfg.AuthCookieSecure, bridge)
	var routed http.Handler = handler
	writeTimeout := cfg.CozeTimeout + 10*time.Second
	if origin := os.Getenv("BANKING_AGENT_PRODUCT_BASE_URL"); origin != "" {
		routed, e = httpapi.NewProductProxy(handler, auth, origin, cfg.Outbound)
		if e != nil {
			return errors.New("invalid product service origin configuration")
		}
		if writeTimeout < 190*time.Second {
			writeTimeout = 190 * time.Second
		}
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: routed, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go plans.Run(ctx, log)
	go jobs.Run(ctx, log)
	go reminders.New(connections.DB).Run(ctx, log, time.Minute)
	go executionGateway.RunScheduled(ctx, log, 2*time.Second)
	done := make(chan error, 1)
	go func() {
		log.Info("server_started", "address", cfg.HTTPAddr, "coze_client_configured", cfg.CozeBaseURL != "", "goal_parser_configured", parser.Configured())
		done <- srv.ListenAndServe()
	}()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
