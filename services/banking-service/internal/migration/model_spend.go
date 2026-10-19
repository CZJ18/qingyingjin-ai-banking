package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	"github.com/qingyingjin/banking-service/internal/storegate"
)

//go:embed model_spend_020_mysql.sql
var modelSpendMySQLSQL string

//go:embed model_spend_020_postgres.sql
var modelSpendPostgresSQL string

func applyModelSpend(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113820)"); err != nil {
			return err
		}
		if err = applyModelSpendOn(ctx, tx, dialect); err != nil {
			return err
		}
		return tx.Commit()
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var locked int
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_model_spend_020',10)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("model spend migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_model_spend_020')")
	return applyModelSpendOn(ctx, c, dialect)
}

func applyModelSpendOn(ctx context.Context, c recipientDirectoryMigrationDB, dialect string) error {
	ddl := modelSpendMySQLSQL
	if dialect == "postgres" {
		ddl = modelSpendPostgresSQL
	}
	fences := storegate.ModelSpendFences(dialect)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl+strings.Join(fences, ";\n"))))
	var previous string
	err := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=20").Scan(&previous)
	if err == nil {
		if previous != checksum {
			return fmt.Errorf("model spend migration checksum mismatch")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	// MySQL DDL auto-commits. Refuse all unregistered existing tables, including
	// partial DDL from an interrupted run. Never infer compatibility from a name,
	// adopt it with IF NOT EXISTS, or remove evidence/data during recovery.
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('banking_model_spend_pools','banking_model_spend_attempts','banking_model_spend_events')"
	if dialect == "postgres" {
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('banking_model_spend_pools','banking_model_spend_attempts','banking_model_spend_events')"
	}
	var existing int64
	if err = c.QueryRowContext(ctx, query).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return fmt.Errorf("unregistered model spend tables exist: manual recovery required before migration 020; no tables changed")
	}
	for _, statement := range strings.Split(ddl, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err = c.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("model spend migration 020 DDL failed; MySQL may require manual recovery: %w", err)
		}
	}
	for _, statement := range fences {
		if _, err = c.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("model spend migration 020 fence failed; MySQL may require manual recovery: %w", err)
		}
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(20,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(20,$1)"
	}
	_, err = c.ExecContext(ctx, insert, checksum)
	return err
}
