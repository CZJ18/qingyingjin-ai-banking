CREATE TABLE IF NOT EXISTS banking_agent_jobs (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,status VARCHAR(32) NOT NULL,strategy VARCHAR(24) NOT NULL,version BIGINT NOT NULL,max_attempts INT NOT NULL,timeout_ms BIGINT NOT NULL,use_coze BOOLEAN NOT NULL,source_plan_id VARCHAR(128) NOT NULL,request_hash CHAR(64) NOT NULL,idempotency_key VARCHAR(128) NOT NULL,lease_token VARCHAR(36) NOT NULL,lease_until BIGINT NOT NULL,trace_id VARCHAR(128) NOT NULL,audit_id VARCHAR(36) NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_job_owner(user_id,id),UNIQUE KEY uq_job_idempotency(user_id,idempotency_key),KEY ix_job_queue(status,lease_until,created_at),CONSTRAINT fk_job_user FOREIGN KEY(user_id) REFERENCES banking_users(id),CONSTRAINT ck_job_attempts CHECK(max_attempts BETWEEN 1 AND 3),CONSTRAINT ck_job_timeout CHECK(timeout_ms BETWEEN 100 AND 60000)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_agent_tasks (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,job_id VARCHAR(36) NOT NULL,task_key VARCHAR(64) NOT NULL,agent_type VARCHAR(24) NOT NULL,tool VARCHAR(64) NOT NULL,mode VARCHAR(24) NOT NULL,priority INT NOT NULL,status VARCHAR(32) NOT NULL,parameters_json JSON NOT NULL,output_json JSON NOT NULL,attempts INT NOT NULL,retry_at BIGINT NOT NULL,confirmation VARCHAR(16) NOT NULL,error_code VARCHAR(64) NOT NULL,protected_blocked BOOLEAN NOT NULL DEFAULT FALSE,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_task_owner(user_id,id),UNIQUE KEY uq_job_task_key(job_id,task_key),CONSTRAINT fk_task_job FOREIGN KEY(user_id,job_id) REFERENCES banking_agent_jobs(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_agent_dependencies (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,user_id BIGINT NOT NULL,job_id VARCHAR(36) NOT NULL,task_id VARCHAR(36) NOT NULL,depends_on VARCHAR(36) NOT NULL,
 UNIQUE KEY uq_agent_dependency(task_id,depends_on),CONSTRAINT fk_dependency_job FOREIGN KEY(user_id,job_id) REFERENCES banking_agent_jobs(user_id,id),CONSTRAINT fk_dependency_task FOREIGN KEY(user_id,task_id) REFERENCES banking_agent_tasks(user_id,id),CONSTRAINT fk_dependency_parent FOREIGN KEY(user_id,depends_on) REFERENCES banking_agent_tasks(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_agent_executions (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,job_id VARCHAR(36) NOT NULL,task_id VARCHAR(36) NOT NULL,agent_type VARCHAR(24) NOT NULL,attempt INT NOT NULL,status VARCHAR(32) NOT NULL,started_at BIGINT NOT NULL,finished_at BIGINT NOT NULL,duration_ms BIGINT NOT NULL,error_code VARCHAR(64) NOT NULL,workflow_execute_id VARCHAR(128) NOT NULL,input_json JSON NOT NULL,output_json JSON NOT NULL,lease_token VARCHAR(36) NOT NULL,
 UNIQUE KEY uq_execution_owner(user_id,id),UNIQUE KEY uq_task_attempt(task_id,attempt),CONSTRAINT fk_execution_job FOREIGN KEY(user_id,job_id) REFERENCES banking_agent_jobs(user_id,id),CONSTRAINT fk_execution_task FOREIGN KEY(user_id,task_id) REFERENCES banking_agent_tasks(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_tool_invocations (
 id VARCHAR(36) NOT NULL PRIMARY KEY,user_id BIGINT NOT NULL,job_id VARCHAR(36) NOT NULL,task_id VARCHAR(36) NOT NULL,execution_id VARCHAR(36) NOT NULL,tool VARCHAR(64) NOT NULL,invocation_key VARCHAR(128) NOT NULL,status VARCHAR(32) NOT NULL,started_at BIGINT NOT NULL,finished_at BIGINT NOT NULL,duration_ms BIGINT NOT NULL,error_code VARCHAR(64) NOT NULL,audit_id VARCHAR(36) NOT NULL,
 UNIQUE KEY uq_invocation_execution(execution_id),CONSTRAINT fk_invocation_job FOREIGN KEY(user_id,job_id) REFERENCES banking_agent_jobs(user_id,id),CONSTRAINT fk_invocation_task FOREIGN KEY(user_id,task_id) REFERENCES banking_agent_tasks(user_id,id),CONSTRAINT fk_invocation_execution FOREIGN KEY(user_id,execution_id) REFERENCES banking_agent_executions(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_execution_traces (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,user_id BIGINT NOT NULL,job_id VARCHAR(36) NOT NULL,task_id VARCHAR(36) NOT NULL,event VARCHAR(48) NOT NULL,status VARCHAR(32) NOT NULL,error_code VARCHAR(64) NOT NULL,audit_id VARCHAR(36) NOT NULL,trace_id VARCHAR(128) NOT NULL,created_at BIGINT NOT NULL,
 KEY ix_job_events(user_id,job_id,id),CONSTRAINT fk_trace_job FOREIGN KEY(user_id,job_id) REFERENCES banking_agent_jobs(user_id,id)
) ENGINE=InnoDB;
