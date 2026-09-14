package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Pepper-related errors. They are returned alongside the (false, _) match
// outcome so the caller can distinguish a corrupt hash from a configuration
// problem (no pepper where one is required).
var (
	// ErrNoPepper is returned by Verify when the hasher is configured with no
	// pepper at all and the stored hash does not carry a "pepperv" segment.
	// In practice the composition root always wires a pepper; this exists for
	// tests and for the (illegal) "no pepper at all" deployment posture.
	ErrNoPepper = errors.New("crypto: no pepper configured but hash requires one")
	// ErrUnknownPepper is returned by Verify when the stored hash was minted
	// under a pepper id that is neither the current nor the previous one —
	// the hash is from a rotation that was never completed and must be
	// rejected.
	ErrUnknownPepper = errors.New("crypto: hash pepper is unknown to this hasher")
)

// PepperedHasher wraps a PasswordHasher with a server-side pepper (ADR §6).
// The pepper is a high-entropy secret, kept in process memory and never
// persisted, that is mixed into the password before Argon2id is applied. A
// stolen database dump therefore still does not let an attacker brute-force
// passwords without also exfiltrating the pepper from a running process.
//
// Rotation model: the hasher accepts a current pepper and an optional
// previous one. A hash minted with the current pepper is verified normally;
// a hash minted with the previous pepper verifies AND is re-hashed under
// the current pepper on the next successful login. A hash minted under any
// other pepper id is rejected (unknown-pepper), which forces an explicit
// migration step rather than silently accepting stale credentials.
//
// The pepper id is carried inside the PHC string as a sixth segment,
// "$argon2id$v=19$m=...,t=...,p=...$salt$hash$pepperv" so the stored hash
// self-describes the pepper that minted it. The id is a short base36 tag
// (e.g. "v1", "v2") so operators can recognise which generation a row
// belongs to at a glance. Legacy hashes without the segment are accepted
// only when the deployment runs with an empty pepper (treated as
// "unpeppered", so old rows still verify during a first-time enable).
type PepperedHasher struct {
	inner     *PasswordHasher
	current   []byte
	currentID string
	previous  []byte
	previousID string
}

// NewPepperedHasher builds the hasher. currentID is the short tag emitted
// into new hashes; current is the secret that prefix-mixes with the
// password before Argon2id. previous / previousID are optional rotation
// hooks: when set, hashes minted under the previous pepper still verify,
// and NeedsRehash / Verify return rehash=true so the auth flow re-mints
// the hash under the current pepper on the next successful login.
//
// current may be nil/empty for deployments that do not use a pepper; the
// resulting hasher then behaves like a thin wrapper around PasswordHasher
// (legacy hashes verify, new hashes are written without a pepperv segment).
func NewPepperedHasher(inner *PasswordHasher, current []byte, currentID string, previous []byte, previousID string) *PepperedHasher {
	if currentID == "" {
		currentID = "v0"
	}
	return &PepperedHasher{
		inner:      inner,
		current:    append([]byte(nil), current...),
		currentID:  currentID,
		previous:   append([]byte(nil), previous...),
		previousID: previousID,
	}
}

// Hash derives a peppered Argon2id PHC string. The pepper is mixed in
// before Argon2id is applied: the input key material is
// pepper || password. The resulting hash carries the current pepper id so
// Verify can pick the right secret on the check.
func (h *PepperedHasher) Hash(plain string) (string, error) {
	if plain == "" {
		return "", ErrEmptyPassword
	}
	material := h.pepperInput(h.current, plain)
	salt := make([]byte, h.inner.Params().SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: read salt: %w", err)
	}
	key := argon2.IDKey(material, salt,
		h.inner.Params().Time,
		h.inner.Params().MemoryKB,
		h.inner.Params().Parallelism,
		h.inner.Params().KeyLength,
	)
	encoded := encodeArgon2(h.inner.Params(), salt, key)
	if len(h.current) == 0 {
		return encoded, nil // no pepper ⇒ legacy format
	}
	return encoded + "$" + h.currentID, nil
}

// VerifyResult tells the caller whether a hash matches AND whether the
// hash should be re-minted under the current pepper. The two booleans are
// independent: a hash that matches but is on a previous pepper is
// functionally valid (the user can log in) but should be transparently
// upgraded. A hash whose pepper is unknown is not a match (false / false)
// so an attacker cannot bypass rotation by replaying a foreign-peppered
// hash.
type VerifyResult struct {
	Match     bool
	NeedsUpgrade bool
}

// Verify checks plain against encoded and reports whether it matches and
// whether the hash should be re-minted under the current pepper.
func (h *PepperedHasher) verify(plain, encoded string) (VerifyResult, error) {
	pepperID, body, err := splitPepperTag(encoded)
	if err != nil {
		return VerifyResult{}, err
	}
	pepper, ok := h.pepperFor(pepperID)
	if !ok {
		return VerifyResult{}, ErrUnknownPepper
	}
	p, salt, want, err := decodeArgon2(body)
	if err != nil {
		return VerifyResult{}, err
	}
	material := h.pepperInput(pepper, plain)
	got := argon2.IDKey(material, salt, p.Time, p.MemoryKB, p.Parallelism, uint32(len(want)))
	match := subtle.ConstantTimeCompare(got, want) == 1
	if !match {
		return VerifyResult{Match: false}, nil
	}
	// Match. If the stored hash was minted under a different pepper than
	// the current one, the auth flow should re-hash on the way out.
	return VerifyResult{
		Match:        true,
		NeedsUpgrade: pepperID != h.currentID,
	}, nil
}

// NeedsUpgrade reports whether encoded was minted with cost parameters
// different from the hasher's current ones OR with a pepper id other than
// the current. The latter is how we drive a smooth rotation: the first
// successful verify after a rotation returns true and the auth flow
// transparently re-mints the hash.
func (h *PepperedHasher) NeedsUpgrade(encoded string) bool {
	return h.needsUpgrade(encoded)
}

func (h *PepperedHasher) needsUpgrade(encoded string) bool {
	pepperID, body, err := splitPepperTag(encoded)
	if err != nil {
		return true
	}
	if pepperID != h.currentID {
		return true
	}
	return h.inner.NeedsRehash(body)
}

// Params returns the underlying cost profile (delegated).
func (h *PepperedHasher) Params() Argon2Params { return h.inner.Params() }

// Verify is the standard PasswordHash interface method: it returns a plain
// (match, err) without the rotation hint, so the bare hasher and the
// peppered one are interchangeable for callers that do not care about
// rotation. The auth flow calls VerifyUpgrade directly to also receive
// the upgrade signal.
func (h *PepperedHasher) Verify(plain, encoded string) (bool, error) {
	r, err := h.VerifyUpgrade(plain, encoded)
	if err != nil {
		return false, err
	}
	return r.Match, nil
}

// VerifyUpgrade is the rotation-aware Verify. See the VerifyResult type
// for the semantics of Match / NeedsUpgrade.
func (h *PepperedHasher) VerifyUpgrade(plain, encoded string) (VerifyResult, error) {
	return h.verify(plain, encoded)
}

// CurrentPepperID is exposed for observability and tests.
func (h *PepperedHasher) CurrentPepperID() string { return h.currentID }

// PreviousPepperID is exposed for observability and tests. Empty when no
// rotation is in progress.
func (h *PepperedHasher) PreviousPepperID() string { return h.previousID }

// pepperFor looks up the secret bytes for a stored pepper id, returning
// (secret, true) on hit. The empty id is handled explicitly: it is only
// acceptable when the deployment also runs pepperless, so we must check
// that case before we fall through to the currentID comparison.
func (h *PepperedHasher) pepperFor(id string) ([]byte, bool) {
	if id == "" {
		if len(h.current) == 0 {
			return nil, true
		}
		return nil, false
	}
	switch id {
	case h.currentID:
		return h.current, true
	case h.previousID:
		if len(h.previous) == 0 {
			return nil, false
		}
		return h.previous, true
	}
	return nil, false
}

// pepperInput mixes the pepper with the password into the input key
// material. A nil/empty pepper is a no-op so a pepperless deployment
// behaves exactly like the bare PasswordHasher.
func (h *PepperedHasher) pepperInput(pepper []byte, plain string) []byte {
	if len(pepper) == 0 {
		return []byte(plain)
	}
	out := make([]byte, 0, len(pepper)+len(plain))
	out = append(out, pepper...)
	out = append(out, plain...)
	return out
}

// splitPepperTag splits an encoded PHC string into its pepper tag (the
// optional 7th segment) and the rest. A string with exactly 6 '$'
// segments is the legacy 6-part format (no tag).
func splitPepperTag(encoded string) (tag string, body string, err error) {
	parts := strings.Split(encoded, "$")
	switch len(parts) {
	case 6:
		// Legacy: "$argon2id$v=19$m=...$salt$hash" — no tag, leading "" part.
		return "", encoded, nil
	case 7:
		// New: "$argon2id$v=19$m=...$salt$hash$tag".
		return parts[6], strings.Join(parts[:6], "$"), nil
	default:
		return "", "", ErrInvalidPasswordHash
	}
}
