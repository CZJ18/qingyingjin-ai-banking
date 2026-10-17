CREATE TABLE IF NOT EXISTS banking_billing_reports (
 id CHAR(36) NOT NULL PRIMARY KEY, user_id BIGINT NOT NULL,
 report_json JSON NOT NULL, audit_id CHAR(36) NOT NULL,
 trace_id VARCHAR(128) NOT NULL, task_id VARCHAR(128) NOT NULL,
 created_at BIGINT NOT NULL,
 KEY ix_billing_user_time(user_id,created_at,id),
 CONSTRAINT fk_billing_report_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
