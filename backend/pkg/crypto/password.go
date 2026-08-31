package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing — Argon2id, the memory-hard KDF recommended by OWASP for
// password storage. This is the single reusable primitive shared by the auth
// module (verify on login, hash on reset) and the user module (hash on create),
// which is why it lives in pkg/crypto rather than in either module: putting it
// here breaks what would otherwise be an import cycle between auth and user.
//
// The encoded output is the standard PHC string
//
//	$argon2id$v=19$m=<memKiB>,t=<time>,p=<par>$<b64 salt>$<b64 hash>
//
// byte-for-byte compatible with the hash produced by cmd/seed (which seeds the
// SUPER_ADMIN credential), so a seeded account verifies against this hasher
// without a re-hash. Encoding is base64 *raw std* (no padding), matching the
// seed; do not switch to URL encoding or the seeded hash stops verifying.

// argon2idVersion is the algorithm version embedded in and required by the PHC
// string. It mirrors golang.org/x/crypto/argon2.Version (0x13 == 19).
const argon2idVersion = argon2.Version

var (
	// ErrInvalidPasswordHash means the stored value is not a well-formed Argon2id
	// PHC string. Callers treat it like a failed verification (never as "match").
	ErrInvalidPasswordHash = errors.New("crypto: malformed argon2id password hash")
	// ErrIncompatibleVersion means the hash was produced by a different Argon2
	// version than this build understands.
	ErrIncompatibleVersion = errors.New("crypto: incompatible argon2 version")
	// ErrEmptyPassword guards against hashing an empty secret, which is almost
	// always a caller bug (an unset field reaching the hasher).
	ErrEmptyPassword = errors.New("crypto: refusing to hash an empty password")
)

// Argon2Params are the cost parameters for the Argon2id hasher. It deliberately
// mirrors config.PasswordConfig.Argon2 by shape, but is redeclared here so
// pkg/crypto never imports the config package (which sits above it in the
// dependency graph). The composition root maps one onto the other.
type Argon2Params struct {
	MemoryKB    uint32 // memory cost in KiB (e.g. 65536 == 64 MiB)
	Time        uint32 // number of passes over memory
	Parallelism uint8  // number of lanes / threads
	KeyLength   uint32 // length of the derived key in bytes
	SaltLength  uint32 // length of the random salt in bytes
}

// DefaultArgon2Params returns the baseline cost used by cmd/seed and the
// config.yaml defaults (64 MiB, t=3, p=2, 32-byte key, 16-byte salt). Any zero
// field passed to NewPasswordHasher falls back to the matching value here, so a
// partial config can never yield a degenerate (weak or panicking) hasher.
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{
		MemoryKB:    64 * 1024,
		Time:        3,
		Parallelism: 2,
		KeyLength:   32,
		SaltLength:  16,
	}
}

// PasswordHasher hashes and verifies passwords with a fixed cost profile. It is
// immutable after construction and therefore safe for concurrent use.
type PasswordHasher struct {
	p Argon2Params
}

// NewPasswordHasher builds a hasher, substituting DefaultArgon2Params for any
// zero-valued field so a misconfiguration degrades to safe defaults rather than
// a panic inside argon2.IDKey (which requires non-zero memory/time/threads).
func NewPasswordHasher(p Argon2Params) *PasswordHasher {
	d := DefaultArgon2Params()
	if p.MemoryKB == 0 {
		p.MemoryKB = d.MemoryKB
	}
	if p.Time == 0 {
		p.Time = d.Time
	}
	if p.Parallelism == 0 {
		p.Parallelism = d.Parallelism
	}
	if p.KeyLength == 0 {
		p.KeyLength = d.KeyLength
	}
	if p.SaltLength == 0 {
		p.SaltLength = d.SaltLength
	}
	return &PasswordHasher{p: p}
}

// Params returns the hasher's active cost profile (useful in tests and for
// building a matching dummy hash for anti-enumeration timing).
func (h *PasswordHasher) Params() Argon2Params { return h.p }

// Hash derives an Argon2id PHC string from plain using a fresh random salt.
func (h *PasswordHasher) Hash(plain string) (string, error) {
	if plain == "" {
		return "", ErrEmptyPassword
	}
	salt := make([]byte, h.p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, h.p.Time, h.p.MemoryKB, h.p.Parallelism, h.p.KeyLength)
	return encodeArgon2(h.p, salt, key), nil
}

// Verify reports whether plain matches the encoded PHC hash. The cost parameters
// and salt are read *from the encoded hash*, not from this hasher, so a hash
// created with an older/different profile still verifies correctly (essential
// for zero-downtime parameter upgrades and for verifying the seeded credential).
// The comparison is constant-time. A malformed hash returns (false, error) and
// must never be treated as a match.
func (h *PasswordHasher) Verify(plain, encoded string) (bool, error) {
	p, salt, want, err := decodeArgon2(encoded)
	if err != nil {
		return false, err
	}
	// Recompute with the *stored* key length so the output lines up with want.
	got := argon2.IDKey([]byte(plain), salt, p.Time, p.MemoryKB, p.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether encoded was produced with a weaker/different cost
// profile than this hasher's current one, so a caller can transparently upgrade
// the stored hash on the next successful login. A malformed hash needs a rehash.
func (h *PasswordHasher) NeedsRehash(encoded string) bool {
	p, _, hash, err := decodeArgon2(encoded)
	if err != nil {
		return true
	}
	return p.MemoryKB != h.p.MemoryKB ||
		p.Time != h.p.Time ||
		p.Parallelism != h.p.Parallelism ||
		uint32(len(hash)) != h.p.KeyLength
}

// encodeArgon2 renders the PHC string. base64 raw-std (no padding) matches the
// seed's encoding exactly.
func encodeArgon2(p Argon2Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2idVersion, p.MemoryKB, p.Time, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// decodeArgon2 parses a PHC string of the form
//
//	$argon2id$v=19$m=65536,t=3,p=2$<b64 salt>$<b64 hash>
//
// into its parameters, salt and expected hash. It is intentionally strict: any
// deviation is an error rather than a best-effort parse, so a corrupt value can
// never silently weaken verification. Parsing is done by hand (splitting on '$'
// and ',') rather than fmt.Sscanf to avoid the latter's whitespace/verb quirks
// on unsigned targets.
func decodeArgon2(encoded string) (Argon2Params, []byte, []byte, error) {
	var zero Argon2Params
	parts := strings.Split(encoded, "$")
	// A leading '$' yields an empty first element: ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash].
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return zero, nil, nil, ErrInvalidPasswordHash
	}

	version, err := parseKV(parts[2], "v")
	if err != nil {
		return zero, nil, nil, err
	}
	if version != int64(argon2idVersion) {
		return zero, nil, nil, ErrIncompatibleVersion
	}

	kv := strings.Split(parts[3], ",")
	if len(kv) != 3 {
		return zero, nil, nil, ErrInvalidPasswordHash
	}
	mem, err := parseKV(kv[0], "m")
	if err != nil {
		return zero, nil, nil, err
	}
	tme, err := parseKV(kv[1], "t")
	if err != nil {
		return zero, nil, nil, err
	}
	par, err := parseKV(kv[2], "p")
	if err != nil {
		return zero, nil, nil, err
	}
	if mem <= 0 || tme <= 0 || par <= 0 || par > 255 {
		return zero, nil, nil, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return zero, nil, nil, ErrInvalidPasswordHash
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return zero, nil, nil, ErrInvalidPasswordHash
	}

	p := Argon2Params{
		MemoryKB:    uint32(mem),
		Time:        uint32(tme),
		Parallelism: uint8(par),
		KeyLength:   uint32(len(hash)),
		SaltLength:  uint32(len(salt)),
	}
	return p, salt, hash, nil
}

// parseKV parses a "<key>=<int>" token, verifying the key matches want.
func parseKV(token, want string) (int64, error) {
	k, v, ok := strings.Cut(token, "=")
	if !ok || k != want {
		return 0, ErrInvalidPasswordHash
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, ErrInvalidPasswordHash
	}
	return n, nil
}
