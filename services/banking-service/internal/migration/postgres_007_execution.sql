-- Reviewed PostgreSQL equivalent of 007_execution.sql.
-- Regenerate only with generate_postgres.py; never edit applied baselines.
CREATE TABLE IF NOT EXISTS banking_operation_intents (
 id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 kind VARCHAR(48) COLLATE qyj_mysql_ci NOT NULL,
 permission VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 agent VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 payload_json JSONB NOT NULL,
 snapshot_json JSONB NOT NULL,
 resource_id BIGINT NOT NULL,
 resource_digest VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 source_account_id BIGINT NOT NULL,
 target_account_id BIGINT NOT NULL,
 amount_minor BIGINT NOT NULL,
 currency VARCHAR(3) COLLATE qyj_mysql_ci NOT NULL,
 intent_digest VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 nonce VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 status VARCHAR(32) COLLATE qyj_mysql_ci NOT NULL,
 version BIGINT NOT NULL,
 expires_at BIGINT NOT NULL,
 authorization_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 risk_level VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 error_code VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 result_json JSONB NOT NULL,
 idempotency_key VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 request_hash VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 trace_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 task_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 audit_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 CONSTRAINT operation_intents_uq_operation_owner UNIQUE (user_id,id),
 CONSTRAINT operation_intents_uq_operation_idempotency UNIQUE (user_id,idempotency_key),
 CONSTRAINT fk_operation_owner FOREIGN KEY(user_id) REFERENCES banking_users(id),
 CONSTRAINT ck_operation_amount CHECK(amount_minor BETWEEN 0 AND 1000000000000)
);
CREATE INDEX operation_intents_ix_operation_resource ON banking_operation_intents (user_id,kind,resource_id);
CREATE TABLE IF NOT EXISTS banking_user_authorizations (
 id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 operation_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 decision VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 intent_digest VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 token_id VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 expires_at BIGINT NOT NULL,
 consumed_at BIGINT NOT NULL,
 created_at BIGINT NOT NULL,
 CONSTRAINT user_authorizations_uq_user_authorization_operation UNIQUE (operation_id),
 CONSTRAINT fk_authorization_operation FOREIGN KEY(user_id,operation_id) REFERENCES banking_operation_intents(user_id,id)
);

CREATE TABLE IF NOT EXISTS banking_risk_rules (
 name VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 version BIGINT NOT NULL,
 enabled BOOLEAN NOT NULL,
 risk_level VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 limit_minor BIGINT NOT NULL,
 description VARCHAR(512) COLLATE qyj_mysql_ci NOT NULL
);

INSERT INTO banking_risk_rules(name,version,enabled,risk_level,limit_minor,description) VALUES
 ('simulation_only',1,TRUE,'critical',0,'Only local simulated financial resources are allowed'),
 ('bound_authorization',1,TRUE,'critical',0,'Human authorization binds canonical operation digest and challenge'),
 ('resource_ownership',1,TRUE,'critical',0,'All affected resources belong to authenticated owner'),
 ('necessary_reserve',1,TRUE,'high',0,'Preserve known normal living obligations and safety buffers'),
 ('amount_limit',1,TRUE,'high',1000000000000,'CNY integer minor units cannot exceed supported ledger limit'),
 ('resource_version',1,TRUE,'high',0,'Sensitive resource terms and versions must still match')
ON CONFLICT (name) DO NOTHING;
CREATE TABLE IF NOT EXISTS banking_risk_events (
 id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 operation_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 rule_name VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 risk_level VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 decision VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 error_code VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 evidence_json JSONB NOT NULL,
 trace_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 created_at BIGINT NOT NULL,
 CONSTRAINT fk_risk_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
);
CREATE INDEX risk_events_ix_risk_operation ON banking_risk_events (user_id,operation_id,created_at);
CREATE TABLE IF NOT EXISTS banking_execution_audits (
 id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL PRIMARY KEY,
 user_id BIGINT NULL,
 agent VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 operation_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 operation_type VARCHAR(48) COLLATE qyj_mysql_ci NOT NULL,
 permission VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 authorization_id VARCHAR(36) COLLATE qyj_mysql_ci NOT NULL,
 authorization_digest VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 risk_level VARCHAR(16) COLLATE qyj_mysql_ci NOT NULL,
 state VARCHAR(32) COLLATE qyj_mysql_ci NOT NULL,
 error_code VARCHAR(64) COLLATE qyj_mysql_ci NOT NULL,
 trace_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 task_id VARCHAR(128) COLLATE qyj_mysql_ci NOT NULL,
 created_at BIGINT NOT NULL,
 CONSTRAINT fk_execution_audit_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
);
CREATE INDEX execution_audits_ix_execution_audit_owner ON banking_execution_audits (user_id,operation_id,created_at);
