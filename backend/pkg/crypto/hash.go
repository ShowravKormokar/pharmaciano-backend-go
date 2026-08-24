package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// SHA256Hex is the canonical fingerprint helper (session tokens, refresh
// tokens, idempotency keys). Never use for passwords — those use Argon2id.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA256Base64 is the same as SHA256Hex but base64-URL encoded (shorter).
func SHA256Base64(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// HMACSHA256Hex signs `msg` with `key`. Used for cursor integrity, webhook
// signatures, and cookie tamper-checks.
// compromise the other.
func HMACSHA256Hex(key, msg []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// HMACVerify compares two HMAC hex strings in constant time.
func HMACVerify(key, msg []byte, expectedHex string) bool {
	got, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return subtle.ConstantTimeCompare(m.Sum(nil), got) == 1
}

// SignToken packages payload into a single opaque, URL-safe, tamper-evident
// string: base64url(payload) + "." + HMAC-SHA256 of that encoded form.
func SignToken(key, payload []byte) string {
	enc := base64.RawURLEncoding.EncodeToString(payload)
	tag := HMACSHA256Hex(key, []byte(enc))
	return enc + "." + tag
}

// VerifyToken reverses SignToken. ok is false for any malformed OR
// tampered token — the two cases are deliberately indistinguishable to the
// caller, so a bad cursor never becomes an oracle for guessing valid ones.
func VerifyToken(key []byte, token string) (payload []byte, ok bool) {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return nil, false
	}
	enc, tag := token[:i], token[i+1:]
	if !HMACVerify(key, []byte(enc), tag) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, false
	}
	return raw, true
}
