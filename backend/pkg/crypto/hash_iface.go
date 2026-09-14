package crypto

// PasswordHash is the minimal hashing surface the auth and user modules
// depend on. Both *PasswordHasher (no pepper) and *PepperedHasher (with
// rotation) satisfy it, so the composition root can swap one for the
// other without touching call sites.
//
// Verify is intentionally defined to return a plain bool + error so
// existing call sites that do not need rotation hints keep working — the
// rotation-aware hasher exposes the rich return type via VerifyUpgrade,
// and the auth flow is the only place that consumes the upgrade signal.
type PasswordHash interface {
	// Hash derives a fresh PHC string from plain. Errors are returned
	// (and never swallowed) so a misconfigured deployment surfaces
	// clearly rather than silently accepting empty input.
	Hash(plain string) (string, error)
	// Verify reports whether plain matches encoded. A malformed hash
	// returns (false, err) so the caller can distinguish a corrupt row
	// from a clean miss.
	Verify(plain, encoded string) (bool, error)
	// Params returns the active cost profile (used for diagnostics and
	// for building a matching dummy hash for anti-enumeration timing).
	Params() Argon2Params
}

// VerifyUpgrader is the rotation-aware surface. The peppered hasher
// implements it; the bare PasswordHasher does not, and the auth flow
// only calls it when it knows it has the rich variant. Keeping the
// interface separate keeps the bare hasher's surface tiny.
type VerifyUpgrader interface {
	PasswordHash
	// VerifyUpgrade returns the match outcome AND a hint that the
	// stored hash should be re-minted under the current pepper. The
	// two booleans are independent: a hash that matches but is on a
	// previous pepper is functionally valid (the user can log in) but
	// should be transparently upgraded on the way out.
	VerifyUpgrade(plain, encoded string) (VerifyResult, error)
	// NeedsUpgrade reports whether encoded should be re-minted under
	// the current pepper (either because the cost profile changed or
	// because the stored pepper id is no longer current). The auth
	// flow uses it as a second-line check after a successful verify.
	NeedsUpgrade(encoded string) bool
}
