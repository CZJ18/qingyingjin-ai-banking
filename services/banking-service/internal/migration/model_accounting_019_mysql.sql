ALTER TABLE banking_model_calls ADD COLUMN raw_reported_cost TEXT NULL;
ALTER TABLE banking_model_calls ADD COLUMN validated_reported_cost VARCHAR(64) NULL;
ALTER TABLE banking_model_calls ADD COLUMN cost_status VARCHAR(32) NOT NULL DEFAULT 'legacy_unverified';
ALTER TABLE banking_model_calls ADD COLUMN cost_currency VARCHAR(3) NOT NULL DEFAULT '';
ALTER TABLE banking_model_calls ADD COLUMN cost_unit VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE banking_model_calls ADD COLUMN confirmed_cost VARCHAR(64) NULL;
ALTER TABLE banking_model_calls ADD COLUMN confirmed_currency VARCHAR(3) NOT NULL DEFAULT '';
ALTER TABLE banking_model_calls ADD COLUMN confirmed_unit VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE banking_model_calls ADD COLUMN usage_status VARCHAR(24) NOT NULL DEFAULT 'legacy';
ALTER TABLE banking_model_calls ADD COLUMN reported_tokens BIGINT NULL;
ALTER TABLE banking_model_calls ADD COLUMN provider_execute_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE banking_model_calls ADD COLUMN report_digest VARCHAR(64) NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS banking_model_reconciliations (
 id VARCHAR(36) NOT NULL PRIMARY KEY,
 call_id VARCHAR(36) NOT NULL,
 user_id BIGINT NOT NULL,
 operator_id VARCHAR(64) NOT NULL,
 request_key CHAR(64) NOT NULL UNIQUE,
 request_hash CHAR(64) NOT NULL,
 tokens BIGINT NULL CHECK(tokens>=0 AND tokens<=1000000000000),
 actual_cost VARCHAR(64) NULL,
 currency VARCHAR(3) NOT NULL DEFAULT '',
 unit VARCHAR(32) NOT NULL DEFAULT '',
 evidence_ref VARCHAR(256) NOT NULL,
 previous_usage_status VARCHAR(24) NOT NULL,
 created_at BIGINT NOT NULL,
 FOREIGN KEY(call_id) REFERENCES banking_model_calls(id),
 FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
