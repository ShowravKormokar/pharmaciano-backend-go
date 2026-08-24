// Package crypto provides field-level encryption (AES-256-GCM) and hashing
// primitives shared across the app.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	envelopeVersion = "v2"
	legacyVersion   = "v1"
	aes256KeySize   = 32
	gcmNonceSize    = 12
	envelopeSepChar = ":"
)

var (
	ErrInvalidCiphertext = errors.New("crypto: invalid ciphertext envelope")
	ErrUnknownKeyID      = errors.New("crypto: unknown key id")
	ErrKeySize           = errors.New("crypto: encryption key must be 32 bytes (AES-256)")
	// ErrLegacyEnvelope is returned for a "v1:" envelope — see the package
	// doc comment's BREAKING CHANGE note above.
	ErrLegacyEnvelope = errors.New("crypto: envelope uses legacy v1 AAD scheme, not supported by this build — re-encrypt or wipe affected rows")
)

// KeyRing holds the current write key plus older keys still needed for
// reads. Safe for concurrent use: current is protected by mu; keysByID is
// never mutated after construction, so it needs no lock to read.
type KeyRing struct {
	mu       sync.RWMutex
	current  string            // key id used for new writes; guarded by mu
	keysByID map[string][]byte // id → 32-byte key; immutable after New
}

func NewKeyRing(currentID string, keys map[string]string) (*KeyRing, error) {
	if currentID == "" {
		return nil, errors.New("crypto: current key id is required")
	}
	ring := &KeyRing{current: currentID, keysByID: map[string][]byte{}}
	for id, b64 := range keys {
		if strings.Contains(id, envelopeSepChar) {
			// The envelope format splits on ':' — a key id containing one
			// would silently corrupt every future Decrypt/KeyIDOf parse.
			return nil, fmt.Errorf("crypto: key id %q must not contain %q", id, envelopeSepChar)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %q not valid base64: %w", id, err)
		}
		if len(raw) != aes256KeySize {
			return nil, fmt.Errorf("%w (key %q had %d bytes)", ErrKeySize, id, len(raw))
		}
		ring.keysByID[id] = raw
	}
	if _, ok := ring.keysByID[currentID]; !ok {
		return nil, fmt.Errorf("crypto: current key id %q not in ring", currentID)
	}
	return ring, nil
}

func (r *KeyRing) Rotate(newCurrentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.keysByID[newCurrentID]; !ok {
		return fmt.Errorf("crypto: cannot rotate to unknown key id %q (add it under old_keys and restart first)", newCurrentID)
	}
	r.current = newCurrentID
	return nil
}

// CurrentKeyID reports the key id currently used for new writes.
func (r *KeyRing) CurrentKeyID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Encrypt returns the envelope string with no context binding.
func (r *KeyRing) Encrypt(plaintext string) (string, error) {
	return r.EncryptWithContext(plaintext, "")
}

// Decrypt reverses Encrypt (context ""). Prefer DecryptWithContext,
// matching whatever context Encrypt/EncryptWithContext used for this value.
func (r *KeyRing) Decrypt(envelope string) (string, error) {
	return r.DecryptWithContext(envelope, "")
}

func (r *KeyRing) EncryptWithContext(plaintext, ctx string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	r.mu.RLock()
	currentID := r.current
	key := r.keysByID[currentID]
	r.mu.RUnlock()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	aad := buildAAD(currentID, ctx)
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), aad)
	return envelopeVersion + envelopeSepChar + currentID + envelopeSepChar +
		base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptWithContext reverses EncryptWithContext.
func (r *KeyRing) DecryptWithContext(envelope, ctx string) (string, error) {
	if envelope == "" {
		return "", nil
	}
	parts := strings.SplitN(envelope, envelopeSepChar, 3)
	if len(parts) != 3 {
		return "", ErrInvalidCiphertext
	}
	if parts[0] == legacyVersion {
		return "", ErrLegacyEnvelope
	}
	if parts[0] != envelopeVersion {
		return "", ErrInvalidCiphertext
	}
	keyID, blob := parts[1], parts[2]

	r.mu.RLock()
	key, ok := r.keysByID[keyID]
	r.mu.RUnlock()
	if !ok {
		return "", ErrUnknownKeyID
	}

	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil || len(raw) < gcmNonceSize {
		return "", ErrInvalidCiphertext
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, ct := raw[:gcmNonceSize], raw[gcmNonceSize:]
	aad := buildAAD(keyID, ctx)
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(pt), nil
}

// KeyIDOf peeks at which key id encrypted envelope, without decrypting it — used by background rotation jobs to find rows still under an old key. Returns "" for an empty envelope.
func (r *KeyRing) KeyIDOf(envelope string) (string, error) {
	if envelope == "" {
		return "", nil
	}
	parts := strings.SplitN(envelope, envelopeSepChar, 3)
	if len(parts) != 3 {
		return "", ErrInvalidCiphertext
	}
	if parts[0] == legacyVersion {
		return "", ErrLegacyEnvelope
	}
	if parts[0] != envelopeVersion {
		return "", ErrInvalidCiphertext
	}
	return parts[1], nil
}

// NeedsRotation reports whether envelope was encrypted under a key id other
// than the ring's current one — the standard predicate a re-encryption background job filters rows by.
func (r *KeyRing) NeedsRotation(envelope string) (bool, error) {
	id, err := r.KeyIDOf(envelope)
	if err != nil {
		return false, err
	}
	if id == "" {
		return false, nil
	}
	return id != r.CurrentKeyID(), nil
}

func buildAAD(keyID, ctx string) []byte {
	buf := make([]byte, 0, 8+len(keyID)+len(ctx))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(keyID)))
	buf = append(buf, keyID...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(ctx)))
	buf = append(buf, ctx...)
	return buf
}

// Last4 returns the last 4 runes of s (or all of s if it has 4 or fewer) —
// used for a searchable, non-sensitive fingerprint stored alongside the
// full ciphertext (e.g. bank_account_last4).
func Last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return s
	}
	return string(r[len(r)-4:])
}

// Field-level encryption for PII (NID, passport, bank account, salary, MFA
// secret). AES-256-GCM: authenticated encryption. Ciphertext envelope
// format:
//
//     v2:<key_id>:<base64(nonce||ciphertext)>
//
// Storing the key_id inside the envelope lets us rotate keys without
// touching existing rows — old rows keep decrypting under whichever key
// encrypted them; new writes use KeyRing.current (see Rotate to switch that
// without a restart, once the new key is loaded into the ring at boot).
//
// GCM's additional-authenticated-data (AAD) binds each ciphertext to BOTH
// the key id AND an optional caller-supplied context string (see
// EncryptWithContext / DecryptWithContext) using length-prefixed encoding
// (buildAAD) — never naive "keyID:context" string concatenation, which
// would let a value crafted as keyID="a:b", context="" collide with
// keyID="a", context="b" and still authenticate. Binding a context such as
// "users.nid_number:<user_id>" means a ciphertext copied into a different
// row/column by anyone with raw DB write access (but not the app's
// encryption key) fails to decrypt there — a real integrity property Encrypt
// alone (context="") does not provide, so prefer *WithContext for new PII
// fields.
//
// BREAKING CHANGE NOTE: this envelope is versioned "v2" specifically
// because its AAD changed from the original "v1" scheme (raw key id, no
// length-prefixing, no context). Data encrypted under v1 will NOT decrypt
// under this build — Decrypt returns ErrLegacyEnvelope for any "v1:"
// envelope instead of a confusing generic GCM-authentication failure. If
// real rows were ever encrypted under v1, that needs a one-time migration
// (decrypt under the old scheme, re-encrypt under v2). At this project's
// current stage — no user data seeded yet — adopting v2 now is the safe
// move.