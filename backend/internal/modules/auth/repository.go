package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
)

// Repository is the auth module's data-access layer. It owns the credential
// projection of the users table (read + login-state writes) and the full
// sessions / refresh_tokens / login_attempts / password_resets tables. Every
// method returns raw driver errors (db.ErrNoRows, unique-violation, …); the
// service maps them to domain errors.
//
// Read routing follows one rule: any read that a security decision hangs on
// goes to the primary via FromCtx, never the replica, because replica lag would
// open a window where a revoked session or a spent refresh token still looks
// valid. Cosmetic reads (the "active devices" list) may use the replica.
//
// INET columns (sessions.ip, login_attempts.ip, password_resets.ip) are written
// with a `$n::inet` cast from a *string and read back with `host(ip) AS ip` so
// the Go models can carry a plain string (see the package doc).
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// -----------------------------------------------------------------------------
// Credentials (users table, auth projection)
// -----------------------------------------------------------------------------

// credentialColumns is the canonical column order shared by every credential
// SELECT; scanCredential Scans in exactly this order. It is the minimal set the
// login/lockout/JWT path needs — notably password_hash (auth's whole job) and
// the lockout counters, but none of the management columns the user module owns.
const credentialColumns = `id, organization_id, branch_id, email, password_hash, ` +
	`status, stage, must_change_password, mfa_enabled, failed_attempts, locked_until, authz_version`

// scanCredential maps one row into a Credential.
func scanCredential(row pgx.Row) (*Credential, error) {
	var c Credential
	if err := row.Scan(
		&c.ID, &c.OrganizationID, &c.BranchID, &c.Email, &c.PasswordHash,
		&c.Status, &c.Stage, &c.MustChangePassword, &c.MFAEnabled,
		&c.FailedAttempts, &c.LockedUntil, &c.AuthzVersion,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

// AuthzVersion returns the current durable authorization epoch from the primary.
// Authenticate already performs a primary-backed session check; this cheap scalar
// check prevents a permission snapshot from outliving an RBAC mutation.
func (r *Repository) AuthzVersion(ctx context.Context, userID uuid.UUID) (int64, error) {
	var version int64
	err := r.db.FromCtx(ctx).QueryRow(ctx, `SELECT authz_version FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&version)
	return version, err
}

// FindCredentialByEmail loads the login credential for an email. email is CITEXT,
// so the match is case-insensitive. Not organization-scoped: login happens before
// any tenant is established and the partial unique index guarantees at most one
// live user per email. Reads the primary — the lockout counters and hash must be
// current. Returns db.ErrNoRows when no live user has that email (the service
// then runs a dummy verify to keep the timing identical to a wrong password).
func (r *Repository) FindCredentialByEmail(ctx context.Context, email string) (*Credential, error) {
	const q = `SELECT ` + credentialColumns + `
		FROM users
		WHERE email = $1 AND deleted_at IS NULL`
	return scanCredential(r.db.FromCtx(ctx).QueryRow(ctx, q, email))
}

// FindCredentialByID loads the login credential for a user id. Used by the
// authenticated password-change flow and by refresh to re-snapshot the account's
// live status/stage into the new access token. Reads the primary. Returns
// db.ErrNoRows if the user is missing or soft-deleted.
func (r *Repository) FindCredentialByID(ctx context.Context, userID uuid.UUID) (*Credential, error) {
	const q = `SELECT ` + credentialColumns + `
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`
	return scanCredential(r.db.FromCtx(ctx).QueryRow(ctx, q, userID))
}

// ListUserBranchIDs returns the principal's effective branch subset (ADR §19):
// the union of (a) the user's home branch, if any, and (b) every live
// user_branch_assignments row. The query is organisation-scoped so a user
// from org A can never get branch ids from org B even if both rows exist.
// Expired assignments (expires_at < now) are excluded at the SQL layer so
// a quick re-issue does not have to filter in Go. The function never
// returns nil: an empty slice means "no branch assignment / org-wide".
func (r *Repository) ListUserBranchIDs(ctx context.Context, userID, orgID uuid.UUID, now time.Time) ([]uuid.UUID, error) {
	const q = `SELECT b.id
		FROM (
			SELECT branch_id FROM users
			 WHERE id = $1 AND deleted_at IS NULL AND branch_id IS NOT NULL
			UNION
			SELECT branch_id FROM user_branch_assignments
			 WHERE user_id = $1 AND organization_id = $2 AND deleted_at IS NULL
			   AND (expires_at IS NULL OR expires_at > $3)
		) AS src
		JOIN branches b ON b.id = src.branch_id AND b.deleted_at IS NULL
		WHERE b.organization_id = $2`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q, userID, orgID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0, 4)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecordLoginSuccess clears the brute-force counters and stamps the last-login
// audit columns in a single write. Called once a login fully succeeds. Resetting
// failed_attempts/locked_until here is what makes the threshold count *consecutive*
// failures. ip is cast to inet ($3::inet); a nil ip stores NULL.
func (r *Repository) RecordLoginSuccess(ctx context.Context, userID uuid.UUID, ip *string, now time.Time) error {
	const q = `UPDATE users SET
			failed_attempts = 0,
			locked_until = NULL,
			last_login_at = $2,
			last_login_ip = $3::inet
		WHERE id = $1 AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, now, ip)
	return err
}

// RecordLoginFailure persists the post-failure lockout state computed by the
// service's LockoutPolicy: the new consecutive-failure count and the resulting
// locked_until (nil below the threshold, a future instant once it is reached).
// Kept as a plain setter — the policy decision lives in limiter.go so it stays
// pure and unit-testable — and written as one UPDATE so a failure costs a single
// round trip on the hot (attack) path.
func (r *Repository) RecordLoginFailure(ctx context.Context, userID uuid.UUID, threshold int, lockedUntil time.Time) error {
	const q = `UPDATE users SET
			failed_attempts = failed_attempts + 1,
			locked_until = CASE WHEN failed_attempts + 1 >= $2 THEN $3 ELSE locked_until END
		WHERE id = $1 AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, threshold, lockedUntil)
	return err
}

// UpdatePassword sets a new password hash and resets everything a password change
// should reset: the change timestamp, the must-change flag, and the lockout
// counters (a successful reset/change clears any standing lock). Returns false if
// no live user matched. The caller revokes the user's other sessions in the same
// transaction so a changed password can't leave stale logins alive.
func (r *Repository) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string, now time.Time) (bool, error) {
	const q = `UPDATE users SET
			password_hash = $2,
			password_changed_at = $3,
			must_change_password = FALSE,
			failed_attempts = 0,
			locked_until = NULL
		WHERE id = $1 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, passwordHash, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// -----------------------------------------------------------------------------
// Sessions
// -----------------------------------------------------------------------------

// sessionColumns is the canonical read projection for sessions; scanSession Scans
// in exactly this order. It is used verbatim in SELECT and in INSERT/UPDATE …
// RETURNING. ip is projected through host(ip) so an inet comes back as a plain
// address string (no netmask), matching Session.IP's *string type.
// security_generation rides along so the Authenticate path can key the Redis
// session cache on (id, generation) — a revocation that advances the counter
// invalidates every cached projection for that session at once (ADR §17).
const sessionColumns = `id, user_id, family_id, device_name, device_fp, host(ip) AS ip, ` +
	`user_agent, browser, os, device_type, country, city, ` +
	`last_seen_at, expires_at, revoked_at, revoked_by, revoke_reason, ` +
	`security_generation, ` +
	`created_at, updated_at, deleted_at`

// scanSession maps one row into a Session.
func scanSession(row pgx.Row) (*Session, error) {
	var s Session
	if err := row.Scan(
		&s.ID, &s.UserID, &s.FamilyID, &s.DeviceName, &s.DeviceFP, &s.IP,
		&s.UserAgent, &s.Browser, &s.OS, &s.DeviceType, &s.Country, &s.City,
		&s.LastSeenAt, &s.ExpiresAt, &s.RevokedAt, &s.RevokedBy, &s.RevokeReason,
		&s.SecurityGeneration,
		&s.CreatedAt, &s.UpdatedAt, &s.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &s, nil
}

// InsertSession creates the session row for a new login and returns the stored
// row (server defaults included). family_id, expires_at and the device metadata
// are set by the service; last_seen_at defaults to now(). ip is cast to inet.
func (r *Repository) InsertSession(ctx context.Context, s *Session) (*Session, error) {
	const q = `INSERT INTO sessions (
			user_id, family_id, device_name, device_fp, ip,
			user_agent, browser, os, device_type, country, city, expires_at
		) VALUES ($1,$2,$3,$4,$5::inet,$6,$7,$8,$9,$10,$11,$12)
		RETURNING ` + sessionColumns
	return scanSession(r.db.FromCtx(ctx).QueryRow(ctx, q,
		s.UserID, s.FamilyID, s.DeviceName, s.DeviceFP, s.IP,
		s.UserAgent, s.Browser, s.OS, s.DeviceType, s.Country, s.City, s.ExpiresAt,
	))
}

// FindSessionByID loads a session by primary key from the primary. This is the
// stateful liveness check Authenticate runs on every request: reading the primary
// (never the replica) guarantees a revoked or expired session is seen with no lag,
// which is the whole point of the hybrid-stateful design. The caller cross-checks
// the row's UserID against the token subject before trusting it.
func (r *Repository) FindSessionByID(ctx context.Context, id uuid.UUID) (*Session, error) {
	const q = `SELECT ` + sessionColumns + `
		FROM sessions
		WHERE id = $1 AND deleted_at IS NULL`
	return scanSession(r.db.FromCtx(ctx).QueryRow(ctx, q, id))
}

// ListActiveSessionsByUser returns every live (not revoked, not expired, not
// deleted) session for a user, newest activity first, for the "active devices"
// UI. Cosmetic, so it reads the replica. Uses the ix_sessions_user_active index.
func (r *Repository) ListActiveSessionsByUser(ctx context.Context, userID uuid.UUID, now time.Time) ([]Session, error) {
	const q = `SELECT ` + sessionColumns + `
		FROM sessions
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND deleted_at IS NULL
		  AND expires_at > $2
		ORDER BY last_seen_at DESC, id ASC`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountActiveSessionsByUser returns the number of live sessions a user holds.
// It reads through the PRIMARY connection tree (FromCtx) rather than the replica
// because it is called inside the login transaction to enforce the concurrent
// session cap: after RecordLoginSuccess has locked the users row, this count is
// serialized per user, so two simultaneous logins cannot both slip past the cap.
// Active = not revoked, not soft-deleted, and not past its absolute expiry.
func (r *Repository) CountActiveSessionsByUser(ctx context.Context, userID uuid.UUID, now time.Time) (int, error) {
	const q = `SELECT COUNT(*)
		FROM sessions
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND deleted_at IS NULL
		  AND expires_at > $2`
	var n int
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, userID, now).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// TouchSession refreshes last_seen_at (and the observed ip) on a successful
// refresh so the device list shows recent activity. It is deliberately *not*
// called on every authenticated request — that would be a write on the hot path;
// refresh (~once per access-token lifetime) is the natural low-frequency anchor.
// idleTimeout > 0 causes the UPDATE to include `AND last_seen_at + $4 > $2`,
// making the call idempotent when the session is still within the idle window
// and a no-op when it has idled out. The bool return is true when the session
// is still active (the row was touched), false when it has idled out (0 rows
// matched).
func (r *Repository) TouchSession(ctx context.Context, id uuid.UUID, ip *string, now time.Time, idleTimeout time.Duration) (bool, error) {
	q := `UPDATE sessions SET last_seen_at = $2, ip = $3::inet
		WHERE id = $1 AND revoked_at IS NULL AND deleted_at IS NULL`
	args := []any{id, now, ip}
	if idleTimeout > 0 {
		q = `UPDATE sessions SET last_seen_at = $2, ip = $3::inet
			WHERE id = $1 AND revoked_at IS NULL AND deleted_at IS NULL
			  AND last_seen_at + $4 > $2`
		args = append(args, idleTimeout)
	}
	ct, err := r.db.FromCtx(ctx).Exec(ctx, q, args...)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// RevokeSession revokes one session (logout of the current device, or an admin
// revoking a specific device). It is keyed by (id, userID) so a caller can only
// ever revoke a session it has already proven belongs to the target user — the
// user id is the tenant-safety boundary here (sessions carry no organization_id;
// the caller resolves the user in-tenant first). revokedBy is the acting user
// (nil for a self-service logout). Returns false if nothing matched (already
// revoked / wrong user), which the service treats as an idempotent no-op.
//
// SecurityGeneration is bumped on every successful revocation (and any
// other security-sensitive write below) so the Redis session cache can key
// on (id, generation) and a stale projection becomes unusable at once
// (ADR §17).
func (r *Repository) RevokeSession(ctx context.Context, id, userID uuid.UUID, revokedBy *uuid.UUID, reason string, now time.Time) (bool, error) {
	const q = `UPDATE sessions SET
			revoked_at = $3, revoked_by = $4, revoke_reason = $5,
			security_generation = security_generation + 1
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, userID, now, revokedBy, reason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeUserSessions revokes every live session for a user and returns the set of
// revoked session IDs. The IDs let the caller evict each session's stale Redis
// projection (the session cache is keyed on (id, security_generation); the
// generation bump orphans the old key but a stale raw entry must still be
// purged for prompt revocation). This backs logout-all, and — via the
// user.SessionRevoker port — the force-logout the user module triggers on a
// status change or password reset. It writes through FromCtx, so when the caller
// runs it inside a transaction the revocation commits atomically with the
// triggering change (a deactivation that rolls back must not leave the user
// logged out, and vice-versa).
func (r *Repository) RevokeUserSessions(ctx context.Context, userID uuid.UUID, revokedBy *uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	const q = `UPDATE sessions SET
			revoked_at = $2, revoked_by = $3, revoke_reason = $4,
			security_generation = security_generation + 1
		WHERE user_id = $1 AND revoked_at IS NULL AND deleted_at IS NULL
		RETURNING id`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q, userID, now, revokedBy, reason)
	return scanRevokedIDs(rows, err)
}

// RevokeUserSessionsExcept revokes every live session for a user *except* one and
// returns the set of revoked session IDs (see RevokeUserSessions for why). It
// backs the authenticated password change, where the device performing the change
// stays logged in while every other device is cut. Keyed by (user_id, id <>
// exceptID) so it can never touch another user's rows; writes through FromCtx so
// it commits atomically with the password write.
func (r *Repository) RevokeUserSessionsExcept(ctx context.Context, userID, exceptID uuid.UUID, revokedBy *uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	const q = `UPDATE sessions SET
			revoked_at = $3, revoked_by = $4, revoke_reason = $5,
			security_generation = security_generation + 1
		WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL AND deleted_at IS NULL
		RETURNING id`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q, userID, exceptID, now, revokedBy, reason)
	return scanRevokedIDs(rows, err)
}

// scanRevokedIDs drains a rows result of session UUIDs.
func scanRevokedIDs(rows pgx.Rows, err error) ([]uuid.UUID, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// -----------------------------------------------------------------------------
// Refresh tokens
// -----------------------------------------------------------------------------

// refreshColumns is the canonical read projection for refresh_tokens;
// scanRefreshToken Scans in exactly this order. token_hash is included because
// the service never exposes it (json:"-") and only ever compares hashes.
const refreshColumns = `id, session_id, user_id, family_id, token_hash, expires_at, ` +
	`used_at, revoked_at, replaced_by, reuse_detected_at, ` +
	`created_at, updated_at, deleted_at`

// scanRefreshToken maps one row into a RefreshToken.
func scanRefreshToken(row pgx.Row) (*RefreshToken, error) {
	var t RefreshToken
	if err := row.Scan(
		&t.ID, &t.SessionID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.ExpiresAt,
		&t.UsedAt, &t.RevokedAt, &t.ReplacedBy, &t.ReuseDetectedAt,
		&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// InsertRefreshToken stores the hash of a freshly minted opaque token (login, or
// the replacement half of a rotation) and returns the stored row. token_hash has
// a unique index, so a hash collision surfaces as a unique violation.
func (r *Repository) InsertRefreshToken(ctx context.Context, t *RefreshToken) (*RefreshToken, error) {
	const q = `INSERT INTO refresh_tokens (
			session_id, user_id, family_id, token_hash, expires_at
		) VALUES ($1,$2,$3,$4,$5)
		RETURNING ` + refreshColumns
	return scanRefreshToken(r.db.FromCtx(ctx).QueryRow(ctx, q,
		t.SessionID, t.UserID, t.FamilyID, t.TokenHash, t.ExpiresAt,
	))
}

// FindRefreshTokenByHashForUpdate looks a token up by its hash and locks the row
// FOR UPDATE. Refresh is a read-modify-write that must be serialized against a
// concurrent presentation of the same token, so it always runs in a transaction
// and on the primary: the lock plus the guarded MarkRefreshTokenUsed below are
// what make rotation and reuse-detection race-free. Returns the row whatever its
// state (used/revoked included) — inspecting that state is how the service tells
// a legitimate rotation from token theft. db.ErrNoRows means "unknown token".
func (r *Repository) FindRefreshTokenByHashForUpdate(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	const q = `SELECT ` + refreshColumns + `
		FROM refresh_tokens
		WHERE token_hash = $1 AND deleted_at IS NULL
		FOR UPDATE`
	return scanRefreshToken(r.db.FromCtx(ctx).QueryRow(ctx, q, tokenHash))
}

// MarkRefreshTokenUsed atomically consumes a token as part of rotation: it stamps
// used_at and links replaced_by, but only if the row is still unused and unrevoked
// (the WHERE guard). A false return means the guard failed — the token was already
// spent — which under the FOR UPDATE lock can only mean a concurrent or replayed
// redemption, i.e. the reuse signal. This guard is the atomic heart of
// reuse-detection: exactly one rotation can ever win for a given token.
func (r *Repository) MarkRefreshTokenUsed(ctx context.Context, id, replacedBy uuid.UUID, now time.Time) (bool, error) {
	const q = `UPDATE refresh_tokens SET used_at = $2, replaced_by = $3
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, now, replacedBy)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeRefreshFamily revokes every still-live token in a family and returns the
// count. Used both for a benign teardown (logout revokes the current family) and,
// with StampFamilyReuseDetected, for the scorched-earth response to theft. Keyed
// by family_id so one presented token can invalidate the entire rotation chain.
func (r *Repository) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, now time.Time) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = $2
		WHERE family_id = $1 AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, familyID, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// StampFamilyReuseDetected marks the whole family with a reuse_detected_at
// forensic timestamp (first detection only — the guard keeps the earliest). It is
// the audit companion to the family revocation, distinguishing "revoked because
// the user logged out" from "revoked because we caught a stolen token".
func (r *Repository) StampFamilyReuseDetected(ctx context.Context, familyID uuid.UUID, now time.Time) error {
	const q = `UPDATE refresh_tokens SET reuse_detected_at = $2
		WHERE family_id = $1 AND reuse_detected_at IS NULL AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, familyID, now)
	return err
}

// RevokeRefreshTokensBySession revokes the live tokens bound to one session, so
// revoking a device (logout / admin revoke of a single session) also kills its
// refresh chain and not just the session row.
func (r *Repository) RevokeRefreshTokensBySession(ctx context.Context, sessionID uuid.UUID, now time.Time) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = $2
		WHERE session_id = $1 AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, sessionID, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokeRefreshTokensByUser revokes every live token for a user, the token-side
// companion to RevokeUserSessions for logout-all and force-logout. Writes through
// FromCtx so it can join the caller's transaction.
func (r *Repository) RevokeRefreshTokensByUser(ctx context.Context, userID uuid.UUID, now time.Time) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokeRefreshTokensByUserExcept revokes every live token for a user except the
// ones bound to one session — the token-side companion to RevokeUserSessionsExcept
// for the authenticated password change, so the device performing the change keeps
// a working refresh chain while every other device's chain is killed. Writes
// through FromCtx so it joins the surrounding transaction.
func (r *Repository) RevokeRefreshTokensByUserExcept(ctx context.Context, userID, exceptSessionID uuid.UUID, now time.Time) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = $2
		WHERE user_id = $1 AND session_id <> $3 AND revoked_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, now, exceptSessionID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// -----------------------------------------------------------------------------
// Login attempts (append-only audit)
// -----------------------------------------------------------------------------

// InsertLoginAttempt appends one row to the forensic login_attempts trail. It is
// fire-and-forget from the caller's point of view (no RETURNING): the row is an
// audit record, never read back on the hot path. ip is cast to inet; email is
// CITEXT. A nil UserID records an attempt against an unknown/typo'd address.
func (r *Repository) InsertLoginAttempt(ctx context.Context, a *LoginAttempt) error {
	const q = `INSERT INTO login_attempts (
			email, ip, user_agent, success, failure_reason, user_id
		) VALUES ($1,$2::inet,$3,$4,$5,$6)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		a.Email, a.IP, a.UserAgent, a.Success, a.FailureReason, a.UserID,
	)
	return err
}

// -----------------------------------------------------------------------------
// Password resets
// -----------------------------------------------------------------------------

// resetColumns is the canonical read projection for password_resets;
// scanPasswordReset Scans in exactly this order. ip is projected through host().
const resetColumns = `id, user_id, token_hash, expires_at, used_at, host(ip) AS ip, ` +
	`created_at, updated_at, deleted_at`

// scanPasswordReset maps one row into a PasswordReset.
func scanPasswordReset(row pgx.Row) (*PasswordReset, error) {
	var p PasswordReset
	if err := row.Scan(
		&p.ID, &p.UserID, &p.TokenHash, &p.ExpiresAt, &p.UsedAt, &p.IP,
		&p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertPasswordReset stores the hash of a freshly issued reset token and returns
// the stored row. token_hash is uniquely indexed. ip is cast to inet.
func (r *Repository) InsertPasswordReset(ctx context.Context, p *PasswordReset) (*PasswordReset, error) {
	const q = `INSERT INTO password_resets (
			user_id, token_hash, expires_at, ip
		) VALUES ($1,$2,$3,$4::inet)
		RETURNING ` + resetColumns
	return scanPasswordReset(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.UserID, p.TokenHash, p.ExpiresAt, p.IP,
	))
}

// InvalidateUserPasswordResets marks all of a user's outstanding reset tokens as
// used, so requesting a new link silently voids any earlier ones — at most one
// reset link is ever live per user. Called just before issuing a fresh token.
func (r *Repository) InvalidateUserPasswordResets(ctx context.Context, userID uuid.UUID, now time.Time) error {
	const q = `UPDATE password_resets SET used_at = $2
		WHERE user_id = $1 AND used_at IS NULL AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, now)
	return err
}

// FindPasswordResetByHashForUpdate looks a reset token up by hash and locks the
// row FOR UPDATE, so redeeming it (read state → MarkPasswordResetUsed) is atomic
// against a concurrent redemption of the same link. Runs on the primary inside
// the reset transaction. db.ErrNoRows means "unknown token".
func (r *Repository) FindPasswordResetByHashForUpdate(ctx context.Context, tokenHash string) (*PasswordReset, error) {
	const q = `SELECT ` + resetColumns + `
		FROM password_resets
		WHERE token_hash = $1 AND deleted_at IS NULL
		FOR UPDATE`
	return scanPasswordReset(r.db.FromCtx(ctx).QueryRow(ctx, q, tokenHash))
}

// MarkPasswordResetUsed consumes a reset token, but only if still unused (the
// guard). A false return means it was already redeemed — under the FOR UPDATE
// lock, a replay — which the service rejects. This makes a reset link strictly
// single-use even under concurrent submission.
func (r *Repository) MarkPasswordResetUsed(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	const q = `UPDATE password_resets SET used_at = $2
		WHERE id = $1 AND used_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// -----------------------------------------------------------------------------
// MFA (users.mfa_secret_encrypted + mfa_challenges + mfa_recovery_codes)
// -----------------------------------------------------------------------------

// FindMFASecret returns the encrypted-at-rest TOTP payload (the JSON envelope
// produced by the service's KeyRing, bound to the user id) for a live user, or
// "" if none is set. The raw secret is never exposed here — only the ciphertext
// the service decrypts. Reads the primary: a security decision hangs on it.
func (r *Repository) FindMFASecret(ctx context.Context, userID uuid.UUID) (string, error) {
	var enc *string
	err := r.db.FromCtx(ctx).QueryRow(ctx,
		`SELECT mfa_secret_encrypted FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&enc)
	if err != nil {
		return "", err
	}
	if enc == nil {
		return "", nil
	}
	return *enc, nil
}

// SetMFASecret persists the caller-encrypted MFA payload for a user, or clears
// it when enc is nil (the disable path nulls the column). The service always
// passes an already-encrypted envelope; the repository never sees plaintext.
func (r *Repository) SetMFASecret(ctx context.Context, userID uuid.UUID, enc *string) error {
	tag, err := r.db.FromCtx(ctx).Exec(ctx,
		`UPDATE users SET mfa_secret_encrypted = $2 WHERE id = $1 AND deleted_at IS NULL`, userID, enc)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetMFAEnabled flips the account's mfa_enabled flag.
func (r *Repository) SetMFAEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	tag, err := r.db.FromCtx(ctx).Exec(ctx,
		`UPDATE users SET mfa_enabled = $2 WHERE id = $1 AND deleted_at IS NULL`, userID, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MFAEnabled reports whether the account currently requires a second factor at
// login. Reads the replica-safe flag via the primary (a security decision).
func (r *Repository) MFAEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := r.db.FromCtx(ctx).QueryRow(ctx,
		`SELECT mfa_enabled FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&enabled)
	return enabled, err
}

// BumpAuthzVersion advances the user's durable authorization epoch. Used on
// any write the auth middleware must see immediately (a branch assignment, an
// MFA posture change) so a stale JWT/{session,org} snapshot cannot outlive the
// mutation (ADR §15). Every bump also invalidates the tenant RBAC generation
// read the authorizer caches against, forcing a fresh policy resolution.
func (r *Repository) BumpAuthzVersion(ctx context.Context, userID uuid.UUID) error {
	tag, err := r.db.FromCtx(ctx).Exec(ctx,
		`UPDATE users SET authz_version = authz_version + 1 WHERE id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// InsertRecoveryCode stores the hash of one recovery code. Only the SHA-256
// hash is stored — the raw code was shown once at setup and is never persisted.
func (r *Repository) InsertRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx,
		`INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, codeHash)
	return err
}

// ConsumeRecoveryCode redeems one matching unused recovery code for the user.
// The conditional UPDATE makes redemption single-use race-free: returns true
// only for the caller that successfully flips used_at on an unused row. A wrong
// or already-spent code returns false without side effects.
func (r *Repository) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string, now time.Time) (bool, error) {
	const q = `UPDATE mfa_recovery_codes SET used_at = $3
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, codeHash, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteRecoveryCodes permanently clears a user's recovery codes — used when
// MFA is disabled. Unlike ConsumeRecoveryCode (which marks single rows used)
// this wipes the whole pool.
func (r *Repository) DeleteRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx,
		`DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID)
	return err
}

// InsertMFAChallenge persists the hash of a "stage 1 complete" challenge token.
// The challenge is short-lived and single-use; its consumer validates both
// properties atomically in ConsumeMFAChallenge.
func (r *Repository) InsertMFAChallenge(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx,
		`INSERT INTO mfa_challenges (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// ConsumeMFAChallenge atomically redeems a challenge token: it flips used_at
// and returns the owning user id only if the token exists, is unused, is not
// expired and is not soft-deleted. A consumed, expired or absent token returns
// (nil, false, nil) so the service can reject the second factor generically.
func (r *Repository) ConsumeMFAChallenge(ctx context.Context, tokenHash string, now time.Time) (uuid.UUID, bool, error) {
	const q = `UPDATE mfa_challenges SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2 AND deleted_at IS NULL
		RETURNING user_id`
	var userID uuid.UUID
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, tokenHash, now).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return userID, true, nil
}

// -----------------------------------------------------------------------------
// Password history (reuse rejection)
// -----------------------------------------------------------------------------

// PasswordHistory is one row of the password_history table: the hash of a
// password the account has retired. Only the hash is ever stored; the plaintext
// is never persisted or logged.
type PasswordHistory struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	PasswordHash string
	CreatedAt    time.Time
	DeletedAt    *time.Time
}

// ListPasswordHistory returns the most-recent `limit` retired hashes for a user,
// newest first. It reads the primary: a reuse-rejection decision hangs on it
// being current, so it must not route through a lagging replica.
func (r *Repository) ListPasswordHistory(ctx context.Context, userID uuid.UUID, limit int) ([]PasswordHistory, error) {
	const q = `SELECT id, user_id, password_hash, created_at, deleted_at
		FROM password_history
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id DESC
		LIMIT $2`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]PasswordHistory, 0, limit)
	for rows.Next() {
		var e PasswordHistory
		if err := rows.Scan(&e.ID, &e.UserID, &e.PasswordHash, &e.CreatedAt, &e.DeletedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// InsertPasswordHistory appends a just-retired hash to the user's history and
// trims it back to the newest `keep` entries in the same statement, so the
// history never grows unbounded regardless of how many times the password turns
// over. The caller passes the hash that UpdatePassword just superseded (the
// account's previous live hash). Runs on the primary inside the change
// transaction, sharing the user-row lock held by UpdatePassword so concurrent
// changes cannot interleave history rows.
func (r *Repository) InsertPasswordHistory(ctx context.Context, userID uuid.UUID, passwordHash string, now time.Time, keep int) error {
	if _, err := r.db.FromCtx(ctx).Exec(ctx,
		`INSERT INTO password_history (user_id, password_hash, created_at) VALUES ($1, $2, $3)`,
		userID, passwordHash, now); err != nil {
		return err
	}
	if keep <= 0 {
		return nil
	}
	// Delete everything in the user's ordered history past the newest `keep`
	// rows. `id` is the tiebreaker so two entries with identical timestamps
	// trim deterministically (the newer id wins).
	const trim = `DELETE FROM password_history
		WHERE user_id = $1 AND deleted_at IS NULL
		  AND id IN (
			SELECT id FROM password_history
			WHERE user_id = $1 AND deleted_at IS NULL
			ORDER BY created_at DESC, id DESC
			OFFSET $2
		  )`
	_, err := r.db.FromCtx(ctx).Exec(ctx, trim, userID, keep)
	return err
}
