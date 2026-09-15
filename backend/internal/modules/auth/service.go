package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/enums"
	errs "backend/internal/errors"
	"backend/internal/modules/rbac"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/pkg/crypto"
)

// AccessResolver is the slice of the rbac enforcer the auth module needs when it
// mints a token: given the request domain (the user's organization) and the user
// id, return the resolved role + permission snapshot to embed in the JWT. It is a
// consumer-side port so auth depends on a one-method surface rather than the whole
// enforcer; *rbac.Enforcer satisfies it directly. auth already imports rbac for
// the Access type and there is no cycle (rbac imports neither auth nor user).
//
// The domain passed is always the user's own organization id: the enforcer keys
// role groupings by the user's org and carries system-role ("*") policy grants,
// so resolving against the user's org correctly yields both tenant roles and the
// SUPER_ADMIN system role (see rbac.Enforcer.ResolveAccess).
type AccessResolver interface {
	ResolveAccess(dom, userID string) rbac.Access
}

// Default token lifetimes, used when ServiceConfig leaves them unset so a
// mis-wired composition root still gets safe, finite windows rather than
// zero-length (instantly-expired) or unbounded tokens.
const (
	defaultRefreshTTL = 30 * 24 * time.Hour // 30 days: the session's absolute cap
	defaultResetTTL   = time.Hour           // password-reset link validity
	// defaultForceChangeTTL is how long a must-change-password gate token stays
	// redeemable at /auth/password/force-change. It is short because it is
	// unauthenticated pre-session state that grants a password change to whoever
	// holds it; a brief window limits its usefulness if exfiltrated.
	defaultForceChangeTTL = 15 * time.Minute

	// MFA defaults. The TOTP window is ±1 30-second step, which comfortably
	// absorbs a client clock skew of up to half a minute each way. The challenge
	// TTL is short because it is unauthenticated state: it binds a successful
	// password check to the next (second-factor) request, so it should not stay
	// redeemable long enough to be worth stealing.
	defaultTOTPWindow       = 1
	defaultMFAChallengeTTL  = 10 * time.Minute
	defaultMFAIssuer        = "Pharmaciano"
	defaultMFARecoveryCount = 10
)

// ServiceConfig carries the tunables the service needs, mapped from the platform
// config by the composition root. The access-token TTL lives on the Signer (it
// stamps the JWT exp itself); this struct covers the refresh/reset windows and
// the account-lockout policy.
type ServiceConfig struct {
	// RefreshTTL is the lifetime of the refresh-token chain. A rotation never
	// extends past it, so a stolen device falls out of trust after at most this
	// long regardless of how often its tokens are refreshed. When AbsolutTimeout
	// is zero it also caps the session itself (legacy behavior).
	RefreshTTL time.Duration
	// AbsoluteTimeout is the absolute life of a session (session.absolute_timeout).
	// It is the durable authority: Authenticate rejects a session that has passed
	// it, and the refresh-token chain inherits it, so a continuous session can
	// never outlive it. Zero falls back to RefreshTTL for the session cap.
	AbsoluteTimeout time.Duration
	// ResetTTL is how long an issued password-reset link stays redeemable.
	ResetTTL time.Duration
	// ForceChangeTTL is how long a must-change-password gate token stays
	// redeemable at /auth/password/force-change. Zero uses the safe default.
	ForceChangeTTL time.Duration
	// MaxConcurrentSessions caps how many active sessions one account may hold at
	// once. <= 0 disables the cap (default). When positive, a new login that would
	// take the account over the cap is refused with TOO_MANY_SESSIONS rather than
	// evicting an existing device.
	MaxConcurrentSessions int
	// PasswordHistorySize is how many recently retired password hashes are kept
	// per user and checked against on every password change (reuse rejection).
	// <= 0 disables history tracking entirely (default).
	PasswordHistorySize int
	// Lockout is the brute-force lockout policy applied to the login endpoint.
	Lockout LockoutPolicy
}

// Service is the auth module's business logic: it turns credentials into tokens
// (login), rotates them safely with reuse detection (refresh), tears sessions
// down (logout / logout-all / force-logout), rebuilds a Principal on every request
// (authenticate), and drives the password change/forgot/reset lifecycle. It is the
// only layer that maps raw driver errors to domain *errs.AppError values, and the
// only place session/token side effects are composed into transactions.
//
// It satisfies two consumer-side ports declared elsewhere by their exact method
// sets, wired at the composition root (no import back-edge):
//
//   - middleware.Authenticator — Authenticate(ctx, bearerToken) (Principal, error)
//   - user.SessionRevoker      — RevokeUserSessions(ctx, userID, reason) error
type Service struct {
	repo   *Repository
	db     *db.DB
	signer *Signer
	hasher crypto.PasswordHash
	// upgrader is the rotation-aware hasher when one is wired; nil means
	// the bare PasswordHasher is in use and no rotation support is
	// available. The login / change / reset paths always call
	// tryUpgrade after a successful verify so a freshly-rotated pepper
	// takes effect on the next interaction with the account.
	upgrader crypto.VerifyUpgrader
	access   AccessResolver
	lockout  LockoutPolicy

	// mfaEnc is the AES-256-GCM KeyRing that encrypts/decrypts the MFA TOTP
	// secret at rest (users.mfa_secret_encrypted). Production always wires it
	// from the field-encryption config; it may be nil only in tests that never
	// touch the MFA flows. nil implies MFA setup/verify are unavailable.
	mfaEnc *crypto.KeyRing

	sessionCache *SessionCache
	metrics      *telemetry.Metrics

	refreshTTL     time.Duration
	// absoluteTimeout is the absolute life of a session (session.absolute_timeout).
	// When zero the session is capped by refreshTTL instead (the legacy behavior).
	// See sessionExpiry.
	absoluteTimeout time.Duration
	resetTTL       time.Duration
	forceChangeTTL time.Duration

	maxConcurrentSessions int

	// historySize caps how many retired password hashes are kept and checked on
	// each password change (reuse rejection). <= 0 disables history tracking.
	historySize int

	// MFA window settings: how many 30-second TOTP steps of clock-drift slack
	// on each side, and how long a "stage 1 complete" challenge may stay
	// redeemable. Both are conservative by default and safe for production.
	totpWindow       int
	mfaChallengeTTL  time.Duration
	mfaIssuer        string
	mfaRecoveryCount int

	// dummyHash is a well-formed Argon2id hash verified against on the unknown-email
	// login path so the response timing matches a real wrong-password attempt (an
	// anti-enumeration measure). Computed once at construction.
	dummyHash string

	// now is the injectable clock. It defaults to time.Now; keeping it a field lets
	// deterministic tests drive the lockout/expiry logic without sleeping.
	now func() time.Time

	log *zap.Logger
}

// NewService assembles the service. repo, database, signer, hasher and access are
// required collaborators; cfg supplies the token windows and lockout policy, each
// falling back to a safe default when unset. The dummy anti-enumeration hash is
// computed once here.
//
// sessionCache may be nil — in which case Authenticate falls straight through
// to the primary database with no Redis fast path.
// metrics may be nil; every observability call site is nil-tolerant.
// upgrader may be nil; the service then assumes the bare PasswordHasher and
// skips the transparent pepper-rotation rehash.
// mfaEnc may be nil only in tests that never touch MFA; production always
// wires it so the TOTP secret is encrypted at rest.
func NewService(repo *Repository, database *db.DB, signer *Signer, hasher crypto.PasswordHash, upgrader crypto.VerifyUpgrader, access AccessResolver, mfaEnc *crypto.KeyRing, sessionCache *SessionCache, metrics *telemetry.Metrics, cfg ServiceConfig, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	refreshTTL := cfg.RefreshTTL
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	resetTTL := cfg.ResetTTL
	if resetTTL <= 0 {
		resetTTL = defaultResetTTL
	}
	forceChangeTTL := cfg.ForceChangeTTL
	if forceChangeTTL <= 0 {
		forceChangeTTL = defaultForceChangeTTL
	}
	return &Service{
		repo:             repo,
		db:               database,
		signer:           signer,
		hasher:           hasher,
		upgrader:         upgrader,
		access:           access,
		mfaEnc:           mfaEnc,
		sessionCache:     sessionCache,
		metrics:          metrics,
		lockout:          cfg.Lockout.normalized(),
		refreshTTL:       refreshTTL,
		absoluteTimeout:  cfg.AbsoluteTimeout,
		resetTTL:         resetTTL,
		forceChangeTTL:   forceChangeTTL,
		maxConcurrentSessions: cfg.MaxConcurrentSessions,
		historySize:      cfg.PasswordHistorySize,
		totpWindow:       defaultTOTPWindow,
		mfaChallengeTTL:  defaultMFAChallengeTTL,
		mfaIssuer:        defaultMFAIssuer,
		mfaRecoveryCount: defaultMFARecoveryCount,
		dummyHash:        newDummyHash(hasher),
		now:              time.Now,
		log:              log,
	}
}

// -----------------------------------------------------------------------------
// Login
// -----------------------------------------------------------------------------

// Login authenticates an email/password pair and, on success, opens a session and
// returns a fresh access+refresh pair. The flow is ordered to leak nothing to an
// attacker:
//
//  1. Unknown email — a dummy Argon2id verify runs so the response takes the same
//     time as a real wrong password, then the generic InvalidCredentials is
//     returned (never "no such user").
//  2. Locked account — rejected before the password is even checked, with a
//     Retry-After hint; the lockout counter is what makes repeated guessing costly.
//  3. Wrong password — the consecutive-failure counter is incremented (and the
//     account locked once it reaches the threshold), then the same generic error.
//  4. Correct password but non-login status — only here, once the caller has
//     proven they hold the password, is the account's status revealed.
//
// Every attempt (success or failure) is appended to the forensic login_attempts
// trail. On success the session row, its first refresh token and the reset of the
// lockout counters are written in one transaction, so a login is all-or-nothing.
func (s *Service) Login(ctx context.Context, req *LoginRequest) (TokenResponse, error) {
	now := s.now()
	ip, ua, fp := s.requestMeta(ctx)

	cred, err := s.repo.FindCredentialByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			// Equalize timing with the wrong-password path, then reject generically.
			_, _ = s.hasher.Verify(req.Password, s.dummyHash)
			s.recordAttempt(ctx, req.Email, ip, ua, nil, false, "unknown_email")
			s.observeLogin("unknown_email")
			return TokenResponse{}, errs.InvalidCredentials()
		}
		s.observeLogin("error")
		return TokenResponse{}, errs.DatabaseError(err)
	}

	// Hard lockout gate — checked before the password so a locked account cannot be
	// probed and so the client gets an actionable Retry-After.
	if retry := s.lockout.RetryAfter(cred.LockedUntil, now); retry > 0 {
		s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, false, "account_locked")
		s.observeLogin("locked")
		s.observeAccountLockout("triggered")
		return TokenResponse{}, errs.New(errs.CodeAccountLocked,
			"account temporarily locked due to too many failed login attempts").
			WithMeta("retry_after_seconds", ceilSeconds(retry))
	}

	ok, verr := s.verifyPassword(req.Password, cred.PasswordHash)
	if verr != nil {
		// A malformed stored hash can never be a match; it is our fault, not the
		// caller's, so surface it as an internal error (Verify already returns
		// false, so there is no risk of treating it as a successful login).
		s.log.Error("password verify failed", zap.String("user_id", cred.ID.String()), zap.Error(verr))
		s.observeLogin("error")
		return TokenResponse{}, errs.Internal(verr)
	}
	if !ok {
		lockUntil := now.Add(s.lockout.Lockout)
		if rerr := s.repo.RecordLoginFailure(ctx, cred.ID, s.lockout.Threshold, lockUntil); rerr != nil {
			// A persistence failure must not silently remove brute-force protection.
			s.observeLogin("error")
			return TokenResponse{}, errs.DatabaseError(rerr)
		}
		s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, false, "invalid_password")
		s.observeLogin("invalid_credentials")
		// Surface the trigger-vs-not distinction separately so an SRE can alert
		// on lockouts being entered for the first time, not on every bad try.
		if cred.LockedUntil != nil && lockUntil.After(*cred.LockedUntil) {
			s.observeAccountLockout("triggered")
		}
		return TokenResponse{}, errs.InvalidCredentials()
	}

	// Password is correct — only now may we speak about the account's status.
	if !cred.CanLogin() {
		s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, false, "status_"+string(cred.Status))
		s.observeLogin("inactive")
		return TokenResponse{}, statusError(cred.Status)
	}

	// Password is correct and the account's status permits login. If the account
	// requires a second factor, we stop at "stage 1": issue a short-lived,
	// single-use challenge and surface MFA_REQUIRED instead of tokens. The client
	// then proves the second factor (TOTP or a recovery code) via
	// /auth/mfa/verify or /auth/mfa/recovery to mint the real session.
	if cred.MFAEnabled {
		if s.mfaEnc == nil {
			// Refuse to log in a user we cannot drive the second factor for.
			s.log.Error("mfa enabled but no field-encryption keyring wired",
				zap.String("user_id", cred.ID.String()))
			s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, false, "mfa_misconfigured")
			s.observeLogin("error")
			return TokenResponse{}, errs.Internal(errors.New("multi-factor authentication is not configured"))
		}
		// Rehash under the current pepper now, while we still hold the plaintext
		// (the second-factor request only has the challenge, not the password).
		// Skip when the account must change its password: UpdatePassword would clear
		// must_change_password and silently disband the forced-change gate before the
		// change ever happens — the fresh pepper hash is (re)computed anyway once the
		// forced change completes.
		if !cred.MustChangePassword && s.upgrader != nil && s.upgrader.NeedsUpgrade(cred.PasswordHash) {
			newHash, herr := s.hasher.Hash(req.Password)
			if herr != nil {
				s.observeLogin("error")
				return TokenResponse{}, errs.Internal(herr)
			}
			if _, uerr := s.repo.UpdatePassword(ctx, cred.ID, newHash, now); uerr != nil {
				s.observeLogin("error")
				return TokenResponse{}, errs.DatabaseError(uerr)
			}
		}
		challenge, cerr := s.newMFAChallenge(ctx, cred)
		if cerr != nil {
			s.observeLogin("error")
			return TokenResponse{}, cerr
		}
		s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, false, "mfa_required")
		s.observeLogin("mfa_required")
		return TokenResponse{}, errs.New(errs.CodeMFARequired, "multi-factor authentication required").
			WithMeta("mfa_challenge", challenge)
	}

	// Account needs no second factor — open the session and mint tokens directly.
	resp, err := s.issueSession(ctx, cred, req.DeviceName, now, ip, ua, fp)
	if err != nil {
		return TokenResponse{}, err
	}

	// Recorded after commit so the audit row is never rolled back with a failed tx.
	s.recordAttempt(ctx, req.Email, ip, ua, &cred.ID, true, "")
	s.observeLogin("success")
	return resp, nil
}

// issueSession opens a password-verified credential's session and mints a fresh
// access+refresh pair in one transaction. It is the shared tail of Login and the
// MFA second-factor verify, so both paths open sessions identically. Preconditions
// the caller has already enforced: the password is verified, the account's status
// permits login, and any pepper rehash is applied. It enforces the
// must_change_password gate: an account that must rotate its password is refused a
// session (passwordChangeGate returns PASSWORD_CHANGE_REQUIRED instead) no matter
// which path called it. Post-commit bookkeeping (audit row, metrics) is left to the
// caller so each path can label it differently.
func (s *Service) issueSession(ctx context.Context, cred *Credential, deviceName *string, now time.Time, ip, ua, fp *string) (TokenResponse, error) {
	// Forced-password-change gate. issueSession is the single shared tail of every
	// full-login path (password-only Login, MFA verify, MFA recovery), so checking
	// here — not in each caller — guarantees an account whose policy demands a
	// password change can never obtain a session from any of them until the change
	// completes. A valid password has been proven; we refuse to mint any access or
	// refresh token and instead hand back a short-lived, single-use change token.
	if cred.MustChangePassword {
		return s.passwordChangeGate(ctx, cred, now, ip, ua)
	}
	var resp TokenResponse
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if rerr := s.repo.RecordLoginSuccess(ctx, cred.ID, ip, now); rerr != nil {
			return errs.DatabaseError(rerr)
		}

		// Concurrent-session cap (session.max_concurrent_per_user). This count runs
		// after RecordLoginSuccess has locked the users row and the new session has
		// NOT been inserted yet, so it is serialized per user — two simultaneous
		// logins cannot both count below the cap. Reject rather than silently evict:
		// evicting an existing device (e.g. a live POS terminal) is a surprise an
		// operator cannot recover from, while a refused login is actionable ("revoke
		// a device from the list").
		if s.maxConcurrentSessions > 0 {
			n, cerr := s.repo.CountActiveSessionsByUser(ctx, cred.ID, now)
			if cerr != nil {
				return errs.DatabaseError(cerr)
			}
			if n >= s.maxConcurrentSessions {
				s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, false, "too_many_sessions")
				s.observeLogin("too_many_sessions")
				return errs.New(errs.CodeTooManySessions,
					"maximum number of active sessions reached for this account").
					WithMeta("active_sessions", n).
					WithMeta("max_sessions", s.maxConcurrentSessions)
			}
		}

		familyID := uuid.New()
		expiresAt := s.sessionExpiry(now)

		session, rerr := s.repo.InsertSession(ctx, &Session{
			UserID:     cred.ID,
			FamilyID:   familyID,
			DeviceName: deviceName,
			DeviceFP:   fp,
			IP:         ip,
			UserAgent:  ua,
			ExpiresAt:  expiresAt,
		})
		if rerr != nil {
			return errs.DatabaseError(rerr)
		}

		// Resolve the effective branch subset before minting the access token
		// so the JWT carries the current assignment (ADR §19).
		branchIDs, berr := s.repo.ListUserBranchIDs(ctx, cred.ID, cred.OrganizationID, now)
		if berr != nil {
			return errs.DatabaseError(berr)
		}

		accessToken, _, aerr := s.mintAccess(cred, session.ID, session.SecurityGeneration, branchIDs)
		if aerr != nil {
			return aerr
		}
		s.observeTokenIssued("access")

		rawRefresh, gerr := generateRefreshToken()
		if gerr != nil {
			return gerr
		}
		if _, rerr := s.repo.InsertRefreshToken(ctx, &RefreshToken{
			SessionID: session.ID,
			UserID:    cred.ID,
			FamilyID:  familyID,
			TokenHash: hashToken(rawRefresh),
			ExpiresAt: expiresAt, // shares the session's absolute expiry
		}); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		s.observeTokenIssued("refresh")

		resp = newTokenResponse(accessToken, rawRefresh, s.signer.AccessTTL(), expiresAt)
		return nil
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// passwordChangeGate implements the must_change_password gate triggered at the top
// of issueSession. It mints a short-lived, single-use change token (stored hashed,
// reusing the password_resets table's existing single-use + expiry machinery),
// records the attempt, and returns a PASSWORD_CHANGE_REQUIRED error carrying the
// raw token in Meta so the handler can surface it via the
// X-Password-Change-Token header. No session and no access/refresh token is
// created — the account gets no credentialed surface until the password is
// rotated via /auth/password/force-change. Anti-enumeration holds because the gate
// only ever fires after the password has SUCCEEDED, so its existence reveals
// nothing to a caller who does not already hold the right password.
func (s *Service) passwordChangeGate(ctx context.Context, cred *Credential, now time.Time, ip, ua *string) (TokenResponse, error) {
	raw, err := generateRefreshToken()
	if err != nil {
		return TokenResponse{}, errs.Internal(err)
	}
	expiresAt := now.Add(s.forceChangeTTL)
	if _, rerr := s.repo.InsertPasswordReset(ctx, &PasswordReset{
		UserID:    cred.ID,
		TokenHash: hashToken(raw),
		ExpiresAt: expiresAt,
		IP:        ip,
	}); rerr != nil {
		return TokenResponse{}, errs.DatabaseError(rerr)
	}
	s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, false, "password_change_required")
	s.observeLogin("password_change_required")
	return TokenResponse{}, errs.New(errs.CodePasswordChangeRequired,
		"password change required before this account can sign in").
		WithMeta("change_token", raw)
}

// newMFAChallenge mints and persists a short-lived, single-use "stage 1
// complete" challenge token for a login whose account has MFA enabled. Only the
// hash is stored (mfa_challenges.token_hash); the opaque token returned here is
// handed to the client and redeemed with the second factor.
func (s *Service) newMFAChallenge(ctx context.Context, cred *Credential) (string, error) {
	raw, err := generateRefreshToken() // an opaque 256-bit value is a fine challenge
	if err != nil {
		return "", errs.Internal(err)
	}
	expiresAt := s.now().Add(s.mfaChallengeTTL)
	if rerr := s.repo.InsertMFAChallenge(ctx, cred.ID, hashToken(raw), expiresAt); rerr != nil {
		return "", errs.DatabaseError(rerr)
	}
	return raw, nil
}

// -----------------------------------------------------------------------------
// MFA setup / verify / recovery / disable
// -----------------------------------------------------------------------------

// mfaSecretPayload is the JSON stored (encrypted) in users.mfa_secret_encrypted:
// the base32 TOTP secret plus the last-observed 30-second step, used as a replay
// floor. The whole payload is AES-256-GCM encrypted with an entry context bound
// to the user id, so a copy of the column alone is not decryptable (the ciphertext
// also cannot be replayed onto another user, whose id context would not match).
type mfaSecretPayload struct {
	Secret      string `json:"secret"`
	LastCounter int64  `json:"last_used_counter,omitempty"`
}

// mfaSecretCtx is the KeyRing AAD context binding an encrypted MFA payload to its
// user. An envelope copied onto another user's row fails decryption.
func mfaSecretCtx(userID uuid.UUID) string { return "users.mfa_secret:" + userID.String() }

// MFASetup creates a new TOTP enrolment for the caller: generates a shared
// secret, encrypts it at rest (bound to the user), issues a fresh batch of
// recovery codes, and enables the second factor. The secret + recovery codes are
// returned exactly once — after this the raw secret is never recoverable, only
// the encrypted payload and the consumed codes. Enabling is rejected if the
// account already has MFA.
func (s *Service) MFASetup(ctx context.Context) (*MFASetupResponse, error) {
	userID := appctx.UserID(ctx)
	if userID == uuid.Nil {
		return nil, errs.Unauthenticated()
	}
	if s.mfaEnc == nil {
		return nil, errs.Internal(errors.New("multi-factor authentication is not configured"))
	}
	// now := s.now()

	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.Unauthenticated()
		}
		return nil, errs.DatabaseError(err)
	}
	if cred.MFAEnabled {
		return nil, errs.Conflict("multi-factor authentication is already enabled")
	}
	if cred.Email == "" {
		return nil, errs.Internal(errors.New("cannot enrol multi-factor authentication without an account email"))
	}

	secret, err := crypto.GenerateTOTPSecret(20)
	if err != nil {
		return nil, errs.Internal(err)
	}
	blob, err := json.Marshal(mfaSecretPayload{Secret: secret})
	if err != nil {
		return nil, errs.Internal(err)
	}
	encSecret, err := s.mfaEnc.EncryptWithContext(string(blob), mfaSecretCtx(userID))
	if err != nil {
		return nil, errs.Internal(err)
	}

	codes, err := crypto.GenerateRecoveryCodes(s.mfaRecoveryCount)
	if err != nil {
		return nil, errs.Internal(err)
	}

	err = s.db.WithTx(ctx, func(txctx context.Context) error {
		if rerr := s.repo.SetMFASecret(txctx, userID, &encSecret); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		if rerr := s.repo.SetMFAEnabled(txctx, userID, true); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		for _, code := range codes {
			if rerr := s.repo.InsertRecoveryCode(txctx, userID, hashToken(crypto.NormalizeRecoveryCode(code))); rerr != nil {
				return errs.DatabaseError(rerr)
			}
		}
		// MFA posture is security-relevant: bump the epoch so cached authorization
		// projections refresh on the next interaction (same invariant as password
		// and branch changes).
		if rerr := s.repo.BumpAuthzVersion(txctx, userID); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &MFASetupResponse{
		Secret:          secret,
		ProvisioningURI: crypto.ProvisioningURI(secret, s.mfaIssuer, cred.Email),
		RecoveryCodes:   codes,
	}, nil
}

// MFAVerify completes a second-factor login. The challenge (issued by Login for
// an MFA-enabled account) proves the password already passed; the code proves the
// TOTP authenticator. On success it consumes the challenge, advances the replay
// floor, and mints the real session tokens exactly as a password-only login
// would. Consuming the challenge before validating the code means a wrong code
// burns the challenge (the caller re-logs-in), which bounds TOTP brute force.
func (s *Service) MFAVerify(ctx context.Context, req *MFAVerifyRequest) (TokenResponse, error) {
	now := s.now()
	ip, ua, fp := s.requestMeta(ctx)

	userID, ok, err := s.repo.ConsumeMFAChallenge(ctx, hashToken(req.Challenge), now)
	if err != nil {
		return TokenResponse{}, errs.DatabaseError(err)
	}
	if !ok {
		return TokenResponse{}, errs.New(errs.CodeMFAInvalid, "invalid or expired challenge")
	}
	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return TokenResponse{}, errs.Unauthenticated()
		}
		return TokenResponse{}, errs.DatabaseError(err)
	}
	if !cred.MFAEnabled {
		return TokenResponse{}, errs.New(errs.CodeMFAInvalid, "multi-factor authentication is not enabled")
	}

	valid, err := s.verifyTOTP(ctx, cred, req.Code, now)
	if err != nil {
		return TokenResponse{}, err
	}
	if !valid {
		s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, false, "mfa_invalid")
		s.observeLogin("mfa_invalid")
		return TokenResponse{}, errs.New(errs.CodeMFAInvalid, "invalid verification code")
	}

	resp, err := s.issueSession(ctx, cred, nil, now, ip, ua, fp)
	if err != nil {
		return TokenResponse{}, err
	}
	s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, true, "mfa")
	s.observeLogin("success")
	return resp, nil
}

// MFARecovery completes a second-factor login with a one-time recovery code from
// the setup batch, for when the authenticator app is unavailable (lost device,
// no TOTP code). The challenge is consumed as in MFAVerify, and the single use of
// the recovery code is enforced by the conditional UPDATE. Recovery codes are
// intended to be scarce; consuming one does not reissue the pool.
func (s *Service) MFARecovery(ctx context.Context, req *MFARecoveryRequest) (TokenResponse, error) {
	now := s.now()
	ip, ua, fp := s.requestMeta(ctx)

	userID, ok, err := s.repo.ConsumeMFAChallenge(ctx, hashToken(req.Challenge), now)
	if err != nil {
		return TokenResponse{}, errs.DatabaseError(err)
	}
	if !ok {
		return TokenResponse{}, errs.New(errs.CodeMFAInvalid, "invalid or expired challenge")
	}
	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return TokenResponse{}, errs.Unauthenticated()
		}
		return TokenResponse{}, errs.DatabaseError(err)
	}

	codeHash := hashToken(crypto.NormalizeRecoveryCode(req.RecoveryCode))
	used, rerr := s.repo.ConsumeRecoveryCode(ctx, cred.ID, codeHash, now)
	if rerr != nil {
		return TokenResponse{}, errs.DatabaseError(rerr)
	}
	if !used {
		s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, false, "recovery_invalid")
		s.observeLogin("recovery_invalid")
		return TokenResponse{}, errs.New(errs.CodeMFAInvalid, "invalid recovery code")
	}

	resp, err := s.issueSession(ctx, cred, nil, now, ip, ua, fp)
	if err != nil {
		return TokenResponse{}, err
	}
	s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, true, "recovery")
	s.observeLogin("recovery")
	return resp, nil
}

// MFADisable turns off MFA for the caller after proving the current second
// factor (so a session alone cannot strip the user's protection): it clears the
// encrypted secret, wipes the recovery pool, and bumps the authorization epoch so
// every live session re-snapshots.
func (s *Service) MFADisable(ctx context.Context, req *MFADisableRequest) error {
	userID := appctx.UserID(ctx)
	if userID == uuid.Nil {
		return errs.Unauthenticated()
	}
	now := s.now()

	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return errs.Unauthenticated()
		}
		return errs.DatabaseError(err)
	}
	if !cred.MFAEnabled {
		return errs.Conflict("multi-factor authentication is not enabled")
	}

	// Prove the current authenticator before removing it.
	valid, err := s.verifyTOTP(ctx, cred, req.Code, now)
	if err != nil {
		return err
	}
	if !valid {
		return errs.New(errs.CodeMFAInvalid, "invalid verification code")
	}

	err = s.db.WithTx(ctx, func(txctx context.Context) error {
		if rerr := s.repo.SetMFAEnabled(txctx, userID, false); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		if rerr := s.repo.SetMFASecret(txctx, userID, nil); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		if rerr := s.repo.DeleteRecoveryCodes(txctx, userID); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		if rerr := s.repo.BumpAuthzVersion(txctx, userID); rerr != nil {
			return errs.DatabaseError(rerr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// verifyTOTP decrypts the caller's stored TOTP secret, checks the submitted code
// within the drift window, and enforces the replay floor: a code whose 30-second
// step is at or below an already-observed step is rejected. On success it
// advances the floor (single decrypt → validate → re-encrypt) so two concurrent
// verifications in the same window cannot both succeed. A missing secret, a
// malformed payload, or a wrong/expired code all report false (not an error) —
// only cryptographic or storage failures are errors.
func (s *Service) verifyTOTP(ctx context.Context, cred *Credential, code string, now time.Time) (bool, error) {
	if s.mfaEnc == nil {
		return false, errs.Internal(errors.New("multi-factor authentication is not configured"))
	}
	enc, err := s.repo.FindMFASecret(ctx, cred.ID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return false, nil
		}
		return false, errs.DatabaseError(err)
	}
	if enc == "" {
		return false, nil
	}
	plain, err := s.mfaEnc.DecryptWithContext(enc, mfaSecretCtx(cred.ID))
	if err != nil {
		// Corrupt or rotated-out ciphertext is an internal fault, never a valid code.
		s.log.Error("mfa secret decrypt failed", zap.String("user_id", cred.ID.String()), zap.Error(err))
		return false, errs.Internal(err)
	}
	var payload mfaSecretPayload
	if uerr := json.Unmarshal([]byte(plain), &payload); uerr != nil || payload.Secret == "" {
		return false, nil
	}
	counter, valid := crypto.TOTPValidate(payload.Secret, code, s.totpWindow, now)
	if !valid {
		return false, nil
	}
	if payload.LastCounter > 0 && counter <= payload.LastCounter {
		return false, nil // replay of an already-observed step
	}

	payload.LastCounter = counter
	blob, err := json.Marshal(payload)
	if err != nil {
		return false, errs.Internal(err)
	}
	newEnc, err := s.mfaEnc.EncryptWithContext(string(blob), mfaSecretCtx(cred.ID))
	if err != nil {
		return false, errs.Internal(err)
	}
	if rerr := s.repo.SetMFASecret(ctx, cred.ID, &newEnc); rerr != nil {
		return false, errs.DatabaseError(rerr)
	}
	return true, nil
}

// -----------------------------------------------------------------------------
// Authenticate (middleware.Authenticator)
// -----------------------------------------------------------------------------

// Authenticate verifies a bearer access token and rebuilds the request Principal.
// It runs on every protected request, so it does the minimum: verify the JWT
// (signature, algorithm, issuer/audience, time window — all in Signer.Parse) and
// then one stateful liveness check — a session lookup that goes Redis-cache
// first, then PostgreSQL primary. The session is the authority; a revoked or
// expired session (e.g. after a logout, a status change, or reuse detection)
// invalidates every access token minted under it immediately, even while the JWT
// itself would still verify. The role/permission snapshot is taken straight from
// the verified claims, so no authorization data is re-read on the hot path.
//
// Token defects propagate as their typed errors (TOKEN_EXPIRED vs TOKEN_INVALID)
// so the middleware can emit the correct WWW-Authenticate challenge; every
// liveness failure collapses to a generic Unauthenticated so a probing client
// learns nothing.
//
// Cache model (ADR §17): Redis stores a versioned projection keyed on
// (session_id, security_generation). A successful lookup populates the cache.
// A revocation advances security_generation so any stale cached entry is
// naturally orphaned; the cache TTL reaps it.
func (s *Service) Authenticate(ctx context.Context, bearerToken string) (appctx.Principal, error) {
	claims, err := s.signer.Parse(bearerToken)
	if err != nil {
		return appctx.Principal{}, err
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return appctx.Principal{}, errs.New(errs.CodeTokenInvalid, "malformed token subject")
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return appctx.Principal{}, errs.New(errs.CodeTokenInvalid, "malformed token session")
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		return appctx.Principal{}, errs.New(errs.CodeTokenInvalid, "malformed token organization")
	}
	var branchID *uuid.UUID
	if claims.BranchID != "" {
		b, berr := uuid.Parse(claims.BranchID)
		if berr != nil {
			return appctx.Principal{}, errs.New(errs.CodeTokenInvalid, "malformed token branch")
		}
		branchID = &b
	}

	now := s.now()

	// Redis fast path (ADR §17): when the session cache holds a current
	// projection — keyed on (id, security_generation), so revocation advances
	// the key and orphans the stale entry — we can serve the entire liveness
	// and account-state check with zero database round-trips. The projection
	// is deliberately fail-closed: any miss, Redis error, or malformed value
	// falls through to the PostgreSQL primary path below, and a stale
	// authz_version or disabled account is denied exactly as the DB path
	// would deny it.
	if s.sessionCache != nil {
		entry, hit, _ := s.sessionCache.Lookup(ctx, sessionID, claims.SecurityGeneration)
		if hit {
			// Staleness guards: compare the cached server-side state against
			// the JWT snapshot. A mismatch means the account or permissions
			// changed since the token was minted and the token is stale.
			if !entry.CanLogin || entry.OrganizationID != orgID || entry.AuthzVersion != claims.AuthzVersion {
				return appctx.Principal{}, errs.Unauthenticated()
			}
			// CRITICAL: the cache entry's AuthzVersion and the JWT's
			// AuthzVersion are both snapshots from token-minting time. A
			// role revocation that bumps authz_version AFTER both were
			// written is invisible to entry-vs-claims comparison. A cheap
			// scalar read detects that immediately — the index makes this
			// sub-millisecond and it is the only safe fast-path check.
			currentAV, avErr := s.repo.AuthzVersion(ctx, userID)
			if avErr != nil {
				return appctx.Principal{}, errs.DatabaseError(avErr)
			}
			if currentAV != entry.AuthzVersion {
				return appctx.Principal{}, errs.Unauthenticated()
			}
			if (entry.BranchID == nil) != (branchID == nil) || (entry.BranchID != nil && *entry.BranchID != *branchID) {
				return appctx.Principal{}, errs.Unauthenticated()
			}
			if entry.BranchID != nil && !containsBranch(entry.BranchIDs, *entry.BranchID) {
				return appctx.Principal{}, errs.Unauthenticated()
			}
			// Mirrors the primary-path predicate (model.Session.Active: now.Before(ExpiresAt))
			// so a session at now == ExpiresAt is expired on BOTH the cache and database
			// paths — otherwise a request racing the expiry instant gets inconsistent
			// outcomes depending on which path served it.
			if !entry.ExpiresAt.IsZero() && !now.Before(entry.ExpiresAt) {
				return appctx.Principal{}, errs.Unauthenticated()
			}
			return appctx.Principal{
				UserID:       userID,
				OrgID:        orgID,
				BranchID:     branchID,
				BranchIDs:    entry.BranchIDs,
				SessionID:    sessionID,
				RoleName:     claims.RoleName,
				Stage:        enums.UserStage(claims.Stage),
				Status:       enums.UserStatus(claims.Status),
				AuthzVersion: claims.AuthzVersion,
			}, nil
		}
	}

	// Stateful liveness check: Redis cache → PostgreSQL primary. The session is
	// the authority — a revoked/expired/missing session denies, period.
	session, err := s.repo.FindSessionByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return appctx.Principal{}, errs.Unauthenticated()
		}
		return appctx.Principal{}, errs.DatabaseError(err)
	}
	// The session must belong to the token's subject and still be active.
	if session.UserID != userID || !session.Active(now) {
		return appctx.Principal{}, errs.Unauthenticated()
	}

	// Account state and tenant/branch binding are authoritative server state, not
	// long-lived token claims. This also catches disablement immediately.
	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return appctx.Principal{}, errs.Unauthenticated()
		}
		return appctx.Principal{}, errs.DatabaseError(err)
	}
	if !cred.CanLogin() || cred.OrganizationID != orgID || cred.AuthzVersion != claims.AuthzVersion {
		return appctx.Principal{}, errs.Unauthenticated()
	}
	if (cred.BranchID == nil) != (branchID == nil) || (cred.BranchID != nil && *cred.BranchID != *branchID) {
		return appctx.Principal{}, errs.Unauthenticated()
	}

	// Resolve the principal's effective branch subset on every request. The
	// token carries the snapshot, but server-derived state is authoritative —
	// a branch assignment added/removed since the token was issued must take
	// effect immediately. The token is short-lived so a small in-line query
	// is the right granularity (a background cache would be premature).
	assigned, branchErr := s.repo.ListUserBranchIDs(ctx, userID, cred.OrganizationID, now)
	if branchErr != nil {
		// Fail closed: a request that cannot establish its branch scope may
		// not proceed. A transient DB blip → 5xx → client retries.
		return appctx.Principal{}, errs.DatabaseError(branchErr)
	}
	// For branch-bound principals, the token's BranchID must appear in the
	// assigned set; otherwise the principal has been re-scoped since the
	// token was minted.
	if cred.BranchID != nil {
		ok := false
		for _, b := range assigned {
			if b == *cred.BranchID {
				ok = true
				break
			}
		}
		if !ok {
			return appctx.Principal{}, errs.Unauthenticated()
		}
	}

	// Populate the Redis session cache (best effort) for the next request.
	if s.sessionCache != nil {
		s.sessionCache.Store(ctx, session.ID, sessionCacheEntry{
			UserID:             session.UserID,
			OrganizationID:     cred.OrganizationID,
			BranchID:           cred.BranchID,
			BranchIDs:          assigned,
			AuthzVersion:       cred.AuthzVersion,
			CanLogin:           cred.CanLogin(),
			SecurityGeneration: session.SecurityGeneration,
			ExpiresAt:          session.ExpiresAt,
		})
	}

	return appctx.Principal{
		UserID:       userID,
		OrgID:        orgID,
		BranchID:     branchID,
		BranchIDs:    assigned,
		SessionID:    sessionID,
		RoleName:     claims.RoleName,
		Stage:        enums.UserStage(claims.Stage),
		Status:       enums.UserStatus(claims.Status),
		AuthzVersion: claims.AuthzVersion,
	}, nil
}

// containsBranch reports whether the given branch UUID appears in the set.
func containsBranch(branches []uuid.UUID, target uuid.UUID) bool {
	for _, b := range branches {
		if b == target {
			return true
		}
	}
	return false
}

// AuthorizationView is the caller's effective authorization snapshot in the
// current organization: the canonical role and the flattened, sorted
// "module:action" permission set. It is resolved from the in-memory rbac
// policy snapshot (ADR §18) with zero database round-trip.
type AuthorizationView struct {
	Role        string
	Permissions []string
}

// AuthorizationView resolves the caller's effective role + permission set from
// the current rbac snapshot. It answers the "what may I do" half of the Me view
// and is derived from the same snapshot the RBAC middleware's Enforce reads, so
// it can never advertise a permission the enforcer would deny. A SUPER_ADMIN
// resolves to the grants their role carries; a caller with no tenant role
// resolves to an empty set (not an error).
//
// This is deliberately separate from the claims snapshot carried in the access
// token: a role change granted since the token was minted is reflected here
// immediately, which is exactly the freshness a UI-gating endpoint wants.
func (s *Service) AuthorizationView(ctx context.Context) (AuthorizationView, error) {
	userID := appctx.UserID(ctx)
	orgID := appctx.OrgID(ctx)
	if userID == uuid.Nil || orgID == uuid.Nil {
		return AuthorizationView{}, errs.Unauthenticated()
	}
	acc := s.access.ResolveAccess(orgID.String(), userID.String())
	return AuthorizationView{Role: acc.RoleName, Permissions: acc.Permissions}, nil
}

// -----------------------------------------------------------------------------
// Refresh
// -----------------------------------------------------------------------------

// Refresh rotates a refresh token: it consumes the presented token and issues a
// brand-new access+refresh pair bound to the same session and token family. The
// whole operation runs in one transaction, and the presented token row is locked
// FOR UPDATE, which — together with the guarded MarkRefreshTokenUsed — makes
// rotation and reuse-detection race-free: for any given token, exactly one
// rotation can win.
//
// Outcomes:
//   - Unknown token → Unauthenticated (client must log in again).
//   - Spent token (already used or revoked) re-presented → token theft: the whole
//     family is revoked, the session is killed and a reuse timestamp is stamped;
//     the caller gets TOKEN_REUSE_DETECTED. Crucially this teardown is *committed*
//     (the closure returns nil and the sentinel is checked afterwards) so the
//     scorched-earth response is not rolled back by returning an error.
//   - Expired token, dead/expired session, or an account that can no longer log in
//     → the family is torn down and Unauthenticated is returned.
//   - Otherwise → rotate, refresh the session's activity, re-snapshot the account's
//     live role/status, and mint a fresh pair whose expiry is bounded by the
//     session's absolute cap.
func (s *Service) Refresh(ctx context.Context, rawToken string) (TokenResponse, error) {
	if rawToken == "" {
		return TokenResponse{}, errs.New(errs.CodeTokenInvalid, "missing refresh token")
	}
	ip, _, _ := s.requestMeta(ctx)
	hash := hashToken(rawToken)

	var (
		resp  TokenResponse
		reuse bool // committed teardown happened; report reuse to the caller
		dead  bool // token/session/account no longer valid; must re-login
	)

	txErr := s.db.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()

		tok, err := s.repo.FindRefreshTokenByHashForUpdate(ctx, hash)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				dead = true // unknown token — nothing to tear down
				return nil
			}
			return errs.DatabaseError(err)
		}

		// Re-presenting an already-spent token is the reuse signal: scorched earth.
		if tok.Spent() {
			if e := s.teardownFamily(ctx, tok, enums.RevokeReasonReuseDetected.String(), true, now); e != nil {
				return e
			}
			reuse = true
			s.observeTokenReuse()
			return nil
		}

		// Expired but never spent: a benign timed-out client. Tear the family down
		// for hygiene and make them log in again.
		if !tok.Usable(now) {
			if e := s.teardownFamily(ctx, tok, enums.RevokeReasonExpired.String(), false, now); e != nil {
				return e
			}
			dead = true
			return nil
		}

		// The owning session must still be live.
		session, err := s.repo.FindSessionByID(ctx, tok.SessionID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				if e := s.teardownFamily(ctx, tok, enums.RevokeReasonExpired.String(), false, now); e != nil {
					return e
				}
				dead = true
				return nil
			}
			return errs.DatabaseError(err)
		}
		if !session.Active(now) {
			if e := s.teardownFamily(ctx, tok, enums.RevokeReasonExpired.String(), false, now); e != nil {
				return e
			}
			dead = true
			return nil
		}

		// Re-snapshot the account: a user disabled since login must not refresh.
		cred, err := s.repo.FindCredentialByID(ctx, tok.UserID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				if e := s.teardownFamily(ctx, tok, enums.RevokeReasonStatusChanged.String(), false, now); e != nil {
					return e
				}
				dead = true
				return nil
			}
			return errs.DatabaseError(err)
		}
		if !cred.CanLogin() {
			if e := s.teardownFamily(ctx, tok, enums.RevokeReasonStatusChanged.String(), false, now); e != nil {
				return e
			}
			if _, e := s.repo.RevokeSession(ctx, session.ID, cred.ID, nil, enums.RevokeReasonStatusChanged.String(), now); e != nil {
				return errs.DatabaseError(e)
			}
			if s.sessionCache != nil {
				s.sessionCache.InvalidateAll(ctx, session.ID)
			}
			dead = true
			return nil
		}

		// Rotate: mint the replacement, insert it, then atomically consume the old.
		// The new token inherits the session's absolute expiry so the chain can
		// never outlive its session.
		rawRefresh, gerr := generateRefreshToken()
		if gerr != nil {
			return gerr
		}
		newTok, ierr := s.repo.InsertRefreshToken(ctx, &RefreshToken{
			SessionID: session.ID,
			UserID:    cred.ID,
			FamilyID:  tok.FamilyID,
			TokenHash: hashToken(rawRefresh),
			ExpiresAt: session.ExpiresAt,
		})
		if ierr != nil {
			return errs.DatabaseError(ierr)
		}

		won, merr := s.repo.MarkRefreshTokenUsed(ctx, tok.ID, newTok.ID, now)
		if merr != nil {
			return errs.DatabaseError(merr)
		}
		if !won {
			// Lost the guard under the row lock → a concurrent/replayed redemption.
			// Tear down the family (the just-inserted replacement is in it and gets
			// revoked too) and report reuse.
			if e := s.teardownFamily(ctx, tok, enums.RevokeReasonReuseDetected.String(), true, now); e != nil {
				return e
			}
			reuse = true
			s.observeTokenReuse()
			return nil
		}

		if _, terr := s.repo.TouchSession(ctx, session.ID, ip, now, 0); terr != nil {
			return errs.DatabaseError(terr)
		}

		branchIDs, berr := s.repo.ListUserBranchIDs(ctx, cred.ID, cred.OrganizationID, now)
		if berr != nil {
			return errs.DatabaseError(berr)
		}

		accessToken, _, aerr := s.mintAccess(cred, session.ID, session.SecurityGeneration, branchIDs)
		if aerr != nil {
			return aerr
		}
		s.observeTokenIssued("access")
		s.observeTokenIssued("refresh")
		resp = newTokenResponse(accessToken, rawRefresh, s.signer.AccessTTL(), session.ExpiresAt)
		return nil
	})
	if txErr != nil {
		s.observeRefresh("error")
		return TokenResponse{}, txErr
	}
	if reuse {
		s.observeRefresh("reuse")
		return TokenResponse{}, errs.New(errs.CodeTokenReuseDetected,
			"refresh token reuse detected; the session was revoked, please log in again")
	}
	if dead {
		// dead covers: unknown_token, expired, session_inactive, account_inactive.
		// We do not know which at this layer; emit a single "expired" bucket
		// because the caller only needs to re-login.
		s.observeRefresh("expired")
		return TokenResponse{}, errs.Unauthenticated()
	}
	s.observeRefresh("success")
	return resp, nil
}

// -----------------------------------------------------------------------------
// Logout / logout-all / force-logout
// -----------------------------------------------------------------------------

// Logout revokes the caller's current session and its refresh chain. The session
// is identified from the request Principal (never from the body), so a caller can
// only ever end its own session. It is idempotent: revoking an already-revoked
// session is a no-op success.
func (s *Service) Logout(ctx context.Context) error {
	userID := appctx.UserID(ctx)
	sessionID := appctx.SessionID(ctx)
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return errs.Unauthenticated()
	}
	now := s.now()
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.RevokeSession(ctx, sessionID, userID, nil, enums.RevokeReasonLogout.String(), now); err != nil {
			return errs.DatabaseError(err)
		}
		if _, err := s.repo.RevokeRefreshTokensBySession(ctx, sessionID, now); err != nil {
			return errs.DatabaseError(err)
		}
		// Evict every generation of this session's cached projection so a
		// logged-out device cannot ride a stale Redis entry on its old JWT.
		if s.sessionCache != nil {
			s.sessionCache.InvalidateAll(ctx, sessionID)
		}
		return nil
	})
}

// LogoutAll revokes every session and refresh token for the caller — "log out
// everywhere", used after a suspected compromise. Identified from the Principal.
func (s *Service) LogoutAll(ctx context.Context) error {
	userID := appctx.UserID(ctx)
	if userID == uuid.Nil {
		return errs.Unauthenticated()
	}
	now := s.now()
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.RevokeUserSessions(ctx, userID, nil, enums.RevokeReasonLogoutAll.String(), now); err != nil {
			return errs.DatabaseError(err)
		}
		if _, err := s.repo.RevokeRefreshTokensByUser(ctx, userID, now); err != nil {
			return errs.DatabaseError(err)
		}
		return nil
	})
}

// BumpAuthzVersion advances a user's durable authorization epoch, invalidating
// every stale token/session snapshot so the authorizer resolves fresh permissions
// on the next interaction (ADR §15). It is the auth module's implementation of
// the user.AuthzBumper port, invoked when the user module grants/revokes a branch
// assignment. The branch subset (and the epoch that guards it) is read by the
// auth Authenticate path, so the bump must reflect immediately — and it does:
// Authenticate's cache-hit path re-reads the epoch on each request and denies a
// mismatched entry.
//
// Contract (honored here): it does NOT open its own transaction — it writes
// through the ctx via the repository's FromCtx, so the bump commits (or rolls
// back) atomically with the assignment write that triggered it. A missing user
// surfaces as db.ErrNoRows for the caller to map.
func (s *Service) BumpAuthzVersion(ctx context.Context, userID uuid.UUID) error {
	return s.repo.BumpAuthzVersion(ctx, userID)
}

// RevokeUserSessions terminates every session and refresh token for a user. It is
// the auth module's implementation of the user.SessionRevoker port: the user
// module calls it inside its own transaction when a user is moved out of a
// login-capable status or soft-deleted.
//
// Contract (honored here): it does NOT open its own transaction — it writes
// through the ctx via the repository's FromCtx, so the revocation commits (or
// rolls back) atomically with the status/delete write that triggered it. reason
// is the free-form audit string supplied by the caller.
func (s *Service) RevokeUserSessions(ctx context.Context, userID uuid.UUID, reason string) error {
	now := s.now()
	ids, err := s.repo.RevokeUserSessions(ctx, userID, nil, reason, now)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if _, err := s.repo.RevokeRefreshTokensByUser(ctx, userID, now); err != nil {
		return errs.DatabaseError(err)
	}
	if s.sessionCache != nil {
		for _, id := range ids {
			s.sessionCache.InvalidateAll(ctx, id)
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Password change / forgot / reset
// -----------------------------------------------------------------------------

// PasswordChange lets an authenticated user set a new password after proving the
// current one. On success every *other* session is revoked (a changed password
// should not leave other devices logged in) while the caller's current session
// survives, and the whole thing — verify, hash, update, revoke — runs in one
// transaction. A wrong current password is a generic credential error; a new
// password equal to the current one is a validation error.
func (s *Service) PasswordChange(ctx context.Context, req *PasswordChangeRequest) error {
	userID := appctx.UserID(ctx)
	sessionID := appctx.SessionID(ctx)
	if userID == uuid.Nil {
		return errs.Unauthenticated()
	}
	if req.NewPassword == req.CurrentPassword {
		s.observePasswordChange("mismatch")
		return errs.Validation("new password must differ from the current password")
	}
	now := s.now()

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		cred, err := s.repo.FindCredentialByID(ctx, userID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Unauthenticated()
			}
			return errs.DatabaseError(err)
		}

		ok, verr := s.verifyPassword(req.CurrentPassword, cred.PasswordHash)
		if verr != nil {
			s.observePasswordChange("error")
			return errs.Internal(verr)
		}
		if !ok {
			s.observePasswordChange("wrong_current")
			return errs.New(errs.CodeInvalidCredentials, "current password is incorrect")
		}

		if herr := s.enforcePasswordHistory(ctx, userID, cred.PasswordHash, req.NewPassword); herr != nil {
			if apperr := errs.As(herr); apperr != nil {
				if apperr.Code == errs.CodeValidationError {
					s.observePasswordChange("reuse")
				}
			}
			return herr
		}

		newHash, herr := s.hasher.Hash(req.NewPassword)
		if herr != nil {
			s.observePasswordChange("error")
			return errs.Internal(herr)
		}
		updated, uerr := s.repo.UpdatePassword(ctx, userID, newHash, now)
		if uerr != nil {
			s.observePasswordChange("error")
			return errs.DatabaseError(uerr)
		}
		if !updated {
			s.observePasswordChange("error")
			return errs.Unauthenticated()
		}
		// Archive the hash being retired, trimming history to the configured size.
		if e := s.recordPasswordHistory(ctx, userID, cred.PasswordHash, now); e != nil {
			s.observePasswordChange("error")
			return errs.DatabaseError(e)
		}

		revokedIDs, e := s.repo.RevokeUserSessionsExcept(ctx, userID, sessionID, &userID, enums.RevokeReasonPasswordChanged.String(), now)
		if e != nil {
			return errs.DatabaseError(e)
		}
		if _, e := s.repo.RevokeRefreshTokensByUserExcept(ctx, userID, sessionID, now); e != nil {
			return errs.DatabaseError(e)
		}
		if s.sessionCache != nil {
			for _, id := range revokedIDs {
				s.sessionCache.InvalidateAll(ctx, id)
			}
		}
		s.observePasswordChange("success")
		return nil
	})
}

// PasswordForceChange completes a login that was stopped by PASSWORD_CHANGE_REQUIRED.
// It redeems the single-use change token minted by the login gate, proves the
// caller still knows the current password (the property that makes a change token
// useless if it leaks), sets the new password (which clears must_change_password),
// and revokes every session so the forced rotation has no stale credentialed
// surface anywhere. On success it mints a fresh session so the user continues
// seamlessly — the flag is now clear, so the gate does not re-fire. Like the MFA
// flow, the change runs in one transaction and only the current-password check and
// the final session issue live outside it.
func (s *Service) PasswordForceChange(ctx context.Context, req *PasswordForceChangeRequest) (TokenResponse, error) {
	now := s.now()
	ip, ua, fp := s.requestMeta(ctx)

	var userID uuid.UUID
	txErr := s.db.WithTx(ctx, func(ctx context.Context) error {
		// The change token rides in the same password_resets table the forgot-password
		// flow uses for its single-use + expiry + hashed-token machinery. Redeeming it
		// is a single-use operation under FOR UPDATE, like a reset link.
		reset, err := s.repo.FindPasswordResetByHashForUpdate(ctx, hashToken(req.Token))
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.New(errs.CodeTokenInvalid, "invalid or expired change token")
			}
			return errs.DatabaseError(err)
		}
		if !reset.Usable(now) {
			return errs.New(errs.CodeTokenInvalid, "invalid or expired change token")
		}

		cred, err := s.repo.FindCredentialByID(ctx, reset.UserID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Unauthenticated()
			}
			return errs.DatabaseError(err)
		}

		if req.NewPassword == req.CurrentPassword {
			s.observePasswordChange("mismatch")
			return errs.Validation("new password must differ from the current password")
		}

		// The distinguishing check: only a caller who holds the pre-change password
		// may redeem a change token. A leaked token alone cannot rotate the account.
		ok, verr := s.verifyPassword(req.CurrentPassword, cred.PasswordHash)
		if verr != nil {
			s.observePasswordChange("error")
			return errs.Internal(verr)
		}
		if !ok {
			s.observePasswordChange("wrong_current")
			return errs.New(errs.CodeInvalidCredentials, "current password is incorrect")
		}

		if herr := s.enforcePasswordHistory(ctx, cred.ID, cred.PasswordHash, req.NewPassword); herr != nil {
			if apperr := errs.As(herr); apperr != nil && apperr.Code == errs.CodeValidationError {
				s.observePasswordChange("reuse")
			}
			return herr
		}

		won, merr := s.repo.MarkPasswordResetUsed(ctx, reset.ID, now)
		if merr != nil {
			return errs.DatabaseError(merr)
		}
		if !won {
			// Concurrent redemption under the FOR UPDATE lock.
			return errs.New(errs.CodeTokenInvalid, "invalid or expired change token")
		}

		newHash, herr := s.hasher.Hash(req.NewPassword)
		if herr != nil {
			s.observePasswordChange("error")
			return errs.Internal(herr)
		}
		updated, uerr := s.repo.UpdatePassword(ctx, cred.ID, newHash, now)
		if uerr != nil {
			s.observePasswordChange("error")
			return errs.DatabaseError(uerr)
		}
		if !updated {
			return errs.Internal(nil).WithMeta("reason", "force-change target user missing")
		}
		// Archive the hash being retired, trimming history to the configured size.
		if e := s.recordPasswordHistory(ctx, cred.ID, cred.PasswordHash, now); e != nil {
			s.observePasswordChange("error")
			return errs.DatabaseError(e)
		}

		// The password changed: kill every session and refresh token account-wide
		// (the user proved control of the current password, not of any device).
		revokedIDs, e := s.repo.RevokeUserSessions(ctx, cred.ID, nil, enums.RevokeReasonPasswordChanged.String(), now)
		if e != nil {
			return errs.DatabaseError(e)
		}
		if _, e := s.repo.RevokeRefreshTokensByUser(ctx, cred.ID, now); e != nil {
			return errs.DatabaseError(e)
		}
		if s.sessionCache != nil {
			for _, id := range revokedIDs {
				s.sessionCache.InvalidateAll(ctx, id)
			}
		}

		userID = cred.ID
		return nil
	})
	if txErr != nil {
		return TokenResponse{}, txErr
	}

	// Reload the fresh credential — must_change_password is now FALSE, so the gate
	// cannot re-fire — and open a session exactly as a normal login would.
	cred, err := s.repo.FindCredentialByID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return TokenResponse{}, errs.Unauthenticated()
		}
		return TokenResponse{}, errs.DatabaseError(err)
	}
	resp, err := s.issueSession(ctx, cred, nil, s.now(), ip, ua, fp)
	if err != nil {
		return TokenResponse{}, err
	}
	s.recordAttempt(ctx, cred.Email, ip, ua, &cred.ID, true, "password_change_forced")
	s.observePasswordChange("success")
	s.observeLogin("success")
	return resp, nil
}

// PasswordForgot issues a single-use, time-boxed reset token for an email. It is
// anti-enumeration: it ALWAYS returns success, whether or not the address maps to
// a login-capable account, so a caller cannot tell which emails are registered.
// When a token is issued the user's earlier outstanding resets are invalidated
// first (at most one live link per user). The email channel is disabled for now,
// so the raw token is logged for an operator to relay — it is never returned in
// the response or persisted in the clear.
func (s *Service) PasswordForgot(ctx context.Context, req *PasswordForgotRequest) error {
	now := s.now()
	ip, _, _ := s.requestMeta(ctx)

	cred, err := s.repo.FindCredentialByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			s.observePasswordResetIssued("unknown_user")
			return nil // unknown address: silently succeed
		}
		return errs.DatabaseError(err)
	}
	if !cred.CanLogin() {
		s.observePasswordResetIssued("unknown_user")
		return nil // non-login account: silently succeed, issue nothing
	}

	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		if e := s.repo.InvalidateUserPasswordResets(ctx, cred.ID, now); e != nil {
			return errs.DatabaseError(e)
		}
		raw, gerr := generateRefreshToken()
		if gerr != nil {
			return gerr
		}
		if _, e := s.repo.InsertPasswordReset(ctx, &PasswordReset{
			UserID:    cred.ID,
			TokenHash: hashToken(raw),
			ExpiresAt: now.Add(s.resetTTL),
			IP:        ip,
		}); e != nil {
			return errs.DatabaseError(e)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Email delivery is not wired yet. Never log the raw reset credential: logs
	// are a common cross-environment exfiltration path.
	s.log.Info("password reset issued (email channel disabled)",
		zap.String("user_id", cred.ID.String()),
		zap.Time("expires_at", now.Add(s.resetTTL)),
	)
	s.observePasswordResetIssued("known_user")
	return nil
}

// PasswordReset consumes a reset token and sets a new password. The token row is
// locked FOR UPDATE and consumed with a guarded update, so a link is strictly
// single-use even under concurrent submission. An unknown, expired or
// already-used token is reported with one indistinct TOKEN_INVALID so a caller
// cannot probe which tokens exist. A successful reset revokes every session and
// refresh token for the account (the user proved control of the email, not of any
// device), forcing a fresh login everywhere.
func (s *Service) PasswordReset(ctx context.Context, req *PasswordResetRequest) error {
	now := s.now()
	hash := hashToken(req.Token)

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		reset, err := s.repo.FindPasswordResetByHashForUpdate(ctx, hash)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				s.observePasswordResetRedeemed("invalid_token")
				return errs.New(errs.CodeTokenInvalid, "invalid or expired reset token")
			}
			return errs.DatabaseError(err)
		}
		if !reset.Usable(now) {
			s.observePasswordResetRedeemed("expired")
			return errs.New(errs.CodeTokenInvalid, "invalid or expired reset token")
		}

		// Load the pre-reset credential so the reuse-rejection rule can compare
		// against the current hash and archive it once superseded.
		cred, err := s.repo.FindCredentialByID(ctx, reset.UserID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Internal(nil).WithMeta("reason", "reset target user missing")
			}
			return errs.DatabaseError(err)
		}

		if herr := s.enforcePasswordHistory(ctx, reset.UserID, cred.PasswordHash, req.NewPassword); herr != nil {
			if apperr := errs.As(herr); apperr != nil && apperr.Code == errs.CodeValidationError {
				s.observePasswordResetRedeemed("reuse")
			}
			return herr
		}

		newHash, herr := s.hasher.Hash(req.NewPassword)
		if herr != nil {
			return errs.Internal(herr)
		}

		won, merr := s.repo.MarkPasswordResetUsed(ctx, reset.ID, now)
		if merr != nil {
			return errs.DatabaseError(merr)
		}
		if !won {
			// Concurrent redemption under the FOR UPDATE lock.
			s.observePasswordResetRedeemed("invalid_token")
			return errs.New(errs.CodeTokenInvalid, "invalid or expired reset token")
		}

		updated, uerr := s.repo.UpdatePassword(ctx, reset.UserID, newHash, now)
		if uerr != nil {
			return errs.DatabaseError(uerr)
		}
		if !updated {
			return errs.Internal(nil).WithMeta("reason", "reset target user missing")
		}
		// Archive the hash being retired, trimming history to the configured size.
		if e := s.recordPasswordHistory(ctx, reset.UserID, cred.PasswordHash, now); e != nil {
			return errs.DatabaseError(e)
		}

		revokedIDs, e := s.repo.RevokeUserSessions(ctx, reset.UserID, nil, enums.RevokeReasonPasswordChanged.String(), now)
		if e != nil {
			return errs.DatabaseError(e)
		}
		if _, e := s.repo.RevokeRefreshTokensByUser(ctx, reset.UserID, now); e != nil {
			return errs.DatabaseError(e)
		}
		if s.sessionCache != nil {
			for _, id := range revokedIDs {
				s.sessionCache.InvalidateAll(ctx, id)
			}
		}
		s.observePasswordResetRedeemed("success")
		return nil
	})
}

// -----------------------------------------------------------------------------
// Session listing & per-device revocation (authenticated self-service)
// -----------------------------------------------------------------------------

// ListSessions returns the caller's live sessions, ordered newest activity
// first, with the request's own session flagged Current so the UI can label
// "This device" and refuse to offer it for revocation.
func (s *Service) ListSessions(ctx context.Context) ([]SessionItem, error) {
	userID := appctx.UserID(ctx)
	currentSessionID := appctx.SessionID(ctx)
	if userID == uuid.Nil {
		return nil, errs.Unauthenticated()
	}
	now := s.now()
	sessions, err := s.repo.ListActiveSessionsByUser(ctx, userID, now)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	items := make([]SessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, toSessionItem(&sess, sess.ID == currentSessionID))
	}
	return items, nil
}

// RevokeOtherSession terminates a single session that the caller owns, *other
// than* the one they're currently authenticated with. A user can only ever
// revoke their own session, keyed on (sessionID, userID). A revoked or
// unknown session is reported as NotFound (no information leak about whether
// the session ever existed). The owning refresh chain is also killed, so a
// stolen device cannot simply refresh its way back in.
func (s *Service) RevokeOtherSession(ctx context.Context, targetID uuid.UUID) error {
	userID := appctx.UserID(ctx)
	currentSessionID := appctx.SessionID(ctx)
	if userID == uuid.Nil || currentSessionID == uuid.Nil {
		return errs.Unauthenticated()
	}
	if targetID == currentSessionID {
		return errs.Validation("use /auth/logout to revoke the current session")
	}
	now := s.now()
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		ok, err := s.repo.RevokeSession(ctx, targetID, userID, &userID, enums.RevokeReasonAdminForce.String(), now)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !ok {
			return errs.NotFound("session")
		}
		if _, err := s.repo.RevokeRefreshTokensBySession(ctx, targetID, now); err != nil {
			return errs.DatabaseError(err)
		}
		if s.sessionCache != nil {
			s.sessionCache.InvalidateAll(ctx, targetID)
		}
		return nil
	})
}

// toSessionItem projects a Session to the SessionItem DTO, computing a
// coarse "city, country" Location string when both halves are present.
func toSessionItem(s *Session, current bool) SessionItem {
	var loc *string
	if s.City != nil && *s.City != "" && s.Country != nil && *s.Country != "" {
		v := *s.City + ", " + *s.Country
		loc = &v
	} else if s.Country != nil && *s.Country != "" {
		v := *s.Country
		loc = &v
	}
	return SessionItem{
		ID:         s.ID,
		DeviceName: s.DeviceName,
		Browser:    s.Browser,
		OS:         s.OS,
		DeviceType: s.DeviceType,
		IP:         s.IP,
		Location:   loc,
		LastSeenAt: s.LastSeenAt,
		CreatedAt:  s.CreatedAt,
		ExpiresAt:  s.ExpiresAt,
		Current:    current,
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// enforcePasswordHistory imposes the reuse-rejection rule on a password change:
// the proposed new plaintext must verify against neither the account's current
// hash nor any of its recently retired history hashes. A mismatch of any of
// them returns a VALIDATION_ERROR; an internal hash-comparison failure returns
// INTERNAL_ERROR. It is a no-op when history tracking is disabled
// (historySize <= 0). Called inside the change transaction, before the new hash
// is written, so the candidate is tested against the pre-change state. Because
// verifyPassword runs the same peppered-Argon2id check the login path uses,
// comparing against a stored history hash is exact (hash-of-password equality),
// not fuzzy.
func (s *Service) enforcePasswordHistory(ctx context.Context, userID uuid.UUID, currentHash, newPlaintext string) error {
	if s.historySize <= 0 {
		return nil
	}

	// Current hash: a change that re-selects the live password is reuse too, and
	// this closes the reset path, which has no new==current field check.
	if match, err := s.verifyPassword(newPlaintext, currentHash); err != nil {
		return errs.Internal(err)
	} else if match {
		return errs.Validation("new password must not match a recently used password")
	}

	entries, err := s.repo.ListPasswordHistory(ctx, userID, s.historySize)
	if err != nil {
		return errs.DatabaseError(err)
	}
	for _, e := range entries {
		if match, verr := s.verifyPassword(newPlaintext, e.PasswordHash); verr != nil {
			return errs.Internal(verr)
		} else if match {
			return errs.Validation("new password must not match a recently used password")
		}
	}
	return nil
}

// recordPasswordHistory archives the hash being retired by a password change
// and trims the user's history back to historySize. It is called inside the
// change transaction immediately after UpdatePassword has written the new
// hash, so the hash archived is exactly the one that just stopped being live.
// No-op when history tracking is disabled.
func (s *Service) recordPasswordHistory(ctx context.Context, userID uuid.UUID, retiredHash string, now time.Time) error {
	if s.historySize <= 0 {
		return nil
	}
	return s.repo.InsertPasswordHistory(ctx, userID, retiredHash, now, s.historySize)
}

// sessionExpiry computes a session's absolute expiry at creation. When an
// absolute session timeout is configured it governs the session — and, because
// the refresh chain inherits the session's expiry, the whole continuous session
// — binding it to a shorter cap than the refresh window if so desired. When
// absolute_timeout is unset it falls back to the refresh TTL, preserving the
// legacy behavior where the refresh window was the de-facto session cap.
func (s *Service) sessionExpiry(now time.Time) time.Time {
	if s.absoluteTimeout > 0 {
		return now.Add(s.absoluteTimeout)
	}
	return now.Add(s.refreshTTL)
}

// mintAccess resolves the user's current role/permission snapshot from the rbac
// enforcer (keyed by the user's own organization, which correctly resolves both
// tenant roles and the SUPER_ADMIN system role) and signs a short-lived access
// token carrying it. A fresh jti makes each token individually identifiable.
//
// The token's BranchID is the principal's *home* branch (single id, for
// backwards compat with every consumer that reads appctx.BranchID). The
// multi-branch subset is also encoded as a separate "branches" claim so a
// multi-branch principal (regional manager) carries the full set forward to
// every downstream request without a re-query on the hot path.
func (s *Service) mintAccess(cred *Credential, sessionID uuid.UUID, securityGeneration int64, branchIDs []uuid.UUID) (token string, expiresAt time.Time, err error) {
	acc := s.access.ResolveAccess(cred.OrganizationID.String(), cred.ID.String())

	var branch string
	if cred.BranchID != nil {
		branch = cred.BranchID.String()
	}
	var branchClaims []string
	if len(branchIDs) > 0 {
		branchClaims = make([]string, 0, len(branchIDs))
		for _, b := range branchIDs {
			branchClaims = append(branchClaims, b.String())
		}
	}
	return s.signer.Sign(Claims{
		Subject:            cred.ID.String(),
		JWTID:              uuid.NewString(),
		TokenType:          "access",
		AuthzVersion:       cred.AuthzVersion,
		SecurityGeneration: securityGeneration,
		OrgID:              cred.OrganizationID.String(),
		BranchID:           branch,
		BranchIDs:          branchClaims,
		SessionID:          sessionID.String(),
		RoleName:           acc.RoleName,
		Stage:              string(cred.Stage),
		Status:             string(cred.Status),
	})
}

// teardownFamily revokes every live token in a refresh-token family. When markReuse
// is set it also stamps the forensic reuse_detected_at timestamp and kills the
// owning session — the scorched-earth response to a stolen token. Callers invoke
// it inside the refresh transaction and then return nil so the teardown commits.
func (s *Service) teardownFamily(ctx context.Context, tok *RefreshToken, reason string, markReuse bool, now time.Time) error {
	if _, err := s.repo.RevokeRefreshFamily(ctx, tok.FamilyID, now); err != nil {
		return errs.DatabaseError(err)
	}
	if markReuse {
		if err := s.repo.StampFamilyReuseDetected(ctx, tok.FamilyID, now); err != nil {
			return errs.DatabaseError(err)
		}
		if _, err := s.repo.RevokeSession(ctx, tok.SessionID, tok.UserID, nil, reason, now); err != nil {
			return errs.DatabaseError(err)
		}
		if s.sessionCache != nil {
			s.sessionCache.InvalidateAll(ctx, tok.SessionID)
		}
	}
	return nil
}

// recordAttempt appends one row to the append-only login_attempts audit trail.
// Best-effort: a failed audit write is logged but never fails the login itself. A
// nil userID records an attempt against an unknown/typo'd email.
func (s *Service) recordAttempt(ctx context.Context, email string, ip, ua *string, userID *uuid.UUID, success bool, reason string) {
	att := &LoginAttempt{
		Email:     email,
		IP:        ip,
		UserAgent: ua,
		Success:   success,
		UserID:    userID,
	}
	if reason != "" {
		att.FailureReason = &reason
	}
	if err := s.repo.InsertLoginAttempt(ctx, att); err != nil {
		s.log.Warn("failed to record login attempt", zap.Error(err))
	}
}

// requestMeta lifts the best-effort client IP, user agent and device fingerprint
// off the request context as nil-if-absent pointers, matching the nullable session
// / login-attempt columns. UA parsing (browser/OS/device) and geo-IP lookup are
// deliberately out of scope, so those columns stay NULL.
func (s *Service) requestMeta(ctx context.Context) (ip, ua, fp *string) {
	return ptrIfNotEmpty(appctx.ClientIP(ctx)),
		ptrIfNotEmpty(appctx.UserAgent(ctx)),
		ptrIfNotEmpty(appctx.DeviceFP(ctx))
}

// ptrIfNotEmpty returns a pointer to s, or nil when s is empty, so an absent
// context value is stored as SQL NULL rather than an empty string.
func ptrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// statusError maps a non-login account status to the matching domain error. It is
// only reached after a correct password, so naming the status leaks nothing to an
// attacker who does not already hold the credentials.
func statusError(status enums.UserStatus) error {
	if status == enums.UserStatusSuspended {
		return errs.New(errs.CodeAccountSuspended, "account is suspended")
	}
	return errs.New(errs.CodeAccountInactive, "account is not active")
}

// ceilSeconds rounds a duration up to whole seconds for a Retry-After hint, so a
// sub-second remainder never rounds down to 0 (which a client would read as "retry
// now").
func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// TouchSession satisfies the middleware.SessionToucher port: it refreshes
// last_seen_at (and the observed ip) on a successful authenticated request
// so the idle-session middleware can reset the idle clock. idleTimeout > 0
// makes the underlying UPDATE include an idle guard so a session that has
// already idled out is not touched.
func (s *Service) TouchSession(ctx context.Context, sessionID uuid.UUID, ip *string, now time.Time, idleTimeout time.Duration) (bool, error) {
	return s.repo.TouchSession(ctx, sessionID, ip, now, idleTimeout)
}

// ---------------------------------------------------------------------------------
// Observability — nil-safe forwards to the shared Prometheus registry.
// ---------------------------------------------------------------------------------
//
// Every call site here is on the hot path; we keep these as one-line wrappers
// (no allocation) so a future metric addition is a single line in one place.

func (s *Service) observeLogin(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveLoginAttempt(outcome)
}
func (s *Service) observeRefresh(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveRefreshAttempt(outcome)
}
func (s *Service) observeTokenIssued(kind string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveTokenIssued(kind)
}
func (s *Service) observeTokenReuse() {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.RecordTokenReuse()
}
func (s *Service) observePasswordChange(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObservePasswordChange(outcome)
}
func (s *Service) observePasswordResetIssued(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObservePasswordResetIssued(outcome)
}
func (s *Service) observePasswordResetRedeemed(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObservePasswordResetRedeemed(outcome)
}
func (s *Service) observeAccountLockout(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveAccountLockout(outcome)
}

// verifyPassword checks plain against encoded using the rotation-aware
// path when one is wired (so a successful verify can also tell us
// whether the hash should be re-minted). The plain bool/error surface
// is preserved so call sites that do not care about the rotation signal
// (the unknown-email dummy path) keep working unchanged.
func (s *Service) verifyPassword(plain, encoded string) (bool, error) {
	if s.upgrader != nil {
		res, err := s.upgrader.VerifyUpgrade(plain, encoded)
		if err != nil {
			// ErrUnknownPepper and ErrInvalidPasswordHash are both
			// recoverable from a non-match perspective: they tell us
			// "this hash does not verify with the current key set",
			// which is the same answer the bare Verify would give
			// under a wrong password. Surface them as false/no-error
			// so the caller can keep its generic "invalid
			// credentials" branch simple.
			if errors.Is(err, crypto.ErrUnknownPepper) || errors.Is(err, crypto.ErrInvalidPasswordHash) {
				return false, nil
			}
			return false, err
		}
		return res.Match, nil
	}
	return s.hasher.Verify(plain, encoded)
}
