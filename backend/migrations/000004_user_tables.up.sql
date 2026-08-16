-- ##### FILE: 000004_user_tables.up.sql #######################################
CREATE TABLE users (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  branch_id              UUID REFERENCES branches(id) ON DELETE SET NULL,
  employee_code          VARCHAR(40),
  email                  CITEXT NOT NULL,
  username               CITEXT,
  phone                  VARCHAR(30),
  password_hash          VARCHAR(255) NOT NULL,
  status                 VARCHAR(20) NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','deactivated','inactive','suspended','resigned','terminated')),
  stage                  VARCHAR(20) NOT NULL DEFAULT 'unverified'
                            CHECK (stage  IN ('unverified','pending','verified')),
  password_changed_at    TIMESTAMPTZ,
  must_change_password   BOOLEAN NOT NULL DEFAULT FALSE,
  mfa_enabled            BOOLEAN NOT NULL DEFAULT FALSE,
  mfa_secret_encrypted   TEXT,
  last_login_at          TIMESTAMPTZ,
  last_login_ip          INET,
  failed_attempts        INTEGER NOT NULL DEFAULT 0,
  locked_until           TIMESTAMPTZ,
  joining_date           DATE,
  employment_type        VARCHAR(20)
                            CHECK (employment_type IS NULL OR employment_type IN ('full_time','part_time','contract','intern')),
  salary_encrypted       TEXT,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at             TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_users_email       ON users(email)                       WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX ux_users_username    ON users(username)                    WHERE deleted_at IS NULL AND username IS NOT NULL;
CREATE UNIQUE INDEX ux_users_emp_code    ON users(organization_id, employee_code) WHERE deleted_at IS NULL AND employee_code IS NOT NULL;
CREATE INDEX        ix_users_org_branch  ON users(organization_id, branch_id) WHERE deleted_at IS NULL;
CREATE INDEX        ix_users_status      ON users(status, deleted_at);
CREATE INDEX        ix_users_locked      ON users(locked_until) WHERE locked_until IS NOT NULL;
CREATE TRIGGER tr_users_upd BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_roles (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id      UUID NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
  branch_id    UUID REFERENCES branches(id) ON DELETE SET NULL,
  assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  assigned_by  UUID,
  expires_at   TIMESTAMPTZ,
  PRIMARY KEY (user_id, role_id)
);
CREATE INDEX ix_user_roles_role   ON user_roles(role_id);
CREATE INDEX ix_user_roles_branch ON user_roles(branch_id);

CREATE TABLE user_profiles (
  id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                        UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  first_name                     VARCHAR(80) NOT NULL,
  last_name                      VARCHAR(80) NOT NULL,
  middle_name                    VARCHAR(80),
  display_name                   VARCHAR(160),
  father_name                    VARCHAR(160),
  mother_name                    VARCHAR(160),
  date_of_birth                  DATE,
  gender                         VARCHAR(10) CHECK (gender IS NULL OR gender IN ('male','female','other')),
  blood_group                    VARCHAR(4),
  marital_status                 VARCHAR(20),
  nationality                    VARCHAR(60),
  religion                       VARCHAR(60),
  description                    TEXT,
  avatar_url                     VARCHAR(500),
  nid_number_encrypted           TEXT,
  nid_last4                      VARCHAR(4),
  birth_cert_number_encrypted    TEXT,
  passport_number_encrypted      TEXT,
  tin_encrypted                  TEXT,
  created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at                     TIMESTAMPTZ
);
CREATE TRIGGER tr_user_profiles_upd BEFORE UPDATE ON user_profiles
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_addresses (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         VARCHAR(20) NOT NULL CHECK (kind IN ('present','permanent','mailing')),
  line1        VARCHAR(255) NOT NULL,
  line2        VARCHAR(255),
  city         VARCHAR(100) NOT NULL,
  state        VARCHAR(100),
  postal_code  VARCHAR(20),
  country      VARCHAR(100) NOT NULL,
  is_primary   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE INDEX ix_user_addresses_user ON user_addresses(user_id);
CREATE TRIGGER tr_user_addresses_upd BEFORE UPDATE ON user_addresses
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_contacts (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  relation      VARCHAR(30) NOT NULL,
  name          VARCHAR(160) NOT NULL,
  phone         VARCHAR(30) NOT NULL,
  email         CITEXT,
  address       VARCHAR(500),
  is_emergency  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE INDEX ix_user_contacts_user ON user_contacts(user_id);
CREATE TRIGGER tr_user_contacts_upd BEFORE UPDATE ON user_contacts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_educations (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  degree            VARCHAR(120) NOT NULL,
  institution       VARCHAR(200) NOT NULL,
  board_university  VARCHAR(200),
  year_of_passing   SMALLINT,
  grade             VARCHAR(20),
  description       TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at        TIMESTAMPTZ
);
CREATE INDEX ix_user_educations_user ON user_educations(user_id);
CREATE TRIGGER tr_user_educations_upd BEFORE UPDATE ON user_educations
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_experiences (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  company_name  VARCHAR(200) NOT NULL,
  designation   VARCHAR(160) NOT NULL,
  from_date     DATE NOT NULL,
  to_date       DATE,
  is_current    BOOLEAN NOT NULL DEFAULT FALSE,
  description   TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ,
  CHECK (to_date IS NULL OR to_date >= from_date)
);
CREATE INDEX ix_user_experiences_user ON user_experiences(user_id);
CREATE TRIGGER tr_user_experiences_upd BEFORE UPDATE ON user_experiences
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_bank_accounts (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  account_type              VARCHAR(20) NOT NULL CHECK (account_type IN ('bank','mobile','other')),
  bank_name                 VARCHAR(160),
  branch_name               VARCHAR(160),
  account_number_encrypted  TEXT NOT NULL,
  account_number_last4      VARCHAR(4) NOT NULL,
  account_holder            VARCHAR(160) NOT NULL,
  routing_number            VARCHAR(40),
  mobile_provider           VARCHAR(30),
  is_primary                BOOLEAN NOT NULL DEFAULT FALSE,
  is_verified               BOOLEAN NOT NULL DEFAULT FALSE,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at                TIMESTAMPTZ
);
CREATE INDEX ix_user_bank_accounts_user ON user_bank_accounts(user_id);
CREATE TRIGGER tr_user_bank_accounts_upd BEFORE UPDATE ON user_bank_accounts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_documents (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  doc_type     VARCHAR(40) NOT NULL,
  file_url     VARCHAR(500) NOT NULL,
  file_hash    VARCHAR(128),
  file_size    BIGINT,
  mime_type    VARCHAR(80),
  verified     BOOLEAN NOT NULL DEFAULT FALSE,
  verified_at  TIMESTAMPTZ,
  verified_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  expires_at   TIMESTAMPTZ,
  notes        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE INDEX ix_user_documents_user ON user_documents(user_id);
CREATE TRIGGER tr_user_documents_upd BEFORE UPDATE ON user_documents
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Sessions & tokens
CREATE TABLE sessions (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id      UUID NOT NULL,
  device_name    VARCHAR(120),
  device_fp      VARCHAR(255),
  ip             INET,
  user_agent     VARCHAR(500),
  browser        VARCHAR(80),
  os             VARCHAR(80),
  device_type    VARCHAR(20),
  country        VARCHAR(80),
  city           VARCHAR(120),
  last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  revoked_at     TIMESTAMPTZ,
  revoked_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  revoke_reason  VARCHAR(200),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at     TIMESTAMPTZ
);
CREATE INDEX ix_sessions_user_active ON sessions(user_id, revoked_at) WHERE deleted_at IS NULL;
CREATE INDEX ix_sessions_family      ON sessions(family_id);
CREATE INDEX ix_sessions_expires     ON sessions(expires_at)          WHERE revoked_at IS NULL;
CREATE TRIGGER tr_sessions_upd BEFORE UPDATE ON sessions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE refresh_tokens (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id          UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  user_id             UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
  family_id           UUID NOT NULL,
  token_hash          VARCHAR(128) NOT NULL,
  expires_at          TIMESTAMPTZ NOT NULL,
  used_at             TIMESTAMPTZ,
  revoked_at          TIMESTAMPTZ,
  replaced_by         UUID REFERENCES refresh_tokens(id) ON DELETE SET NULL,
  reuse_detected_at   TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_refresh_tokens_hash ON refresh_tokens(token_hash);
CREATE INDEX        ix_refresh_tokens_session ON refresh_tokens(session_id);
CREATE INDEX        ix_refresh_tokens_family  ON refresh_tokens(family_id);
CREATE TRIGGER tr_refresh_tokens_upd BEFORE UPDATE ON refresh_tokens
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE login_attempts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email           CITEXT NOT NULL,
  ip              INET,
  user_agent      VARCHAR(500),
  success         BOOLEAN NOT NULL,
  failure_reason  VARCHAR(120),
  user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX ix_login_attempts_email_time ON login_attempts(email, created_at DESC);
CREATE INDEX ix_login_attempts_ip_time    ON login_attempts(ip, created_at DESC);

CREATE TABLE password_resets (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   VARCHAR(128) NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ,
  ip           INET,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_password_resets_hash ON password_resets(token_hash);
CREATE INDEX        ix_password_resets_user ON password_resets(user_id);