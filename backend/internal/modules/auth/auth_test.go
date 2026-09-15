package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	errs "backend/internal/errors"
	"backend/internal/platform/config"
	"backend/pkg/crypto"
)

// These tests are white-box (package auth) and deliberately DB- and Redis-free:
// they cover the self-contained, security-critical primitives — the hand-rolled
// HS256 signer (round-trip, expiry, tamper, wrong-secret, and the alg-confusion
// pin), the brute-force lockout policy, opaque-token minting/hashing, the
// anti-enumeration dummy hash, and the refresh-cookie manager. The transactional
// service flows (login/refresh/logout/reset) require a live Postgres and are
// exercised by integration tests on the host, not here.

func init() { gin.SetMode(gin.TestMode) }

// -----------------------------------------------------------------------------
// JWT signer
// -----------------------------------------------------------------------------

func testSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(SignerConfig{
		Secret:    []byte("0123456789abcdef0123456789abcdef"), // 32 bytes
		Issuer:    "pharmaciano",
		Audience:  "pharmaciano-api",
		KeyID:     "test-kid",
		AccessTTL: 15 * time.Minute,
		ClockSkew: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func sampleClaims() Claims {
	return Claims{
		Subject:      "11111111-1111-1111-1111-111111111111",
		OrgID:        "22222222-2222-2222-2222-222222222222",
		SessionID:    "33333333-3333-3333-3333-333333333333",
		RoleName:     "PHARMACIST",
		Stage:        "active",
		Status:       "active",
		JWTID:        "44444444-4444-4444-4444-444444444444",
		TokenType:    "access",
		AuthzVersion: 1,
	}
}

func TestNewSigner_RejectsWeakConfig(t *testing.T) {
	if _, err := NewSigner(SignerConfig{Secret: []byte("too-short"), AccessTTL: time.Minute}); err == nil {
		t.Fatal("secret under 32 bytes must be rejected")
	}
	if _, err := NewSigner(SignerConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), AccessTTL: 0}); err == nil {
		t.Fatal("non-positive access TTL must be rejected")
	}
}

func TestSigner_SignParseRoundTrip(t *testing.T) {
	s := testSigner(t)
	in := sampleClaims()

	tok, exp, err := s.Sign(in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry %v is not in the future", exp)
	}

	got, err := s.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Subject != in.Subject || got.OrgID != in.OrgID || got.SessionID != in.SessionID {
		t.Fatalf("identity claims not preserved: %+v", got)
	}
	if got.RoleName != in.RoleName || got.Stage != in.Stage || got.Status != in.Status {
		t.Fatalf("role/stage/status not preserved: %+v", got)
	}
	if strings.Contains(tok, "perms") {
		t.Fatal("access token must not contain permission claims")
	}
	if len(tok) > 2048 {
		t.Fatalf("access token is %d bytes; maximum is 2048", len(tok))
	}
	// Sign stamps the registered claims authoritatively.
	if got.Issuer != "pharmaciano" || got.Audience != "pharmaciano-api" {
		t.Fatalf("issuer/audience not stamped: iss=%q aud=%q", got.Issuer, got.Audience)
	}
	if got.ExpiresAt == 0 || got.IssuedAt == 0 || got.NotBefore == 0 {
		t.Fatalf("temporal claims not stamped: %+v", got)
	}
}

func TestSigner_ParseRejectsExpired(t *testing.T) {
	s := testSigner(t)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	tok, _, err := s.Sign(sampleClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Still valid inside the skew window (exp = base+15m, skew = 30s).
	s.now = func() time.Time { return base.Add(15*time.Minute + 15*time.Second) }
	if _, err := s.Parse(tok); err != nil {
		t.Fatalf("token within clock-skew window must remain valid: %v", err)
	}

	// Past exp + skew → TOKEN_EXPIRED (distinct code so clients know to refresh).
	s.now = func() time.Time { return base.Add(15*time.Minute + 31*time.Second) }
	if code := errs.CodeOf(mustErr(t, s, tok)); code != errs.CodeTokenExpired {
		t.Fatalf("want CodeTokenExpired, got %v", code)
	}
}

func TestSigner_ParseRejectsWrongSecret(t *testing.T) {
	s := testSigner(t)
	tok, _, err := s.Sign(sampleClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	other, err := NewSigner(SignerConfig{
		Secret:    []byte("FEDCBA9876543210FEDCBA9876543210"),
		Issuer:    "pharmaciano",
		Audience:  "pharmaciano-api",
		KeyID:     "other-kid",
		AccessTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSigner(other): %v", err)
	}
	if code := errs.CodeOf(mustErr(t, other, tok)); code != errs.CodeTokenInvalid {
		t.Fatalf("token signed with a different secret must be TOKEN_INVALID, got %v", code)
	}
}

func TestSigner_ParseRejectsTamperedClaims(t *testing.T) {
	s := testSigner(t)
	tok, _, err := s.Sign(sampleClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Attacker edits the payload to escalate their role but cannot re-sign it.
	parts := strings.Split(tok, ".")
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	c.RoleName = constants.RoleSuperAdmin
	edited, _ := json.Marshal(c)
	forged := parts[0] + "." + b64.EncodeToString(edited) + "." + parts[2]

	if code := errs.CodeOf(mustErr(t, s, forged)); code != errs.CodeTokenInvalid {
		t.Fatalf("tampered claims must be TOKEN_INVALID, got %v", code)
	}
}

// TestSigner_ParseRejectsAlgConfusion proves the algorithm pin holds even when the
// signature itself is valid: the forged token is MAC'd with the real secret but
// labelled alg:"none". Verifying the signature first and *then* rejecting the
// algorithm is what closes the classic alg-confusion / "none" downgrade.
func TestSigner_ParseRejectsAlgConfusion(t *testing.T) {
	s := testSigner(t)

	hdr, _ := json.Marshal(jwtHeader{Alg: "none", Typ: "JWT"})
	c := sampleClaims()
	now := time.Now()
	c.Issuer, c.Audience = "pharmaciano", "pharmaciano-api"
	c.IssuedAt, c.NotBefore = now.Unix(), now.Unix()
	c.ExpiresAt = now.Add(time.Hour).Unix()
	payload, _ := json.Marshal(c)

	signingInput := b64.EncodeToString(hdr) + "." + b64.EncodeToString(payload)
	forged := signingInput + "." + b64.EncodeToString(s.mac(signingInput)) // valid MAC

	if code := errs.CodeOf(mustErr(t, s, forged)); code != errs.CodeTokenInvalid {
		t.Fatalf("alg=none must be rejected even with a valid MAC, got %v", code)
	}
}

func TestSigner_ParseRejectsMalformed(t *testing.T) {
	s := testSigner(t)
	for _, tok := range []string{"", "a.b", "a.b.c.d", "onlyonesegment", "..", "a..c", "a.b."} {
		if _, err := s.Parse(tok); err == nil {
			t.Fatalf("malformed token %q must be rejected", tok)
		}
	}
}

// mustErr parses a token expected to fail and returns the (non-nil) error.
func mustErr(t *testing.T, s *Signer, tok string) error {
	t.Helper()
	_, err := s.Parse(tok)
	if err == nil {
		t.Fatalf("expected Parse(%q) to fail", tok)
	}
	return err
}

// -----------------------------------------------------------------------------
// Lockout policy
// -----------------------------------------------------------------------------

func TestLockoutPolicy_Defaults(t *testing.T) {
	p := DefaultLockoutPolicy()
	if p.Threshold != constants.MaxLoginAttempts {
		t.Fatalf("threshold = %d, want %d", p.Threshold, constants.MaxLoginAttempts)
	}
	if p.Lockout != 15*time.Minute {
		t.Fatalf("lockout = %v, want 15m", p.Lockout)
	}
}

func TestLockoutPolicy_NormalizedFillsDefaults(t *testing.T) {
	got := LockoutPolicy{}.normalized()
	if got.Threshold != constants.MaxLoginAttempts || got.Lockout != 15*time.Minute {
		t.Fatalf("zero policy must normalise to defaults, got %+v", got)
	}
	// A partially set policy keeps its explicit field and defaults only the rest.
	got = LockoutPolicy{Threshold: 3}.normalized()
	if got.Threshold != 3 || got.Lockout != 15*time.Minute {
		t.Fatalf("partial policy normalised wrong: %+v", got)
	}
}

func TestLockoutPolicy_RetryAfter(t *testing.T) {
	p := DefaultLockoutPolicy()
	now := time.Unix(1_700_000_000, 0)

	if d := p.RetryAfter(nil, now); d != 0 {
		t.Fatalf("nil lockedUntil must be 0, got %v", d)
	}
	past := now.Add(-time.Minute)
	if d := p.RetryAfter(&past, now); d != 0 {
		t.Fatalf("expired lock must be 0, got %v", d)
	}
	future := now.Add(5 * time.Minute)
	if d := p.RetryAfter(&future, now); d != 5*time.Minute {
		t.Fatalf("future lock must report remaining, got %v", d)
	}
}

func TestLockoutPolicy_NextLock(t *testing.T) {
	p := DefaultLockoutPolicy()
	now := time.Unix(1_700_000_000, 0)

	if got := p.NextLock(constants.MaxLoginAttempts-1, now); got != nil {
		t.Fatalf("below threshold must not lock, got %v", got)
	}
	got := p.NextLock(constants.MaxLoginAttempts, now)
	if got == nil {
		t.Fatal("reaching the threshold must lock")
	}
	if !got.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("lock-until = %v, want %v", got, now.Add(15*time.Minute))
	}
}

// -----------------------------------------------------------------------------
// Opaque token minting / hashing
// -----------------------------------------------------------------------------

func TestGenerateRefreshToken(t *testing.T) {
	a, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("generateRefreshToken: %v", err)
	}
	b, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("generateRefreshToken: %v", err)
	}
	if a == "" || a == b {
		t.Fatalf("tokens must be non-empty and unique (a=%q b=%q)", a, b)
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		t.Fatalf("token must be base64url: %v", err)
	}
	if len(raw) != refreshTokenBytes {
		t.Fatalf("want %d bytes of entropy, got %d", refreshTokenBytes, len(raw))
	}
}

func TestHashToken(t *testing.T) {
	h := hashToken("tok-abc")
	if h != hashToken("tok-abc") {
		t.Fatal("hash must be deterministic")
	}
	if h == hashToken("tok-xyz") {
		t.Fatal("distinct inputs must produce distinct hashes")
	}
	if len(h) != 64 {
		t.Fatalf("SHA-256 hex must be 64 chars, got %d", len(h))
	}
}

func TestNewDummyHash(t *testing.T) {
	h := crypto.NewPasswordHasher(crypto.DefaultArgon2Params())

	dummy := newDummyHash(h)
	if !strings.HasPrefix(dummy, "$argon2id$") {
		t.Fatalf("dummy must be an argon2id PHC string, got %q", dummy)
	}
	ok, err := h.Verify("anything", dummy)
	if err != nil {
		t.Fatalf("verifying against the dummy must not error: %v", err)
	}
	if ok {
		t.Fatal("the dummy hash must never verify true")
	}

	// The nil-hasher fallback must still be a well-formed, unverifiable PHC string.
	fb := newDummyHash(nil)
	if !strings.HasPrefix(fb, "$argon2id$") {
		t.Fatalf("fallback must be an argon2id PHC string, got %q", fb)
	}
	if ok, _ := h.Verify("anything", fb); ok {
		t.Fatal("fallback dummy hash must never verify true")
	}
}

// -----------------------------------------------------------------------------
// Password-history reuse rejection
// -----------------------------------------------------------------------------

// TestEnforcePasswordHistory exercises the two branches of enforcePasswordHistory
// that are reachable without a live Postgres: the disabled-history no-op and the
// current-hash rejection. Both terminate before the repository is consulted, so a
// nil repo proves the repo is never touched on these paths. (The history-entry
// branch that reads password_history is exercised by the host integration suite.)
func TestEnforcePasswordHistory(t *testing.T) {
	h := crypto.NewPasswordHasher(crypto.DefaultArgon2Params())
	cur, err := h.Hash("current-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	subtests := []struct {
		name       string
		historySize int
		repo       *Repository // nil ⇒ any repo touch would nil-deref and panic
		newPass    string
		wantCode   errs.Code
	}{
		{
			name:        "disabled history is a no-op",
			historySize: 0, // enforcement off ⇒ returns nil before touching repo
		},
		{
			name:        "reuse of current password rejected",
			historySize: 5,
			newPass:     "current-pass", // matches the live hash ⇒ VALIDATION_ERROR (no repo read)
			wantCode:   errs.CodeValidationError,
		},
	}

	for _, tt := range subtests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{historySize: tt.historySize, hasher: h, repo: tt.repo}
			got := s.enforcePasswordHistory(context.Background(), uuid.New(), cur, tt.newPass)
			if tt.wantCode == "" {
				if got != nil {
					t.Fatalf("expected no error, got %v", got)
				}
				return
			}
			ae := errs.As(got)
			if ae == nil || ae.Code != tt.wantCode {
				t.Fatalf("expected %s, got %v", tt.wantCode, got)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Refresh-cookie manager
// -----------------------------------------------------------------------------

func newTestCtx(t *testing.T, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

func findCookie(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCookieManager_WriteSetsSecureAttributes(t *testing.T) {
	cm := cookieManager{
		name:     "mc_refresh",
		path:     "/",
		secure:   true,
		httpOnly: true,
		sameSite: http.SameSiteStrictMode,
		maxAge:   3600,
	}
	c, w := newTestCtx(t, http.MethodPost, "/auth/login")
	cm.write(c, "rawtoken123", time.Time{})

	ck := findCookie(w.Result().Cookies(), "mc_refresh")
	if ck == nil {
		t.Fatal("refresh cookie was not set")
	}
	if ck.Value != "rawtoken123" {
		t.Fatalf("value = %q", ck.Value)
	}
	if !ck.HttpOnly || !ck.Secure {
		t.Fatalf("cookie must be HttpOnly and Secure: %+v", ck)
	}
	if ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("SameSite = %v", ck.SameSite)
	}
	if ck.MaxAge != 3600 || ck.Path != "/" {
		t.Fatalf("MaxAge/Path wrong: %+v", ck)
	}
}

func TestCookieManager_ClearExpiresCookie(t *testing.T) {
	cm := cookieManager{name: "mc_refresh", path: "/"}
	c, w := newTestCtx(t, http.MethodPost, "/auth/logout")
	cm.clear(c)

	ck := findCookie(w.Result().Cookies(), "mc_refresh")
	if ck == nil {
		t.Fatal("clear must still emit a Set-Cookie to delete it")
	}
	if ck.MaxAge >= 0 {
		t.Fatalf("clear must set MaxAge < 0, got %d", ck.MaxAge)
	}
	if ck.Value != "" {
		t.Fatalf("clear must blank the value, got %q", ck.Value)
	}
}

func TestCookieManager_Read(t *testing.T) {
	cm := cookieManager{name: "mc_refresh"}

	c, _ := newTestCtx(t, http.MethodPost, "/auth/refresh")
	c.Request.AddCookie(&http.Cookie{Name: "mc_refresh", Value: "cookie-tok"})
	if got := cm.read(c); got != "cookie-tok" {
		t.Fatalf("read = %q, want cookie-tok", got)
	}

	c2, _ := newTestCtx(t, http.MethodPost, "/auth/refresh")
	if got := cm.read(c2); got != "" {
		t.Fatalf("absent cookie must read empty, got %q", got)
	}
}

// -----------------------------------------------------------------------------
// Cookie config mapping
// -----------------------------------------------------------------------------

func TestParseSameSite(t *testing.T) {
	cases := map[string]http.SameSite{
		"strict": http.SameSiteStrictMode,
		"Strict": http.SameSiteStrictMode,
		"lax":    http.SameSiteLaxMode,
		"none":   http.SameSiteNoneMode,
		"":       http.SameSiteLaxMode, // safe default
		"bogus":  http.SameSiteLaxMode, // safe default
	}
	for in, want := range cases {
		if got := parseSameSite(in); got != want {
			t.Fatalf("parseSameSite(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewCookieManager(t *testing.T) {
	// Unset config → safe defaults.
	cm := newCookieManager(config.CookieConfig{}, 0)
	if cm.name != constants.CookieRefreshToken {
		t.Fatalf("name default = %q, want %q", cm.name, constants.CookieRefreshToken)
	}
	if cm.path != "/" {
		t.Fatalf("path default = %q", cm.path)
	}
	if cm.maxAge != int(defaultRefreshTTL/time.Second) {
		t.Fatalf("maxAge default = %d, want %d", cm.maxAge, int(defaultRefreshTTL/time.Second))
	}

	// Explicit config is honoured, and maxAge tracks the refresh TTL.
	cm = newCookieManager(config.CookieConfig{
		Name:     "x",
		Path:     "/auth",
		Domain:   "example.test",
		Secure:   true,
		HTTPOnly: true,
		SameSite: "strict",
	}, 2*time.Hour)
	if cm.name != "x" || cm.path != "/auth" || cm.domain != "example.test" {
		t.Fatalf("explicit fields not honoured: %+v", cm)
	}
	if !cm.secure || !cm.httpOnly || cm.sameSite != http.SameSiteStrictMode {
		t.Fatalf("flags not honoured: %+v", cm)
	}
	if cm.maxAge != 7200 {
		t.Fatalf("maxAge = %d, want 7200", cm.maxAge)
	}
}

// -----------------------------------------------------------------------------
// Login challenge surfacing (X-MFA-Challenge / X-Password-Change-Token)
// -----------------------------------------------------------------------------

func TestSurfaceLoginChallenge_Headers(t *testing.T) {
	h := &Handler{svc: nil, log: zap.NewNop()}

	// PASSWORD_CHANGE_REQUIRED must surface the change token header.
	{
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		err := errs.New(errs.CodePasswordChangeRequired, "change required").WithMeta("change_token", "tok-123")
		h.surfaceLoginChallenge(c, err)
		if got := c.Writer.Header().Get(constants.HeaderPasswordChangeToken); got != "tok-123" {
			t.Fatalf("change token header = %q, want tok-123", got)
		}
		if got := c.Writer.Header().Get(constants.HeaderMFAChallenge); got != "" {
			t.Fatalf("MFA header must stay empty on a password-change error, got %q", got)
		}
	}

	// MFA_REQUIRED must surface the MFA challenge header, unchanged behaviour.
	{
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		err := errs.New(errs.CodeMFARequired, "mfa required").WithMeta("mfa_challenge", "mfa-xyz")
		h.surfaceLoginChallenge(c, err)
		if got := c.Writer.Header().Get(constants.HeaderMFAChallenge); got != "mfa-xyz" {
			t.Fatalf("MFA header = %q, want mfa-xyz", got)
		}
		if got := c.Writer.Header().Get(constants.HeaderPasswordChangeToken); got != "" {
			t.Fatalf("change header must stay empty on MFA_REQUIRED, got %q", got)
		}
	}

	// An unrelated error surfaces neither header.
	{
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		h.surfaceLoginChallenge(c, errs.InvalidCredentials())
		if c.Writer.Header().Get(constants.HeaderPasswordChangeToken) != "" || c.Writer.Header().Get(constants.HeaderMFAChallenge) != "" {
			t.Fatalf("unexpected challenge headers on a generic credential error")
		}
	}
}
