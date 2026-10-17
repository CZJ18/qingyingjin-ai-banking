package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

// Migration 013 extends identity metadata without changing existing credentials,
// historical migration checksums, or the 46-table financial storage contract.
// Verification method distinguishes developer inbox proof from real SMS proof.
//
//go:embed sms_auth_013_mysql.sql
var smsAuthMySQLSQL string

//go:embed sms_auth_013_postgres.sql
var smsAuthPostgresSQL string

func smsAuthStatements(dialect string) ([]string, error) {
	var ddl string
	switch dialect {
	case "mysql":
		ddl = smsAuthMySQLSQL
	case "postgres":
		ddl = smsAuthPostgresSQL
	default:
		return nil, fmt.Errorf("unsupported SMS authentication migration dialect")
	}
	var statements []string
	for _, statement := range strings.Split(ddl, ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements, nil
}

func applySMSAuth(ctx context.Context, db *sql.DB, dialect string) error {
	statements, err := smsAuthStatements(dialect)
	if err != nil {
		return err
	}
	ddl := smsAuthMySQLSQL
	if dialect == "postgres" {
		ddl = smsAuthPostgresSQL
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl)))
	if dialect == "postgres" {
		return applySMSAuthPostgres(ctx, db, statements, checksum)
	}
	return applySMSAuthMySQL(ctx, db, statements, checksum)
}

func applySMSAuthPostgres(ctx context.Context, db *sql.DB, statements []string, checksum string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113813)"); err != nil {
		return fmt.Errorf("SMS authentication migration lock unavailable: %w", err)
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=13").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("SMS authentication migration checksum mismatch")
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("SMS authentication migration 13: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(13,$1)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

func applySMSAuthMySQL(ctx context.Context, db *sql.DB, statements []string, checksum string) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var locked int
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_sms_auth_013',10)").Scan(&locked); err != nil {
		return fmt.Errorf("SMS authentication migration lock unavailable: %w", err)
	}
	if locked != 1 {
		return fmt.Errorf("SMS authentication migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_sms_auth_013')")
	var existing string
	err = c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=13").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("SMS authentication migration checksum mismatch")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	// MySQL DDL commits individually. Exact definition checks allow re-entry after
	// interruption while rejecting incompatible manually-created columns.
	if err = applySMSAuthMySQLDDL(ctx, c, "banking_users", statements); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(13,?)", checksum)
	return err
}

func applySMSAuthMySQLDDL(ctx context.Context, c *sql.Conn, table string, statements []string) error {
	for i, statement := range statements {
		present, err := mysqlSMSAuthColumnPresent(ctx, c, table, i)
		if err != nil {
			return err
		}
		if !present {
			if _, err = c.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("SMS authentication migration 13: %w", err)
			}
		}
	}
	return nil
}

func mysqlSMSAuthColumnPresent(ctx context.Context, c *sql.Conn, table string, index int) (bool, error) {
	columns := []struct {
		name, dataType, defaultValue string
		length                       int64
	}{
		{"phone_verified_at", "bigint", "0", 0},
		{"password_changed_at", "bigint", "0", 0},
		{"phone_verification_method", "varchar", "", 16},
	}
	if !phoneAuthIdentifier.MatchString(table) || index < 0 || index >= len(columns) {
		return false, fmt.Errorf("invalid SMS authentication migration column")
	}
	want := columns[index]
	var dataType, columnType, nullable, extra, generated string
	var defaultValue sql.NullString
	var length sql.NullInt64
	err := c.QueryRowContext(ctx, `SELECT DATA_TYPE,COLUMN_TYPE,IS_NULLABLE,COLUMN_DEFAULT,CHARACTER_MAXIMUM_LENGTH,EXTRA,GENERATION_EXPRESSION
FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, want.name).Scan(&dataType, &columnType, &nullable, &defaultValue, &length, &extra, &generated)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if dataType != want.dataType || strings.Contains(columnType, "unsigned") || nullable != "NO" || !defaultValue.Valid || defaultValue.String != want.defaultValue || (want.length != 0 && (!length.Valid || length.Int64 != want.length)) || extra != "" || generated != "" {
		return false, fmt.Errorf("SMS authentication migration column %s has an incompatible definition", want.name)
	}
	return true, nil
}
