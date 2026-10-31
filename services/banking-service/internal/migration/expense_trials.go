package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/storegate"
	"strings"
)

//go:embed expense_trials_022_postgres.sql
var expenseTrialsPostgresSQL string

//go:embed expense_trials_022_mysql.sql
var expenseTrialsMySQLSQL string

// Additive migration; existing table data, authority and checksums stay intact.
func applyExpenseTrials(ctx context.Context, db *sql.DB, dialect string) error {
	conn, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	if dialect == "postgres" {
		tx, e := conn.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113822)"); e != nil {
			return e
		}
		if e = applyExpenseTrialsOn(ctx, tx, dialect); e != nil {
			return e
		}
		return tx.Commit()
	}
	var locked int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK('banking_expense_trials_022',10)").Scan(&locked); e != nil || locked != 1 {
		return fmt.Errorf("trial migration lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_expense_trials_022')")
	return applyExpenseTrialsOn(ctx, conn, dialect)
}
func applyExpenseTrialsOn(ctx context.Context, conn recipientDirectoryMigrationDB, dialect string) error {
	ddl := expenseTrialsMySQLSQL
	if dialect == "postgres" {
		ddl = expenseTrialsPostgresSQL
	} else {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			ddl += "\n" + storegate.ExpenseTrialMySQLFence(event) + ";"
		}
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl)))
	var old string
	e := conn.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=22").Scan(&old)
	if e == nil {
		if old != sum {
			return fmt.Errorf("trial migration checksum mismatch")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	check := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='banking_expense_trials'"
	if dialect == "postgres" {
		check = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='banking_expense_trials'"
	}
	var existing int
	if e = conn.QueryRowContext(ctx, check).Scan(&existing); e != nil {
		return e
	}
	if existing != 0 {
		return fmt.Errorf("unregistered expense trial table; retain it for manual recovery")
	}
	// PostgreSQL trigger body is an existing reviewed function. MySQL triggers
	// contain semicolons and must be executed as whole statements below.
	base := expenseTrialsMySQLSQL
	if dialect == "postgres" {
		base = expenseTrialsPostgresSQL
	}
	for _, statement := range strings.Split(base, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, e = conn.ExecContext(ctx, statement); e != nil {
				return fmt.Errorf("expense trial migration DDL failed: %w", e)
			}
		}
	}
	if dialect != "postgres" {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, e = conn.ExecContext(ctx, storegate.ExpenseTrialMySQLFence(event)); e != nil {
				return e
			}
		}
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(22,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(22,$1)"
	}
	_, e = conn.ExecContext(ctx, insert, sum)
	return e
}
