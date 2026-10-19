// Offline operator-only settlement; never registered as an HTTP/Agent tool.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/modelbudget"
	"github.com/qingyingjin/banking-service/internal/platform"
	"os"
	"strconv"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "model reconciliation refused; check operator authority, scope and settlement evidence")
		os.Exit(1)
	}
}
func run() error {
	env := flag.String("env-file", "", "private operator configuration")
	list := flag.Bool("list-pending", false, "list at most100 pending records")
	user := flag.Int64("user-id", 0, "explicit owner")
	id := flag.String("call-id", "", "reservation ID")
	key := flag.String("idempotency-key", "", "operator request key")
	tokens := flag.String("tokens", "", "independently verified actual token count")
	amount := flag.String("actual-cost", "", "independently verified fixed-decimal expense")
	currency := flag.String("currency", "", "verified currency")
	unit := flag.String("unit", "", "currency_major when verified")
	evidence := flag.String("evidence-ref", "", "nonsecret invoice/log reference")
	inactive := flag.Bool("confirm-no-active-run", false, "operator verified provider run is no longer active")
	database := flag.String("expected-database", "", "refuse mismatched database")
	flag.Parse()
	if *env == "" || godotenv.Load(*env) != nil {
		return fmt.Errorf("operator configuration required")
	}
	expected := os.Getenv("BANKING_MODEL_RECONCILIATION_ADMIN_TOKEN")
	presented := os.Getenv("BANKING_MODEL_RECONCILIATION_TOKEN")
	a, b := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(presented))
	if len(expected) < 32 || len(presented) < 32 || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return fmt.Errorf("operator credential required")
	}
	for _, v := range []string{os.Getenv("BANKING_USER_JWT_SECRET"), os.Getenv("BANKING_TOOL_TOKEN"), os.Getenv("BANKING_COZE_PAT")} {
		if expected == v {
			return fmt.Errorf("separate operator credential required")
		}
	}
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		return e
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if *database == "" || name != *database {
		return fmt.Errorf("database confirmation required")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		return fmt.Errorf("operator database unavailable")
	}
	defer conn.Close()
	admin, e := modelbudget.NewReconciler(conn.DB, expected, os.Getenv("BANKING_MODEL_RECONCILIATION_OPERATOR"))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if *list {
		rows, e := admin.Pending(ctx, presented)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	in := modelbudget.ReconcileInput{UserID: *user, CallID: *id, Currency: *currency, Unit: *unit, EvidenceRef: *evidence, ConfirmNoActiveRun: *inactive}
	if *tokens != "" {
		v, e := strconv.ParseInt(*tokens, 10, 64)
		if e != nil {
			return fmt.Errorf("invalid tokens")
		}
		in.Tokens = &v
	}
	if *amount != "" {
		in.ActualCost = amount
	}
	if e = admin.Reconcile(ctx, presented, *key, in); e != nil {
		return e
	}
	fmt.Println("reconciliation recorded with audit; no model or financial operation dispatched")
	return nil
}
