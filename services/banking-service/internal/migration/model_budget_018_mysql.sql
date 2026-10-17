CREATE TABLE IF NOT EXISTS banking_model_budget_windows (
 id VARCHAR(64) NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 period VARCHAR(10) NOT NULL,
 calls_used BIGINT NOT NULL DEFAULT 0 CHECK(calls_used>=0),
 tokens_used BIGINT NOT NULL DEFAULT 0 CHECK(tokens_used>=0),
 tokens_reserved BIGINT NOT NULL DEFAULT 0 CHECK(tokens_reserved>=0),
 UNIQUE KEY uq_model_budget_user_period(user_id,period),
 FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_model_calls (
 id VARCHAR(36) NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL,
 window_id VARCHAR(64) NOT NULL,
 request_key CHAR(64) NOT NULL,
 request_hash CHAR(64) NOT NULL,
 workflow_id VARCHAR(32) NOT NULL,
 status VARCHAR(16) NOT NULL CHECK(status IN ('reserved','unknown','completed')),
 reserved_tokens BIGINT NOT NULL CHECK(reserved_tokens>=0),
 actual_tokens BIGINT NULL CHECK(actual_tokens>=0),
 reported_cost VARCHAR(64) NOT NULL DEFAULT '',
 response_json MEDIUMTEXT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_model_call_user_request(user_id,request_key),
 FOREIGN KEY(user_id) REFERENCES banking_users(id),
 FOREIGN KEY(window_id) REFERENCES banking_model_budget_windows(id),
 CHECK(response_json IS NULL OR (JSON_VALID(response_json) AND OCTET_LENGTH(response_json)<=1048576))
) ENGINE=InnoDB;
