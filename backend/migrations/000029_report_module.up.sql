-- Report module schema (MVP: jobs, durable idempotency, artifact metadata,
-- provenance, and attempt history). Schedule tables are deferred to Phase 4.
-- Additive only; never modifies an applied migration.
CREATE TABLE report_jobs (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  requested_by          UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  report_code           TEXT NOT NULL CHECK (length(report_code) > 0),
  definition_version    INTEGER NOT NULL CHECK (definition_version > 0),
  format                TEXT NOT NULL CHECK (format IN ('csv', 'xlsx', 'pdf')),
  normalized_filters    JSONB NOT NULL CHECK (jsonb_typeof(normalized_filters) = 'object'),
  filters_hash          BYTEA NOT NULL,
  scope_snapshot        JSONB NOT NULL CHECK (jsonb_typeof(scope_snapshot) = 'object'),
  scope_hash            BYTEA NOT NULL,
  status                TEXT NOT NULL
                         CHECK (status IN ('queued','running','retry_wait','succeeded','failed','cancelled')),
  priority              SMALLINT NOT NULL DEFAULT 0 CHECK (priority >= 0),
  available_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  request_hash          BYTEA NOT NULL,
  attempt_count         INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  max_attempts          INTEGER NOT NULL CHECK (max_attempts > 0),
  queued_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at            TIMESTAMPTZ,
  completed_at          TIMESTAMPTZ,
  cancel_requested_at   TIMESTAMPTZ,
  lease_owner           TEXT,
  lease_until           TIMESTAMPTZ,
  lease_generation      BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
  heartbeat_at          TIMESTAMPTZ,
  failure_code          VARCHAR(64)
                         CHECK (failure_code IS NULL OR failure_code ~ '^[A-Z][A-Z0-9_]*$'),
  failure_message_safe  TEXT,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  version               BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  CONSTRAINT ux_report_jobs_org_id UNIQUE (organization_id, id),
  CONSTRAINT ck_report_jobs_lease_state CHECK (
    (status IN ('running','cancel_requested') AND lease_owner IS NOT NULL AND lease_until IS NOT NULL)
    OR
    (status NOT IN ('running','cancel_requested') AND lease_owner IS NULL AND lease_until IS NULL)
  ),
  CONSTRAINT ck_report_jobs_terminal CHECK (
    (status = 'succeeded' AND completed_at IS NOT NULL)
    OR (status IN ('failed','cancelled') AND completed_at IS NOT NULL)
    OR (status IN ('queued','running','retry_wait') AND completed_at IS NULL)
  )
);

CREATE INDEX ix_report_jobs_list
  ON report_jobs (organization_id, requested_by, created_at DESC, id DESC);

CREATE INDEX ix_report_jobs_queue
  ON report_jobs (priority DESC, available_at, id)
  WHERE status IN ('queued', 'retry_wait');

CREATE INDEX ix_report_jobs_recovery
  ON report_jobs (lease_until)
  WHERE status = 'running';

CREATE INDEX ix_report_jobs_code_status
  ON report_jobs (organization_id, report_code, status, created_at DESC);

-- Durable request idempotency for POST /reports/generate. PostgreSQL is the
-- authority; Redis middleware is a best-effort HTTP replay layer only.
CREATE TABLE report_idempotency (
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  requested_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  key_hmac         TEXT NOT NULL CHECK (length(key_hmac) > 0),
  hmac_key_version INTEGER NOT NULL CHECK (hmac_key_version > 0),
  request_hash     BYTEA NOT NULL,
  job_id           UUID,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NOT NULL,
  CONSTRAINT ux_report_idempotency UNIQUE (organization_id, requested_by, key_hmac),
  CONSTRAINT fk_report_idempotency_job
    FOREIGN KEY (organization_id, job_id)
    REFERENCES report_jobs (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX ix_report_idempotency_expiry
  ON report_idempotency (expires_at)
  WHERE job_id IS NULL;

-- Artifact metadata. Storage object keys are opaque and never public URLs.
CREATE TABLE report_artifacts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  job_id           UUID NOT NULL,
  format           TEXT NOT NULL CHECK (format IN ('csv', 'xlsx', 'pdf')),
  storage_provider TEXT NOT NULL CHECK (length(storage_provider) > 0),
  storage_object_key TEXT NOT NULL CHECK (length(storage_object_key) > 0),
  mime_type        TEXT NOT NULL CHECK (length(mime_type) > 0),
  file_name_safe   TEXT NOT NULL CHECK (length(file_name_safe) > 0),
  size_bytes       BIGINT NOT NULL CHECK (size_bytes >= 0),
  checksum_sha256  TEXT NOT NULL CHECK (length(checksum_sha256) = 64),
  encryption_key_ref VARCHAR(255),
  status           TEXT NOT NULL
                   CHECK (status IN ('staging','available','revoked','deleting','deleted','corrupt','expired')),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NOT NULL,
  deleted_at       TIMESTAMPTZ,
  CONSTRAINT ux_report_artifacts_job UNIQUE (organization_id, job_id, format),
  CONSTRAINT fk_report_artifacts_job
    FOREIGN KEY (organization_id, job_id)
    REFERENCES report_jobs (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX ix_report_artifacts_expiry
  ON report_artifacts (organization_id, expires_at)
  WHERE status = 'available';

-- Immutable execution provenance (one row per completed job).
CREATE TABLE report_run_provenance (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  job_id              UUID NOT NULL,
  definition_version  INTEGER NOT NULL CHECK (definition_version > 0),
  renderer_version    TEXT NOT NULL CHECK (length(renderer_version) > 0),
  normalized_request_hash BYTEA NOT NULL,
  scope_snapshot      JSONB NOT NULL CHECK (jsonb_typeof(scope_snapshot) = 'object'),
  source_watermarks   JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(source_watermarks) = 'object'),
  data_as_of          TIMESTAMPTZ,
  consistency_mode    TEXT NOT NULL CHECK (consistency_mode IN ('repeatable_read','snapshot','best_effort')),
  timezone            TEXT NOT NULL CHECK (length(timezone) > 0),
  resolved_from       TIMESTAMPTZ,
  resolved_to         TIMESTAMPTZ,
  row_count           BIGINT NOT NULL CHECK (row_count >= 0),
  warning_codes       TEXT[] NOT NULL DEFAULT '{}',
  completeness        TEXT NOT NULL CHECK (completeness IN ('complete','partial','degraded')),
  checksum_sha256     TEXT NOT NULL CHECK (length(checksum_sha256) = 64),
  generated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_report_run_provenance_job UNIQUE (organization_id, job_id),
  CONSTRAINT fk_report_run_provenance_job
    FOREIGN KEY (organization_id, job_id)
    REFERENCES report_jobs (organization_id, id) ON DELETE RESTRICT
);

-- Append-only execution attempt history (one row per attempt).
CREATE TABLE report_job_attempts (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  job_id             UUID NOT NULL,
  attempt_number     INTEGER NOT NULL CHECK (attempt_number > 0),
  lease_generation   BIGINT NOT NULL CHECK (lease_generation >= 0),
  task_id            TEXT NOT NULL CHECK (length(task_id) > 0),
  correlation_id     TEXT,
  worker_identity    TEXT,
  started_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at           TIMESTAMPTZ,
  outcome_class      TEXT NOT NULL CHECK (outcome_class IN ('succeeded','failed','cancelled','retried')),
  safe_error_code    VARCHAR(64)
                     CHECK (safe_error_code IS NULL OR safe_error_code ~ '^[A-Z][A-Z0-9_]*$'),
  safe_error_message TEXT,
  retry_delay_ms     BIGINT CHECK (retry_delay_ms >= 0),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_report_job_attempts UNIQUE (organization_id, job_id, attempt_number),
  CONSTRAINT fk_report_job_attempts_job
    FOREIGN KEY (organization_id, job_id)
    REFERENCES report_jobs (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX ix_report_job_attempts_job
  ON report_job_attempts (organization_id, job_id, attempt_number);

-- Function to keep updated_at fresh on report_jobs.
CREATE FUNCTION set_updated_at() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at = now();
  NEW.version = OLD.version + 1;
  RETURN NEW;
END;
$$;

CREATE TRIGGER tr_report_jobs_upd BEFORE UPDATE ON report_jobs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
