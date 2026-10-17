CREATE TABLE IF NOT EXISTS banking_subscriptions (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, bank_account_id BIGINT NOT NULL,
 fingerprint CHAR(64) NOT NULL, merchant_name VARCHAR(128) NOT NULL, category VARCHAR(64) NOT NULL,
 billing_interval VARCHAR(16) NOT NULL, amount_minor BIGINT NOT NULL, currency CHAR(3) NOT NULL,
 next_billing_at BIGINT NOT NULL, confidence_bps BIGINT NOT NULL, status VARCHAR(24) NOT NULL,
 version BIGINT NOT NULL, is_simulated BOOLEAN NOT NULL DEFAULT TRUE, last_used_at BIGINT NULL, cancelled_at BIGINT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_subscription_owner(user_id,id), UNIQUE KEY uq_subscription_pattern(user_id,fingerprint),
 CONSTRAINT fk_subscription_account FOREIGN KEY(user_id,bank_account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT ck_subscription_amount CHECK(amount_minor>0 AND amount_minor<=1000000000000),
 CONSTRAINT ck_subscription_status CHECK(status IN ('candidate','active','cancelled')),
 CONSTRAINT ck_subscription_interval CHECK(billing_interval IN ('weekly','monthly','quarterly','annual')),
 CONSTRAINT ck_subscription_simulated CHECK(is_simulated=TRUE),
 CONSTRAINT ck_subscription_confidence CHECK(confidence_bps BETWEEN 0 AND 10000)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_subscription_payments (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, subscription_id BIGINT NOT NULL,
 transaction_id BIGINT NOT NULL, amount_minor BIGINT NOT NULL, occurred_at BIGINT NOT NULL,
 is_possible_duplicate BOOLEAN NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_subscription_payment(user_id,transaction_id),
 CONSTRAINT fk_subscription_payment_parent FOREIGN KEY(user_id,subscription_id) REFERENCES banking_subscriptions(user_id,id),
 CONSTRAINT fk_subscription_payment_tx FOREIGN KEY(transaction_id) REFERENCES banking_transactions(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_subscription_analyses (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, payload JSON NOT NULL,
 trace_id VARCHAR(128) NOT NULL, task_id VARCHAR(128) NOT NULL, audit_id CHAR(36) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 KEY ix_subscription_analysis(user_id,id), CONSTRAINT fk_subscription_analysis_user FOREIGN KEY(user_id) REFERENCES banking_users(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_subscription_action_intents (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, subscription_id BIGINT NOT NULL,
 subscription_version BIGINT NOT NULL, action VARCHAR(16) NOT NULL, status VARCHAR(24) NOT NULL,
 confirmation_hash CHAR(64) NOT NULL, expires_at BIGINT NOT NULL, executed_at BIGINT NULL,
 failure_code VARCHAR(64) NOT NULL, trace_id VARCHAR(128) NOT NULL, task_id VARCHAR(128) NOT NULL, audit_id CHAR(36) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_subscription_intent_owner(user_id,id), KEY ix_subscription_intent_parent(user_id,subscription_id,id),
 CONSTRAINT fk_subscription_intent_parent FOREIGN KEY(user_id,subscription_id) REFERENCES banking_subscriptions(user_id,id),
 CONSTRAINT ck_subscription_intent_action CHECK(action='cancel'),
 CONSTRAINT ck_subscription_intent_status CHECK(status IN ('pending_approval','executed','rejected','failed','expired'))
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_subscription_approvals (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, intent_id BIGINT NOT NULL,
 decision VARCHAR(16) NOT NULL, confirmation_hash CHAR(64) NOT NULL, consumed_at BIGINT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_subscription_approval(user_id,intent_id),
 CONSTRAINT fk_subscription_approval_intent FOREIGN KEY(user_id,intent_id) REFERENCES banking_subscription_action_intents(user_id,id),
 CONSTRAINT ck_subscription_approval_decision CHECK(decision IN ('approve','reject'))
) ENGINE=InnoDB;
