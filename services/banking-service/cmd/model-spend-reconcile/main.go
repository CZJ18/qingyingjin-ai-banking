// Offline bill import. This command is never a financial API or Agent tool.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/spendconfig"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "model bill import refused; any earlier committed row remains auditable; retry identical reviewed file safely")
		os.Exit(1)
	}
}
func readBounded(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("file unavailable")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(raw)) > max {
		return nil, errors.New("file limit exceeded")
	}
	return raw, nil
}
func run(args []string) error {
	f := flag.NewFlagSet("model-spend-reconcile", flag.ContinueOnError)
	env := f.String("env-file", "", "protected operator-only configuration")
	policy := f.String("policy-file", "", "original monetary policy snapshot")
	database := f.String("expected-database", "", "exact database confirmation")
	list := f.Bool("list-pending", false, "show at most100 unresolved project attempts without model responses")
	bill := f.String("bill-file", "", "normalized independently reviewed bill JSON")
	evidence := f.String("evidence-file", "", "independent supplier evidence file (SHA256 must match bill)")
	reviewed := f.Bool("confirm-bill-review", false, "operator has independently checked attribution and settled amount")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return errors.New("invalid operator flags")
	}
	// Authenticate before opening any SQL connection, even when listing pending.
	settings, e := godotenv.Read(*env)
	if e != nil {
		return errors.New("protected operator configuration required")
	}
	presented := os.Getenv("BANKING_MODEL_SPEND_TOKEN")
	if e = spendconfig.Authorize(settings, presented); e != nil {
		return e
	}
	c, e := spendconfig.Load(*env, *policy, *database, false)
	if e != nil {
		return e
	}
	var rows []modelspend.Settlement
	if *list {
		if *bill != "" || *evidence != "" || *reviewed {
			return errors.New("ambiguous operator action")
		}
	} else {
		if !*reviewed || *bill == "" || *evidence == "" {
			return errors.New("independent reviewed evidence required")
		}
		source, e := readBounded(*evidence, 16<<20)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(source)
		raw, e := readBounded(*bill, 512<<10)
		if e != nil {
			return e
		}
		rows, e = modelspend.ParseBill(raw, hex.EncodeToString(digest[:]))
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.ProjectID != c.Policy.ProjectID || r.Currency != c.Policy.Currency {
				return errors.New("bill scope mismatch")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, e := spendconfig.Open(ctx, c)
	if e != nil {
		return e
	}
	defer conn.Close()
	s, e := modelspend.New(conn.DB, c.Policy)
	if e != nil {
		return e
	}
	admin, e := modelspend.NewReconciler(s, settings["BANKING_MODEL_SPEND_ADMIN_TOKEN"], settings["BANKING_MODEL_SPEND_OPERATOR"])
	if e != nil {
		return e
	}
	if *list {
		pending, e := admin.Pending(ctx, presented)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(pending)
	}
	// Each row is atomic with its own immutable audit; output committed progress.
	// A later rejected row never disguises earlier commits as rolled back.
	for i, r := range rows {
		request := sha256.Sum256([]byte(r.ProjectID + "\x00" + r.BillID + "\x00" + r.LineID))
		result, e := admin.Reconcile(ctx, presented, hex.EncodeToString(request[:]), r)
		if e != nil {
			return e
		}
		if e = json.NewEncoder(os.Stdout).Encode(struct {
			Row    int                         `json:"row"`
			Result modelspend.SettlementResult `json:"result"`
		}{i + 1, result}); e != nil {
			return e
		}
	}
	return nil
}
