package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed card_portfolio_014.sql
var cardPortfolioSQL string

type cardMigrationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func applyCardPortfolio(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113814)"); e != nil {
			return e
		}
		if e = applyCardPortfolioOn(ctx, tx, dialect); e != nil {
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
	if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_cards_014',10)").Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return fmt.Errorf("card migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_cards_014')")
	return applyCardPortfolioOn(ctx, c, dialect)
}
func applyCardPortfolioOn(ctx context.Context, c cardMigrationDB, dialect string) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(cardPortfolioSQL)))
	var existing string
	e := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=14").Scan(&existing)
	if e == nil {
		if existing != checksum {
			return fmt.Errorf("card migration checksum mismatch")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	q := "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='banking_users' AND column_name='card_portfolio'"
	if dialect == "postgres" {
		q = "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_users' AND column_name='card_portfolio'"
	}
	var dataType, nullable string
	var def sql.NullString
	e = c.QueryRowContext(ctx, q).Scan(&dataType, &nullable, &def)
	if e == sql.ErrNoRows {
		if _, e = c.ExecContext(ctx, cardPortfolioSQL); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else if dataType != "text" || nullable != "YES" || def.Valid {
		return fmt.Errorf("incompatible card portfolio column")
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(14,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(14,$1)"
	}
	_, e = c.ExecContext(ctx, insert, checksum)
	return e
}
