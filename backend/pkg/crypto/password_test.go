package crypto

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestPasswordHashVerifyRoundTrip(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2Params())
	const pw = "S3cure-P@ssw0rd!"

	encoded, err := h.Hash(pw)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC prefix: %q", encoded)
	}

	ok, err := h.Verify(pw, encoded)
	if err != nil || !ok {
		t.Fatalf("Verify correct password = (%v, %v), want (true, nil)", ok, err)
	}

	ok, err = h.Verify("wrong-password", encoded)
	if err != nil {
		t.Fatalf("Verify wrong password errored: %v", err)
	}
	if ok {
		t.Fatal("Verify accepted a wrong password")
	}
}

// TestVerifySeedFormat proves this hasher accepts a hash produced exactly the
// way cmd/seed/main.go produces it (raw-std base64, m=65536,t=3,p=2). If this
// breaks, the seeded SUPER_ADMIN can no longer log in.
func TestVerifySeedFormat(t *testing.T) {
	const pw = "SuperAdmin#2026"
	const (
		memory  = 64 * 1024
		time    = 3
		threads = 2
		keyLen  = 32
	)
	salt := []byte("0123456789abcdef") // 16 bytes, fixed for determinism
	key := argon2.IDKey([]byte(pw), salt, time, memory, threads, keyLen)
	seedHash := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)

	h := NewPasswordHasher(DefaultArgon2Params())
	ok, err := h.Verify(pw, seedHash)
	if err != nil || !ok {
		t.Fatalf("Verify(seed hash) = (%v, %v), want (true, nil)", ok, err)
	}
	if h.NeedsRehash(seedHash) {
		t.Fatal("seed hash flagged for rehash despite matching default params")
	}
}

func TestNeedsRehashOnStrongerParams(t *testing.T) {
	weak := NewPasswordHasher(Argon2Params{MemoryKB: 8 * 1024, Time: 1, Parallelism: 1, KeyLength: 32, SaltLength: 16})
	strong := NewPasswordHasher(DefaultArgon2Params())

	encoded, err := weak.Hash("pw-abcdef")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strong.NeedsRehash(encoded) {
		t.Fatal("stronger hasher should flag a weaker hash for rehash")
	}
	// A hash at the target profile must not be flagged.
	atProfile, _ := strong.Hash("pw-abcdef")
	if strong.NeedsRehash(atProfile) {
		t.Fatal("hash at current profile should not need rehash")
	}
	// It must still verify regardless of profile.
	if ok, err := strong.Verify("pw-abcdef", encoded); err != nil || !ok {
		t.Fatalf("cross-profile Verify = (%v,%v), want (true,nil)", ok, err)
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2Params())
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3$onlyfourparts",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",  // wrong variant
		"$argon2id$v=99$m=65536,t=3,p=2$c2FsdA$aGFzaA",  // wrong version
		"$argon2id$v=19$m=x,t=3,p=2$c2FsdA$aGFzaA",      // non-numeric memory
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",     // bad base64 salt
	}
	for _, c := range cases {
		ok, err := h.Verify("whatever", c)
		if ok || err == nil {
			t.Fatalf("Verify(%q) = (%v, %v), want (false, non-nil)", c, ok, err)
		}
	}
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2Params())
	if _, err := h.Hash(""); err == nil {
		t.Fatal("Hash(\"\") should error")
	}
}
