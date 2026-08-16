-- ##### FILE: 000012_notification_settings.up.sql #############################
CREATE TABLE notifications (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id          UUID REFERENCES branches(id) ON DELETE SET NULL,
  user_id            UUID REFERENCES users(id) ON DELETE SET NULL,
  type               VARCHAR(60) NOT NULL,
  priority           VARCHAR(20) NOT NULL DEFAULT 'normal'
                       CHECK (priority IN ('low','normal','high','critical')),
  title              VARCHAR(255) NOT NULL,
  message            TEXT NOT NULL,
  data               JSONB,
  action_url         VARCHAR(500),
  is_broadcast       BOOLEAN NOT NULL DEFAULT FALSE,
  scope              VARCHAR(20) CHECK (scope IS NULL OR scope IN ('org','branch','role','user')),
  scope_target_id    UUID,
  created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  expires_at         TIMESTAMPTZ,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at         TIMESTAMPTZ
);
CREATE INDEX ix_notifications_user ON notifications(user_id) WHERE user_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX ix_notifications_org_branch ON notifications(organization_id, branch_id);
CREATE INDEX ix_notifications_type ON notifications(type);
CREATE INDEX ix_notifications_expires ON notifications(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX ix_notifications_priority ON notifications(priority) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_notifications_upd BEFORE UPDATE ON notifications
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE notification_reads (
  notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (notification_id, user_id)
);

CREATE TABLE system_settings (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID REFERENCES organizations(id) ON DELETE CASCADE,
  key              VARCHAR(120) NOT NULL,
  value            TEXT NOT NULL,
  value_type       VARCHAR(20) NOT NULL DEFAULT 'string'
                     CHECK (value_type IN ('string','int','bool','json','float')),
  description      TEXT,
  is_sensitive     BOOLEAN NOT NULL DEFAULT FALSE,
  updated_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ,
  UNIQUE (organization_id, key)  -- null org = global
);
CREATE INDEX ix_system_settings_org ON system_settings(organization_id);
CREATE TRIGGER tr_system_settings_upd BEFORE UPDATE ON system_settings
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE feature_flags (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID REFERENCES organizations(id) ON DELETE CASCADE,
  key              VARCHAR(120) NOT NULL,
  enabled          BOOLEAN NOT NULL DEFAULT FALSE,
  description      TEXT,
  target_scope     JSONB,
  updated_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ,
  UNIQUE (organization_id, key)
);
CREATE INDEX ix_feature_flags_org ON feature_flags(organization_id);
CREATE TRIGGER tr_feature_flags_upd BEFORE UPDATE ON feature_flags
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
