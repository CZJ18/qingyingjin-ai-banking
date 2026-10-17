CREATE TABLE IF NOT EXISTS banking_life_goals (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 current_version BIGINT NOT NULL, status VARCHAR(32) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_life_goal_owner(id,user_id), KEY ix_life_goal_user(user_id,id),
 CONSTRAINT fk_life_goal_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_goal_versions (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL, version BIGINT NOT NULL, text TEXT NOT NULL,
 context_json JSON NOT NULL, parsed_json JSON NULL,
 parse_status VARCHAR(32) NOT NULL, parse_id VARCHAR(64) NOT NULL DEFAULT '',
 lease_until BIGINT NOT NULL DEFAULT 0, workflow_id VARCHAR(64) NOT NULL DEFAULT '',
 execute_id VARCHAR(64) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '',
 trace_id VARCHAR(128) NOT NULL, audit_id VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_goal_version(goal_id,version), UNIQUE KEY uq_goal_version_owner(id,user_id),
 CONSTRAINT fk_goal_version_owner FOREIGN KEY(goal_id,user_id) REFERENCES banking_life_goals(id,user_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_goal_constraints (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_version_id BIGINT NOT NULL, kind VARCHAR(32) NOT NULL,
 description TEXT NOT NULL, evidence TEXT NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 KEY ix_goal_constraint_version(goal_version_id,user_id),
 CONSTRAINT fk_goal_constraint_owner FOREIGN KEY(goal_version_id,user_id) REFERENCES banking_goal_versions(id,user_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_task_plans (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL, goal_version_id BIGINT NOT NULL, version BIGINT NOT NULL,
 status VARCHAR(32) NOT NULL, plan_json JSON NOT NULL,
 trace_id VARCHAR(128) NOT NULL, audit_id VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_task_plan_version(goal_version_id), UNIQUE KEY uq_task_plan_owner(id,user_id),
 KEY ix_task_plan_goal(goal_id,user_id),
 CONSTRAINT fk_task_plan_version_owner FOREIGN KEY(goal_version_id,user_id) REFERENCES banking_goal_versions(id,user_id),
 CONSTRAINT fk_task_plan_goal_owner FOREIGN KEY(goal_id,user_id) REFERENCES banking_life_goals(id,user_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_task_dependencies (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 plan_id BIGINT NOT NULL, task_id VARCHAR(64) NOT NULL, depends_on VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_task_dependency(plan_id,task_id,depends_on),
 CONSTRAINT fk_task_dependency_owner FOREIGN KEY(plan_id,user_id) REFERENCES banking_task_plans(id,user_id)
) ENGINE=InnoDB;
