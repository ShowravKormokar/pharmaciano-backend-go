# ADR 0008 — JWT Auth with Refresh Rotation and Reuse Detection

- **Status:** Accepted (supersedes original design — updated 2026-09-14)
- **Date:** 2026-07-15 (original) · 2026-09-14 (revision)
- **Deciders:** Backend Lead, Security
- **Related:** ADR-0003, ADR-0004, ADR-0015, ADR-0017, ADR-0019

---

## Context

Pharmaciano ERP is used from multiple devices (POS terminals, back-office
laptops, phones, desktop apps). Sessions must:

- Survive across app restarts on the client.
- Be revocable centrally (SUPER_ADMIN can force-logout any user).
- Detect and respond to refresh-token theft.
- Track each device with metadata (name, OS, browser, IP, geo, last-seen)
  so the user and SUPER_ADMIN can see "My Devices" / "Live sessions" pages.
- Be short-lived on the wire (access tokens) yet ergonomic (few real
  logouts).
- Support multi-tenant isolation (organization + branch scoping).
- Support horizontal scaling (no sticky sessions, no in-memory session state).

Options considered:

1. **Long-lived JWTs** in localStorage — bad on every axis (XSS, no
   revocation).
2. **Server-side sessions** with a cookie — hard to scale statelessly.
3. **Access + refresh pair, refresh token in HttpOnly cookie, rotation
   with reuse detection, stateful session as authority.**
4. **OAuth2/OIDC with an external identity provider.**

## Decision

We use option **3**: short-lived access JWTs plus rotating opaque refresh
tokens, with **stateful PostgreSQL sessions as the authority** and
**Redis as a generation-keyed performance cache**.

### Token model

| Token | Lifetime | Where it lives | Purpose |
|---|---|---|---|
| Access token | **15 min** | `Authorization: Bearer` header | Carries identity + branch snapshot; not sole authority |
| Refresh token | **7 days** (session-bounded) | `HttpOnly; SameSite=Strict` cookie on `/api/v1/auth` (browser) or JSON body (non-browser) | Exchange for new access token |
| Session record | **7 days** absolute, **30 min** idle | PostgreSQL (authority) + Redis (60s cache) | Device tracking, revocation, liveness |

### Algorithm

- **HS256** (symmetric) for all environments.
- Key rotation via `key_id` field (e.g., `key-2026-07`); old keys retained
  for verification during a grace window.
- `JWT_SECRET` env var provides the HMAC key.

Claims (validated by `Signer.Parse` with strict algorithm, issuer, audience,
and time-window checks):

```
iss:  pharmaciano
aud:  pharmaciano-users
sub:  <user_id>
sid:  <session_id>
org:  <organization_id>
bid:  <branch_id>           -- nullable; nil for org-wide principals
role: <role_name>           -- snapshot for UI display
stage: <account_stage>      -- snapshot for UI gating
status: <account_status>    -- snapshot for UI gating
av:   <authz_version>       -- staleness guard (epoch counter)
sg:   <security_generation> -- cache key dimension
iat, exp, jti
```

**Critical invariant:** JWT claims are NEVER the sole authority for
authorization. The server-side session check + authz_version read always
happen. A valid JWT with a revoked/expired session is rejected.

Access tokens deliberately do **not** carry email, name, or full permission
sets — reloaded from DB/RBAC snapshot on need. This keeps them short and
reduces PII in logs.

### Hybrid-stateful design

The "hybrid" in our auth is:

- **JWT** = transport mechanism for identity claims (stateless, fast)
- **PostgreSQL session row** = authority for liveness (stateful, revocable)
- **Redis session cache** = performance optimization (60s TTL, generation-keyed)

No single layer is sufficient:
- JWT alone → cannot revoke mid-lifetime
- PostgreSQL alone → too slow for every request (DB round-trip)
- Redis alone → not durable, cannot be sole authority

Together: JWT carries claims, Redis absorbs 90%+ of session checks,
PostgreSQL is the fallback and source of truth.

### Refresh rotation with reuse detection

Refresh tokens are **opaque** (32-byte CSPRSG, base64url-encoded), not JWTs.
Only their SHA-256 hash is stored at rest. The hash has a unique index.

Every refresh call:

1. Loads the token row `SELECT ... FOR UPDATE` (row-level lock).
2. If `used_at IS NULL AND revoked_at IS NULL` → legitimate rotation:
   - Mints a new refresh token (same family_id).
   - Inserts the new row.
   - Atomically marks the old token: `used_at = now(), replaced_by = new_id`
     (guarded UPDATE — only succeeds if still unused).
   - Touches the session's `last_seen_at`.
   - Re-snapshots the account's live role/status.
   - Returns new access + refresh pair.
3. The new token's expiry is bounded by the session's absolute expiry
   (token chain can never outlive the session).

If a used refresh token is presented again (`used_at IS NOT NULL`), that is
**proof of theft**:

1. `teardownFamily()`: revoke every live token in the family
   (`revoked_at = now()`).
2. `StampFamilyReuseDetected()`: forensic `reuse_detected_at` timestamp
   on all family tokens (first detection only).
3. Session revoked (`security_generation` bumped).
4. Redis session cache entries evicted for all affected sessions.
5. `TOKEN_REUSE_DETECTED` returned to client.
6. Prometheus counter `observeTokenReuse()` incremented.
7. Audit log entry recorded with offending IP and UA.

The user must log in again on every device tied to that family.

**Race-free guarantee:** The `SELECT FOR UPDATE` + guarded
`MarkRefreshTokenUsed` ensures that even two concurrent presentations of
the same token produce exactly one winner. The loser gets reuse-detected
after commit.

### Session lifecycle

Sessions are PostgreSQL rows with:

- **Device metadata:** name, fingerprint, IP, user-agent, browser, OS,
  device type, country, city.
- **Lifecycle:** `created_at`, `last_seen_at`, `expires_at`, `revoked_at`,
  `revoked_by`, `revoke_reason`.
- **Security generation:** monotonic counter bumped on every revocation.
  Redis cache key includes this generation, so revocation orphans stale
  cache entries instantly.

**Timeouts:**

| Timeout | Value | Behavior |
|---|---|---|
| Access token TTL | 15 min | JWT expiry; client must refresh |
| Idle timeout | 30 min | `last_seen_at + 30m > now`; refreshed on each refresh |
| Absolute timeout | 7 days | Hard cap regardless of activity |
| Concurrent session cap | 10 per user | Oldest evicted on overflow |

### Session cache (Redis)

Key: `mc:sess:{session_id}:g{security_generation}`

Value (JSON): `{uid, org, bid, bids, av, cl, g, exp}`

- **60s TTL** — short enough that a failed invalidation leaves only a tiny
  stale window; long enough to absorb login spikes.
- **Written** after every successful Authenticate (both cache-hit and
  DB-hit paths).
- **Read** on every protected request. Cache hit + generation match + all
  staleness guards pass → skip PostgreSQL session check.
- **Invalidation:** On logout/revoke, `InvalidateAll` does SCAN + DEL to
  purge all generation keys. Revocation advances generation, orphaning old
  entries even if DEL fails.
- **Fail-closed:** Any Redis error, malformed entry, or cache miss → falls
  back to PostgreSQL primary. No authentication or authorization is blocked.

### Revocation

Three shapes:

- **Logout this device:** Mark session revoked (generation bumped).
  Access tokens expire naturally within 15 min. Redis cache entries
  evicted. No jti blacklist needed — the session liveness check catches
  revoked sessions immediately.
- **Logout everywhere (LogoutAll):** Revoke all user sessions + all
  refresh tokens. Redis cache entries evicted for all.
- **Admin force-logout:** Same as LogoutAll, triggered by SUPER_ADMIN
  or by user status change. Audit-logged.

**Password change** also triggers revocation:
- Authenticated change: all OTHER sessions revoked (current survives).
- Force change (must_change_password): ALL sessions revoked, fresh session issued.
- Password reset (email link): ALL sessions revoked, must re-login everywhere.

### Dual delivery (browser vs non-browser)

The `X-Client-Type: browser` header controls refresh token delivery:

| Client Type | Refresh Token Delivery | Cookie Behavior |
|---|---|---|
| `X-Client-Type: browser` | HttpOnly cookie only; body stripped | Set on login/refresh; cleared on logout/error |
| Non-browser / absent | JSON response body | Cookie still set as fallback |

This means a stolen access-log or browser dev-tools "copy response" cannot
leak the long-lived credential for browser clients.

### Cookies

The refresh cookie:

- `HttpOnly` (JS cannot read it → XSS-safe).
- `Secure` flag configurable (false in dev, true in prod).
- `SameSite=Strict` (blocks CSRF for cookie-bearing auth flows).
- `Path=/api/v1/auth` (scope limited to auth endpoints).
- `Domain` from config (defaults to `localhost` in dev).

Access tokens live only in memory in the client (not in localStorage) to
keep them out of reach of XSS.

### Rate limits

- `POST /api/v1/auth/login`: **5 attempts / 15 min per email** (HMAC-normalized);
  **20 / 15 min per IP**. Both enforced simultaneously.
- `POST /api/v1/auth/refresh`: **60 / min per IP**.
- `POST /api/v1/auth/password/*`: **3 / hour per IP** (forgot + reset);
  per-email limits on forgot.
- `POST /api/v1/auth/mfa/verify|recovery`: same as login per-IP.

See ADR-0003 for the hashing budget that shapes these numbers.

### Account lockout

In addition to rate limiting, a **per-account lockout** operates on the
`users` table:

- **5 consecutive failures** → account locked for **15 minutes**.
- Stored in `users.failed_attempts` and `users.locked_until`.
- Successful login resets the counter to 0.
- Rate limiting and lockout operate simultaneously — different mechanisms
  targeting different axes (per-IP vs per-account).

## Rationale

- Access + refresh pair with stateful session is the mainstream pattern for
  SPAs and mobile clients that need central revocation.
- PostgreSQL as session authority gives us ACID transactions, no split-brain,
  and instant revocation via `security_generation` bump.
- Opaque refresh tokens (not JWTs) mean a database leak cannot be replayed
  as valid tokens — only hashes are stored.
- Generation-keyed Redis cache gives us sub-millisecond session checks on
  the hot path with instant invalidation semantics.
- Branch-scope in the JWT and cache ensures branch-bound principals cannot
  access out-of-scope resources.

Full OAuth2/OIDC was rejected for MVP because we do not need third-party
identity providers yet. Nothing here prevents adding an OIDC layer later.

## Consequences

### Positive

- Central revocation with fine granularity (single session, all sessions,
  family teardown).
- Live "My Devices" / "Live sessions" experiences are straightforward.
- Theft is detected and the entire family neutered automatically.
- Access tokens are stateless — API scales horizontally.
- Redis cache absorbs 90%+ of session checks — sub-millisecond hot path.
- Fail-closed: Redis outage degrades to DB-only; never blocks auth.

### Negative

- Refresh path is stateful (PostgreSQL transaction with FOR UPDATE lock).
  Under extreme load this is a write-contention point, but the row-level
  lock scopes it to one token at a time.
- Session cache requires Redis. Redis outage means every request hits
  PostgreSQL primary — higher latency but no functional breakage.
- The `security_generation` bump + cache eviction on revocation adds a
  small write cost to every session kill.

### Neutral

- Cookie-based refresh adds CSRF considerations to `/auth/refresh`. Our
  `SameSite=Strict` cookie plus the OriginGuard middleware make CSRF
  infeasible in practice.
- The hybrid design adds conceptual complexity, but each layer has a clear
  role (transport / authority / cache).

## Guardrails

- `auth` middleware **must** call `Signer.Parse` which enforces
  `WithValidMethods([]string{"HS256"})` — never accept `alg=none`.
- JWT parsing errors must not leak reasons to the client; always return a
  generic `401 UNAUTHENTICATED`.
- Session liveness check **must** read from PostgreSQL primary, never replica,
  to avoid revocation lag.
- Refresh cookie is only ever written by the auth handler; nothing else
  touches it.
- Any code path that reads `used_at` on a refresh row must do so inside a
  transaction with `SELECT FOR UPDATE`, to avoid double-spend races.
- The session cache is strictly a performance optimization — any miss or
  error must fall through to the primary, never to a denial.

## References

- IETF RFC 7519 (JSON Web Token).
- Auth0 blog, *Refresh Token Rotation and Automatic Reuse Detection*.
- OWASP Cheat Sheet, *Session Management*.
- OWASP ASVS 4.0, chapter V3 (Session Management).
