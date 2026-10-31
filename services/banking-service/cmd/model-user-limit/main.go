// Offline create-only user quota provisioning. Never exposed to Agent tools.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/spendconfig"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "user quota provisioning refused; verify operator authority, exact database and immutable quota")
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("model-user-limit", flag.ContinueOnError)
	env := f.String("env-file", "", "protected operator configuration")
	policy := f.String("policy-file", "", "reviewed project policy")
	database := f.String("expected-database", "", "exact target database")
	userRaw := f.String("user", "", "operator-verified Banking user ID")
	limit := f.String("monthly-limit", "", "monthly amount in policy currency, ordinary decimal")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return errors.New("invalid arguments")
	}
	settings, e := godotenv.Read(*env)
	if e != nil {
		return e
	}
	if e = spendconfig.Authorize(settings, os.Getenv("BANKING_MODEL_SPEND_TOKEN")); e != nil {
		return e
	}
	user, e := strconv.ParseInt(*userRaw, 10, 64)
	if e != nil || user <= 0 || strconv.FormatInt(user, 10) != *userRaw {
		return errors.New("invalid user")
	}
	c, e := spendconfig.Load(*env, *policy, *database, false)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, e := spendconfig.Open(ctx, c)
	if e != nil {
		return e
	}
	defer conn.Close()
	ledger, e := modelspend.New(conn.DB, c.Policy)
	if e != nil {
		return e
	}
	if e = ledger.ProvisionUserLimit(ctx, user, *limit); e != nil {
		return e
	}
	fmt.Println("user monthly quota persisted; timezone=Asia/Shanghai; currency=" + c.Policy.Currency)
	return nil
}
