CREATE TABLE IF NOT EXISTS banking_operation_intents (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,kind VARCHAR(48) NOT NULL,permission VARCHAR(16) NOT NULL,agent VARCHAR(128) NOT NULL,payload_json JSON NOT NULL,snapshot_json JSON NOT NULL,resource_id BIGINT NOT NULL,resource_digest VARCHAR(64) NOT NULL,source_account_id BIGINT NOT NULL,target_account_id BIGINT NOT NULL,amount_minor BIGINT NOT NULL,currency VARCHAR(3) NOT NULL,intent_digest CHAR(64) NOT NULL,nonce VARCHAR(36) NOT NULL,status VARCHAR(32) NOT NULL,version BIGINT NOT NULL,expires_at BIGINT NOT NULL,authorization_id VARCHAR(36) NOT NULL,risk_level VARCHAR(16) NOT NULL,error_code VARCHAR(64) NOT NULL,result_json JSON NOT NULL,idempotency_key VARCHAR(128) NOT NULL,request_hash CHAR(64) NOT NULL,trace_id VARCHAR(128) NOT NULL,task_id VARCHAR(128) NOT NULL,audit_id VARCHAR(36) NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_operation_owner(user_id,id),UNIQUE KEY uq_operation_idempotency(user_id,idempotency_key),KEY ix_operation_resource(user_id,kind,resource_id),CONSTRAINT fk_operation_owner FOREIGN KEY(user_id) REFERENCES banking_users(id),CONSTRAINT ck_operation_amount CHECK(amount_minor BETWEEN 0 AND 1000000000000)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_user_authorizations (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,operation_id VARCHAR(36) NOT NULL,decision VARCHAR(16) NOT NULL,intent_digest CHAR(64) NOT NULL,token_id VARCHAR(64) NOT NULL,expires_at BIGINT NOT NULL,consumed_at BIGINT NOT NULL,created_at BIGINT NOT NULL,
 UNIQUE KEY uq_user_authorization_operation(operation_id),CONSTRAINT fk_authorization_operation FOREIGN KEY(user_id,operation_id) REFERENCES banking_operation_intents(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_risk_rules (
 name VARCHAR(64) NOT NULL PRIMARY KEY,version BIGINT NOT NULL,enabled BOOLEAN NOT NULL,risk_level VARCHAR(16) NOT NULL,limit_minor BIGINT NOT NULL,description VARCHAR(512) NOT NULL
) ENGINE=InnoDB;
INSERT IGNORE INTO banking_risk_rules(name,version,enabled,risk_level,limit_minor,description) VALUES
 ('simulation_only',1,TRUE,'critical',0,'Only local simulated financial resources are allowed'),
 ('bound_authorization',1,TRUE,'critical',0,'Human authorization binds canonical operation digest and challenge'),
 ('resource_ownership',1,TRUE,'critical',0,'All affected resources belong to authenticated owner'),
 ('necessary_reserve',1,TRUE,'high',0,'Preserve known normal living obligations and safety buffers'),
 ('amount_limit',1,TRUE,'high',1000000000000,'CNY integer minor units cannot exceed supported ledger limit'),
 ('resource_version',1,TRUE,'high',0,'Sensitive resource terms and versions must still match');
CREATE TABLE IF NOT EXISTS banking_risk_events (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,operation_id VARCHAR(36) NOT NULL,rule_name VARCHAR(64) NOT NULL,risk_level VARCHAR(16) NOT NULL,decision VARCHAR(16) NOT NULL,error_code VARCHAR(64) NOT NULL,evidence_json JSON NOT NULL,trace_id VARCHAR(128) NOT NULL,created_at BIGINT NOT NULL,
 KEY ix_risk_operation(user_id,operation_id,created_at),CONSTRAINT fk_risk_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_execution_audits (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NULL,agent VARCHAR(128) NOT NULL,operation_id VARCHAR(36) NOT NULL,operation_type VARCHAR(48) NOT NULL,permission VARCHAR(16) NOT NULL,authorization_id VARCHAR(36) NOT NULL,authorization_digest VARCHAR(64) NOT NULL,risk_level VARCHAR(16) NOT NULL,state VARCHAR(32) NOT NULL,error_code VARCHAR(64) NOT NULL,trace_id VARCHAR(128) NOT NULL,task_id VARCHAR(128) NOT NULL,created_at BIGINT NOT NULL,
 KEY ix_execution_audit_owner(user_id,operation_id,created_at),CONSTRAINT fk_execution_audit_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
