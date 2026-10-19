CREATE TABLE banking_model_spend_pools (
 project_id VARCHAR(64) NOT NULL PRIMARY KEY,
 currency VARCHAR(3) NOT NULL,
 policy_hash VARCHAR(64) NOT NULL,
 limit_units BIGINT NOT NULL CHECK(limit_units>=0),
 used_units BIGINT NOT NULL CHECK(used_units>=0),
 held_units BIGINT NOT NULL CHECK(held_units>=0),
 frozen BOOLEAN NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL
);
CREATE TABLE banking_model_spend_attempts (
 id VARCHAR(36) NOT NULL PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL REFERENCES banking_model_spend_pools(project_id),
 request_key VARCHAR(64) NOT NULL,
 request_hash VARCHAR(64) NOT NULL,
 policy_hash VARCHAR(64) NOT NULL,
 status VARCHAR(24) NOT NULL CHECK(status IN ('reserved','unknown','reported','settled')),
 reserved_units BIGINT NOT NULL CHECK(reserved_units>=0),
 estimated_units BIGINT NULL CHECK(estimated_units>=0),
 actual_units BIGINT NULL CHECK(actual_units>=0),
 provider_id VARCHAR(128) NOT NULL DEFAULT '',
 usage_json TEXT NOT NULL DEFAULT '',
 response_body TEXT NULL CHECK(OCTET_LENGTH(response_body)<=2097152),
 content_type VARCHAR(64) NOT NULL DEFAULT '',
 response_digest VARCHAR(64) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 UNIQUE(project_id,request_key)
);
CREATE TABLE banking_model_spend_events (
 id VARCHAR(36) NOT NULL PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL REFERENCES banking_model_spend_pools(project_id),
 attempt_id VARCHAR(36) NOT NULL REFERENCES banking_model_spend_attempts(id),
 kind VARCHAR(32) NOT NULL,
 dedupe_key VARCHAR(64) NOT NULL UNIQUE,
 request_hash VARCHAR(64) NOT NULL,
 amount_units BIGINT NOT NULL,
 provider_id VARCHAR(128) NOT NULL DEFAULT '',
 bill_id VARCHAR(128) NOT NULL DEFAULT '',
 line_id VARCHAR(128) NOT NULL DEFAULT '',
 evidence_digest VARCHAR(64) NOT NULL DEFAULT '',
 operator_id VARCHAR(64) NOT NULL DEFAULT '',
 snapshot_json TEXT NOT NULL,
 created_at BIGINT NOT NULL
);
