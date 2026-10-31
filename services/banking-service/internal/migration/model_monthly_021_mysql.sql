CREATE TABLE banking_model_user_limits (
 id VARCHAR(64) NOT NULL PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL,
 user_id BIGINT NOT NULL CHECK(user_id>0),
 currency VARCHAR(3) NOT NULL,
 limit_units BIGINT NOT NULL CHECK(limit_units>=0),
 created_at BIGINT NOT NULL,
 UNIQUE(project_id,user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE banking_model_user_windows (
 id VARCHAR(64) NOT NULL PRIMARY KEY,
 limit_id VARCHAR(64) NOT NULL,
 FOREIGN KEY (limit_id) REFERENCES banking_model_user_limits(id),
 user_id BIGINT NOT NULL CHECK(user_id>0),
 period VARCHAR(7) NOT NULL,
 currency VARCHAR(3) NOT NULL,
 limit_units BIGINT NOT NULL CHECK(limit_units>=0),
 used_units BIGINT NOT NULL CHECK(used_units>=0),
 held_units BIGINT NOT NULL CHECK(held_units>=0),
 frozen BOOLEAN NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 UNIQUE(limit_id,period)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE banking_model_attempt_owners (
 id VARCHAR(36) NOT NULL PRIMARY KEY,
 FOREIGN KEY (id) REFERENCES banking_model_spend_attempts(id),
 user_id BIGINT NOT NULL CHECK(user_id>0),
 window_id VARCHAR(64) NOT NULL,
 FOREIGN KEY (window_id) REFERENCES banking_model_user_windows(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
