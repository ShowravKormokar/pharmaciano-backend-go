-- ##### FILE: 000019_password_history.up.sql ##################################
-- Password history + reuse rejection.
--
-- A single, size-capped table stores the Argon2id (peppered, ADR §6) hashes of
-- passwords the account has retired. It backs the reuse-rejection rule enforced
-- in the service on PasswordChange / PasswordForceChange / PasswordReset: any new
-- password that verifies against the account's current hash OR one of the recent
-- history entries is refused, and the hash being retired is appended to the top
-- of the history before older entries are trimmed off the bottom.
--
-- Only the retired hashes live here — the live hash stays in
-- users.password_hash. Entries are as tamper-resistant as the live hash itself:
-- Argon2id + server-side pepper, so a database dump does not yield recoverable
-- passwords. `history_size` in config (password.history_size) caps how many
-- entries are kept per user; the trim is a single DELETE keyed on the same
-- (user_id, created_at DESC, id DESC) order used for lookup.
CREATE TABLE password_history (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  password_hash TEXT NOT NULL,       -- retired peppered Argon2id hash
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);

-- Lookup + trim share this order: newest retired hash first.
CREATE INDEX ix_password_history_user ON password_history(user_id, created_at DESC, id DESC)
  WHERE deleted_at IS NULL;

CREATE TRIGGER tr_password_history_upd
  BEFORE UPDATE ON password_history
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();