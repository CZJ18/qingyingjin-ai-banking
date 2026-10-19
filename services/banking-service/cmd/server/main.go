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
	"github.com/qingyingjin/banking-service/internal/userauth"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	executionGateway := execution.New(connections.DB, subscription.LocalSimulator{})
	plans := planning.New(connections.DB)
	lives := lifestyle.New(connections.DB)
	lives.Parser = &lifestyle.Parser{Runner: parser.Runner, WorkflowID: cfg.LifestyleGoalWorkflowID}
	executor, e := orchestrator.NewHTTPExecutor(cfg.OrchestratorBaseURL, auth, parser.Runner, map[string]string{"lifestyle": cfg.LifestyleAgentWorkflowID, "supervisor": cfg.SupervisorWorkflowID, "billing": cfg.BillingAgentWorkflowID, "subscription": cfg.SubscriptionAgentWorkflowID, "savings": cfg.SavingsAgentWorkflowID}, executionGateway, cfg.Outbound)
	if e != nil {
		return e
	}
	jobs := orchestrator.New(connections.DB, executor, executor)
	handler := httpapi.NewWithPhoneAuthentication(auth, log, map[string]httpapi.Checker{cfg.DatabaseDriver: connections.CheckDatabase, "redis": connections.CheckRedis, "storage": func(ctx context.Context) error { return storegate.CheckActive(ctx, connections.SQL) }, "schema": func(ctx context.Context) error {
		var version int
		if err := connections.SQL.QueryRowContext(ctx, "SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version != migration.CurrentVersion {
			return errors.New("financial schema version mismatch")
		}
		return nil
	}}, cfg.CheckTimeout, core, goals, bills, subs, plans, jobs, executionGateway, lives, accounts, cfg.AuthCookieSecure, httpapi.NewLangGraphBridge(cfg.LangGraphServiceToken, executor))
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: cfg.CozeTimeout + 10*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go plans.Run(ctx, log)
	go jobs.Run(ctx, log)
	go reminders.New(connections.DB).Run(ctx, log, time.Minute)
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
