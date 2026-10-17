-- Reviewed PostgreSQL equivalent of 008_skills.sql.
-- Regenerate only with generate_postgres.py; never edit applied baselines.
ALTER TABLE banking_agent_tasks ADD COLUMN IF NOT EXISTS skill_id VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL DEFAULT '';
ALTER TABLE banking_agent_tasks ADD COLUMN IF NOT EXISTS skill_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_goal_versions ADD COLUMN IF NOT EXISTS skill_id VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL DEFAULT '';
ALTER TABLE banking_goal_versions ADD COLUMN IF NOT EXISTS skill_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_operation_intents ADD COLUMN IF NOT EXISTS skill_id VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL DEFAULT '';
ALTER TABLE banking_operation_intents ADD COLUMN IF NOT EXISTS skill_version BIGINT NOT NULL DEFAULT 0;
