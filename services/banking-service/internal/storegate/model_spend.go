package storegate

import (
	"fmt"
	"strings"
)

// ModelSpendFences belongs only to additive migration 020. Historical 011,
// 018 and 019 fence generators and their checksum inputs remain unchanged.
func ModelSpendFences(dialect string) []string {
	var out []string
	for _, table := range []string{"banking_model_spend_pools", "banking_model_spend_attempts"} {
		if dialect == "postgres" {
			out = append(out, fmt.Sprintf(`CREATE OR REPLACE TRIGGER qyj_storage_fence BEFORE INSERT OR UPDATE OR DELETE ON "%s" FOR EACH ROW EXECUTE FUNCTION qyj_storage_fence()`, table))
		} else {
			for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
				name := "qyj_fence_" + strings.TrimPrefix(table, "banking_") + "_" + strings.ToLower(event)
				out = append(out, "DROP TRIGGER IF EXISTS `"+name+"`", mysqlTrigger(name, table, event))
			}
		}
	}
	if dialect == "postgres" {
		return append(out, modelSpendEventsPostgresFunction,
			`CREATE OR REPLACE TRIGGER qyj_storage_fence BEFORE INSERT OR UPDATE OR DELETE ON banking_model_spend_events FOR EACH ROW EXECUTE FUNCTION public.qyj_model_spend_events_fence()`,
			`CREATE OR REPLACE TRIGGER qyj_spend_events_no_truncate BEFORE TRUNCATE ON banking_model_spend_events FOR EACH STATEMENT EXECUTE FUNCTION public.qyj_model_spend_events_fence()`,
			modelSpendPostgresPermissions)
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		name := "qyj_fence_model_spend_events_" + strings.ToLower(event)
		statement := mysqlTrigger(name, "banking_model_spend_events", event)
		if event != "INSERT" {
			statement = fmt.Sprintf("CREATE TRIGGER `%s` BEFORE %s ON banking_model_spend_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY'", name, event)
		}
		out = append(out, "DROP TRIGGER IF EXISTS `"+name+"`", statement)
	}
	return out
}

// INSERT is permitted in active storage and during an authenticated offline
// owner import. Events can never be updated, deleted or truncated, including by
// the importer. SECURITY DEFINER grants only this fixed storage-row SHARE lock.
const modelSpendEventsPostgresFunction = `CREATE OR REPLACE FUNCTION public.qyj_model_spend_events_fence() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path TO pg_catalog AS $spend_gate$
DECLARE storage_mode text; owner_name text;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'BANKING_MODEL_SPEND_EVENTS_APPEND_ONLY' USING ERRCODE='55000';
 END IF;
 SELECT mode INTO storage_mode FROM public.banking_storage_control WHERE id=1 FOR SHARE;
 SELECT pg_get_userbyid(relowner) INTO owner_name FROM pg_class WHERE oid='public.banking_storage_control'::regclass;
 IF storage_mode IS NULL OR (storage_mode<>'active' AND NOT(storage_mode='loading' AND COALESCE(current_setting('banking.migration_import',true),'off')='on' AND pg_has_role(session_user,owner_name,'MEMBER'))) THEN
  RAISE EXCEPTION 'BANKING_STORAGE_FROZEN' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $spend_gate$`

// Default business-table grants must not expose the monetary ledger to existing
// application roles. Operators explicitly grant SELECT/INSERT/UPDATE on pools
// and attempts, and SELECT/INSERT on events, to a dedicated trusted gateway role.
const modelSpendPostgresPermissions = `DO $spend_permissions$
DECLARE role_row record;
BEGIN
 REVOKE ALL ON public.banking_model_spend_pools,public.banking_model_spend_attempts,public.banking_model_spend_events FROM PUBLIC;
 FOR role_row IN
  SELECT DISTINCT p.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a JOIN pg_roles p ON p.oid=a.grantee
  WHERE c.oid IN ('public.banking_model_spend_pools'::regclass,'public.banking_model_spend_attempts'::regclass,'public.banking_model_spend_events'::regclass) AND a.grantee<>c.relowner
 LOOP
  EXECUTE format('REVOKE ALL ON public.banking_model_spend_pools,public.banking_model_spend_attempts,public.banking_model_spend_events FROM %I',role_row.rolname);
 END LOOP;
END $spend_permissions$`
