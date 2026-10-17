-- ##### FILE: 000023_backup_module.up.sql ###################################
-- System-scoped encrypted logical-backup metadata. Artifact bytes live in a
-- private provider-neutral store; this schema never stores database URLs,
-- plaintext dumps, signing URLs, or plaintext encryption keys.

CREATE TABLE system_backup_runs (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  backup_type                 VARCHAR(20) NOT NULL CHECK (backup_type IN ('full')),
  status                      VARCHAR(20) NOT NULL CHECK (status IN ('queued','in_progress','succeeded','failed','cancelled','expired')),
  trigger                     VARCHAR(20) NOT NULL CHECK (trigger IN ('manual','scheduled')),
  triggered_by                UUID REFERENCES users(id) ON DELETE SET NULL,
  reason                      VARCHAR(500),
  started_at                  TIMESTAMPTZ,
  finished_at                 TIMESTAMPTZ,
  size_bytes                  BIGINT CHECK (size_bytes >= 0),
  sha256                      CHAR(64) CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  storage_provider            VARCHAR(64),
  storage_ref                 TEXT,
  manifest_ref                TEXT,
  encryption_algorithm        VARCHAR(64),
  encryption_key_id           VARCHAR(255),
  wrapped_dek                 BYTEA,
  encryption_format_version   INTEGER CHECK (encryption_format_version > 0),
  postgres_version            VARCHAR(255),
  pg_dump_version             VARCHAR(255),
  schema_version              BIGINT CHECK (schema_version >= 0),
  latest_verification_status  VARCHAR(20) NOT NULL DEFAULT 'unavailable'
                                CHECK (latest_verification_status IN ('queued','in_progress','succeeded','failed','unavailable')),
  verified_at                 TIMESTAMPTZ,
  expires_at                  TIMESTAMPTZ,
  legal_hold                  BOOLEAN NOT NULL DEFAULT FALSE,
  artifact_deleted_at         TIMESTAMPTZ,
  error_code                  VARCHAR(100),
  error_message               VARCHAR(500),
  attempt_count               INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  lease_generation            BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
  lease_owner                 VARCHAR(255),
  lease_expires_at            TIMESTAMPTZ,
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at                  TIMESTAMPTZ,
  CONSTRAINT ck_system_backup_runs_time_order
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at),
  CONSTRAINT ck_system_backup_runs_success_complete
    CHECK (status <> 'succeeded' OR (
      started_at IS NOT NULL AND finished_at IS NOT NULL AND size_bytes IS NOT NULL
      AND sha256 IS NOT NULL AND storage_provider IS NOT NULL AND storage_ref IS NOT NULL
      AND encryption_algorithm IS NOT NULL AND encryption_key_id IS NOT NULL
      AND wrapped_dek IS NOT NULL AND encryption_format_version IS NOT NULL
    )),
  CONSTRAINT ck_system_backup_runs_active_incomplete
    CHECK (status NOT IN ('queued','in_progress') OR (
      finished_at IS NULL AND size_bytes IS NULL AND sha256 IS NULL
      AND storage_provider IS NULL AND storage_ref IS NULL AND manifest_ref IS NULL
      AND encryption_algorithm IS NULL AND encryption_key_id IS NULL
      AND wrapped_dek IS NULL AND encryption_format_version IS NULL
    )),
  CONSTRAINT ck_system_backup_runs_verified_at
    CHECK (verified_at IS NULL OR latest_verification_status = 'succeeded'),
  CONSTRAINT ck_system_backup_runs_artifact_delete
    CHECK (artifact_deleted_at IS NULL OR status = 'succeeded')
);

CREATE UNIQUE INDEX ux_system_backup_runs_one_active_full
  ON system_backup_runs (backup_type)
  WHERE status IN ('queued', 'in_progress') AND deleted_at IS NULL;
CREATE INDEX ix_system_backup_runs_timeline
  ON system_backup_runs (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_system_backup_runs_status_timeline
  ON system_backup_runs (status, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_system_backup_runs_expiry
  ON system_backup_runs (expires_at) WHERE expires_at IS NOT NULL AND artifact_deleted_at IS NULL;

CREATE TRIGGER tr_system_backup_runs_upd BEFORE UPDATE ON system_backup_runs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE system_backup_verifications (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  backup_id                 UUID NOT NULL REFERENCES system_backup_runs(id) ON DELETE RESTRICT,
  verification_type         VARCHAR(20) NOT NULL CHECK (verification_type IN ('integrity','restore_drill')),
  status                    VARCHAR(20) NOT NULL CHECK (status IN ('queued','in_progress','succeeded','failed','unavailable')),
  started_at                TIMESTAMPTZ,
  finished_at               TIMESTAMPTZ,
  checksum_ok               BOOLEAN,
  decrypt_ok                BOOLEAN,
  restore_ok                BOOLEAN,
  schema_version_expected   BIGINT CHECK (schema_version_expected >= 0),
  schema_version_restored   BIGINT CHECK (schema_version_restored >= 0),
  sanity_check_summary      JSONB,
  measured_recovery_point   TIMESTAMPTZ,
  measured_restore_time_ms  BIGINT CHECK (measured_restore_time_ms >= 0),
  scratch_target_id         VARCHAR(255),
  cleanup_completed_at      TIMESTAMPTZ,
  error_code                VARCHAR(100),
  error_message             VARCHAR(500),
  worker_version            VARCHAR(255),
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ck_system_backup_verifications_time_order
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at),
  CONSTRAINT ck_system_backup_verifications_success_complete
    CHECK (status <> 'succeeded' OR (started_at IS NOT NULL AND finished_at IS NOT NULL AND decrypt_ok IS TRUE)),
  CONSTRAINT ck_system_backup_verifications_restore_result
    CHECK (verification_type = 'restore_drill' OR restore_ok IS NULL),
  CONSTRAINT ck_system_backup_verifications_integrity_result
    CHECK (verification_type <> 'integrity' OR measured_restore_time_ms IS NULL),
  CONSTRAINT ck_system_backup_verifications_sanity_summary
    CHECK (sanity_check_summary IS NULL OR jsonb_typeof(sanity_check_summary) = 'object')
);

CREATE INDEX ix_system_backup_verifications_backup_timeline
  ON system_backup_verifications (backup_id, created_at DESC, id DESC);
CREATE INDEX ix_system_backup_verifications_status_timeline
  ON system_backup_verifications (status, created_at DESC, id DESC);

CREATE FUNCTION reject_completed_backup_verification_mutation() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' OR OLD.finished_at IS NOT NULL THEN
    RAISE EXCEPTION 'completed backup verification evidence is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tr_system_backup_verifications_immutable
  BEFORE UPDATE OR DELETE ON system_backup_verifications
  FOR EACH ROW EXECUTE FUNCTION reject_completed_backup_verification_mutation();
