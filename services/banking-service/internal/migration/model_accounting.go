package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/qingyingjin/banking-service/internal/storegate"
)

//go:embed model_accounting_019_mysql.sql
var modelAccountingMySQLSQL string

//go:embed model_accounting_019_postgres.sql
var modelAccountingPostgresSQL string

func applyModelAccounting(ctx context.Context, db *sql.DB, dialect string) error {
	if dialect == "postgres" {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(77113819)"); e != nil {
			return e
		}
		if e = applyModelAccountingOn(ctx, tx, dialect); e != nil {
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
	if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_model_accounting_019',10)").Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return fmt.Errorf("model budget migration lock unavailable")
	}
	defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_model_accounting_019')")
	return applyModelAccountingOn(ctx, c, dialect)
}
func applyModelAccountingOn(ctx context.Context, c recipientDirectoryMigrationDB, dialect string) error {
	ddl := modelAccountingMySQLSQL
	if dialect == "postgres" {
		ddl = modelAccountingPostgresSQL
	}
	fences := storegate.ModelReconciliationFences(dialect)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl+strings.Join(fences, ";\n"))))
	var previous string
	e := c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=19").Scan(&previous)
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
		statement = strings.TrimSpace(statement)
		if m := regexp.MustCompile(`^ALTER TABLE banking_model_calls ADD COLUMN ([a-z_]+) (.+)$`).FindStringSubmatch(statement); m != nil {
			q := "SELECT data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='banking_model_calls' AND column_name=?"
			if dialect == "postgres" {
				q = "SELECT data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='banking_model_calls' AND column_name=$1"
			}
			var typ, nullable string
			var length sql.NullInt64
			var defaultValue sql.NullString
			e = c.QueryRowContext(ctx, q, m[1]).Scan(&typ, &length, &nullable, &defaultValue)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if e == nil {
				if !accountingColumnMatches(m[2], typ, length, nullable, defaultValue) {
					return fmt.Errorf("incompatible accounting column %s", m[1])
				}
				continue
			}
		}
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
	if dialect == "postgres" {
		// Default business-table grants must not grant runtime roles audit writes.
		_, e = c.ExecContext(ctx, `DO $permissions$
 DECLARE role_row record;
 BEGIN
 REVOKE ALL ON public.banking_model_reconciliations FROM PUBLIC;
 FOR role_row IN SELECT DISTINCT p.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a JOIN pg_roles p ON p.oid=a.grantee WHERE c.oid='public.banking_model_reconciliations'::regclass AND a.grantee<>c.relowner
 LOOP
 EXECUTE format('REVOKE INSERT,UPDATE,DELETE,TRUNCATE ON public.banking_model_reconciliations FROM %I',role_row.rolname);
 END LOOP;
 END $permissions$`)
		if e != nil {
			return e
		}
	}
	insert := "INSERT INTO banking_schema_migrations(version,checksum) VALUES(19,?)"
	if dialect == "postgres" {
		insert = "INSERT INTO banking_schema_migrations(version,checksum) VALUES(19,$1)"
	}
	_, e = c.ExecContext(ctx, insert, checksum)
	return e
}

func accountingColumnMatches(definition, typ string, length sql.NullInt64, nullable string, defaultValue sql.NullString) bool {
	fields := strings.Fields(definition)
	wanted := strings.ToLower(fields[0])
	actual := strings.ToLower(typ)
	if strings.HasPrefix(wanted, "varchar(") {
		m := regexp.MustCompile(`varchar\(([0-9]+)\)`).FindStringSubmatch(wanted)
		n, _ := strconv.ParseInt(m[1], 10, 64)
		if actual != "varchar" && actual != "character varying" || !length.Valid || length.Int64 != n {
			return false
		}
	} else if wanted != actual {
		return false
	}
	if (nullable == "NO") != strings.Contains(definition, "NOT NULL") {
		return false
	}
	match := regexp.MustCompile(`DEFAULT '([^']*)'`).FindStringSubmatch(definition)
	if match == nil {
		return !defaultValue.Valid
	}
	if !defaultValue.Valid {
		return false
	}
	value := strings.Split(defaultValue.String, "::")[0]
	value = strings.Trim(value, "'")
	return value == match[1]
}
