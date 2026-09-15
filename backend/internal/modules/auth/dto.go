package auth

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common/enums"
)

// tokenTypeBearer is the token_type echoed in every TokenResponse and the scheme
// the middleware expects in the Authorization header ("Authorization: Bearer
// <access_token>").
const tokenTypeBearer = "Bearer"

// LoginRequest is the body of POST /auth/login. Password is bounded (not
// length-validated against the creation policy): enforcing MinPasswordLength here
// would both leak the policy to an attacker and lock out any legitimate older
// password, so login only requires a non-empty value and caps length as a
// defence against pathologically large Argon2 inputs. DeviceName is an optional,
// user-friendly label for the session ("Alfa's iPhone") shown in the session list.
type LoginRequest struct {
	Email      string  `json:"email"       validate:"required,email,max=255"`
	Password   string  `json:"password"    validate:"required,max=128"`
	DeviceName *string `json:"device_name" validate:"omitempty,max=120"`
}

// TokenResponse is the success body of login and refresh. AccessToken is the
// short-lived HS256 JWT sent as a Bearer credential on subsequent requests;
// ExpiresIn is its lifetime in seconds so a client can schedule a refresh.
// RefreshToken is the raw opaque token — for non-browser clients (mobile /
// server-to-server, identified by an X-Client-Type header that is NOT "browser")
// it is echoed in the body. For browser clients it is NEVER in the body; it is
// only delivered as an HttpOnly, SameSite cookie and is never logged, returned
// in error messages, or reflected in any other response. It is shown exactly
// once on a non-browser client and only its hash is ever persisted.
//
// `omitempty` makes the field disappear from browser responses instead of
// leaving a "refresh_token":"" ghost value.
type TokenResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshExpiresAt time.Time `json:"-"`
}

// newTokenResponse assembles a Bearer TokenResponse from the minted pair and the
// access-token lifetime, centralising the token_type/expires_in conventions.
func newTokenResponse(accessToken, refreshToken string, accessTTL time.Duration, refreshExpiresAt time.Time) TokenResponse {
	return TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenTypeBearer,
		ExpiresIn:    int64(accessTTL.Seconds()),
		RefreshExpiresAt: refreshExpiresAt,
	}
}

// RefreshRequest is the body of POST /auth/refresh. RefreshToken is optional
// because the token is normally read from the HttpOnly cookie; the body is the
// fallback for non-browser clients. The handler resolves cookie first, then body,
// and treats "neither present" as an invalid-token error rather than a 400 — it
// must not reveal whether a value was structurally accepted.
type RefreshRequest struct {
	RefreshToken *string `json:"refresh_token" validate:"omitempty,min=1"`
}

// LogoutRequest is the body of POST /auth/logout. Logout is an authenticated
// endpoint: the session to end is identified by the caller's own access token
// (Principal.SessionID), so no token is needed in the body. All=true instead
// revokes every session of the user (log out everywhere), used after a suspected
// compromise or a password change.
type LogoutRequest struct {
	All bool `json:"all"`
}

// SessionItem is one row of GET /auth/sessions: the device/origin metadata a user
// needs to recognise and manage their active logins. Secrets (family id, device
// fingerprint, token hashes) are never included. Current flags the session the
// request itself is authenticated with, so the UI can label "This device" and
// avoid offering to revoke the session out from under the caller.
type SessionItem struct {
	ID         uuid.UUID `json:"id"`
	DeviceName *string   `json:"device_name,omitempty"`
	Browser    *string   `json:"browser,omitempty"`
	OS         *string   `json:"os,omitempty"`
	DeviceType *string   `json:"device_type,omitempty"`
	IP         *string   `json:"ip,omitempty"`
	Location   *string   `json:"location,omitempty"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`

	Current bool `json:"current"`
}

// MeResponse is the body of GET /auth/me: the identity + authorization view as
// this token sees it. Identity fields are resolved from the request Principal
// with no database round-trip; Role and Permissions are resolved from the
// in-memory rbac snapshot (also zero DB), so a role granted after this token was
// minted is reflected immediately. It is deliberately distinct from the user
// module's GET /users/me (which returns the editable user record): this endpoint
// answers "who am I and what may I do", exposing the effective role and
// permission set a frontend uses to gate its UI.
type MeResponse struct {
	UserID         uuid.UUID  `json:"user_id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	BranchID       *uuid.UUID `json:"branch_id,omitempty"`
	SessionID      uuid.UUID  `json:"session_id"`

	Role string `json:"role"`

	// Permissions is the caller's flattened, sorted "module:action" key set in the
	// current organization. An empty array (not null) means the caller holds no
	// tenant role.
	Permissions []string `json:"permissions"`

	Stage  enums.UserStage  `json:"stage"`
	Status enums.UserStatus `json:"status"`
}

// MePermissionsResponse is the body of GET /auth/me/permissions: the caller's
// effective permission set in the current organization, resolved from the
// in-memory rbac snapshot with zero database work. It answers the "what may I
// do" half of the Me view on its own, so a frontend can gate its UI on the exact
// "module:action" keys the RBAC middleware will enforce. An empty array means the
// caller holds no tenant role.
type MePermissionsResponse struct {
	Permissions []string `json:"permissions"`
}

// MFASetupResponse is the body of POST /auth/mfa/setup. The secret and recovery
// codes are returned exactly once — copy them now; after this the raw secret is
// never recoverable (the column holds only the encrypted payload) and the codes
// only as hashes.
type MFASetupResponse struct {
	Secret          string   `json:"secret"`
	ProvisioningURI string   `json:"provisioning_uri"`
	RecoveryCodes   []string `json:"recovery_codes"`
}

// MFAVerifyRequest is the body of POST /auth/mfa/verify, which completes a
// second-factor login. mfa_challenge is the token issued by Login (MFA_REQUIRED
// metadata) after the password passed; code is the current TOTP. Code is bounded
// rather than exactly-6 because the validator must not reject a code submitted
// with separators before the service normalizes it.
type MFAVerifyRequest struct {
	Challenge string `json:"mfa_challenge" validate:"required,max=512"`
	Code      string `json:"code"          validate:"required,max=12"`
}

// MFARecoveryRequest is the body of POST /auth/mfa/recovery, which completes a
// second-factor login with a one-time recovery code from the setup batch instead
// of a TOTP code.
type MFARecoveryRequest struct {
	Challenge    string `json:"mfa_challenge" validate:"required,max=512"`
	RecoveryCode string `json:"recovery_code" validate:"required,min=6,max=32"`
}

// MFADisableRequest is the body of POST /auth/mfa/disable. The caller must
// submit a valid current TOTP code to prove the second factor before disabling.
type MFADisableRequest struct {
	Code string `json:"code" validate:"required,max=12"`
}

// PasswordChangeRequest is the body of POST /auth/password/change (authenticated).
// The caller proves knowledge of the current password before setting a new one.
// CurrentPassword is only bounded (not policy-validated) for the same reason as
// LoginRequest.Password — the account's existing password predates any policy
// change. NewPassword carries the creation policy (min=8) so a change can never
// weaken an account below the bar new users must clear. The service additionally
// rejects a new password equal to the current one.
type PasswordChangeRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,max=128"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=128"`
}

// PasswordForgotRequest is the body of POST /auth/password/forgot (public). It
// carries only the email; the endpoint always responds success regardless of
// whether the address maps to an account, so a caller cannot enumerate users.
type PasswordForgotRequest struct {
	Email string `json:"email" validate:"required,email,max=255"`
}

// PasswordResetRequest is the body of POST /auth/password/reset (public + token).
// Token is the opaque reset token delivered out of band (email channel, currently
// logged); NewPassword carries the same creation policy as a change. Redeeming a
// reset revokes every existing session for the account.
type PasswordResetRequest struct {
	Token       string `json:"token"        validate:"required,min=1,max=512"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=128"`
}

// PasswordForceChangeRequest is the body of POST /auth/password/force-change
// (public + token). It completes a login that was stopped by PASSWORD_CHANGE_REQUIRED.
// Token is the single-use change token minted by the login gate and delivered via
// the X-Password-Change-Token header. CurrentPassword proves the caller still knows
// the pre-change password — this is what distinguishes a forced change from a
// forgot-password reset, so a drained/exfiltrated change token is useless without
// the current password. NewPassword carries the same creation policy as a normal
// change, and revocation of all sessions is implicit (same as a password reset).
type PasswordForceChangeRequest struct {
	Token           string `json:"token"            validate:"required,min=1,max=512"`
	CurrentPassword string `json:"current_password" validate:"required,max=128"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=128"`
}
