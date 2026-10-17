package storegate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^banking_[a-z0-9_]+$`)

const controlDDL = `CREATE TABLE IF NOT EXISTS banking_storage_control (
 id SMALLINT NOT NULL PRIMARY KEY CHECK(id=1),mode VARCHAR(16) NOT NULL,
 generation BIGINT NOT NULL DEFAULT 1,mutation_seq BIGINT NOT NULL DEFAULT 0,
 snapshot_digest VARCHAR(64) NOT NULL DEFAULT '',updated_at BIGINT NOT NULL DEFAULT 0,
 CHECK(mode IN ('active','frozen','standby','loading'))
)`
const transitionsDDL = `CREATE TABLE IF NOT EXISTS banking_storage_transitions (
 id VARCHAR(36) NOT NULL PRIMARY KEY,from_mode VARCHAR(16) NOT NULL,to_mode VARCHAR(16) NOT NULL,
 generation BIGINT NOT NULL,snapshot_digest VARCHAR(64) NOT NULL,created_at BIGINT NOT NULL
)`

// Install is additive migration 011; historical dialect migrations are untouched.
func Install(ctx context.Context, db *sql.DB, dialect string, outboxSQL []string) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if dialect == "postgres" {
		if _, err = c.ExecContext(ctx, "SELECT pg_advisory_lock(77113811)"); err != nil {
			return err
		}
		defer c.ExecContext(context.Background(), "SELECT pg_advisory_unlock(77113811)")
	} else {
		var locked int
		if err = c.QueryRowContext(ctx, "SELECT GET_LOCK('banking_storage_011',10)").Scan(&locked); err != nil || locked != 1 {
			return fmt.Errorf("storage migration lock unavailable")
		}
		defer c.ExecContext(context.Background(), "SELECT RELEASE_LOCK('banking_storage_011')")
	}
	query := "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE 'banking_%' AND table_type='BASE TABLE' ORDER BY table_name"
	if dialect == "postgres" {
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_name LIKE 'banking_%' AND table_type='BASE TABLE' ORDER BY table_name"
	}
	rows, err := c.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		// Schema 018 installs its own guards; keep immutable 011's checksum and
		// 46-table contract unchanged on existing stores and subsequent reruns.
		if name == "banking_schema_migrations" || strings.HasPrefix(name, "banking_storage_") || name == "banking_model_budget_windows" || name == "banking_model_calls" {
			continue
		}
		if !identifier.MatchString(name) {
			rows.Close()
			return fmt.Errorf("invalid financial table identifier")
		}
		tables = append(tables, name)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if len(tables) != 46 {
		return fmt.Errorf("storage fencing requires all 46 financial tables")
	}
	statements := []string{controlDDL, transitionsDDL}
	if dialect == "postgres" {
		statements = append(statements, "INSERT INTO banking_storage_control(id,mode) VALUES(1,'standby') ON CONFLICT DO NOTHING", postgresFunction)
		for _, table := range tables {
			statements = append(statements, fmt.Sprintf(`CREATE OR REPLACE TRIGGER qyj_storage_fence BEFORE INSERT OR UPDATE OR DELETE ON "%s" FOR EACH ROW EXECUTE FUNCTION qyj_storage_fence()`, table))
		}
	} else {
		statements = append(statements, "INSERT IGNORE INTO banking_storage_control(id,mode) VALUES(1,'active')")
		for _, table := range tables {
			for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
				name := "qyj_fence_" + strings.TrimPrefix(table, "banking_") + "_" + strings.ToLower(event)
				statements = append(statements, "DROP TRIGGER IF EXISTS `"+name+"`", mysqlTrigger(name, table, event))
			}
		}
		for _, statement := range outboxSQL {
			statement = strings.TrimSpace(statement)
			if !strings.HasPrefix(statement, "CREATE TRIGGER qyj_replan_") {
				continue
			}
			parts := strings.Fields(statement)
			statements = append(statements, "DROP TRIGGER IF EXISTS `"+parts[2]+"`", suppressMySQLImport(statement))
		}
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(statements, ";\n"))))
	var existing string
	err = c.QueryRowContext(ctx, "SELECT checksum FROM banking_schema_migrations WHERE version=11").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("storage migration checksum mismatch")
		}
		return secureMetadata(ctx, c, dialect)
	}
	if err != sql.ErrNoRows {
		return err
	}
	if dialect == "postgres" {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("storage schema installation failed: %w", err)
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(11,$1)", checksum); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return secureMetadata(ctx, c, dialect)
	}
	for _, statement := range statements {
		if _, err = c.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("storage schema installation failed: %w", err)
		}
	}
	_, err = c.ExecContext(ctx, "INSERT INTO banking_schema_migrations(version,checksum) VALUES(11,?)", checksum)
	return err
}

// Provisioning grants default DML on business tables. Storage authority and
// schema/risk policy remain owned by the migration role, including on reruns.
func secureMetadata(ctx context.Context, c *sql.Conn, dialect string) error {
	if dialect != "postgres" {
		return nil
	}
	_, err := c.ExecContext(ctx, `DO $permissions$
 DECLARE role_row record;
 BEGIN
 REVOKE ALL ON public.banking_storage_control,public.banking_storage_transitions,public.banking_schema_migrations,public.banking_risk_rules FROM PUBLIC;
 FOR role_row IN
  SELECT DISTINCT p.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a JOIN pg_roles p ON p.oid=a.grantee
  WHERE c.oid IN ('public.banking_storage_control'::regclass,'public.banking_storage_transitions'::regclass,'public.banking_schema_migrations'::regclass,'public.banking_risk_rules'::regclass) AND a.grantee<>c.relowner
 LOOP
  EXECUTE format('REVOKE ALL ON public.banking_storage_control,public.banking_storage_transitions,public.banking_schema_migrations,public.banking_risk_rules FROM %I',role_row.rolname);
  EXECUTE format('GRANT SELECT ON public.banking_storage_control,public.banking_storage_transitions,public.banking_schema_migrations,public.banking_risk_rules TO %I',role_row.rolname);
 END LOOP;
 END $permissions$`)
	if err != nil {
		return err
	}
	// Row-level SHARE locks require UPDATE privilege in PostgreSQL. Keep that
	// privilege away from runtime roles and execute only this fixed trigger as
	// its owner, with a pinned search path and session_user import checks.
	_, err = c.ExecContext(ctx, `ALTER FUNCTION public.qyj_storage_fence() SECURITY DEFINER; ALTER FUNCTION public.qyj_storage_fence() SET search_path TO pg_catalog`)
	return err
}

func mysqlTrigger(name, table, event string) string {
	return fmt.Sprintf("CREATE TRIGGER `%s` BEFORE %s ON `%s` FOR EACH ROW BEGIN DECLARE storage_mode VARCHAR(16); SELECT mode INTO storage_mode FROM banking_storage_control WHERE id=1 FOR SHARE; IF storage_mode IS NULL OR (storage_mode<>'active' AND NOT(storage_mode='loading' AND COALESCE(@banking_migration_import,0)=1)) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='BANKING_STORAGE_FROZEN'; END IF; END", name, event, table)
}
func suppressMySQLImport(statement string) string {
	guard := "NOT(COALESCE(@banking_migration_import,0)=1 AND (SELECT mode FROM banking_storage_control WHERE id=1)='loading')"
	if i := strings.Index(statement, " WHERE "); i >= 0 {
		return statement[:i] + " WHERE (" + statement[i+7:] + ") AND " + guard
	}
	return statement + " WHERE " + guard
}

const postgresFunction = `CREATE OR REPLACE FUNCTION qyj_storage_fence() RETURNS trigger LANGUAGE plpgsql AS $gate$
DECLARE storage_mode text; owner_name text;
BEGIN
 SELECT mode INTO storage_mode FROM public.banking_storage_control WHERE id=1 FOR SHARE;
 SELECT pg_get_userbyid(relowner) INTO owner_name FROM pg_class WHERE oid='public.banking_storage_control'::regclass;
 IF storage_mode IS NULL OR (storage_mode<>'active' AND NOT(storage_mode='loading' AND COALESCE(current_setting('banking.migration_import',true),'off')='on' AND pg_has_role(session_user,owner_name,'MEMBER'))) THEN
  RAISE EXCEPTION 'BANKING_STORAGE_FROZEN' USING ERRCODE='55000';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $gate$`
