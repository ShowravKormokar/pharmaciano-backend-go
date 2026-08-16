-- ##### FILE: 000003_rbac_tables.up.sql #######################################
CREATE TABLE roles (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID REFERENCES organizations(id) ON DELETE CASCADE,
  name             VARCHAR(80)  NOT NULL,
  description      VARCHAR(500),
  is_active        BOOLEAN NOT NULL DEFAULT TRUE,
  is_system        BOOLEAN NOT NULL DEFAULT FALSE,
  priority         INTEGER NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
-- Unique role name per org; system roles are unique globally (organization_id NULL)
CREATE UNIQUE INDEX ux_roles_org_name ON roles(COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'::uuid), name)
  WHERE deleted_at IS NULL;
CREATE INDEX ix_roles_org ON roles(organization_id) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_roles_upd BEFORE UPDATE ON roles
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE permissions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  module       VARCHAR(60) NOT NULL,
  action       VARCHAR(40) NOT NULL,
  description  VARCHAR(255),
  is_system    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ,
  CONSTRAINT ux_permissions_module_action UNIQUE (module, action)
);
CREATE TRIGGER tr_permissions_upd BEFORE UPDATE ON permissions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE role_permissions (
  role_id       UUID NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
  permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
  granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  granted_by    UUID,
  PRIMARY KEY (role_id, permission_id)
);
CREATE INDEX ix_role_permissions_perm ON role_permissions(permission_id);
