package auth

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the auth endpoints under the given /api/v1 group, so the
// full paths are /api/v1/auth/*. The chains are deliberately different from a
// normal domain module's Protected() chain:
//
// # Public endpoints (login, refresh, password/forgot, password/reset)
//
// These run BEFORE any identity exists, so they carry neither Auth, Tenant nor
// RBAC. They are rate-limited by client IP (there is no user to key on yet) and
// Audit-wrapped so every attempt is recorded with its sensitive fields masked.
// Policy choice per endpoint:
//   - login   → login_per_ip: a tight per-address budget against credential
//     stuffing. The complementary per-account lockout lives in the service.
//   - refresh → refresh: a looser budget sized for legitimate token rotation.
//   - forgot/reset → reset: a very tight budget (a few per hour) against reset
//     spamming and token-guessing.
//
// # Authenticated self-service endpoints (logout, logout-all, password/change, me)
//
// These use Auth ONLY — deliberately NOT the full Protected() chain. Tenant is
// omitted on purpose: these operate on the caller's own session/identity and must
// keep working for a branch-bound principal that has no assigned branch (Tenant
// fails such a request closed with BRANCH_SCOPE_DENIED, which would wrongly stop a
// user from logging out or changing their password). RBAC is omitted because
// managing one's own session/password needs no module permission — every
// authenticated user may do it. Mutations additionally carry Audit; the read-only
// /me and /me/permissions do not (Audit only records mutations anyway) and use
// the read limiter.
//
// # MFA
//
// Enrolment and disable are authenticated writes gated exactly like the other
// authenticated mutations. The second-factor verify/recovery routes that
// complete an MFA login are PUBLIC (Auth omitted on purpose): a caller at that
// point holds a stage-1 challenge, not a bearer token. They are IP-throttled like
// login, and each wrong code burns its single-use challenge, so guessing the
// 6-digit code requires redoing the (rate-limited) password step per attempt.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/auth")

	// Public (pre-authentication). Each endpoint is throttled twice: by IP
	// (cheap, catches drive-by scanners) AND by email (an HMAC-normalized
	// subject so a botnet with N IPs still has to grind one bucket per
	// account). The email limiter is keyed on the parsed JSON body — the
	// handler still re-validates fully, so a missing/invalid email just falls
	// through to the IP bucket.
	grp.POST("/login",
		mw.RateLimitByIP("login_per_ip"),
		mw.RateLimitByEmail("login_per_email"),
		mw.Audit(), h.Login)
	grp.POST("/refresh",
		mw.RateLimitByIP("refresh"),
		mw.Audit(), h.Refresh)
	grp.POST("/password/forgot",
		mw.RateLimitByIP("reset"),
		mw.RateLimitByEmail("forgot"),
		mw.Audit(), h.PasswordForgot)
	grp.POST("/password/reset",
		mw.RateLimitByIP("reset"),
		mw.RateLimitByEmail("reset"),
		mw.Audit(), h.PasswordReset)
	grp.POST("/password/force-change",
		mw.RateLimitByIP("reset"),
		mw.RateLimitByEmail("reset"),
		mw.Audit(), h.PasswordForceChange)

	// Authenticated self-service (Auth only — no Tenant, no RBAC).
	grp.POST("/logout", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.Logout)
	grp.POST("/logout-all", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.LogoutAll)
	grp.POST("/password/change", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.PasswordChange)
	grp.GET("/me", mw.RateLimit("auth_read"), mw.Auth(), h.Me)
	grp.GET("/me/permissions", mw.RateLimit("auth_read"), mw.Auth(), h.MePermissions)

	// Session listing + per-device revocation — Auth-only (a user manages their
	// own devices, not a permission-gated admin action). Reads use the auth_read
	// limiter; writes use auth_write and carry Audit so each kill is recorded.
	grp.GET("/sessions", mw.RateLimit("auth_read"), mw.Auth(), h.ListSessions)
	grp.DELETE("/sessions/:id", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.RevokeSession)

	// MFA — authenticated enrolment + disable.
	grp.POST("/mfa/setup", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.MFASetup)
	grp.POST("/mfa/disable", mw.RateLimit("auth_write"), mw.Auth(), mw.Audit(), h.MFADisable)

	// MFA — public second-factor completion (Auth omitted: caller has a challenge,
	// not a bearer token). The single-use challenge bounds per-attempt guessing.
	grp.POST("/mfa/verify", mw.RateLimitByIP("login_per_ip"), mw.Audit(), h.MFAVerify)
	grp.POST("/mfa/recovery", mw.RateLimitByIP("login_per_ip"), mw.Audit(), h.MFARecovery)
}
