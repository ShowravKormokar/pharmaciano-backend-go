package crypto

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B test vector: the 20-byte ASCII secret
// "12345678901234567890", augmented with the per-counter expected 6-digit TOTP
// (the RFC prints 8 digits; the trailing six are the 6-digit variant).
var rfc6238Vectors = []struct {
	counter uint64
	want    string
}{
	{counter: 0x0000000000000001, want: "287082"}, // T=59
	{counter: 0x00000000023523EC, want: "081804"}, // T=1111111109
	{counter: 0x00000000023523ED, want: "050471"}, // T=1111111111
	{counter: 0x000000000273EF07, want: "005924"}, // T=1234567890
	{counter: 0x0000000003F940AA, want: "279037"}, // T=2000000000
	{counter: 0x0000000027BC86AA, want: "353130"}, // T=20000000000
}

func TestTOTPCodeRFC6238Vector(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, v := range rfc6238Vectors {
		code := totpCode(key, v.counter)
		got := string(code[:])
		if got != v.want {
			t.Errorf("counter %#x: got %s, want %s", v.counter, got, v.want)
		}
	}
}

func TestTOTPValidateWindow(t *testing.T) {
	secret, err := GenerateTOTPSecret(20)
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}

	now := time.Unix(1_600_000_000, 0) // arbitrary
	expectedCounter := uint64(now.Unix() / int64(totpPeriod/time.Second))
	wantCode := totpCode(key, expectedCounter)
	want := string(wantCode[:])

	// Exact-time match.
	actualCounter, ok := TOTPValidate(secret, want, 1, now)
	if !ok || actualCounter != int64(expectedCounter) {
		t.Fatalf("expected exact match, ok=%v counter=%d", ok, actualCounter)
	}

	// A future code from one step ahead is accepted within window=1.
	nextCode := totpCode(key, expectedCounter+1)
	next := string(nextCode[:])
	if _, ok := TOTPValidate(secret, next, 1, now); !ok {
		t.Errorf("expected +1 window step to validate")
	}

	// A wrong code never matches.
	if _, ok := TOTPValidate(secret, "000000", 1, now); ok {
		t.Errorf("expected wrong code to be rejected")
	}

	// Empty / malformed secret never matches.
	if _, ok := TOTPValidate("", want, 1, now); ok {
		t.Errorf("expected empty secret to be rejected")
	}
	if _, ok := TOTPValidate("!!not-base32!!", want, 1, now); ok {
		t.Errorf("expected malformed secret to be rejected")
	}
}

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("expected 10 codes, got %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("code %q has unexpected shape", c)
		}
		// Only valid alphabet characters.
		for _, r := range c {
			if r != '-' && !strings.ContainsRune(recoveryAlphabet, r) {
				t.Errorf("code %q contains disallowed char %q", c, r)
			}
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	cases := map[string]string{
		"GX7QM-K2NP4":   "GX7QMK2NP4",
		"gx7qm-k2np4":   "GX7QMK2NP4",
		"  gx7qmk2np4 ": "GX7QMK2NP4",
	}
	for in, want := range cases {
		if got := NormalizeRecoveryCode(in); got != want {
			t.Errorf("NormalizeRecoveryCode(%q) = %q, want %q", in, got, want)
		}
	}
}
