ALTER TABLE banking_users ADD COLUMN phone_number VARCHAR(20) NULL;
ALTER TABLE banking_users ADD COLUMN password_hash VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE banking_users ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE banking_users ADD COLUMN refresh_hash VARCHAR(64) NULL;
ALTER TABLE banking_users ADD COLUMN refresh_expires_at BIGINT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX banking_users_phone_number_uq ON banking_users (phone_number);
CREATE UNIQUE INDEX banking_users_refresh_hash_uq ON banking_users (refresh_hash);
