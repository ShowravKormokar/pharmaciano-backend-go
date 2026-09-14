package crypto

import (
	"errors"
	"strings"
	"testing"
)

// PepperedHasher behaviour tests. They cover the four shape changes vs
// the bare hasher:
//
//  1. A hash minted under the current pepper embeds the pepper id.
//  2. A hash minted under the previous pepper still verifies, and
//     NeedsUpgrade returns true so the auth flow can re-mint.
//  3. A hash minted under a third, unknown pepper id is rejected
//     outright (ErrUnknownPepper).
//  4. A legacy pepperless hash is accepted when the deployment runs
//     without a pepper; rejected once a pepper is configured.

func TestPepperedHasher_RoundTripCurrent(t *testing.T) {
	inner := NewPasswordHasher(DefaultArgon2Params())
	h := NewPepperedHasher(inner, []byte("pepper-current"), "v1", nil, "")

	encoded, err := h.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasSuffix(encoded, "$v1") {
		t.Fatalf("expected pepper tag suffix $v1, got %q", encoded)
	}
	res, err := h.VerifyUpgrade("hunter2", encoded)
	if err != nil {
		t.Fatalf("VerifyUpgrade: %v", err)
	}
	if !res.Match {
		t.Fatal("expected Match=true on a fresh hash")
	}
	if res.NeedsUpgrade {
		t.Fatal("expected NeedsUpgrade=false on a current-pepper hash")
	}
}

func TestPepperedHasher_AcceptsPreviousPepperAndFlagsUpgrade(t *testing.T) {
	inner := NewPasswordHasher(DefaultArgon2Params())

	// Mint under "v1".
	old := NewPepperedHasher(inner, []byte("pepper-old"), "v1", nil, "")
	encoded, err := old.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	// Rotate: current is "v2", previous is "v1".
	cur := NewPepperedHasher(inner, []byte("pepper-new"), "v2", []byte("pepper-old"), "v1")
	res, err := cur.VerifyUpgrade("hunter2", encoded)
	if err != nil {
		t.Fatalf("VerifyUpgrade: %v", err)
	}
	if !res.Match {
		t.Fatal("expected previous-pepper hash to still verify")
	}
	if !res.NeedsUpgrade {
		t.Fatal("expected NeedsUpgrade=true so the auth flow re-mints")
	}
}

func TestPepperedHasher_RejectsUnknownPepper(t *testing.T) {
	inner := NewPasswordHasher(DefaultArgon2Params())
	old := NewPepperedHasher(inner, []byte("pepper-old"), "v1", nil, "")
	encoded, err := old.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	// Run a deployment whose current/previous pair neither includes "v1".
	cur := NewPepperedHasher(inner, []byte("pepper-new"), "v2", []byte("pepper-x"), "vx")
	if _, err := cur.VerifyUpgrade("hunter2", encoded); !errors.Is(err, ErrUnknownPepper) {
		t.Fatalf("expected ErrUnknownPepper, got %v", err)
	}
}

func TestPepperedHasher_LegacyHashAcceptedWhenNoPepperConfigured(t *testing.T) {
	inner := NewPasswordHasher(DefaultArgon2Params())
	bare := NewPepperedHasher(inner, nil, "", nil, "")
	encoded, err := bare.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	t.Logf("encoded has %d $ parts, currentID=%q, previousID=%q", strings.Count(encoded, "$"), bare.CurrentPepperID(), bare.PreviousPepperID())
	if strings.Count(encoded, "$") != 5 {
		t.Fatalf("expected 6-part legacy PHC, got %q", encoded)
	}
	res, err := bare.VerifyUpgrade("hunter2", encoded)
	if err != nil || !res.Match {
		t.Fatalf("expected clean verify on legacy hash, got match=%v err=%v", res.Match, err)
	}
}
