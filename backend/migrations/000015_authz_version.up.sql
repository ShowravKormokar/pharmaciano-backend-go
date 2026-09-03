-- Authorization epochs invalidate access-token permission snapshots immediately.
-- Incremented in the same transaction as role/grant changes.
ALTER TABLE users ADD COLUMN authz_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE users ADD CONSTRAINT ck_users_authz_version_positive CHECK (authz_version > 0);
