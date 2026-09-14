package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"strings"
	"time"
)

// TOTP provides the standard RFC 6238 time-based one-time password used by
// common authenticator apps (Google Authenticator, Authy, 1Password, ...).
// This package only derives and compares codes; the caller (the auth module)
// owns the shared secret's encrypted-at-rest storage and the replay counter.
//
// The defaults chosen here are the ones every authenticator app and the RFC
// recommend: a 30-second period, 6 digits, HMAC-SHA1, and a 160-bit secret.
const (
	totpPeriod    = 30 * time.Second
	totpDigits    = 6
	totpModulo    = 1_000_000 // 10^digits, for the final mod
	totpSecretLen = 20        // 160-bit secret, the RFC-recommended length
)

// base32NoPad is the RFC 4648 base32 alphabet without padding, which is what
// authenticator apps expect when a user types or scans an OTP secret.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh random shared secret, base32-encoded
// (RFC 4648, no padding). n is the secret's byte length; n <= 0 picks the
// RFC-recommended 160-bit default.
func GenerateTOTPSecret(n int) (string, error) {
	if n <= 0 {
		n = totpSecretLen
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(buf), nil
}

// ProvisioningURI builds the otpauth://totp/ URI a frontend renders as a QR
// code and an authenticator app scans. issuer and account are URL-escaped per
// the spec; account is conventionally the user's email.
func ProvisioningURI(secret, issuer, account string) string {
	label := account
	if issuer != "" {
		label = issuer + ":" + account
	}
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(label) + "?" + q.Encode()
}

// TOTPValidate reports whether code is the current (or a nearby) verification
// code for secret at time now, using constant-time comparison so timing cannot
// distinguish a near-miss from a far-miss. window is the number of 30-second
// steps of slack on each side (±window) to absorb clock drift and the user
// submitting a code just as a window turns over. On success it returns the
// matched counter (whole 30-second steps since the Unix epoch) so the caller
// can persist it as the replay floor: a code at or below an already-seen
// counter must be rejected as a replay.
func TOTPValidate(secret, code string, window int, now time.Time) (counter int64, ok bool) {
	if window < 0 {
		window = 0
	}
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return 0, false
	}
	want := sanitizeCode(code)
	if len(key) == 0 || want == "" {
		return 0, false
	}
	current := now.Unix() / int64(totpPeriod/time.Second)
	for step := int64(-window); step <= int64(window); step++ {
		cand := current + step
		if hmacCompareEquivalent(totpCode(key, uint64(cand)), want) {
			return cand, true
		}
	}
	return 0, false
}

// totpCode derives the n-digit code for a given counter using RFC 4226 HOTP
// dynamic truncation over HMAC-SHA1.
func totpCode(key []byte, counter uint64) [totpDigits]byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	// Dynamic truncation: the low 4 bits of the last byte index a 4-byte
	// window that is masked to 31 bits and reduced modulo 10^digits.
	off := sum[len(sum)-1] & 0x0f
	bin := uint32(sum[off])&0x7f<<24 |
		uint32(sum[off+1])<<16 |
		uint32(sum[off+2])<<8 |
		uint32(sum[off+3])
	val := bin % totpModulo

	var out [totpDigits]byte
	for i := totpDigits - 1; i >= 0; i-- {
		out[i] = byte('0' + val%10)
		val /= 10
	}
	return out
}

// sanitizeCode strips any non-digit character (separators, spaces) that a user
// might type, so "123 456" and "123456" compare equal. An empty result means
// the code was absent or malformed.
func sanitizeCode(code string) string {
	var sb strings.Builder
	sb.Grow(len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c >= '0' && c <= '9' {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// hmacCompareEquivalent compares a derived code with a submitted one in
// constant time (same pattern as hmac.Equal) so the response time does not
// reveal how many header digits matched.
func hmacCompareEquivalent(a [totpDigits]byte, b string) bool {
	if len(b) != len(a) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ---------------------------------------------------------------------------
// Recovery codes
// ---------------------------------------------------------------------------

// recoveryAlphabet is exactly 32 unambiguous characters (no 0/O or 1/I/L).
// Its size is a power of two, so a uniform byte mapped with a simple modulus
// stays uniform.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateRecoveryCodes returns size cryptographically random recovery codes.
// Each is 10 characters in two 5-character groups separated by a hyphen (e.g.
// "GX7QM-K2NP4"). They are shown once at MFA setup; each may redeem a single
// login when the authenticator is unavailable, and must be stored only as a
// hash (the auth module hashes them like refresh tokens).
func GenerateRecoveryCodes(size int) ([]string, error) {
	if size <= 0 {
		size = 10
	}
	codes := make([]string, 0, size)
	seen := make(map[string]struct{}, size)
	for len(codes) < size {
		code, err := randomRecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

func randomRecoveryCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.Grow(11) // 10 chars + 1 hyphen
	for i := 0; i < len(buf); i++ {
		if i == 5 {
			sb.WriteByte('-')
		}
		// len(recoveryAlphabet) == 32 divides 256, so this mapping is uniform.
		sb.WriteByte(recoveryAlphabet[int(buf[i])%len(recoveryAlphabet)])
	}
	return sb.String(), nil
}

// NormalizeRecoveryCode makes a user-entered recovery code comparable to a
// stored hash: uppercased with separators stripped, so "gx7qm-k2np4" and
// "GX7QMK2NP4" both normalize to the same canonical form for hashing.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	var sb strings.Builder
	sb.Grow(len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c == '-' {
			continue
		}
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}