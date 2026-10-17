// storage-control is operator-only. It is deliberately absent from REST/Tools.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/storage"
	"github.com/qingyingjin/banking-service/internal/storegate"
	"os"
	"strings"
	"time"
)

type output struct {
	Mode         string          `json:"command"`
	State        storegate.State `json:"state"`
	Verification *storage.Report `json:"verification,omitempty"`
}

func open(path string) (config.Config, *platform.Connections, error) {
	env, err := godotenv.Read(path)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("private environment file unavailable")
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		return cfg, nil, err
	}
	cfg.CheckTimeout = max(cfg.CheckTimeout, 15*time.Second)
	c, err := platform.Open(cfg)
	return cfg, c, err
}

func coordination(ctx context.Context, db *sql.DB) (func(), error) {
	c, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var locked int
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('qingrongjin_financial_authority',10)").Scan(&locked); err != nil || locked != 1 {
		c.Close()
		return nil, fmt.Errorf("authority coordinator unavailable")
	}
	return func() {
		c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('qingrongjin_financial_authority')")
		c.Close()
	}, nil
}

func empty(ctx context.Context, db *sql.DB) error {
	for _, table := range storage.Tables {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if table == "banking_risk_rules" && count <= 6 {
			continue
		}
		if count != 0 {
			return fmt.Errorf("target has business data; cannot initialize it as empty")
		}
	}
	return nil
}

func execute(ctx context.Context, mode, path, peerPath string) (output, error) {
	out := output{Mode: mode}
	cfg, c, err := open(path)
	if err != nil {
		return out, err
	}
	defer c.Close()
	if mode == "status" {
		out.State, err = storegate.Status(ctx, c.SQL)
		return out, err
	}
	if mode == "init-test" {
		name := cfg.MySQLDatabase
		if cfg.DatabaseDriver == "postgres" {
			name = cfg.PostgresDatabase
		}
		if !strings.HasSuffix(name, "_test") {
			return out, fmt.Errorf("test bootstrap requires an isolated _test database")
		}
		if err = empty(ctx, c.SQL); err != nil {
			return out, err
		}
		s, e := storegate.Status(ctx, c.SQL)
		if e != nil {
			return out, e
		}
		if s.Mode == "active" {
			out.State = s
			return out, nil
		}
		out.State, err = storegate.Change(ctx, c.SQL, cfg.DatabaseDriver, s.Mode, "active", fmt.Sprintf("%x", sha256.Sum256([]byte("isolated-test-bootstrap:"+name))))
		return out, err
	}
	if mode == "prepare-target" {
		if err = empty(ctx, c.SQL); err != nil {
			return out, err
		}
		s, e := storegate.Status(ctx, c.SQL)
		if e != nil {
			return out, e
		}
		if s.Mode == "standby" {
			out.State = s
			return out, nil
		}
		if s.Mode == "active" {
			if _, err = storegate.Change(ctx, c.SQL, cfg.DatabaseDriver, "active", "frozen", ""); err != nil {
				return out, err
			}
		}
		// The first emptiness check can overlap a final active writer. Freeze
		// drains its transaction; check again before treating this store as empty.
		if err = empty(ctx, c.SQL); err != nil {
			return out, err
		}
		out.State, err = storegate.Change(ctx, c.SQL, cfg.DatabaseDriver, "frozen", "standby", "")
		return out, err
	}
	if mode == "freeze" {
		// Freezing can only reduce authority. Activation additionally holds the
		// peer row, so this operation cannot create two active databases.
		out.State, err = storegate.Change(ctx, c.SQL, cfg.DatabaseDriver, "active", "frozen", "")
		return out, err
	}
	if mode != "activate" && mode != "rollback" {
		return out, fmt.Errorf("unsupported storage command")
	}
	if peerPath == "" {
		return out, fmt.Errorf("verified peer environment required")
	}
	pcfg, peer, err := open(peerPath)
	if err != nil {
		return out, err
	}
	defer peer.Close()
	if pcfg.DatabaseDriver == cfg.DatabaseDriver {
		return out, fmt.Errorf("P7 authority changes require distinct MySQL/PostgreSQL stores")
	}
	anchor := c.SQL
	if pcfg.DatabaseDriver == "mysql" {
		anchor = peer.SQL
	}
	release, err := coordination(ctx, anchor)
	if err != nil {
		return out, err
	}
	defer release()
	peerLock, err := peer.SQL.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer peerLock.Rollback()
	var peerMode string
	if err = peerLock.QueryRowContext(ctx, "SELECT mode FROM banking_storage_control WHERE id=1 FOR SHARE").Scan(&peerMode); err != nil {
		return out, err
	}
	if peerMode != "frozen" {
		return out, fmt.Errorf("peer must stay frozen until activation commits")
	}
	// Capture the target generation before reading its snapshot. A completed
	// import advances it, so an import racing the comparison cannot activate a
	// different snapshot under this report's digest.
	s, err := storegate.Status(ctx, c.SQL)
	if err != nil {
		return out, err
	}
	if s.Mode != "standby" && s.Mode != "frozen" {
		return out, fmt.Errorf("target is not standby or frozen")
	}
	verification, err := storage.Compare(ctx, peer.SQL, c.SQL, pcfg.DatabaseDriver, cfg.DatabaseDriver)
	out.Verification = &verification
	if err != nil {
		return out, err
	}
	if !verification.Passed {
		return out, fmt.Errorf("snapshot mismatch; stale store cannot become active")
	}
	out.State, err = storegate.Change(ctx, c.SQL, cfg.DatabaseDriver, s.Mode, "active", verification.Digest, s.Generation)
	return out, err
}

func main() {
	path := flag.String("env-file", "", "private operator database configuration")
	peer := flag.String("peer-env", "", "frozen peer configuration for activation/rollback")
	mode := flag.String("mode", "status", "status|freeze|prepare-target|init-test|activate|rollback")
	report := flag.String("output", "", "optional non-secret result file")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := execute(ctx, *mode, *path, *peer)
	if *report != "" {
		raw, _ := json.MarshalIndent(out, "", "  ")
		if e := os.WriteFile(*report, append(raw, '\n'), 0600); e != nil {
			fmt.Fprintln(os.Stderr, "cannot write control report")
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "storage authority command failed; source was not automatically unfrozen; inspect states and any verification report")
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
