-- Preserve the latest live values in the legacy table before removing the
-- replacement schema. Revision history has no equivalent legacy representation
-- and must be retained in a database backup before rolling back.
UPDATE system_settings AS legacy
SET value = CASE definition.value_type
      WHEN 'string' THEN COALESCE(setting.value #>> '{}', 'null')
      ELSE setting.value::TEXT
    END,
    value_type = CASE definition.value_type
      WHEN 'integer' THEN 'int'
      WHEN 'boolean' THEN 'bool'
      ELSE definition.value_type
    END,
    description = definition.description,
    is_sensitive = definition.is_sensitive,
    updated_by = setting.updated_by,
    updated_at = setting.updated_at,
    deleted_at = NULL
FROM system_setting_values AS setting
JOIN setting_definitions AS definition ON definition.key = setting.key
WHERE legacy.organization_id IS NULL
  AND legacy.key = setting.key
  AND legacy.deleted_at IS NULL
  AND setting.deleted_at IS NULL;

INSERT INTO system_settings
  (key, value, value_type, description, is_sensitive, updated_by, created_at, updated_at)
SELECT
  setting.key,
  CASE definition.value_type
    WHEN 'string' THEN COALESCE(setting.value #>> '{}', 'null')
    ELSE setting.value::TEXT
  END,
  CASE definition.value_type
    WHEN 'integer' THEN 'int'
    WHEN 'boolean' THEN 'bool'
    ELSE definition.value_type
  END,
  definition.description,
  definition.is_sensitive,
  setting.updated_by,
  setting.created_at,
  setting.updated_at
FROM system_setting_values AS setting
JOIN setting_definitions AS definition ON definition.key = setting.key
WHERE setting.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM system_settings AS legacy
    WHERE legacy.organization_id IS NULL
      AND legacy.key = setting.key
      AND legacy.deleted_at IS NULL
  );

INSERT INTO system_settings
  (organization_id, key, value, value_type, description, is_sensitive,
   updated_by, created_at, updated_at)
SELECT
  setting.organization_id,
  setting.key,
  CASE definition.value_type
    WHEN 'string' THEN COALESCE(setting.value #>> '{}', 'null')
    ELSE setting.value::TEXT
  END,
  CASE definition.value_type
    WHEN 'integer' THEN 'int'
    WHEN 'boolean' THEN 'bool'
    ELSE definition.value_type
  END,
  definition.description,
  definition.is_sensitive,
  setting.updated_by,
  setting.created_at,
  setting.updated_at
FROM organization_setting_values AS setting
JOIN setting_definitions AS definition ON definition.key = setting.key
WHERE setting.deleted_at IS NULL
ON CONFLICT (organization_id, key) DO UPDATE SET
  value = EXCLUDED.value,
  value_type = EXCLUDED.value_type,
  description = EXCLUDED.description,
  is_sensitive = EXCLUDED.is_sensitive,
  updated_by = EXCLUDED.updated_by,
  updated_at = EXCLUDED.updated_at,
  deleted_at = NULL;

DROP TABLE IF EXISTS setting_revisions;
DROP FUNCTION IF EXISTS reject_setting_revision_mutation();
DROP TABLE IF EXISTS organization_setting_values;
DROP TABLE IF EXISTS system_setting_values;
DROP TABLE IF EXISTS setting_definitions;
