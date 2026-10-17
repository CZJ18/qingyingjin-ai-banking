package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed recipients_017_mysql.sql
var recipientMySQLSQL string

//go:embed recipients_017_postgres.sql
var recipientPostgresSQL string

type recipientDirectoryMigrationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func applyRecipientDirectory(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113817)"); e != nil {
			return e
		}
		if e = applyRecipientDirectoryOn(ctx, tx, dialect); e != nil {
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
	if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_recipients_017',10)").Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return fmt.Errorf("recipientDirectory migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_recipients_017')")
	return applyRecipientDirectoryOn(ctx, c, dialect)
}
func applyRecipientDirectoryOn(ctx context.Context, c recipientDirectoryMigrationDB, dialect string) error {
	ddl := recipientMySQLSQL
	if dialect == "postgres" {
		ddl = recipientPostgresSQL
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl)))
	var existing string
	e := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=17").Scan(&existing)
	if e == nil {
		if existing != checksum {
			return fmt.Errorf("recipientDirectory migration checksum mismatch")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	q := "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='banking_users' AND column_name='transfer_directory'"
	if dialect == "postgres" {
		q = "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_users' AND column_name='transfer_directory'"
	}
	var dataType, nullable string
	var def sql.NullString
	e = c.QueryRowContext(ctx, q).Scan(&dataType, &nullable, &def)
	if e == sql.ErrNoRows {
		if _, e = c.ExecContext(ctx, ddl); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else if (dialect == "postgres" && dataType != "text" || dialect == "mysql" && dataType != "mediumtext") || nullable != "YES" || def.Valid {
		return fmt.Errorf("incompatible recipientDirectory notices column")
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(17,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(17,$1)"
	}
	_, e = c.ExecContext(ctx, insert, checksum)
	return e
}
