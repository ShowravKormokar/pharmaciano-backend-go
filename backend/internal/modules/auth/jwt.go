package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	errs "backend/internal/errors"
)

// Hand-rolled HS256 JWT for the access token.
//
// The project deliberately does NOT depend on a third-party JWT library
// (golang-jwt et al.): the standard library gives us everything a symmetric,
// single-issuer token needs — crypto/hmac, crypto/sha256, encoding/base64 and
// encoding/json — and hand-rolling keeps the trusted surface tiny and audited.
//
// # Token shape
//
// A compact JWS: base64url(header) "." base64url(payload) "." base64url(sig),
// where sig = HMAC-SHA256(secret, signingInput) and signingInput is the first
// two segments joined by ".". Segments use base64url *without* padding
// (RFC 7515 §2), so a token is URL- and header-safe.
//
// # Claims
//
// The payload is the compact snapshot the middleware needs to build an
// appctx.Principal with zero database work on the hot path: the standard
// registered claims (iss/sub/aud/exp/nbf/iat/jti) plus the org, branch, session,
// role, permission set, stage and status. Because the enforcer's ResolveAccess
// derives the permissions from the very same policy snapshot Enforce reads, the
// embedded fast-path permissions can never grant more than the enforcer itself
// would (see rbac.Access). Session liveness is re-checked statefully on every
// request, so a token whose snapshot has gone stale (role revoked, user locked)
// is still stopped at the session gate — the JWT is an optimization, not the
// sole authority.
type Claims struct {
	// Registered claims (RFC 7519).
	Issuer       string `json:"iss,omitempty"`
	Subject      string `json:"sub,omitempty"` // user id
	Audience     string `json:"aud,omitempty"`
	ExpiresAt    int64  `json:"exp,omitempty"` // unix seconds
	NotBefore    int64  `json:"nbf,omitempty"` // unix seconds
	IssuedAt     int64  `json:"iat,omitempty"` // unix seconds
	JWTID        string `json:"jti,omitempty"` // unique token id
	TokenType    string `json:"typ,omitempty"` // access; prevents refresh/reset confusion
	AuthzVersion       int64  `json:"av,omitempty"`
	SecurityGeneration int64  `json:"sg,omitempty"` // session's security_generation; used by Authenticate cache path

	// Private claims — the Principal snapshot.
	OrgID     string   `json:"org,omitempty"`
	BranchID  string   `json:"branch,omitempty"`    // empty = no single home branch (org-wide user)
	BranchIDs []string `json:"branches,omitempty"`  // subset the principal may act on; empty = no branch assignment
	SessionID string   `json:"sid,omitempty"`
	RoleName  string   `json:"role,omitempty"`
	Stage     string   `json:"stage,omitempty"`
	Status    string   `json:"status,omitempty"`
}

// algHS256 is the only algorithm this signer accepts. Pinning it (and rejecting
// anything else on parse) closes the classic JWT "alg confusion" hole where an
// attacker swaps the header to "none" or an asymmetric algorithm.
const algHS256 = "HS256"

const maxAccessTokenBytes = 4096

// jwtHeader is the fixed JOSE header. Only HS256 is ever emitted or accepted.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid,omitempty"`
}

// b64 is the pad/less base64url alphabet mandated for JWS compact serialization.
var b64 = base64.RawURLEncoding

// SignerConfig carries the knobs NewSigner needs, mapped from config.JWTConfig by
// the composition root. Keeping it a plain struct (rather than importing the
// config package here) keeps jwt.go independently unit-testable.
type SignerConfig struct {
	Secret          []byte
	PreviousSecrets map[string][]byte
	Issuer          string
	Audience        string
	KeyID           string
	AccessTTL       time.Duration
	ClockSkew       time.Duration
}

// Signer mints and verifies access tokens. It is immutable after construction
// and safe for concurrent use.
type Signer struct {
	secret           []byte
	verificationKeys map[string][]byte
	issuer           string
	audience         string
	kid              string
	accessTTL        time.Duration
	skew             time.Duration
	now              func() time.Time
}

// NewSigner validates the config and builds a Signer. A short or empty secret,
// or a non-positive TTL, is a startup misconfiguration and returns an error so
// main.go fails fast rather than issuing forgeable or instantly-expired tokens.
// (config validation already enforces a >=32-byte HS256 secret; this is defence
// in depth for callers that construct a Signer directly, e.g. tests.)
func NewSigner(cfg SignerConfig) (*Signer, error) {
	if len(cfg.Secret) < 32 {
		return nil, errs.Internal(nil).WithMeta("reason", "jwt secret must be at least 32 bytes")
	}
	if strings.TrimSpace(cfg.KeyID) == "" {
		return nil, errs.Internal(nil).WithMeta("reason", "jwt key id is required")
	}
	if cfg.AccessTTL <= 0 {
		return nil, errs.Internal(nil).WithMeta("reason", "jwt access ttl must be positive")
	}
	skew := cfg.ClockSkew
	if skew < 0 {
		skew = 0
	}
	// Copy the secret so a later mutation of the caller's slice can't change the
	// signing key underneath us.
	secret := make([]byte, len(cfg.Secret))
	copy(secret, cfg.Secret)
	keys := map[string][]byte{cfg.KeyID: secret}
	for kid, key := range cfg.PreviousSecrets {
		if strings.TrimSpace(kid) == "" || kid == cfg.KeyID || len(key) < 32 {
			return nil, errs.Internal(nil).WithMeta("reason", "invalid previous jwt signing key")
		}
		copyKey := make([]byte, len(key))
		copy(copyKey, key)
		keys[kid] = copyKey
	}

	return &Signer{
		secret:           secret,
		verificationKeys: keys,
		issuer:           cfg.Issuer,
		audience:         cfg.Audience,
		kid:              cfg.KeyID,
		accessTTL:        cfg.AccessTTL,
		skew:             skew,
		now:              time.Now,
	}, nil
}

// AccessTTL is the configured access-token lifetime, surfaced so the service can
// report expires_in without re-reading config.
func (s *Signer) AccessTTL() time.Duration { return s.accessTTL }

// Sign stamps the time-based and issuer claims authoritatively (overwriting any
// caller-supplied iss/aud/iat/nbf/exp) onto c, then returns the signed compact
// token and the absolute expiry. The caller supplies only identity claims (sub,
// org, sid, role, ...). Stamping the temporal claims here keeps the token's
// notion of time in one trusted place.
func (s *Signer) Sign(c Claims) (token string, expiresAt time.Time, err error) {
	now := s.now()
	expiresAt = now.Add(s.accessTTL)

	c.Issuer = s.issuer
	c.Audience = s.audience
	c.IssuedAt = now.Unix()
	c.NotBefore = now.Unix()
	c.ExpiresAt = expiresAt.Unix()

	headerJSON, err := json.Marshal(jwtHeader{Alg: algHS256, Typ: "JWT", Kid: s.kid})
	if err != nil {
		return "", time.Time{}, errs.Internal(err)
	}
	payloadJSON, err := json.Marshal(c)
	if err != nil {
		return "", time.Time{}, errs.Internal(err)
	}

	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)
	sig := s.mac(signingInput)
	return signingInput + "." + b64.EncodeToString(sig), expiresAt, nil
}

// Parse verifies a compact token's signature, algorithm, issuer, audience and
// time window, returning the decoded claims. Every failure is a typed AppError:
// an expired token maps to TOKEN_EXPIRED (so the middleware can emit the right
// WWW-Authenticate challenge and the client knows to refresh), and every other
// defect — bad structure, wrong algorithm, bad signature, wrong issuer/audience,
// not-yet-valid — maps to the deliberately indistinct TOKEN_INVALID so a probing
// client learns nothing about *why* a forged token was rejected.
func (s *Signer) Parse(token string) (*Claims, error) {
	if len(token) > maxAccessTokenBytes {
		return nil, errs.New(errs.CodeTokenInvalid, "token exceeds maximum length")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token")
	}

	// 1) Verify the signature over the exact received signing input before we
	//    trust any byte of the header or payload.
	signingInput := parts[0] + "." + parts[1]
	gotSig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token signature")
	}
	// Decode and validate the protected header before selecting a key. Its kid is
	// not trusted until the signature below verifies with that configured key.
	headerJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token header")
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token header")
	}
	if hdr.Alg != algHS256 || hdr.Typ != "JWT" || hdr.Kid == "" {
		return nil, errs.New(errs.CodeTokenInvalid, "unexpected token header")
	}
	key, ok := s.verificationKeys[hdr.Kid]
	if !ok || !hmac.Equal(gotSig, mac(key, signingInput)) {
		return nil, errs.New(errs.CodeTokenInvalid, "token signature mismatch")
	}

	// 3) Payload.
	payloadJSON, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token payload")
	}
	var c Claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return nil, errs.New(errs.CodeTokenInvalid, "malformed token payload")
	}

	// 4) Registered-claim checks, with clock skew tolerance.
	now := s.now()
	if s.issuer != "" && c.Issuer != s.issuer {
		return nil, errs.New(errs.CodeTokenInvalid, "token issuer mismatch")
	}
	if s.audience != "" && c.Audience != s.audience {
		return nil, errs.New(errs.CodeTokenInvalid, "token audience mismatch")
	}
	if c.ExpiresAt == 0 || now.After(time.Unix(c.ExpiresAt, 0).Add(s.skew)) {
		return nil, errs.New(errs.CodeTokenExpired, "token expired")
	}
	if c.NotBefore != 0 && now.Add(s.skew).Before(time.Unix(c.NotBefore, 0)) {
		return nil, errs.New(errs.CodeTokenInvalid, "token not yet valid")
	}
	if c.Subject == "" || c.JWTID == "" || c.TokenType != "access" || c.AuthzVersion < 1 {
		return nil, errs.New(errs.CodeTokenInvalid, "required token claims missing or invalid")
	}
	return &c, nil
}

// mac computes HMAC-SHA256(secret, signingInput).
func (s *Signer) mac(signingInput string) []byte {
	return mac(s.secret, signingInput)
}

func mac(key []byte, signingInput string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(signingInput))
	return h.Sum(nil)
}
