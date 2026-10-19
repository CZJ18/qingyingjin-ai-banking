// model-gateway owns the supplier credential and monetary admission boundary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/spendconfig"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "model gateway refused; verify protected configuration, budget policy and database schema")
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("model-gateway", flag.ContinueOnError)
	env := f.String("env-file", "", "protected gateway-only configuration")
	policy := f.String("policy-file", "", "independently verified model price and cumulative limit policy")
	database := f.String("expected-database", "", "explicit exact database scope")
	addr := f.String("listen", "127.0.0.1:18160", "private listener")
	cert, key := f.String("tls-cert", "", "listener certificate"), f.String("tls-key", "", "listener private key")
	probe := f.String("probe", "", "probe private loopback readiness without loading credentials")
	priceEvidence := f.String("price-evidence-file", "", "independent reviewed provider price/bounds evidence")
	priceReviewed := f.Bool("confirm-price-review", false, "operator has independently confirmed provider price and bounds")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return errors.New("invalid gateway flags")
	}
	if *probe != "" {
		return checkProbe(*probe)
	}
	c, e := spendconfig.Load(*env, *policy, *database, true)
	if e != nil {
		return e
	}
	if c.Env["BANKING_MODEL_SPEND_ADMIN_TOKEN"] != "" || c.Env["BANKING_MODEL_SPEND_OPERATOR"] != "" {
		return errors.New("operator authority must not enter model gateway")
	}
	if e = spendconfig.ValidateListener(c.Outbound.Environment, *addr, *cert, *key); e != nil {
		return e
	}
	timeout := 90 * time.Second
	if raw := c.Env["BANKING_MODEL_GATEWAY_TIMEOUT"]; raw != "" {
		timeout, e = time.ParseDuration(raw)
		if e != nil || timeout < time.Second || timeout > 3*time.Minute {
			return errors.New("invalid supplier timeout")
		}
	}
	gc := modelgateway.Config{Policy: c.Policy, GatewayToken: c.Env["BANKING_MODEL_GATEWAY_TOKEN"], SupplierKey: c.Env["BANKING_MODEL_SUPPLIER_API_KEY"], BaseURL: c.Env["BANKING_MODEL_SUPPLIER_BASE_URL"], Timeout: timeout, Outbound: c.Outbound}
	// Only explicit loopback fake suppliers in test mode skip paid-price review.
	parsed, parseError := url.Parse(gc.BaseURL)
	if parseError != nil {
		return errors.New("invalid supplier origin")
	}
	testIP := net.ParseIP(parsed.Hostname())
	if !(c.Outbound.Environment == "test" && testIP != nil && testIP.IsLoopback()) {
		file, e := os.Open(*priceEvidence)
		if e != nil {
			return errors.New("reviewed price evidence required")
		}
		raw, e := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		file.Close()
		if e != nil || spendconfig.ReviewPrice(c.Policy, raw, *priceReviewed) != nil {
			return errors.New("reviewed price evidence required")
		}
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
	h, e := modelgateway.New(gc, ledger)
	if e != nil {
		return e
	}
	baseHandler := h
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
			check, done := context.WithTimeout(r.Context(), time.Second)
			defer done()
			if spendconfig.Check(check, c, conn) != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		baseHandler.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: timeout + 10*time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	go func() {
		<-stop.Done()
		end, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(end)
	}()
	if *cert != "" {
		e = server.ListenAndServeTLS(*cert, *key)
	} else {
		e = server.ListenAndServe()
	}
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func checkProbe(target string) error {
	u, e := url.Parse(target)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/readyz" || u.RawPath != "" || u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("invalid readiness target")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("readiness must be loopback")
	}
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Get(target)
	if e != nil {
		return errors.New("gateway unready")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("gateway unready")
	}
	return nil
}
