// Package auth implements the authentication module: login, token refresh,
// logout and the per-request identity check the middleware depends on. It owns
// the *credential* surface of the shared users table (password verification,
// login tracking, lockout, MFA flags) plus the sessions, refresh_tokens,
// login_attempts and password_resets tables (migration 000004). The user module
// owns the disjoint *management* surface of the same users row; splitting the two
// concerns across separate repositories means neither can mutate the other's
// invariants.
//
// # Auth model (locked)
//
// A hybrid-stateful design hidden behind the middleware.Authenticator interface,
// so a purely-stateless strategy can drop in later with no middleware change:
//
//   - Access token — a short-lived, hand-rolled HS256 JWT (see jwt.go) carrying a
//     snapshot of the Principal (org/branch/session/role/permissions/stage/status)
//     so the hot path builds an appctx.Principal with zero database work.
//   - Refresh token — an opaque 256-bit random value (see password.go), stored
//     only as a SHA-256 hash, rotated on every use, with token-family reuse
//     detection: presenting an already-spent token nukes the whole family.
//   - Statefulness — Authenticate re-checks session liveness on every request, so
//     a revoked session or a locked/deactivated user is stopped immediately even
//     while their unexpired JWT would otherwise still verify. The JWT is an
//     optimization layered on top of the session, never the sole authority.
//
// # INET columns
//
// The pgx pool registers no custom inet codec, so the reference models carry IP
// as a plain *string. The repository bridges to Postgres inet with explicit SQL
// casts — `$n::inet` on write, `host(ip) AS ip` on read — keeping the Go surface
// a simple, JSON-friendly string while the column stays a real inet.
package auth

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	"backend/internal/common/enums"
)

// NewSessionExpiry returns the absolute expiry for a freshly created session:
// now plus the given number of hours. A session is a hard-capped window — the
// refresh-token chain rotates within it but never extends past it, so a stolen
// device is guaranteed to fall out of trust after at most this long regardless
// of how often the tokens are refreshed. The service passes the configured
// refresh-token TTL (in hours) so the session and its first refresh token share
// one expiry.
func NewSessionExpiry(hours int) time.Time {
	return time.Now().Add(time.Duration(hours) * time.Hour)
}

// Credential is the auth module's projection of a users row — exactly the
// columns the login/lockout/JWT-minting path reads, and no more. Unlike the user
// module (which never selects the hash), auth *does* read password_hash because
// verifying it is its whole job; the field is json:"-" so it can never leak
// through an accidental serialization. mfa_secret_encrypted and salary_encrypted
// are deliberately outside this projection — the MFA challenge flow that needs
// the secret is a separate, encryption-aware concern, and the login decision
// only needs the mfa_enabled flag.
//
// Field order lines up with credentialColumns in the repository so a fixed
// SELECT can Scan straight into this struct.
type Credential struct {
	ID             uuid.UUID  `db:"id"              json:"id"`
	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID `db:"branch_id"       json:"branch_id,omitempty"`

	Email        string `db:"email"         json:"email"`
	PasswordHash string `db:"password_hash" json:"-"`

	Status enums.UserStatus `db:"status" json:"status"`
	Stage  enums.UserStage  `db:"stage"  json:"stage"`

	MustChangePassword bool `db:"must_change_password" json:"must_change_password"`
	MFAEnabled         bool `db:"mfa_enabled"          json:"mfa_enabled"`

	// Lockout state, owned and mutated by this module.
	FailedAttempts int        `db:"failed_attempts" json:"-"`
	LockedUntil    *time.Time `db:"locked_until"    json:"-"`
	AuthzVersion   int64      `db:"authz_version"   json:"-"`
}

// CanLogin reports whether this account's *status* permits authentication.
// It is only the status gate — the caller separately checks the lockout window
// (locked_until) and password. Kept as a method so the single source of truth for
// "which status may log in" stays in enums.UserStatus.CanLogin.
func (c Credential) CanLogin() bool { return c.Status.CanLogin() }

// Session mirrors a row of the sessions table: one long-lived login on one
// device, the anchor for stateful revocation. The refresh-token family is keyed
// by FamilyID; rotating a refresh token keeps the same session and family. A
// session is the authority Authenticate re-checks on every request, so revoking
// it (RevokedAt) or letting it expire (ExpiresAt) instantly invalidates every
// access token minted under it regardless of the JWT's own expiry.
type Session struct {
	common.BaseModel

	UserID   uuid.UUID `db:"user_id"   json:"user_id"`
	FamilyID uuid.UUID `db:"family_id" json:"-"`

	// Device / origin metadata, best-effort from the login request. All nullable.
	// IP is a plain string bridged to the inet column via SQL casts (see package doc).
	DeviceName *string `db:"device_name" json:"device_name,omitempty"`
	DeviceFP   *string `db:"device_fp"   json:"-"`
	IP         *string `db:"ip"          json:"ip,omitempty"`
	UserAgent  *string `db:"user_agent"  json:"user_agent,omitempty"`
	Browser    *string `db:"browser"     json:"browser,omitempty"`
	OS         *string `db:"os"          json:"os,omitempty"`
	DeviceType *string `db:"device_type" json:"device_type,omitempty"`
	Country    *string `db:"country"     json:"country,omitempty"`
	City       *string `db:"city"        json:"city,omitempty"`

	LastSeenAt time.Time `db:"last_seen_at" json:"last_seen_at"`
	ExpiresAt  time.Time `db:"expires_at"   json:"expires_at"`

	RevokedAt    *time.Time `db:"revoked_at"    json:"revoked_at,omitempty"`
	RevokedBy    *uuid.UUID `db:"revoked_by"    json:"-"`
	RevokeReason *string    `db:"revoke_reason" json:"revoke_reason,omitempty"`

	// SecurityGeneration is the per-session invalidation counter (ADR §17).
	// Every revocation or security-sensitive write advances it; the Redis
	// session cache keys on (id, generation) so a stale cached projection
	// becomes unusable as soon as the row moves forward, even if Redis never
	// sees the invalidation.
	SecurityGeneration int64 `db:"security_generation" json:"-"`
}

// Active reports whether the session may still authorize requests at time now:
// not soft-deleted, not revoked, not past its absolute expiry. This is the exact
// predicate Authenticate applies statefully on every request.
func (s Session) Active(now time.Time) bool {
	return s.DeletedAt == nil && s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// RefreshToken mirrors a row of the refresh_tokens table. Only the SHA-256 hash
// of the opaque token is ever stored (TokenHash, json:"-"); the raw value lives
// only in the client's possession. Rotation threads the chain via ReplacedBy,
// and presenting a token whose UsedAt or RevokedAt is already set is treated as
// theft — the service stamps ReuseDetectedAt and revokes the whole FamilyID.
type RefreshToken struct {
	common.BaseModel

	SessionID uuid.UUID `db:"session_id" json:"session_id"`
	UserID    uuid.UUID `db:"user_id"    json:"user_id"`
	FamilyID  uuid.UUID `db:"family_id"  json:"family_id"`

	TokenHash string    `db:"token_hash" json:"-"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`

	UsedAt          *time.Time `db:"used_at"           json:"used_at,omitempty"`
	RevokedAt       *time.Time `db:"revoked_at"        json:"revoked_at,omitempty"`
	ReplacedBy      *uuid.UUID `db:"replaced_by"       json:"replaced_by,omitempty"`
	ReuseDetectedAt *time.Time `db:"reuse_detected_at" json:"reuse_detected_at,omitempty"`
}

// Usable reports whether the token can be redeemed at time now: never used,
// never revoked, and not past its expiry. A stored token that is *not* Usable
// but is presented anyway is the reuse signal the service acts on.
func (t RefreshToken) Usable(now time.Time) bool {
	return t.UsedAt == nil && t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// Spent reports whether the token has already been redeemed or explicitly
// revoked — i.e. re-presenting it is the token-reuse attack, distinct from a
// merely expired token (which is a benign, timed-out client).
func (t RefreshToken) Spent() bool { return t.UsedAt != nil || t.RevokedAt != nil }

// LoginAttempt mirrors a row of the login_attempts table: an append-only audit
// trail of every authentication attempt, keyed by the submitted email (which may
// match no user, hence the nullable UserID). The lockout decision itself is
// driven off users.failed_attempts, not by counting these rows; this table is
// the forensic record for investigating credential-stuffing and account-takeover.
type LoginAttempt struct {
	common.BaseModel

	Email string `db:"email" json:"email"`

	IP        *string `db:"ip"         json:"ip,omitempty"`
	UserAgent *string `db:"user_agent" json:"user_agent,omitempty"`

	Success       bool       `db:"success"        json:"success"`
	FailureReason *string    `db:"failure_reason" json:"failure_reason,omitempty"`
	UserID        *uuid.UUID `db:"user_id"        json:"user_id,omitempty"`
}

// PasswordReset mirrors a row of the password_resets table: a single-use,
// time-boxed token that authorizes setting a new password without the old one.
// Only the SHA-256 hash of the opaque token is stored (TokenHash, json:"-"); the
// raw value is delivered out of band (email channel, currently logged) and never
// persisted. Redeeming a reset stamps UsedAt so the same link can't be replayed.
type PasswordReset struct {
	common.BaseModel

	UserID    uuid.UUID  `db:"user_id"    json:"user_id"`
	TokenHash string     `db:"token_hash" json:"-"`
	ExpiresAt time.Time  `db:"expires_at" json:"expires_at"`
	UsedAt    *time.Time `db:"used_at"   json:"used_at,omitempty"`
	IP        *string    `db:"ip"         json:"-"`
}

// Usable reports whether the reset token can still be redeemed at time now:
// never used, not soft-deleted, and not past its expiry.
func (p PasswordReset) Usable(now time.Time) bool {
	return p.UsedAt == nil && p.DeletedAt == nil && now.Before(p.ExpiresAt)
}
