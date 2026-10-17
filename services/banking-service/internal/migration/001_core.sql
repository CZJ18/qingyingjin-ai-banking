CREATE TABLE IF NOT EXISTS banking_users (
 id BIGINT NOT NULL PRIMARY KEY, display_name VARCHAR(128) NOT NULL, status VARCHAR(16) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_bank_accounts (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 external_account_id VARCHAR(128) NULL, display_name VARCHAR(128) NOT NULL,
 account_type VARCHAR(32) NOT NULL, masked_account_number VARCHAR(32) NOT NULL,
 currency CHAR(3) NOT NULL, balance_minor BIGINT NOT NULL DEFAULT 0,
 status VARCHAR(16) NOT NULL, version BIGINT NOT NULL DEFAULT 1, is_simulated BOOLEAN NOT NULL DEFAULT TRUE,
 savings_goal_id BIGINT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_account_owner (user_id,id), UNIQUE KEY uq_account_external (user_id,external_account_id),
 CONSTRAINT fk_account_user FOREIGN KEY (user_id) REFERENCES banking_users(id),
 CONSTRAINT ck_balance CHECK (balance_minor >= 0 AND balance_minor <= 1000000000000),
 CONSTRAINT ck_account_status CHECK (status IN ('active','frozen','inactive','closed')),
 CONSTRAINT ck_account_simulated CHECK (is_simulated = TRUE)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_transactions (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, bank_account_id BIGINT NOT NULL,
 external_transaction_id VARCHAR(128) NULL, direction VARCHAR(16) NOT NULL,
 amount_minor BIGINT NOT NULL, currency CHAR(3) NOT NULL, category VARCHAR(64) NOT NULL,
 merchant_name VARCHAR(128) NOT NULL, description VARCHAR(512) NOT NULL, occurred_at BIGINT NOT NULL,
 source_metadata JSON NULL, kind VARCHAR(32) NOT NULL, effect_mode VARCHAR(32) NOT NULL,
 journal_id VARCHAR(36) NULL, transfer_id BIGINT NULL, trace_id VARCHAR(128) NOT NULL,
 task_id VARCHAR(128) NOT NULL, audit_id VARCHAR(36) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_external_transaction (user_id,bank_account_id,external_transaction_id),
 KEY ix_transaction_period (user_id,occurred_at,id), KEY ix_transaction_category (user_id,category,occurred_at),
 CONSTRAINT fk_transaction_account FOREIGN KEY (user_id,bank_account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT ck_transaction_amount CHECK (amount_minor > 0 AND amount_minor <= 1000000000000),
 CONSTRAINT ck_direction CHECK (direction IN ('income','expense'))
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_ledger_entries (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, journal_id VARCHAR(36) NOT NULL,
 sequence INT NOT NULL, account_id BIGINT NULL, transaction_id BIGINT NULL,
 currency CHAR(3) NOT NULL, delta_minor BIGINT NOT NULL, side VARCHAR(8) NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE KEY uq_journal_leg (journal_id,sequence), KEY ix_ledger_account (user_id,account_id,id),
 CONSTRAINT fk_ledger_account FOREIGN KEY (user_id,account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT fk_ledger_transaction FOREIGN KEY (transaction_id) REFERENCES banking_transactions(id),
 CONSTRAINT ck_ledger_sequence CHECK (sequence IN (1,2)),
 CONSTRAINT ck_ledger_delta CHECK (delta_minor <> 0 AND delta_minor BETWEEN -1000000000000 AND 1000000000000),
 CONSTRAINT ck_ledger_side CHECK ((side='debit' AND delta_minor>0) OR (side='credit' AND delta_minor<0))
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_core_audits (
 id VARCHAR(36) NOT NULL PRIMARY KEY, user_id BIGINT NOT NULL, action VARCHAR(64) NOT NULL,
 resource_id BIGINT NOT NULL, outcome VARCHAR(16) NOT NULL, trace_id VARCHAR(128) NOT NULL,
 task_id VARCHAR(128) NOT NULL, error_code VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL,
 KEY ix_audit_user (user_id,created_at,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_core_idempotency (
 user_id BIGINT NOT NULL, action VARCHAR(64) NOT NULL, idem_key VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
 request_hash CHAR(64) NOT NULL, response LONGBLOB NULL, created_at BIGINT NOT NULL,
 PRIMARY KEY (user_id,action,idem_key)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_transfer_intents (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
 source_account_id BIGINT NOT NULL, target_account_id BIGINT NOT NULL, amount_minor BIGINT NOT NULL,
 currency CHAR(3) NOT NULL, description VARCHAR(512) NOT NULL, purpose VARCHAR(32) NOT NULL,
 savings_goal_id BIGINT NULL, status VARCHAR(32) NOT NULL, approval_hash CHAR(64) NOT NULL,
 expires_at BIGINT NOT NULL, journal_id VARCHAR(36) NOT NULL, failure_code VARCHAR(64) NOT NULL,
 audit_id VARCHAR(36) NOT NULL, trace_id VARCHAR(128) NOT NULL, task_id VARCHAR(128) NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 KEY ix_transfer_user (user_id,id),
 CONSTRAINT fk_transfer_source FOREIGN KEY (user_id,source_account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT fk_transfer_target FOREIGN KEY (user_id,target_account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT ck_transfer_amount CHECK (amount_minor>0 AND amount_minor<=1000000000000),
 CONSTRAINT ck_transfer_accounts CHECK (source_account_id<>target_account_id),
 CONSTRAINT ck_transfer_status CHECK (status IN ('pending_approval','succeeded','failed','rejected'))
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_transfer_approvals (
 transfer_id BIGINT NOT NULL PRIMARY KEY, user_id BIGINT NOT NULL, decision VARCHAR(16) NOT NULL,
 approval_hash CHAR(64) NOT NULL, expires_at BIGINT NOT NULL, consumed_at BIGINT NOT NULL,
 created_at BIGINT NOT NULL,
 CONSTRAINT fk_approval_transfer FOREIGN KEY (transfer_id) REFERENCES banking_transfer_intents(id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_budgets (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, month CHAR(7) NOT NULL,
 currency CHAR(3) NOT NULL, total_minor BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_budget_month (user_id,month,currency), UNIQUE KEY uq_budget_owner (user_id,id),
 CONSTRAINT fk_budget_user FOREIGN KEY (user_id) REFERENCES banking_users(id),
 CONSTRAINT ck_budget_amount CHECK (total_minor>0 AND total_minor<=1000000000000)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_budget_categories (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, budget_id BIGINT NOT NULL,
 category VARCHAR(64) NOT NULL, allocated_minor BIGINT NOT NULL, essential_reserve_minor BIGINT NOT NULL,
 UNIQUE KEY uq_budget_category (budget_id,category),
 CONSTRAINT fk_category_budget FOREIGN KEY (user_id,budget_id) REFERENCES banking_budgets(user_id,id),
 CONSTRAINT ck_category_amount CHECK (allocated_minor>=0 AND allocated_minor<=1000000000000 AND essential_reserve_minor>=0 AND essential_reserve_minor<=allocated_minor)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_budget_rules (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, budget_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL, enabled BOOLEAN NOT NULL,
 UNIQUE KEY uq_budget_rule (budget_id,kind),
 CONSTRAINT fk_rule_budget FOREIGN KEY (user_id,budget_id) REFERENCES banking_budgets(user_id,id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_savings_goals (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, name VARCHAR(128) NOT NULL,
 target_minor BIGINT NOT NULL, currency CHAR(3) NOT NULL, starts_at BIGINT NOT NULL, ends_at BIGINT NOT NULL,
 account_id BIGINT NOT NULL, status VARCHAR(16) NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE KEY uq_goal_account (account_id), UNIQUE KEY uq_goal_owner (user_id,id),
 CONSTRAINT fk_goal_account FOREIGN KEY (user_id,account_id) REFERENCES banking_bank_accounts(user_id,id),
 CONSTRAINT ck_goal_target CHECK (target_minor>0 AND target_minor<=1000000000000),
 CONSTRAINT ck_goal_period CHECK (ends_at>starts_at)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_savings_plans (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, goal_id BIGINT NOT NULL,
 monthly_minor BIGINT NOT NULL, cadence VARCHAR(16) NOT NULL, status VARCHAR(16) NOT NULL,
 UNIQUE KEY uq_goal_plan (goal_id),
 CONSTRAINT fk_plan_goal FOREIGN KEY (user_id,goal_id) REFERENCES banking_savings_goals(user_id,id),
 CONSTRAINT ck_plan_amount CHECK (monthly_minor>0 AND monthly_minor<=1000000000000)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS banking_savings_ledgers (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL, goal_id BIGINT NOT NULL,
 transfer_id BIGINT NOT NULL, journal_id VARCHAR(36) NOT NULL, delta_minor BIGINT NOT NULL,
 currency CHAR(3) NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE KEY uq_savings_transfer (transfer_id), KEY ix_savings_goal (user_id,goal_id,id),
 CONSTRAINT fk_savings_goal FOREIGN KEY (user_id,goal_id) REFERENCES banking_savings_goals(user_id,id),
 CONSTRAINT fk_savings_transfer FOREIGN KEY (transfer_id) REFERENCES banking_transfer_intents(id)
) ENGINE=InnoDB;
