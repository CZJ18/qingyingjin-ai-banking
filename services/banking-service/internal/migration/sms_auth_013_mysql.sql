ALTER TABLE banking_users ADD COLUMN phone_verified_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_users ADD COLUMN password_changed_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_users ADD COLUMN phone_verification_method VARCHAR(16) NOT NULL DEFAULT '';
