CREATE TABLE outbox_events (
  id UUID PRIMARY KEY,
  organization_id UUID NOT NULL,
  source_module VARCHAR(64) NOT NULL
    CHECK (source_module ~ '^[a-z][a-z0-9_]*$'),
  branch_id UUID,
  correlation_id UUID,
  causation_id UUID,
  aggregate_type VARCHAR(64) NOT NULL
    CHECK (aggregate_type ~ '^[a-z][a-z0-9_]*$'),
  aggregate_id UUID NOT NULL,
  aggregate_version BIGINT NOT NULL CHECK (aggregate_version > 0),
  event_type VARCHAR(128) NOT NULL
    CHECK (event_type ~ '^[a-z][a-z0-9_.-]*$'),
  schema_version INTEGER NOT NULL CHECK (schema_version > 0),
  payload JSONB NOT NULL
    CHECK (jsonb_typeof(payload) = 'object'),
  occurred_at TIMESTAMPTZ NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
  retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
  dispatch_count INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_count >= 0),
  next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  last_error_class VARCHAR(48)
    CHECK (last_error_class IS NULL OR last_error_class ~ '^[a-z][a-z0-9_.-]*$'),
  last_error_at TIMESTAMPTZ,
  processed_at TIMESTAMPTZ,
  failed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_outbox_aggregate_version
    UNIQUE (organization_id, aggregate_type, aggregate_id, aggregate_version),
  CONSTRAINT ck_outbox_lease_state CHECK (
    (status = 'processing' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR
    (status <> 'processing' AND lease_token IS NULL AND lease_expires_at IS NULL)
  ),
  CONSTRAINT ck_outbox_terminal_state CHECK (
    (status = 'completed' AND processed_at IS NOT NULL AND failed_at IS NULL)
    OR
    (status = 'failed' AND processed_at IS NULL AND failed_at IS NOT NULL)
    OR
    (status IN ('pending', 'processing') AND processed_at IS NULL AND failed_at IS NULL)
  )
);

CREATE INDEX ix_outbox_ready
  ON outbox_events (next_retry_at, occurred_at, id)
  WHERE status IN ('pending', 'processing');

CREATE INDEX ix_outbox_aggregate_history
  ON outbox_events (
    organization_id, aggregate_type, aggregate_id, aggregate_version
  );

CREATE INDEX ix_outbox_failed
  ON outbox_events (failed_at, id)
  WHERE status = 'failed';

CREATE FUNCTION prevent_outbox_event_content_update() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(
    NEW.id, NEW.organization_id, NEW.source_module, NEW.branch_id,
    NEW.correlation_id, NEW.causation_id, NEW.aggregate_type,
    NEW.aggregate_id, NEW.aggregate_version, NEW.event_type,
    NEW.schema_version, NEW.payload, NEW.occurred_at
  ) IS DISTINCT FROM ROW(
    OLD.id, OLD.organization_id, OLD.source_module, OLD.branch_id,
    OLD.correlation_id, OLD.causation_id, OLD.aggregate_type,
    OLD.aggregate_id, OLD.aggregate_version, OLD.event_type,
    OLD.schema_version, OLD.payload, OLD.occurred_at
  ) THEN
    RAISE EXCEPTION 'outbox event contents are immutable';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_outbox_event_content_immutable
  BEFORE UPDATE ON outbox_events
  FOR EACH ROW EXECUTE FUNCTION prevent_outbox_event_content_update();
