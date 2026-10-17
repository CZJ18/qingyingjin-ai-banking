package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed 001_core.sql
var CoreSQL string

//go:embed 002_goals.sql
var GoalsSQL string

//go:embed 003_billing.sql
var BillingSQL string

//go:embed 004_subscriptions.sql
var SubscriptionsSQL string

//go:embed 005_planning.sql
var PlanningSQL string

//go:embed 006_orchestrator.sql
var OrchestratorSQL string

//go:embed 007_execution.sql
var ExecutionSQL string

//go:embed 008_skills.sql
var SkillsSQL string

//go:embed 009_skill_traces.sql
var SkillTracesSQL string

//go:embed 010_lifestyle.sql
var LifestyleSQL string

// Apply uses a dedicated connection because MySQL named locks are connection
// scoped. MySQL DDL auto-commits; interrupted creation is safely rerunnable.
func applyMySQL(ctx context.Context, db *sql.DB) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var acquired int
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('qingyingjin_core_migrations', 10)").Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return fmt.Errorf("migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('qingyingjin_core_migrations')")
	if _, err = c.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS banking_schema_migrations (version INT NOT NULL PRIMARY KEY, checksum CHAR(64) NOT NULL, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`); err != nil {
		return err
	}
	for i, migrationSQL := range []string{CoreSQL, GoalsSQL, BillingSQL, SubscriptionsSQL, PlanningSQL, OrchestratorSQL, ExecutionSQL, SkillsSQL, SkillTracesSQL, LifestyleSQL} {
		version := i + 1
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(migrationSQL)))
		var existing string
		err = c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=?", version).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("migration checksum mismatch")
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		// Reject an unrelated legacy schema; never silently create a zero-balance
		// ledger for pre-existing accounts or mutate the Coze production database.
		var tableCount, columnCount int
		if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='banking_bank_accounts'").Scan(&tableCount); err != nil {
			return err
		}
		if version == 1 && tableCount > 0 {
			if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='banking_bank_accounts' AND column_name IN ('version','is_simulated','savings_goal_id')").Scan(&columnCount); err != nil {
				return err
			}
			if columnCount != 3 {
				return fmt.Errorf("existing legacy accounts require an explicit audited data migration; use independent Banking schema")
			}
		}
		for _, statement := range strings.Split(migrationSQL, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if version == 8 {
				if err = applySkillColumn(ctx, c, statement); err != nil {
					return err
				}
				continue
			}
			if _, err = c.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("core migration: %w", err)
			}
		}
		_, err = c.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(?,?)", version, checksum)
		if err != nil {
			return err
		}
	}
	return nil
}

func applySkillColumn(ctx context.Context, c *sql.Conn, statement string) error {
	parts := strings.Fields(statement)
	if len(parts) != 11 || parts[0] != "ALTER" || parts[3] != "ADD" || parts[4] != "COLUMN" {
		return fmt.Errorf("invalid skill migration statement")
	}
	var count int
	if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", parts[2], parts[5]).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := c.ExecContext(ctx, statement)
	return err
}
