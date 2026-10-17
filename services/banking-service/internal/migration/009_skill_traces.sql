ALTER TABLE banking_execution_traces ADD COLUMN skill_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE banking_execution_traces ADD COLUMN skill_version BIGINT NOT NULL DEFAULT 0;
