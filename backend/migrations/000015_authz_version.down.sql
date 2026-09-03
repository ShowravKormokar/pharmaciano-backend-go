ALTER TABLE users DROP CONSTRAINT IF EXISTS ck_users_authz_version_positive;
ALTER TABLE users DROP COLUMN IF EXISTS authz_version;
