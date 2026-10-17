-- Reviewed PostgreSQL equivalent of 009_skill_traces.sql.
-- Regenerate only with generate_postgres.py; never edit applied baselines.
ALTER TABLE banking_execution_traces ADD COLUMN IF NOT EXISTS skill_id VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL DEFAULT '';
ALTER TABLE banking_execution_traces ADD COLUMN IF NOT EXISTS skill_version BIGINT NOT NULL DEFAULT 0;
