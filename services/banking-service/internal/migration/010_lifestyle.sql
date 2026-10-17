CREATE TABLE IF NOT EXISTS banking_lifestyle_goals (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 current_version BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY lifestyle_goal_owner(id,user_id), KEY lifestyle_goal_user(user_id),
 FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_lifestyle_goal_versions (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL, version BIGINT NOT NULL, text TEXT NOT NULL,
 spec_json JSON NOT NULL, trace_id VARCHAR(128) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY lifestyle_goal_version(goal_id,version),
 FOREIGN KEY(goal_id,user_id) REFERENCES banking_lifestyle_goals(id,user_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_lifestyle_plans (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL, goal_version BIGINT NOT NULL, input_digest CHAR(64) NOT NULL,
 snapshot_json JSON NOT NULL, result_json JSON NOT NULL, expires_at BIGINT NOT NULL,
 audit_id CHAR(36) NOT NULL, trace_id VARCHAR(128) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY lifestyle_plan_owner(id,user_id), KEY lifestyle_plan_goal(goal_id,id),
 FOREIGN KEY(goal_id,user_id) REFERENCES banking_lifestyle_goals(id,user_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_lifestyle_recommendations (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, type VARCHAR(64) NOT NULL,
 expected_impact_minor BIGINT NOT NULL, impact_period VARCHAR(32) NOT NULL,
 explanation TEXT NOT NULL, confidence VARCHAR(64) NOT NULL,
 status VARCHAR(32) NOT NULL, version BIGINT NOT NULL, expires_at BIGINT NOT NULL,
 rule_version VARCHAR(64) NOT NULL, option_json JSON NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY lifestyle_rec_owner(id,user_id), UNIQUE KEY lifestyle_rec_option(plan_id,type),
 KEY lifestyle_rec_goal(user_id,goal_id,status),
 FOREIGN KEY(plan_id,user_id) REFERENCES banking_lifestyle_plans(id,user_id),
 FOREIGN KEY(goal_id,user_id) REFERENCES banking_lifestyle_goals(id,user_id),
 CHECK(expected_impact_minor >= 0)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_lifestyle_decisions (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 recommendation_id BIGINT NOT NULL, decision VARCHAR(16) NOT NULL,
 request_key VARCHAR(128) NOT NULL, input_digest CHAR(64) NOT NULL,
 audit_id CHAR(36) NOT NULL, trace_id VARCHAR(128) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY lifestyle_rec_decision(recommendation_id),
 FOREIGN KEY(recommendation_id,user_id) REFERENCES banking_lifestyle_recommendations(id,user_id)
) ENGINE=InnoDB;
