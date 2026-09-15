package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	"backend/internal/middleware"
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	platformredis "backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
	"backend/pkg/crypto"
)

// Module is the auth module's assembled surface. Like rbac (and unlike a leaf
// domain module that hides everything behind New → *Handler), auth must hand its
// Service back to the composition root, because that one object satisfies two
// consumer-side ports wired elsewhere:
//
//   - middleware.Authenticator — main.go injects Service as WithAuthenticator, so
//     every Protected route validates its bearer token through auth.Authenticate.
//   - user.SessionRevoker      — the user module calls Service.RevokeUserSessions
//     when an admin deactivates/deletes a user, killing their live sessions in the
//     same transaction.
//
// The Handler is exposed via RegisterRoutes (the router's ModuleRegistrar), so the
// Module can sit in main.go's modules slice while main.go still holds the Service
// reference for the wiring above.
type Module struct {
	Handler *Handler
	Service *Service
}

// New wires the auth dependency graph: signer (from JWT config) → repository →
// service → handler. It fails fast if the JWT configuration cannot produce a valid
// signer (short/empty secret, non-positive TTL), so a mis-secured deployment never
// boots and starts minting forgeable or instantly-expired tokens.
//
// Collaborators supplied by the composition root:
//   - hasher — the shared Argon2id password hasher (also used by the user module),
//     so there is one tuned cost setting process-wide.
//   - access — the AccessResolver port, satisfied by *rbac.Enforcer; auth calls it
//     at login/refresh to snapshot the caller's role + permissions into the JWT.
//   - keyring — the AES-256-GCM KeyRing (from the field-encryption config) that
//     encrypts the MFA TOTP secret at rest. Passing nil disables MFA setup/verify
//     and Login rejects MFA-enabled accounts (refusing to open a session it cannot
//     drive the second factor for).
//   - rdb    — the shared Redis client. The SessionCache (ADR §17) is wired when
//     rdb is non-nil; passing nil disables the cache and Authenticate falls
//     straight through to the primary database.
//
// Construction order at the composition root is rbac → auth → user: rbac produces
// the Enforcer that auth needs here as access, and user needs this Module's Service
// as its SessionRevoker.
func New(
	database *db.DB,
	cfg *config.Config,
	rdb *platformredis.Client,
	metrics *telemetry.Metrics,
	hasher *crypto.PasswordHasher,
	access AccessResolver,
	keyring *crypto.KeyRing,
	v *validator.Validator,
	log *zap.Logger,
) (*Module, error) {
	if log == nil {
		log = zap.NewNop()
	}

	signer, err := NewSigner(SignerConfig{
		Secret:          []byte(cfg.JWT.Secret),
		PreviousSecrets: byteKeys(cfg.JWT.PreviousSecrets),
		Issuer:          cfg.JWT.Issuer,
		Audience:        cfg.JWT.Audience,
		KeyID:           cfg.JWT.KeyID,
		AccessTTL:       cfg.JWT.AccessTokenTTL,
		ClockSkew:       cfg.JWT.ClockSkew,
	})
	if err != nil {
		return nil, err
	}

	repo := NewRepository(database)

	// Session cache (ADR §17): best-effort. nil rdb ⇒ no cache. The TTL is kept
	// short (60s) so that a revocation whose Redis eviction failed can only grant
	// a stale projection's session a brief window — never up to a logout
	// lifecycle. PostgreSQL primary remains the deny-authority.
	var sessionCache *SessionCache
	if rdb != nil {
		sessionCache = NewSessionCache(rdb, 60*time.Second, metrics, log)
	}

	// Wrap the bare Argon2id hasher with the pepper layer (ADR §6). A
	// config without a pepper degrades to a passthrough wrapper so the
	// auth flow's rotation path stays exercised in tests; a populated
	// pepper enables the on-login transparent rehash and the rejection
	// of hashes minted under a retired pepper.
	pwHash, pwUpgrade := buildPepperedHasher(hasher, cfg)

	svc := NewService(repo, database, signer, pwHash, pwUpgrade, access, keyring, sessionCache, metrics, ServiceConfig{
		RefreshTTL:           cfg.JWT.RefreshTokenTTL,
		AbsoluteTimeout:      cfg.Session.AbsoluteTimeout,
		// ResetTTL has no dedicated config knob yet; 0 lets the service apply its
		// safe default (1h).
		ResetTTL:             0,
		MaxConcurrentSessions: cfg.Session.MaxConcurrentPerUser,
		PasswordHistorySize:   cfg.Password.HistorySize,
		Lockout: LockoutPolicy{
			Threshold: cfg.Login.Threshold,
			Lockout:   cfg.Login.Duration,
		},
	}, log)

	handler := NewHandler(svc, v, newCookieManager(cfg.Cookie, cfg.JWT.RefreshTokenTTL), log)

	return &Module{Handler: handler, Service: svc}, nil
}

// buildPepperedHasher wraps the bare Argon2id hasher in a PepperedHasher
// when config.Password.Pepper carries a current secret. It returns the
// PasswordHash the auth service will use for verify/hash and the
// VerifyUpgrader for the rotation-aware rehash path. When no pepper is
// configured, the function returns the bare hasher as both, so call
// sites do not have to nil-check.
func buildPepperedHasher(bare *crypto.PasswordHasher, cfg *config.Config) (crypto.PasswordHash, crypto.VerifyUpgrader) {
	if cfg == nil || cfg.Password.Pepper.Current == "" {
		return bare, nil
	}
	peppered := crypto.NewPepperedHasher(
		bare,
		[]byte(cfg.Password.Pepper.Current),
		cfg.Password.Pepper.CurrentID,
		[]byte(cfg.Password.Pepper.Previous),
		cfg.Password.Pepper.PreviousID,
	)
	return peppered, peppered
}

func byteKeys(in map[string]string) map[string][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for kid, secret := range in {
		out[kid] = []byte(secret)
	}
	return out
}

// RegisterRoutes lets the Module satisfy the router's ModuleRegistrar directly,
// delegating to its Handler so main.go can place the Module in the modules slice
// while still holding the Service reference for the port wiring.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	m.Handler.RegisterRoutes(rg, mw)
}

// newCookieManager resolves the refresh-cookie attributes from platform config,
// substituting safe defaults for unset fields: the shared cookie name, a root path,
// and a max-age equal to the refresh-token lifetime (falling back to the service's
// default refresh window if unconfigured). Keeping this mapping here leaves the
// handler independent of the config package.
func newCookieManager(cc config.CookieConfig, refreshTTL time.Duration) cookieManager {
	name := cc.Name
	if name == "" {
		name = constants.CookieRefreshToken
	}
	path := cc.Path
	if path == "" {
		path = "/"
	}
	maxAge := int(refreshTTL / time.Second)
	if maxAge <= 0 {
		maxAge = int(defaultRefreshTTL / time.Second)
	}
	return cookieManager{
		name:     name,
		domain:   cc.Domain,
		path:     path,
		secure:   cc.Secure,
		httpOnly: cc.HTTPOnly,
		sameSite: parseSameSite(cc.SameSite),
		maxAge:   maxAge,
	}
}

// parseSameSite maps the configured string onto Go's http.SameSite. An unset or
// unrecognised value defaults to Lax — a sensible, CSRF-resistant default for a
// refresh cookie that is only ever sent to our own /auth/refresh endpoint.
func parseSameSite(s string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	default:
		return http.SameSiteLaxMode
	}
}
