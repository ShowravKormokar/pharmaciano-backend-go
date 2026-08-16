-- ##### FILE: 000011_audit_partitioned.up.sql #################################
-- Partitioned audit_logs by month on created_at.
CREATE TABLE audit_logs (
  id               UUID NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL,
  organization_id  UUID,
  branch_id        UUID,
  user_id          UUID,
  role_name        VARCHAR(80),
  session_id       UUID,
  request_id       VARCHAR(80),
  module           VARCHAR(60) NOT NULL,
  action           VARCHAR(40) NOT NULL,
  entity_type      VARCHAR(60),
  entity_id        UUID,
  ip               INET,
  browser          VARCHAR(80),
  os               VARCHAR(80),
  device           VARCHAR(40),
  location         VARCHAR(100),
  user_agent       VARCHAR(500),
  details          JSONB,
  before_data      JSONB,
  after_data       JSONB,
  outcome          VARCHAR(20) NOT NULL CHECK (outcome IN ('success','failure')),
  error_code       VARCHAR(40),
  duration_ms      INTEGER,
  PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Create a default partition for future/unknown months.
CREATE TABLE audit_logs_default PARTITION OF audit_logs DEFAULT;

-- We need a trigger to prevent updates/deletes (append-only).
CREATE TRIGGER tr_audit_logs_write_protect BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION prevent_write();

-- Optional: create index on commonly queried columns.
CREATE INDEX ix_audit_logs_org ON audit_logs(organization_id) WHERE organization_id IS NOT NULL;
CREATE INDEX ix_audit_logs_user ON audit_logs(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX ix_audit_logs_module ON audit_logs(module);
CREATE INDEX ix_audit_logs_entity ON audit_logs(entity_type, entity_id) WHERE entity_id IS NOT NULL;
CREATE INDEX ix_audit_logs_outcome ON audit_logs(outcome);

-- Archive table (unpartitioned, but can be partitioned similarly if needed).
CREATE TABLE audit_log_archive (LIKE audit_logs INCLUDING ALL);
-- Remove the default partition inheritance? Actually archive is separate, not partitioned.
ALTER TABLE audit_log_archive DROP CONSTRAINT IF EXISTS audit_logs_pkey;
ALTER TABLE audit_log_archive ADD PRIMARY KEY (id);

CREATE INDEX ix_audit_log_archive_created ON audit_log_archive(created_at DESC);
CREATE INDEX ix_audit_log_archive_org ON audit_log_archive(organization_id);
CREATE INDEX ix_audit_log_archive_user ON audit_log_archive(user_id);
CREATE TRIGGER tr_audit_log_archive_write_protect BEFORE UPDATE OR DELETE ON audit_log_archive
  FOR EACH ROW EXECUTE FUNCTION prevent_write();