-- Reviewed PostgreSQL equivalent of 003_billing.sql.
-- Regenerate only with generate_postgres.py; never edit applied baselines.
CREATE TABLE IF NOT EXISTS banking_billing_reports (
 id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 report_json JSONB NOT NULL,
 audit_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 trace_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 task_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 created_at BIGINT NOT NULL,
 CONSTRAINT fk_billing_report_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
);
CREATE INDEX billing_reports_ix_billing_user_time ON banking_billing_reports (user_id,created_at,id);
