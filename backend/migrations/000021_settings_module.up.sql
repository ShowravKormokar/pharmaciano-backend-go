-- Settings definitions are the allow-list. The legacy table is retained for
-- rollback, but its values are copied into typed JSONB rows.
DO $$
DECLARE
  duplicate_key TEXT;
  invalid_key TEXT;
  conflicting_key TEXT;
BEGIN
  SELECT key INTO duplicate_key
  FROM system_settings
  WHERE organization_id IS NULL AND deleted_at IS NULL
  GROUP BY key
  HAVING count(*) > 1
  LIMIT 1;
  IF duplicate_key IS NOT NULL THEN
    RAISE EXCEPTION 'settings migration: duplicate global key %; resolve duplicate rows before migrating', duplicate_key;
  END IF;

  SELECT key INTO invalid_key
  FROM system_settings
  WHERE deleted_at IS NULL
    AND key !~ '^[a-z0-9][a-z0-9._-]{0,119}$'
  LIMIT 1;
  IF invalid_key IS NOT NULL THEN
    RAISE EXCEPTION 'settings migration: invalid setting key %; rename or remove the row before migrating', invalid_key;
  END IF;

  SELECT key INTO conflicting_key
  FROM system_settings
  WHERE deleted_at IS NULL
  GROUP BY key
  HAVING count(DISTINCT CASE value_type
    WHEN 'int' THEN 'integer'
    WHEN 'bool' THEN 'boolean'
    WHEN 'duration' THEN 'string'
    ELSE value_type
  END) > 1
  LIMIT 1;
  IF conflicting_key IS NOT NULL THEN
    RAISE EXCEPTION 'settings migration: conflicting value types for key %; resolve inconsistent legacy definitions before migrating', conflicting_key;
  END IF;

  SELECT key INTO invalid_key
  FROM system_settings
  WHERE deleted_at IS NULL
    AND (
      (value_type IN ('int', 'integer') AND
        (value !~ '^-?[0-9]+$' OR NOT pg_input_is_valid(value, 'bigint')))
      OR (value_type = 'float' AND
        (value !~ '^-?[0-9]+([.][0-9]+)?([eE][+-]?[0-9]+)?$'
          OR NOT pg_input_is_valid(value, 'double precision')))
      OR (value_type IN ('bool', 'boolean') AND lower(value) NOT IN ('true', 'false'))
      OR (value_type = 'json' AND NOT pg_input_is_valid(value, 'jsonb'))
      OR value_type NOT IN ('string', 'int', 'integer', 'float', 'bool', 'boolean', 'json')
    )
  LIMIT 1;
  IF invalid_key IS NOT NULL THEN
    RAISE EXCEPTION 'settings migration: invalid or unsupported legacy value/type for key %; correct the row before migrating', invalid_key;
  END IF;
END $$;

CREATE TABLE setting_definitions (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key                   VARCHAR(120) NOT NULL UNIQUE,
  value_type            VARCHAR(20) NOT NULL
                          CHECK (value_type IN ('string','integer','boolean','float','json')),
  schema_version        INTEGER NOT NULL DEFAULT 1 CHECK (schema_version > 0),
  default_value         JSONB,
  allowed_scopes        TEXT[] NOT NULL
                          CHECK (cardinality(allowed_scopes) > 0
                            AND allowed_scopes <@ ARRAY['system','organization']::TEXT[]),
  description           TEXT,
  is_sensitive          BOOLEAN NOT NULL DEFAULT FALSE,
  secret_reference_only BOOLEAN NOT NULL DEFAULT FALSE,
  validator_key         VARCHAR(120),
  status                VARCHAR(20) NOT NULL DEFAULT 'deprecated'
                          CHECK (status IN ('active','deprecated')),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ
);

CREATE TABLE system_setting_values (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key         VARCHAR(120) NOT NULL REFERENCES setting_definitions(key) ON DELETE RESTRICT,
  value       JSONB NOT NULL,
  version     BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
  updated_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_system_setting_values_key_live
  ON system_setting_values(key) WHERE deleted_at IS NULL;
CREATE INDEX ix_system_setting_values_key_live
  ON system_setting_values(key) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_system_setting_values_upd BEFORE UPDATE ON system_setting_values
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE organization_setting_values (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  key             VARCHAR(120) NOT NULL REFERENCES setting_definitions(key) ON DELETE RESTRICT,
  value           JSONB NOT NULL,
  version         BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
  updated_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_organization_setting_values_owner_key_live
  ON organization_setting_values(organization_id, key) WHERE deleted_at IS NULL;
CREATE INDEX ix_organization_setting_values_org_key_live
  ON organization_setting_values(organization_id, key) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_organization_setting_values_upd BEFORE UPDATE ON organization_setting_values
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE setting_revisions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key             VARCHAR(120) NOT NULL REFERENCES setting_definitions(key) ON DELETE RESTRICT,
  scope           VARCHAR(20) NOT NULL CHECK (scope IN ('system','organization')),
  -- Retain the organization identifier as an audit snapshot after tenant deletion.
  organization_id UUID,
  version         BIGINT NOT NULL CHECK (version >= 1),
  schema_version  INTEGER NOT NULL CHECK (schema_version > 0),
  actor_id        UUID REFERENCES users(id) ON DELETE SET NULL,
  change_reason   VARCHAR(500),
  correlation_id  VARCHAR(512),
  previous_value  JSONB,
  new_value       JSONB,
  values_redacted BOOLEAN NOT NULL DEFAULT FALSE,
  changed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((scope = 'system' AND organization_id IS NULL)
      OR (scope = 'organization' AND organization_id IS NOT NULL))
);
CREATE INDEX ix_setting_revisions_key_scope_changed
  ON setting_revisions(key, scope, organization_id, changed_at DESC);
CREATE INDEX ix_setting_revisions_actor_changed
  ON setting_revisions(actor_id, changed_at DESC) WHERE actor_id IS NOT NULL;

CREATE FUNCTION reject_setting_revision_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'setting revisions are append-only';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER tr_setting_revisions_immutable
  BEFORE UPDATE OR DELETE ON setting_revisions
  FOR EACH ROW EXECUTE FUNCTION reject_setting_revision_mutation();

INSERT INTO setting_definitions
  (key, value_type, schema_version, allowed_scopes, description, is_sensitive, status)
SELECT
  s.key,
  CASE max(s.value_type)
    WHEN 'int' THEN 'integer'
    WHEN 'bool' THEN 'boolean'
    WHEN 'duration' THEN 'string'
    ELSE max(s.value_type)
  END,
  1,
  CASE
    WHEN bool_or(s.organization_id IS NOT NULL) THEN ARRAY['system','organization']::TEXT[]
    ELSE ARRAY['system']::TEXT[]
  END,
  max(NULLIF(s.description, '')),
  bool_or(s.is_sensitive),
  'deprecated'
FROM system_settings s
WHERE s.deleted_at IS NULL
GROUP BY s.key;

INSERT INTO system_setting_values
  (id, key, value, version, updated_by, created_at, updated_at)
SELECT
  s.id,
  s.key,
  CASE s.value_type
    WHEN 'int' THEN to_jsonb(s.value::BIGINT)
    WHEN 'integer' THEN to_jsonb(s.value::BIGINT)
    WHEN 'bool' THEN to_jsonb(s.value::BOOLEAN)
    WHEN 'boolean' THEN to_jsonb(s.value::BOOLEAN)
    WHEN 'float' THEN to_jsonb(s.value::NUMERIC)
    WHEN 'json' THEN s.value::JSONB
    ELSE to_jsonb(s.value)
  END,
  1,
  s.updated_by,
  s.created_at,
  s.updated_at
FROM system_settings s
WHERE s.organization_id IS NULL AND s.deleted_at IS NULL;

INSERT INTO organization_setting_values
  (id, organization_id, key, value, version, updated_by, created_at, updated_at)
SELECT
  s.id,
  s.organization_id,
  s.key,
  CASE s.value_type
    WHEN 'int' THEN to_jsonb(s.value::BIGINT)
    WHEN 'integer' THEN to_jsonb(s.value::BIGINT)
    WHEN 'bool' THEN to_jsonb(s.value::BOOLEAN)
    WHEN 'boolean' THEN to_jsonb(s.value::BOOLEAN)
    WHEN 'float' THEN to_jsonb(s.value::NUMERIC)
    WHEN 'json' THEN s.value::JSONB
    ELSE to_jsonb(s.value)
  END,
  1,
  s.updated_by,
  s.created_at,
  s.updated_at
FROM system_settings s
WHERE s.organization_id IS NOT NULL AND s.deleted_at IS NULL;

INSERT INTO setting_revisions
  (key, scope, organization_id, version, schema_version, actor_id,
   previous_value, new_value, values_redacted, changed_at)
SELECT
  s.key,
  CASE WHEN s.organization_id IS NULL THEN 'system' ELSE 'organization' END,
  s.organization_id,
  1,
  1,
  s.updated_by,
  NULL,
  CASE WHEN d.is_sensitive THEN NULL ELSE
    CASE s.value_type
      WHEN 'int' THEN to_jsonb(s.value::BIGINT)
      WHEN 'integer' THEN to_jsonb(s.value::BIGINT)
      WHEN 'bool' THEN to_jsonb(s.value::BOOLEAN)
      WHEN 'boolean' THEN to_jsonb(s.value::BOOLEAN)
      WHEN 'float' THEN to_jsonb(s.value::NUMERIC)
      WHEN 'json' THEN s.value::JSONB
      ELSE to_jsonb(s.value)
    END
  END,
  d.is_sensitive,
  s.updated_at
FROM system_settings s
JOIN setting_definitions d ON d.key = s.key
WHERE s.deleted_at IS NULL;
