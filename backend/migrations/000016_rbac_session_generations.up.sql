-- ##### FILE: 000016_rbac_session_generations.up.sql ##########################
-- Org-level RBAC generation: a single counter per organization that
-- invalidates every cached authorization snapshot for that org when a
-- role definition or grant changes (per ADR §27 and §30). User-scoped
-- authz_version (000015) covers per-user mutations like assigning a role
-- to one user; org rbac_generation covers the O(N users) blast radius of
-- a role definition change at O(1).
ALTER TABLE organizations ADD COLUMN rbac_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE organizations ADD CONSTRAINT ck_organizations_rbac_generation_positive
  CHECK (rbac_generation > 0);

-- Session-level security generation: a counter that increments every
-- time the session is revoked or its security context changes (per ADR
-- §17). The Redis session cache keys on (session_id, generation); when
-- the row is revoked the counter advances and the cached projection is
-- immediately unusable — no fan-out invalidation needed even if Redis
-- never sees the update.
ALTER TABLE sessions ADD COLUMN security_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD CONSTRAINT ck_sessions_security_generation_positive
  CHECK (security_generation > 0);

-- Helper to bump rbac_generation on every organization.
CREATE OR REPLACE FUNCTION bump_organization_rbac_generation(p_org_id UUID)
RETURNS VOID AS $$
BEGIN
  UPDATE organizations
  SET rbac_generation = rbac_generation + 1
  WHERE id = p_org_id AND deleted_at IS NULL;
END;
$$ LANGUAGE plpgsql;
