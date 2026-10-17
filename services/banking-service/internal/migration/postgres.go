package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

// PostgreSQL baselines are intentionally separate from immutable MySQL DDL.
// Existing MySQL migration checksums therefore remain unchanged.
//
//go:embed postgres_001_core.sql
var postgresCoreSQL string

//go:embed postgres_002_goals.sql
var postgresGoalsSQL string

//go:embed postgres_003_billing.sql
var postgresBillingSQL string

//go:embed postgres_004_subscriptions.sql
var postgresSubscriptionsSQL string

//go:embed postgres_005_planning.sql
var postgresPlanningSQL string

//go:embed postgres_006_orchestrator.sql
var postgresOrchestratorSQL string

//go:embed postgres_007_execution.sql
var postgresExecutionSQL string

//go:embed postgres_008_skills.sql
var postgresSkillsSQL string

//go:embed postgres_009_skill_traces.sql
var postgresSkillTracesSQL string

//go:embed postgres_010_lifestyle.sql
var postgresLifestyleSQL string

func postgresBaselines() []string {
	return []string{postgresCoreSQL, postgresGoalsSQL, postgresBillingSQL, postgresSubscriptionsSQL, postgresPlanningSQL, postgresOrchestratorSQL, postgresExecutionSQL, postgresSkillsSQL, postgresSkillTracesSQL, postgresLifestyleSQL}
}

// ApplyPostgres atomically creates or upgrades the independent Banking schema.
// An advisory transaction lock serializes runners, and PostgreSQL rolls back all
// DDL and migration records if any statement fails. It never adopts an unrelated
// existing database or rewrites an applied baseline.
func ApplyPostgres(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); err != nil {
		return err
	}
	// A fixed application namespace, not a user-provided lock identifier.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(81813782903778837)"); err != nil {
		return fmt.Errorf("PostgreSQL migration lock unavailable: %w", err)
	}
	var serverVersion int
	if err = tx.QueryRowContext(ctx, "SHOW server_version_num").Scan(&serverVersion); err != nil {
		return err
	}
	if serverVersion < 140000 {
		return fmt.Errorf("Banking PostgreSQL requires version 14 or later with ICU")
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS banking_schema_migrations (version INT NOT NULL PRIMARY KEY, checksum VARCHAR(64) COLLATE "C" NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	var applied int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_schema_migrations").Scan(&applied); err != nil {
		return err
	}
	if applied == 0 {
		var existingTables int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' AND table_name<>'banking_schema_migrations'").Scan(&existingTables); err != nil {
			return err
		}
		if existingTables != 0 {
			return fmt.Errorf("existing tables require an explicit audited migration; use an independent empty Banking PostgreSQL schema")
		}
	}
	for i, baseline := range postgresBaselines() {
		version := i + 1
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(baseline)))
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=$1", version).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("PostgreSQL migration %d checksum mismatch", version)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		// A missing earlier record must never silently adopt a partial baseline.
		if version <= applied {
			return fmt.Errorf("PostgreSQL migration history is not contiguous at version %d", version)
		}
		if _, err = tx.ExecContext(ctx, baseline); err != nil {
			return fmt.Errorf("PostgreSQL migration %d: %w", version, err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES($1,$2)", version, checksum); err != nil {
			return err
		}
	}
	var correctCollation bool
	if err = tx.QueryRowContext(ctx, `SELECT c.collprovider='i' AND NOT c.collisdeterministic AND COALESCE(to_jsonb(c)->>'colllocale',to_jsonb(c)->>'colliculocale')='und-u-ks-level1' FROM pg_collation c WHERE c.oid='qyj_mysql_ci'::regcollation`).Scan(&correctCollation); err != nil {
		return err
	}
	if !correctCollation {
		return fmt.Errorf("Banking PostgreSQL requires its reviewed case/accent insensitive ICU collation")
	}
	return tx.Commit()
}

// PostgresSchemaTables returns baseline business tables in FK-safe creation
// order. Migration metadata, storage fencing and application views are excluded.
func PostgresSchemaTables() []string {
	var tables []string
	for _, baseline := range postgresBaselines() {
		for _, line := range strings.Split(baseline, "\n") {
			const prefix = "CREATE TABLE IF NOT EXISTS "
			if strings.HasPrefix(line, prefix) {
				tables = append(tables, strings.Fields(strings.TrimPrefix(line, prefix))[0])
			}
		}
	}
	return tables
}
