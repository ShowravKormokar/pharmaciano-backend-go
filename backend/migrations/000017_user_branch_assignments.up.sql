-- ##### FILE: 000017_user_branch_assignments.up.sql ################################
-- Multi-branch subset assignments (ADR §19).
--
-- A user may be permitted to act on a *subset* of the branches in their org
-- (regional manager, area accountant, etc.) without being org-wide. The
-- existing users.branch_id column carries the user's home / primary branch;
-- this table lists the additional branches they may target. A row with NULL
-- branch_id in user_roles already grants the role org-wide; a row with a
-- branch_id narrows the grant to that branch. user_branch_assignments extends
-- that model by letting a non-org-wide principal act on a hand-picked subset.
--
-- The assignment is enforced at the tenant middleware layer (X-Branch-IDs
-- header is intersected with this set) and at the repository layer (every
-- branch-scoped query is `WHERE branch_id = ANY($branches)`). The JWT carries
-- the resolved subset so an honest client never needs to re-derive it.
--
-- Insertion/deletion of assignments must bump users.authz_version so stale
-- tokens cannot keep acting on the old subset (the auth middleware already
-- pins that to the authz_version claim).
CREATE TABLE user_branch_assignments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id       UUID NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
  granted_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ,
  CHECK (expires_at IS NULL OR expires_at > granted_at)
);

-- A user cannot be assigned the same branch twice (and soft-deletes must not
-- collide with live rows).
CREATE UNIQUE INDEX ux_user_branch_assignments_live
  ON user_branch_assignments(user_id, branch_id)
  WHERE deleted_at IS NULL;

-- Hot lookups: "what branches can this user act on in this org?"
CREATE INDEX ix_user_branch_assignments_user
  ON user_branch_assignments(user_id, organization_id)
  WHERE deleted_at IS NULL;

-- Reverse lookups for admin views.
CREATE INDEX ix_user_branch_assignments_branch
  ON user_branch_assignments(branch_id)
  WHERE deleted_at IS NULL;

-- Expiry sweep index.
CREATE INDEX ix_user_branch_assignments_expires
  ON user_branch_assignments(expires_at)
  WHERE deleted_at IS NULL AND expires_at IS NOT NULL;

CREATE TRIGGER tr_user_branch_assignments_upd
  BEFORE UPDATE ON user_branch_assignments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
