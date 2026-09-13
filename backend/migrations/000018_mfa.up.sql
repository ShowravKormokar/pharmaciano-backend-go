-- ##### FILE: 000018_mfa.up.sql ################################################
-- Multi-factor authentication support (ADR §? TOTP).
--
-- Two new tables back the second-factor flows:
--
--  * mfa_challenges — a single-use, short-lived ("stage 1 complete") proof
--    issued when a login whose account has MFA enabled passes the password
--    check. The opaque challenge token is stored only as a SHA-256 hash and
--    expires quickly; the second factor (TOTP or a recovery code) completes
--    the login and mints the real session tokens.
--
--  * mfa_recovery_codes — the N hashed fallback codes handed out once at MFA
--    setup. Each unused row may redeem exactly one login when the
--    authenticator app is unavailable. Only the hash is ever stored — the raw
--    code is shown to the user exactly once at setup and never persisted.
--
-- The TOTP shared secret itself is NOT stored here: it lives in the existing
-- users.mfa_secret_encrypted column, encrypted at rest (AES-256-GCM via the
-- KeyRing) with an entry context bound to the user id, alongside the replay
-- counter. MFA secrets, like refresh tokens and recovery codes, are never
-- stored in plaintext.
CREATE TABLE mfa_challenges (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash VARCHAR(64) NOT NULL UNIQUE,      -- SHA-256 hex of the opaque token
  expires_at TIMESTAMPTZ NOT NULL,             -- hard cap on how long stage 1 stays redeemable
  used_at    TIMESTAMPTZ,                      -- set once: a challenge is single-use
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- Challenge lookup by token hash (the login second-factor path).
CREATE INDEX ix_mfa_challenges_user ON mfa_challenges(user_id)
  WHERE deleted_at IS NULL;

-- Expiry sweep index.
CREATE INDEX ix_mfa_challenges_expires ON mfa_challenges(expires_at)
  WHERE deleted_at IS NULL AND used_at IS NULL;

CREATE TRIGGER tr_mfa_challenges_upd
  BEFORE UPDATE ON mfa_challenges
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE mfa_recovery_codes (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  VARCHAR(64) NOT NULL,             -- SHA-256 hex of the normalized code
  used_at    TIMESTAMPTZ,                      -- set once: a code is single-use
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- A user cannot hold two copies of the same live code.
CREATE UNIQUE INDEX ux_mfa_recovery_codes_live
  ON mfa_recovery_codes(user_id, code_hash)
  WHERE deleted_at IS NULL AND used_at IS NULL;

-- "which unused codes does this user hold" (used to keep the pool topped up).
CREATE INDEX ix_mfa_recovery_codes_user ON mfa_recovery_codes(user_id)
  WHERE deleted_at IS NULL AND used_at IS NULL;

CREATE TRIGGER tr_mfa_recovery_codes_upd
  BEFORE UPDATE ON mfa_recovery_codes
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();