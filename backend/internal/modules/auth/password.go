package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	errs "backend/internal/errors"
	"backend/pkg/crypto"
)

// Credential-material helpers: opaque refresh-token minting/hashing and the
// anti-enumeration dummy hash. Password *hashing* itself lives in pkg/crypto
// (Argon2id), shared with the user module; this file only wraps the pieces the
// auth flows need on top of it.

// refreshTokenBytes is the entropy of an opaque refresh token: 32 bytes = 256
// bits, far beyond guessing range, which is why a single SHA-256 (not a slow
// KDF) is the correct at-rest hash for it — there is nothing to brute-force.
const refreshTokenBytes = 32

// generateRefreshToken returns a fresh, cryptographically random opaque token,
// base64url-encoded for safe transport in a JSON body or cookie. This raw value
// is handed to the client exactly once and never stored; only its hash is
// persisted (see hashToken). A failure of the OS CSPRNG is a hard internal error
// — we must never fall back to a predictable token.
func generateRefreshToken() (string, error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", errs.Internal(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken maps an opaque token to the value stored in refresh_tokens.token_hash
// (and password_resets.token_hash). SHA-256 hex is 64 chars, comfortably within
// the column's VARCHAR(128), and lets a lookup be a single indexed equality on
// the hash — the raw token is never at rest, so a database leak cannot be
// replayed as a valid refresh token.
func hashToken(raw string) string {
	return crypto.SHA256Hex(raw)
}

// newDummyHash produces a throwaway Argon2id hash at construction time, used to
// equalize login timing when the supplied email matches no user: the service
// runs a real Verify against this hash so a caller cannot distinguish "unknown
// email" from "wrong password" by response latency (a user-enumeration side
// channel). If hashing somehow fails we return a fixed, well-formed PHC string
// so Verify still does comparable work rather than erroring out early.
//
// The interface parameter accepts both the bare PasswordHasher and the
// peppered wrapper so the auth service can keep the surface uniform.
func newDummyHash(h crypto.PasswordHash) string {
	if h != nil {
		if encoded, err := h.Hash("mc-dummy-anti-enumeration-secret"); err == nil {
			return encoded
		}
	}
	// Fallback: a syntactically valid Argon2id PHC string (m=64MiB,t=3,p=2) with
	// zero salt/hash, built by encoding fixed-length byte slices so the base64 is
	// always well-formed and decodable. Verify then still spends the full KDF cost
	// recomputing before returning false, preserving constant-time behavior.
	salt := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	digest := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", salt, digest)
}
