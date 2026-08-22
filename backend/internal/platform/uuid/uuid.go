// Package uuid wraps google/uuid with generation + parse helpers tuned for this project. Every entity ID goes through here so we can swap generators (e.g. UUIDv7 for time-ordered PKs) in one place.

package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	guuid "github.com/google/uuid"
)

// ErrInvalidUUID is the sentinel returned for any parse failure.
var ErrInvalidUUID = errors.New("uuid: invalid identifier")

// Nil is the all-zero UUID
var Nil = guuid.Nil

// New returns a random UUID v4.
func New() guuid.UUID {
	return guuid.New()
}

// NewString is a convenience that returns New's string form directly.
func NewString() string {
	return guuid.NewString()
}

func NewV7() guuid.UUID {
	id, err := guuid.NewV7()
	if err != nil {
		panic("uuid: OS RNG unavailable: " + err.Error())
	}
	return id
}

// NewV7String is a convenience that returns NewV7's string form directly.
func NewV7String() string {
	return NewV7().String()
}

// NewV7Bytes returns a 16-byte time-ordered UUID as a byte slice, handy for
// pgx binary parameter passing.
func NewV7Bytes() []byte {
	id := NewV7()
	return id[:]
}

// Parse parses a canonical UUID string.
func Parse(s string) (guuid.UUID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Nil, ErrInvalidUUID
	}
	u, err := guuid.Parse(s)
	if err != nil {
		return Nil, fmt.Errorf("%w: %s", ErrInvalidUUID, err)
	}
	return u, nil
}

// MustParse panics on invalid input. Only use in tests / seeds.
func MustParse(s string) guuid.UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// ParsePointer returns *UUID (nil if s is empty). Handy for optional query
// params like `?branch_id=`.
func ParsePointer(s string) (*guuid.UUID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil //nolint:nilnil
	}
	u, err := Parse(s)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// IsValid reports whether s is a well-formed UUID.
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// IsNil reports whether id is the all-zero UUID — useful for checking an
// unset foreign key (e.g. `if order.BranchID.IsNil()`-style checks become
// `uuid.IsNil(order.BranchID)`) without every caller needing to spell out
// `id == guuid.Nil` and import google/uuid directly.
func IsNil(id guuid.UUID) bool {
	return id == Nil
}

// TimeFromV7 extracts the embedded unix-millisecond timestamp from a v7 UUID's leading 48 bits (big-endian).
func TimeFromV7(id guuid.UUID) time.Time {
	if id.Version() != 7 {
		return time.Time{}
	}
	ms := int64(id[0])<<40 | int64(id[1])<<32 | int64(id[2])<<24 |
		int64(id[3])<<16 | int64(id[4])<<8 | int64(id[5])
	return time.UnixMilli(ms).UTC()
}

// RandomHex returns a hex-encoded, cryptographically random string of n
// bytes (2*n hex characters) — used for non-UUID identifiers such as
// device fingerprints, invoice-number suffixes, or one-time lookup tokens
// where a full UUID would be needlessly long.
func RandomHex(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("uuid: RandomHex n must be > 0")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("uuid: random hex: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
