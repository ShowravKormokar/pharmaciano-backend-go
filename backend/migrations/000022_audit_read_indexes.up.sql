-- Index audit reads by the tenant, branch, and occurrence-time predicates used
-- by the scoped cursor API. Creating these on the partitioned parent also
-- creates matching indexes on existing partitions and applies to new ones.
CREATE INDEX ix_audit_logs_org_timeline
  ON audit_logs (organization_id, created_at DESC, id DESC)
  WHERE organization_id IS NOT NULL;

CREATE INDEX ix_audit_logs_branch_timeline
  ON audit_logs (organization_id, branch_id, created_at DESC, id DESC)
  WHERE organization_id IS NOT NULL AND branch_id IS NOT NULL;

CREATE INDEX ix_audit_logs_org_user_timeline
  ON audit_logs (organization_id, user_id, created_at DESC, id DESC)
  WHERE organization_id IS NOT NULL AND user_id IS NOT NULL;

CREATE INDEX ix_audit_logs_platform_timeline
  ON audit_logs (created_at DESC, id DESC)
  WHERE organization_id IS NULL;

-- pg_trgm is installed by migration 000001. These indexes support the
-- repository's minimum-three-character wildcard search over scalar metadata.
CREATE INDEX ix_audit_logs_module_trgm
  ON audit_logs USING gin (module gin_trgm_ops);

CREATE INDEX ix_audit_logs_action_trgm
  ON audit_logs USING gin (action gin_trgm_ops);

CREATE INDEX ix_audit_logs_entity_type_trgm
  ON audit_logs USING gin (entity_type gin_trgm_ops)
  WHERE entity_type IS NOT NULL;

CREATE INDEX ix_audit_logs_request_id_trgm
  ON audit_logs USING gin (request_id gin_trgm_ops)
  WHERE request_id IS NOT NULL;

CREATE INDEX ix_audit_logs_error_code_trgm
  ON audit_logs USING gin (error_code gin_trgm_ops)
  WHERE error_code IS NOT NULL;
