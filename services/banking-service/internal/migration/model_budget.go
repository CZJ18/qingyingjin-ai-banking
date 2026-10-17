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

//go:embed model_budget_018_mysql.sql
var modelBudgetMySQLSQL string

//go:embed model_budget_018_postgres.sql
var modelBudgetPostgresSQL string

func applyModelBudget(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113818)"); e != nil {
			return e
		}
		if e = applyModelBudgetOn(ctx, tx, dialect); e != nil {
			return e
		}
		return tx.Commit()
	}
	c, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	var locked int
	if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_model_budget_018',10)").Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return fmt.Errorf("model budget migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_model_budget_018')")
	return applyModelBudgetOn(ctx, c, dialect)
}
func applyModelBudgetOn(ctx context.Context, c recipientDirectoryMigrationDB, dialect string) error {
	ddl := modelBudgetMySQLSQL
	if dialect == "postgres" {
		ddl = modelBudgetPostgresSQL
	}
	fences := storegate.ModelBudgetFences(dialect)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl+strings.Join(fences, ";\n"))))
	var previous string
	e := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=18").Scan(&previous)
	if e == nil {
		if previous != checksum {
			return fmt.Errorf("model budget migration checksum mismatch")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	for _, statement := range strings.Split(ddl, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, e = c.ExecContext(ctx, statement); e != nil {
				return e
			}
		}
	}
	for _, statement := range fences {
		if _, e = c.ExecContext(ctx, statement); e != nil {
			return e
		}
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(18,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(18,$1)"
	}
	_, e = c.ExecContext(ctx, insert, checksum)
	return e
}
