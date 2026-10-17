package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

// Migration 012 only extends the existing identity table. Historical baseline
// bytes/checksums and the 46 business table snapshot contract remain unchanged.
//
//go:embed phone_auth_012_mysql.sql
var phoneAuthMySQLSQL string

//go:embed phone_auth_012_postgres.sql
var phoneAuthPostgresSQL string

func phoneAuthStatements(dialect string) ([]string, error) {
	var ddl string
	switch dialect {
	case "mysql":
		ddl = phoneAuthMySQLSQL
	case "postgres":
		ddl = phoneAuthPostgresSQL
	default:
		return nil, fmt.Errorf("unsupported phone authentication migration dialect")
	}
	var statements []string
	for _, statement := range strings.Split(ddl, ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements, nil
}

func applyPhoneAuth(ctx context.Context, db *sql.DB, dialect string) error {
	statements, err := phoneAuthStatements(dialect)
	if err != nil {
		return err
	}
	ddl := phoneAuthMySQLSQL
	if dialect == "postgres" {
		ddl = phoneAuthPostgresSQL
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl)))
	if dialect == "postgres" {
		return applyPhoneAuthPostgres(ctx, db, statements, checksum)
	}
	return applyPhoneAuthMySQL(ctx, db, statements, checksum)
}

func applyPhoneAuthPostgres(ctx context.Context, db *sql.DB, statements []string, checksum string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '10s'"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113812)"); err != nil {
		return fmt.Errorf("phone authentication migration lock unavailable: %w", err)
	}
	// Re-read after taking the lock: concurrent startup must apply exactly once.
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=12").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("phone authentication migration checksum mismatch")
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("phone authentication migration 12: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(12,$1)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

func applyPhoneAuthMySQL(ctx context.Context, db *sql.DB, statements []string, checksum string) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var locked int
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_phone_auth_012',10)").Scan(&locked); err != nil {
		return fmt.Errorf("phone authentication migration lock unavailable: %w", err)
	}
	if locked != 1 {
		return fmt.Errorf("phone authentication migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_phone_auth_012')")
	var existing string
	err = c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=12").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("phone authentication migration checksum mismatch")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	// MySQL DDL auto-commits. Check each exact column/index definition before
	// skipping it, allowing safe retry after interruption without adopting an
	// incompatible manually-created credential column.
	if err = applyPhoneAuthMySQLDDL(ctx, c, "banking_users", statements); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(12,?)", checksum)
	return err
}

func applyPhoneAuthMySQLDDL(ctx context.Context, c *sql.Conn, table string, statements []string) error {
	for i, statement := range statements {
		present, err := mysqlPhoneAuthObjectPresent(ctx, c, table, i)
		if err != nil {
			return err
		}
		if !present {
			if _, err = c.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("phone authentication migration 12: %w", err)
			}
		}
	}
	return nil
}

var phoneAuthIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func mysqlPhoneAuthObjectPresent(ctx context.Context, c *sql.Conn, table string, index int) (bool, error) {
	if !phoneAuthIdentifier.MatchString(table) || index < 0 {
		return false, fmt.Errorf("invalid phone authentication migration object")
	}
	columns := []struct {
		name, dataType, nullable, defaultValue string
		length                                 int64
		hasDefault                             bool
	}{
		{"phone_number", "varchar", "YES", "", 20, false},
		{"password_hash", "varchar", "NO", "", 255, true},
		{"auth_version", "bigint", "NO", "0", 0, true},
		{"refresh_hash", "varchar", "YES", "", 64, false},
		{"refresh_expires_at", "bigint", "NO", "0", 0, true},
	}
	if index < len(columns) {
		want := columns[index]
		var dataType, columnType, nullable string
		var length sql.NullInt64
		var defaultValue sql.NullString
		err := c.QueryRowContext(ctx, `SELECT DATA_TYPE,COLUMN_TYPE,IS_NULLABLE,CHARACTER_MAXIMUM_LENGTH,COLUMN_DEFAULT
FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, want.name).Scan(&dataType, &columnType, &nullable, &length, &defaultValue)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if dataType != want.dataType || nullable != want.nullable || (want.length != 0 && (!length.Valid || length.Int64 != want.length)) || strings.Contains(columnType, "unsigned") || defaultValue.Valid != want.hasDefault || (want.hasDefault && defaultValue.String != want.defaultValue) {
			return false, fmt.Errorf("phone authentication migration column %s has an incompatible definition", want.name)
		}
		return true, nil
	}
	indexes := []struct{ name, column string }{
		{"banking_users_phone_number_uq", "phone_number"},
		{"banking_users_refresh_hash_uq", "refresh_hash"},
	}
	if index >= len(columns)+len(indexes) {
		return false, fmt.Errorf("phone authentication migration contains an unexpected statement")
	}
	want := indexes[index-len(columns)]
	var nonUnique, parts, prefixes int
	var names string
	err := c.QueryRowContext(ctx, `SELECT MIN(NON_UNIQUE),COUNT(*),GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX),SUM(CASE WHEN SUB_PART IS NULL THEN 0 ELSE 1 END)
FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=? GROUP BY INDEX_NAME`, table, want.name).Scan(&nonUnique, &parts, &names, &prefixes)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if nonUnique != 0 || parts != 1 || names != want.column || prefixes != 0 {
		return false, fmt.Errorf("phone authentication migration index %s has an incompatible definition", want.name)
	}
	return true, nil
}
