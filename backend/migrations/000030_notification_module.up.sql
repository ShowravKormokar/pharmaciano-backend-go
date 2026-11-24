-- Creates the Notification module's durable in-app inbox tables.
-- See docs/adr/NOTIFICATION_CONTRACT.md §6 (database contract).
-- No cascading deletes from user, branch, or source aggregate into
-- historical notification content.

CREATE TABLE notifications (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL REFERENCES organizations(id),
  branch_id             UUID REFERENCES branches(id),
  event_id              UUID,
  source_module         VARCHAR(64) NOT NULL
    CHECK (source_module ~ '^[a-z][a-z0-9_]*$'),
  source_event_type     VARCHAR(128) NOT NULL
    CHECK (source_event_type ~ '^[a-z][a-z0-9_.-]*$'),
  source_aggregate_type VARCHAR(64) NOT NULL
    CHECK (source_aggregate_type ~ '^[a-z][a-z0-9_]*$'),
  source_aggregate_id   UUID,
  source_aggregate_version BIGINT,
  type                  VARCHAR(128) NOT NULL,
  priority              VARCHAR(16) NOT NULL
    CHECK (priority IN ('low', 'normal', 'high', 'critical')),
  title                 TEXT NOT NULL CHECK (char_length(title) <= 200),
  message               TEXT NOT NULL CHECK (char_length(message) <= 2000),
  data                  JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(data) = 'object'),
  action_url            TEXT,
  created_by_user_id    UUID REFERENCES users(id),
  content_version       INTEGER NOT NULL DEFAULT 1 CHECK (content_version > 0),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at            TIMESTAMPTZ,
  CONSTRAINT ck_notification_expires_after_created
    CHECK (expires_at IS NULL OR expires_at > created_at),
  CONSTRAINT ck_notification_action_url_internal
    CHECK (action_url IS NULL OR action_url ~ '^/[a-zA-Z0-9_/.-]*$')
);

CREATE INDEX ix_notifications_org_created
  ON notifications (organization_id, created_at DESC, id DESC);

CREATE INDEX ix_notifications_org_branch_created
  ON notifications (organization_id, branch_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX ux_notification_source_event_category
  ON notifications (organization_id, source_event_type, type)
  WHERE source_event_type IS NOT NULL;

CREATE TABLE notification_recipients (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id),
  notification_id  UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users(id),
  read_at          TIMESTAMPTZ,
  dismissed_at     TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_notification_recipient_unique
    UNIQUE (organization_id, notification_id, user_id)
);

CREATE INDEX ix_notification_recipients_user_created
  ON notification_recipients (organization_id, user_id, created_at DESC, notification_id DESC);

CREATE INDEX ix_notification_recipients_unread
  ON notification_recipients (organization_id, user_id, created_at DESC, notification_id DESC)
  WHERE read_at IS NULL AND dismissed_at IS NULL;

CREATE TABLE notification_event_receipts (
  organization_id UUID NOT NULL REFERENCES organizations(id),
  consumer_name   VARCHAR(128) NOT NULL,
  event_id        UUID NOT NULL,
  event_type      VARCHAR(128) NOT NULL,
  processed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  result          VARCHAR(16) NOT NULL
    CHECK (result IN ('created', 'suppressed', 'ignored')),
  notification_id UUID REFERENCES notifications(id),
  payload_hash    TEXT,
  PRIMARY KEY (organization_id, consumer_name, event_id)
);

CREATE TABLE notification_broadcasts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id),
  creator_user_id  UUID NOT NULL REFERENCES users(id),
  scope            VARCHAR(16) NOT NULL
    CHECK (scope IN ('org', 'branch', 'role', 'user')),
  target_id        UUID,
  title            TEXT NOT NULL CHECK (char_length(title) <= 200),
  message          TEXT NOT NULL CHECK (char_length(message) <= 2000),
  priority         VARCHAR(16) NOT NULL DEFAULT 'normal'
    CHECK (priority IN ('low', 'normal', 'high', 'critical')),
  action_url       TEXT,
  status           VARCHAR(16) NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'completed', 'partial', 'failed', 'cancelled')),
  requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at       TIMESTAMPTZ,
  finished_at      TIMESTAMPTZ,
  audience_count   INTEGER NOT NULL DEFAULT 0 CHECK (audience_count >= 0),
  inserted_count   INTEGER NOT NULL DEFAULT 0 CHECK (inserted_count >= 0),
  suppressed_count INTEGER NOT NULL DEFAULT 0 CHECK (suppressed_count >= 0),
  failed_count     INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
  idempotency_key  VARCHAR(255) NOT NULL,
  request_hash     VARCHAR(64) NOT NULL,
  failure_code     VARCHAR(48),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_notification_broadcast_idempotency UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX ix_notification_broadcasts_org_status
  ON notification_broadcasts (organization_id, status, requested_at DESC);

CREATE TABLE notification_broadcast_batches (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id),
  broadcast_id     UUID NOT NULL REFERENCES notification_broadcasts(id) ON DELETE CASCADE,
  batch_number     INTEGER NOT NULL CHECK (batch_number > 0),
  status           VARCHAR(16) NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'completed', 'retry', 'failed', 'cancelled')),
  lease_owner      VARCHAR(128),
  lease_until      TIMESTAMPTZ,
  lease_generation INTEGER NOT NULL DEFAULT 0,
  attempt_count    INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error_code  VARCHAR(48),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_notification_broadcast_batch UNIQUE (organization_id, broadcast_id, batch_number)
);

CREATE INDEX ix_notification_broadcast_batches_claim
  ON notification_broadcast_batches (status, next_attempt_at, lease_until, id)
  WHERE status IN ('pending', 'retry')
    AND (lease_until IS NULL OR lease_until <= now());

CREATE TABLE notification_preferences (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id),
  user_id          UUID NOT NULL REFERENCES users(id),
  category         VARCHAR(128) NOT NULL,
  channel          VARCHAR(32) NOT NULL DEFAULT 'in_app',
  enabled          BOOLEAN NOT NULL DEFAULT true,
  version          INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ux_notification_preference UNIQUE (organization_id, user_id, category, channel)
);

CREATE TABLE category_policies (
  category         VARCHAR(128) PRIMARY KEY,
  owner_module     VARCHAR(64) NOT NULL
    CHECK (owner_module ~ '^[a-z][a-z0-9_]*$'),
  default_priority VARCHAR(16) NOT NULL DEFAULT 'normal'
    CHECK (default_priority IN ('low', 'normal', 'high', 'critical')),
  mandatory        BOOLEAN NOT NULL DEFAULT false,
  preference_policy VARCHAR(32) NOT NULL DEFAULT 'configurable'
    CHECK (preference_policy IN ('configurable', 'mandatory_only')),
  content_version  INTEGER NOT NULL DEFAULT 1 CHECK (content_version > 0),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_category_policies_owner ON category_policies (owner_module);

-- Seed controlled categories (contract §5.1 examples).
INSERT INTO category_policies (category, owner_module, default_priority, mandatory, preference_policy, content_version) VALUES
  ('inventory.low_stock', 'inventory', 'high', false, 'configurable', 1),
  ('inventory.expiry_soon', 'inventory', 'high', false, 'configurable', 1),
  ('inventory.adjustment_posted', 'inventory', 'normal', false, 'configurable', 1),
  ('inventory.transfer_pending', 'inventory', 'normal', false, 'configurable', 1),
  ('purchase.approval_needed', 'purchase', 'high', false, 'configurable', 1),
  ('purchase.receipt_posted', 'purchase', 'normal', false, 'configurable', 1),
  ('purchase.payment_due', 'purchase', 'high', false, 'configurable', 1),
  ('sale.refund_completed', 'sale', 'normal', false, 'configurable', 1),
  ('sale.voided', 'sale', 'high', false, 'configurable', 1),
  ('ledger.period_closed', 'ledger', 'normal', false, 'configurable', 1),
  ('report.ready', 'report', 'normal', false, 'configurable', 1),
  ('report.failed', 'report', 'high', false, 'configurable', 1),
  ('system.security_notice', 'system', 'critical', true, 'mandatory_only', 1),
  ('system.broadcast', 'system', 'normal', false, 'configurable', 1);
