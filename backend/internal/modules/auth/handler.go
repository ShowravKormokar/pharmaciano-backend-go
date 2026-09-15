package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/constants"
	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/validator"
)

// Generic, non-committal bodies for the two public password endpoints. Both are
// intentionally identical regardless of whether the email/token matched anything
// real: the anti-enumeration guarantee lives in the service (which always returns
// nil for a miss), and the handler must not undo it by varying the response.
const (
	forgotPasswordMessage = "If an account matches that email, a password reset link has been sent."
	resetPasswordMessage  = "Your password has been reset. Please sign in again with your new password."
	// clientTypeBrowser is the value the SPA must send in the X-Client-Type
	// header to receive the cookie-only flow (no refresh token in the JSON
	// body). Anything else — empty, "mobile", "cli", "server" — gets the
	// legacy JSON body that includes the raw refresh token, which those
	// non-browser clients need to keep their own refresh-token store.
	clientTypeBrowser = "browser"
)

// messageResponse is the tiny envelope payload for endpoints that have nothing to
// return but a human-readable acknowledgement (the two password flows). It is a
// named type rather than an ad-hoc map so the response shape is stable and
// documented.
type messageResponse struct {
	Message string `json:"message"`
}

// Handler is the HTTP surface of the auth module. It is a thin adapter: it binds
// and validates the request, delegates the entire decision to the Service, maps
// the typed error (or success) onto the standard response envelope, and — for the
// token-issuing and session-ending endpoints — manages the HttpOnly refresh
// cookie. It holds no state of its own beyond its collaborators.
type Handler struct {
	svc    *Service
	val    *validator.Validator
	cookie cookieManager
	log    *zap.Logger
}

// NewHandler assembles the handler. The cookieManager is built by the module from
// platform config (name/domain/flags/lifetime) so the handler stays free of any
// config dependency and is trivially unit-testable.
func NewHandler(svc *Service, v *validator.Validator, cookie cookieManager, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, val: v, cookie: cookie, log: log}
}

// -----------------------------------------------------------------------------
// Public endpoints (no authentication)
// -----------------------------------------------------------------------------

// Login handles POST /auth/login. On success it sets the raw refresh token as an
// HttpOnly cookie (for browsers) and also returns it in the body (for non-browser
// clients); the access token is only ever in the body. Every failure is a generic,
// timing-equalised error from the service so the endpoint leaks nothing about
// which accounts exist or why a login was refused.
//
// The X-Client-Type header controls the body shape: a value of "browser" means
// the refresh token is delivered ONLY as an HttpOnly cookie and is omitted from
// the JSON body (so a stolen access-log or a browser dev-tools "copy response"
// cannot leak the long-lived credential). The cookie path remains identical so
// /auth/refresh still works for the SPA.
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	tokens, err := h.svc.Login(c.Request.Context(), &req)
	if err != nil {
		h.surfaceLoginChallenge(c, err)
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.write(c, tokens.RefreshToken, tokens.RefreshExpiresAt)
	if h.clientIsBrowser(c) {
		// ADR: the refresh token must never leak into the response body for a
		// browser. Clear it; omitempty on TokenResponse.RefreshToken drops the
		// field entirely.
		tokens.RefreshToken = ""
	}
	httpx.OK(c, tokens)
}

// Refresh handles POST /auth/refresh. The refresh token is resolved cookie-first,
// then from the optional body, so browsers need send nothing but the HttpOnly
// cookie while mobile/server clients can present it in JSON. A missing token is
// reported as TOKEN_INVALID (never a 400) so a caller cannot distinguish "you
// sent no token" from "your token was rejected".
//
// On any non-5xx failure the cookie is cleared: such a failure means the presented
// token is permanently unusable (malformed, expired, or — via the service's
// reuse detection — the whole family has just been revoked), so keeping the stale
// cookie would only produce repeated failures. A 5xx is left alone because the
// rotation transaction most likely rolled back, leaving the token still valid.
func (h *Handler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := h.bindOptionalJSON(c, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	raw := h.cookie.read(c)
	if raw == "" && req.RefreshToken != nil {
		raw = *req.RefreshToken
	}
	if raw == "" {
		httpx.Error(c, h.log, errs.New(errs.CodeTokenInvalid, "refresh token is required"))
		return
	}

	tokens, err := h.svc.Refresh(c.Request.Context(), raw)
	if err != nil {
		if errs.HTTPStatus(err) < 500 {
			h.cookie.clear(c)
		}
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.write(c, tokens.RefreshToken, tokens.RefreshExpiresAt)
	if h.clientIsBrowser(c) {
		// Same body-stripping policy as Login: a browser's rotated refresh token
		// rides the new HttpOnly cookie, never the response body.
		tokens.RefreshToken = ""
	}
	httpx.OK(c, tokens)
}

// PasswordForgot handles POST /auth/password/forgot. It always responds 200 with
// the same generic message; the service silently no-ops for an unknown or
// non-loginable address, so this endpoint can never be used to enumerate users.
func (h *Handler) PasswordForgot(c *gin.Context) {
	var req PasswordForgotRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	if err := h.svc.PasswordForgot(c.Request.Context(), &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	httpx.OK(c, messageResponse{Message: forgotPasswordMessage})
}

// PasswordReset handles POST /auth/password/reset. Redeeming a valid reset token
// sets the new password and revokes every session for the account (a forgotten
// password proves control of the email, not of any device). Any refresh cookie on
// the calling client is therefore cleared.
func (h *Handler) PasswordReset(c *gin.Context) {
	var req PasswordResetRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	if err := h.svc.PasswordReset(c.Request.Context(), &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.clear(c)
	httpx.OK(c, messageResponse{Message: resetPasswordMessage})
}

// PasswordForceChange handles POST /auth/password/force-change. It completes a
// login that was stopped by PASSWORD_CHANGE_REQUIRED: redeeming the one-time
// change token (from the X-Password-Change-Token header of the blocked login)
// together with the current + new password rotates the credential and returns a
// fresh session, so the user continues seamlessly. The service verifies the
// current password before applying the change, so a leaked token alone is inert.
func (h *Handler) PasswordForceChange(c *gin.Context) {
	var req PasswordForceChangeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	tokens, err := h.svc.PasswordForceChange(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.write(c, tokens.RefreshToken, tokens.RefreshExpiresAt)
	if h.clientIsBrowser(c) {
		tokens.RefreshToken = ""
	}
	httpx.OK(c, tokens)
}

// -----------------------------------------------------------------------------
// Authenticated endpoints
// -----------------------------------------------------------------------------

// Logout handles POST /auth/logout. It ends the caller's current session (the one
// the access token was minted under). As a convenience it also accepts an optional
// {"all": true} body to log out everywhere, mirroring the dedicated /logout-all
// route. The refresh cookie is cleared either way.
func (h *Handler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := h.bindOptionalJSON(c, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	ctx := c.Request.Context()
	var err error
	if req.All {
		err = h.svc.LogoutAll(ctx)
	} else {
		err = h.svc.Logout(ctx)
	}
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.clear(c)
	httpx.NoContent(c)
}

// LogoutAll handles POST /auth/logout-all: revoke every session of the current
// user (all devices), used after a suspected compromise. The refresh cookie on
// this client is cleared.
func (h *Handler) LogoutAll(c *gin.Context) {
	if err := h.svc.LogoutAll(c.Request.Context()); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	h.cookie.clear(c)
	httpx.NoContent(c)
}

// PasswordChange handles POST /auth/password/change. The caller proves the current
// password and sets a new one; the service revokes every *other* session but keeps
// the current one alive, so the refresh cookie stays valid and is deliberately not
// cleared.
func (h *Handler) PasswordChange(c *gin.Context) {
	var req PasswordChangeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	if err := h.svc.PasswordChange(c.Request.Context(), &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	httpx.NoContent(c)
}

// Me handles GET /auth/me: the identity + authorization view a frontend uses to
// gate its UI (effective role + permission set + branch scope). Identity fields
// come entirely from the request Principal with zero database work; the role and
// permission set are resolved from the in-memory rbac snapshot (also zero DB), so
// a role granted after this token was minted is reflected immediately. It
// is distinct from the user module's editable /users/me record.
func (h *Handler) Me(c *gin.Context) {
	p, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		// Defensive: the route is mounted behind Auth, so this should be
		// unreachable. Fail closed rather than emit a half-built identity.
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}

	view, err := h.svc.AuthorizationView(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	httpx.OK(c, MeResponse{
		UserID:         p.UserID,
		OrganizationID: p.OrgID,
		BranchID:       p.BranchID,
		SessionID:      p.SessionID,
		Role:           view.Role,
		Permissions:    view.Permissions,
		Stage:          p.Stage,
		Status:         p.Status,
	})
}

// MePermissions handles GET /auth/me/permissions: the caller's effective
// permission set, resolved from the in-memory rbac snapshot with zero database
// work. It answers the "what may I do" half of the Me authorization view on its
// own, so a frontend can gate its UI on the exact "module:action" keys the RBAC
// middleware will enforce.
func (h *Handler) MePermissions(c *gin.Context) {
	perms, err := h.svc.AuthorizationView(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, MePermissionsResponse{Permissions: perms.Permissions})
}

// ListSessions handles GET /auth/sessions: the caller's live sessions, ordered
// newest activity first, with the request's own session flagged. The UI uses
// this for the "active devices" page; the data is cosmetic so it reads the
// replica via the repository.
func (h *Handler) ListSessions(c *gin.Context) {
	items, err := h.svc.ListSessions(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, items)
}

// RevokeSession handles DELETE /auth/sessions/{id}: end a single *other*
// session of the caller (admin-grade self-service — "this is not my laptop,
// kill it"). The current session must be revoked via /auth/logout, so the
// handler rejects a self-target early.
func (h *Handler) RevokeSession(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, h.log, errs.Validation("path parameter id must be a valid UUID").WithCause(err))
		return
	}
	if err := h.svc.RevokeOtherSession(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// -----------------------------------------------------------------------------
// MFA — enrolment, second-factor verify, recovery, disable
// -----------------------------------------------------------------------------

// MFASetup handles POST /auth/mfa/setup (authenticated): provisions a TOTP
// enrolment for the caller, encrypts the secret at rest, issues a fresh batch of
// recovery codes, and enables the second factor. The secret + codes are returned
// exactly once.
func (h *Handler) MFASetup(c *gin.Context) {
	resp, err := h.svc.MFASetup(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

// MFAVerify handles POST /auth/mfa/verify (public): completes a second-factor
// login. The call is unauthenticated by design — a caller at this point has only
// a stage-1 challenge, not a bearer token. The service consumes the challenge,
// validates the TOTP code, and mints the real session tokens.
func (h *Handler) MFAVerify(c *gin.Context) {
	var req MFAVerifyRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.MFAVerify(c.Request.Context(), &req)
	if err != nil {
		// An MFA-enabled account that must also change its password passes the
		// second factor but still stops before a session (issueSession gate); carry
		// the change token out in a header so the client can complete the rotation.
		h.surfaceLoginChallenge(c, err)
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

// MFARecovery handles POST /auth/mfa/recovery (public): completes a second-factor
// login with a single-use recovery code from the setup batch when the
// authenticator is unavailable. Like MFAVerify it is unauthenticated by design.
func (h *Handler) MFARecovery(c *gin.Context) {
	var req MFARecoveryRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.MFARecovery(c.Request.Context(), &req)
	if err != nil {
		h.surfaceLoginChallenge(c, err)
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

// MFADisable handles POST /auth/mfa/disable (authenticated): turns off MFA for
// the caller after proving the current TOTP code, and clears the secret + codes.
func (h *Handler) MFADisable(c *gin.Context) {
	var req MFADisableRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.MFADisable(c.Request.Context(), &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// surfaceLoginChallenge lifts a single-use continuation token out of a
// login/verify error and onto a response header. Two gates produce these: an
// MFA-enabled account (MFA_REQUIRED → X-MFA-Challenge) and an account whose
// password must be changed before it can sign in (PASSWORD_CHANGE_REQUIRED →
// X-Password-Change-Token). Both fire only after the password (and, where
// applicable, the second factor) has succeeded, so the header reveals nothing to
// a caller who does not already hold the right credential. The generic error body
// carries no metadata, hence the header.
func (h *Handler) surfaceLoginChallenge(c *gin.Context, err error) {
	ae := errs.As(err)
	if ae == nil || ae.Meta == nil {
		return
	}
	if ae.Code == errs.CodeMFARequired {
		if tok, ok := ae.Meta["mfa_challenge"].(string); ok && tok != "" {
			c.Header(constants.HeaderMFAChallenge, tok)
		}
	}
	if ae.Code == errs.CodePasswordChangeRequired {
		if tok, ok := ae.Meta["change_token"].(string); ok && tok != "" {
			c.Header(constants.HeaderPasswordChangeToken, tok)
		}
	}
}

// bindOptionalJSON binds and validates a JSON body only when one is actually
// present. Logout and refresh are routinely sent by browsers with no body at all
// (the refresh token rides in the HttpOnly cookie), so treating an empty body as a
// 400 would be wrong; a *present* body, however, is still validated. A non-JSON or
// chunked body is treated as absent for these endpoints.
func (h *Handler) bindOptionalJSON(c *gin.Context, dst any) error {
	if c.Request == nil || c.Request.Body == nil || c.Request.ContentLength <= 0 {
		return nil
	}
	if ct := c.ContentType(); ct != "" && !strings.EqualFold(ct, "application/json") {
		return nil
	}
	return httpx.BindJSON(c, h.val, dst)
}

// -----------------------------------------------------------------------------
// Refresh-cookie management
// -----------------------------------------------------------------------------

// cookieManager encapsulates the single HttpOnly refresh cookie: how it is set on
// login/refresh, cleared on logout/reset, and read on refresh. It carries the
// resolved attributes (name/domain/path/flags and the max-age derived from the
// refresh-token lifetime) so the handler never touches platform config directly.
type cookieManager struct {
	name     string
	domain   string
	path     string
	secure   bool
	httpOnly bool
	sameSite http.SameSite
	maxAge   int // seconds; the refresh token's absolute lifetime
}

// write (re)issues the refresh cookie with the given raw token. It is called on
// login and on every successful refresh (rotation), so the cookie always holds the
// current token and its expiry tracks the session window.
func (cm cookieManager) write(c *gin.Context, raw string, expiresAt time.Time) {
	maxAge := cm.maxAge
	if !expiresAt.IsZero() {
		maxAge = int(time.Until(expiresAt).Seconds())
		if maxAge < 1 { maxAge = 1 }
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cm.name,
		Value:    raw,
		Path:     cm.path,
		Domain:   cm.domain,
		MaxAge:   maxAge,
		Expires:  expiresAt,
		Secure:   cm.secure,
		HttpOnly: cm.httpOnly,
		SameSite: cm.sameSite,
	})
}

// clear expires the refresh cookie immediately (MaxAge < 0 tells the browser to
// delete it). The attributes must otherwise match the ones used to set it or some
// browsers will ignore the deletion.
func (cm cookieManager) clear(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cm.name,
		Value:    "",
		Path:     cm.path,
		Domain:   cm.domain,
		MaxAge:   -1,
		Secure:   cm.secure,
		HttpOnly: cm.httpOnly,
		SameSite: cm.sameSite,
	})
}

// read returns the refresh token carried in the cookie, or "" when absent.
func (cm cookieManager) read(c *gin.Context) string {
	v, err := c.Cookie(cm.name)
	if err != nil {
		return ""
	}
	return v
}

// clientIsBrowser reports whether the caller opted into the cookie-only refresh
// flow by sending X-Client-Type: browser. Anything else (no header, an
// unrecognised value) keeps the legacy body shape that includes the raw refresh
// token so non-browser clients (mobile, CLI, server-to-server) that already
// maintain their own refresh-token store keep working unchanged.
//
// The signal is opt-in (a header the SPA sets on every call) rather than
// inferred from Origin/Referer/Accept because those are unreliable in mixed
// environments (mobile WebViews, native fetch, etc.). A misbehaving caller that
// claims "browser" without a real cookie will simply fail on /auth/refresh — no
// privilege gain.
func (h *Handler) clientIsBrowser(c *gin.Context) bool {
	return strings.EqualFold(strings.TrimSpace(c.GetHeader(constants.HeaderClientType)), clientTypeBrowser)
}
