ALTER TABLE banking_agent_tasks ADD COLUMN skill_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE banking_agent_tasks ADD COLUMN skill_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_goal_versions ADD COLUMN skill_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE banking_goal_versions ADD COLUMN skill_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_operation_intents ADD COLUMN skill_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE banking_operation_intents ADD COLUMN skill_version BIGINT NOT NULL DEFAULT 0;
