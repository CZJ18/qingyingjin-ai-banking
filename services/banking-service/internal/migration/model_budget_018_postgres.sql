CREATE TABLE IF NOT EXISTS banking_model_budget_windows (
 id VARCHAR(64) COLLATE "C" NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES banking_users(id),
 period VARCHAR(10) COLLATE "C" NOT NULL,
 calls_used BIGINT NOT NULL DEFAULT 0 CHECK(calls_used>=0),
 tokens_used BIGINT NOT NULL DEFAULT 0 CHECK(tokens_used>=0),
 tokens_reserved BIGINT NOT NULL DEFAULT 0 CHECK(tokens_reserved>=0),
 UNIQUE(user_id,period)
);
CREATE TABLE IF NOT EXISTS banking_model_calls (
 id VARCHAR(36) COLLATE "C" NOT NULL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES banking_users(id),
 window_id VARCHAR(64) COLLATE "C" NOT NULL REFERENCES banking_model_budget_windows(id),
 request_key CHAR(64) COLLATE "C" NOT NULL,
 request_hash CHAR(64) COLLATE "C" NOT NULL,
 workflow_id VARCHAR(32) COLLATE "C" NOT NULL,
 status VARCHAR(16) NOT NULL CHECK(status IN ('reserved','unknown','completed')),
 reserved_tokens BIGINT NOT NULL CHECK(reserved_tokens>=0),
 actual_tokens BIGINT NULL CHECK(actual_tokens>=0),
 reported_cost VARCHAR(64) NOT NULL DEFAULT '',
 response_json TEXT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 UNIQUE(user_id,request_key),
 CHECK(response_json IS NULL OR (jsonb_typeof(response_json::jsonb)='object' AND OCTET_LENGTH(response_json)<=1048576))
);
