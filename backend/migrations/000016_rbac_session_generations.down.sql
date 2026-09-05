-- ##### FILE: 000016_rbac_session_generations.down.sql ########################
DROP FUNCTION IF EXISTS bump_organization_rbac_generation(UUID);
ALTER TABLE sessions      DROP CONSTRAINT IF EXISTS ck_sessions_security_generation_positive;
ALTER TABLE sessions      DROP COLUMN     IF EXISTS security_generation;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS ck_organizations_rbac_generation_positive;
ALTER TABLE organizations DROP COLUMN     IF EXISTS rbac_generation;
