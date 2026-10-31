package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed birthdays_023_mysql.sql
var birthdayMySQLSQL string

//go:embed birthdays_023_postgres.sql
var birthdayPostgresSQL string

type birthdayMigrationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func applyBirthdays(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113823)"); e != nil {
			return e
		}
		if e = applyBirthdaysOn(ctx, tx, dialect); e != nil {
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
	if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_birthdays_023',10)").Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return fmt.Errorf("birthdayCalendar migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_birthdays_023')")
	return applyBirthdaysOn(ctx, c, dialect)
}
func applyBirthdaysOn(ctx context.Context, c birthdayMigrationDB, dialect string) error {
	ddl := birthdayMySQLSQL
	if dialect == "postgres" {
		ddl = birthdayPostgresSQL
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl)))
	var existing string
	e := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=23").Scan(&existing)
	if e == nil {
		if existing != checksum {
			return fmt.Errorf("birthdayCalendar migration checksum mismatch")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	q := "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='banking_users' AND column_name='birthday_calendar'"
	if dialect == "postgres" {
		q = "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_users' AND column_name='birthday_calendar'"
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
		return fmt.Errorf("incompatible birthdayCalendar notices column")
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(23,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(23,$1)"
	}
	_, e = c.ExecContext(ctx, insert, checksum)
	return e
}
