# Authentication and Authorization Runbook

This document describes the code currently implemented in this repository. It mirrors the architecture in `docs/adr/pharmaciano_authentication_authorization_architecture.md` and is the operational reference.

## IMPLEMENTED

### Architecture, authentication, and login

The Go/Gin backend uses PostgreSQL as the durable security authority, Redis for distributed rate limiting, Argon2id password hashing, opaque refresh tokens, HS256 access JWTs, and an in-memory RBAC policy engine. The protected path is `Auth → Tenant → RBAC → handler`. JWT claims alone never authorize a request: PostgreSQL re-verifies session liveness and authorization version on every protected request.

```
REQUEST
  → TLS / reverse proxy
  → CORS / CSRF / rate-limit / security headers
  → JWT verification
  → Session validation  (Redis cache → PostgreSQL PRIMARY)
  → Principal creation
  → Effective organization + branch context
  → Current authorization versions
  → AuthorizationResolver (Redis cache → enforcer → PostgreSQL)
  → Enforce(resource, action)
```

`POST /api/v1/auth/login` accepts email/password and optional device metadata. It is IP rate-limited, runs a dummy Argon2id verification on unknown emails to equalize timing, returns generic invalid-credential failures, applies durable account lockout, validates login-capable status, then in one transaction:

1. Records login success and resets failure counters.
2. Creates the session row.
3. Creates the refresh-token family and stores the SHA-256 hash of the first token.
4. Mints a short-lived HS256 access JWT and the raw refresh token.

### Access tokens, signing, and key rotation

Access tokens are compact HS256 JWSs (target 600–1500 bytes, hard cap 4096 bytes) with a short TTL (`jwt.access_token_ttl`). Verification pins `alg=HS256`, requires JOSE `typ=JWT` and configured `kid`, compares HMACs in constant time, and validates issuer, audience, expiry, not-before, subject, JWT ID, token type, and authorization version.

Claims are `iss`, `sub`, `aud`, `exp`, `nbf`, `iat`, `jti`, `typ=access`, `av` (authz_version), `org`, `branch`, `branches` (multi-branch subset, empty for org-wide), `sid`, `role`, `stage`, `status`. Tokens carry **no** permissions, password data, refresh tokens, or signing secrets.

Only HS256 is supported. New tokens use `jwt.secret` and `jwt.key_id`. `jwt.previous_secrets` maps retiring key IDs to verification-only secrets. Rotation procedure: deploy a new active key id/secret while retaining the old pair as previous; wait longer than the maximum access-token TTL; remove the old pair. Startup rejects an invalid algorithm, missing key id, short secret (<32 bytes), or malformed previous-key configuration. Emergency removal of a compromised key invalidates every access token signed with it.

### Sessions, session cache, and immediate revocation

A session is created at login and records user, refresh-family id, device metadata, IP, user agent, last use, absolute expiry, revocation reason, and a `security_generation` counter. The session is the authority for "is this caller still allowed to act"; revoking it (or letting it expire) invalidates every access token minted under it immediately, even while the JWT would still verify.

`auth.Authenticate` checks session liveness on every request. The hot path is the **Redis session cache** (`internal/modules/auth/session_cache.go`):

- Cache key: `mc:sess:{session_id}:g{security_generation}`.
- On every successful primary lookup the projection is written to Redis (TTL 5 min, best effort).
- Every revocation (`RevokeSession`, `RevokeUserSessions`, `RevokeUserSessionsExcept`) advances the `security_generation` counter atomically; the old cached projection is naturally orphaned and reaped by TTL.
- `Logout` and `RevokeOtherSession` proactively run a `SCAN` and `DEL` on the session's cache entries.
- A Redis miss, error, or malformed JSON all fall through to the PostgreSQL primary. Redis is never the authority.

### Refresh tokens, rotation, replay, and reuse detection

Refresh tokens are random opaque 256-bit credentials (`crypto/rand`, base64url-encoded). PostgreSQL stores only their SHA-256 hashes plus session/user/family ids, expiry, use/revocation/replacement state, and a `reuse_detected_at` forensic timestamp.

`POST /auth/refresh` resolves the refresh token from the `mc_refresh` HttpOnly cookie first and then from the optional JSON body (for non-browser clients). The whole operation runs in one transaction with `SELECT … FOR UPDATE` on the token row:

1. Validate the token: not used, not revoked, not expired.
2. Re-validate the session: live, not expired.
3. Re-validate the account: login-capable status, correct org, current authz_version.
4. Insert the replacement token, atomically mark the old one used, touch the session, re-snapshot the account, and mint a new access token.

A spent or revoked token is a replay: the whole refresh family and the owning session are revoked, `reuse_detected_at` is stamped on the family, and the API returns `TOKEN_REUSE_DETECTED`. Concurrent refresh submissions are safe: the `MarkRefreshTokenUsed` guarded `UPDATE` ensures exactly one rotation can ever win for a given token.

### Logout, password change, password reset, and MFA

- `POST /auth/logout` revokes the caller's current session and its refresh chain, identified from the authenticated principal. `{"all": true}` revokes everything.
- `POST /auth/logout-all` revokes every session and refresh token for the user.
- `POST /auth/password/change` requires the current password, writes a new Argon2id hash, and revokes every **other** session while keeping the caller's current session alive.
- `POST /auth/password/forgot` always returns the same generic success message; only login-capable accounts receive a reset link, and earlier outstanding tokens are invalidated before the new one is issued. Raw reset tokens are never logged.
- `POST /auth/password/reset` redeems a single-use, time-boxed token (default 1 h). The token row is locked `FOR UPDATE` and consumed under a guarded update so a link is strictly single-use even under concurrent submission. A successful reset revokes every session and refresh token for the account.
- `GET /auth/sessions` lists the caller's live sessions with the current session flagged.
- `DELETE /auth/sessions/{id}` revokes one other session of the caller (and its refresh chain); the current session must be revoked via `/auth/logout`.
- MFA is reserved in the route surface: `POST /auth/mfa/{setup,verify,disable}` return 501. The login flow does not branch on `mfa_enabled` yet — enabling MFA today would otherwise lock the user out.

### Refresh cookie and browser body stripping

Cookie attributes come from `refresh_cookie.*`: name, domain, path, `Secure`, `HttpOnly`, and `SameSite`. Production requires `Secure=true`; an unset `SameSite` defaults to `Lax`. Cookie-based refresh relies on `SameSite` as the currently implemented CSRF strategy — there is no separate CSRF-token middleware.

To prevent the refresh token from leaking into browser response bodies (e.g. via dev-tools "Copy response"), the auth handler checks the `X-Client-Type` header:

- `X-Client-Type: browser` → the `refresh_token` field is stripped from the JSON body. The token is delivered ONLY as the HttpOnly cookie.
- Any other value (or missing header) → legacy behavior: the raw refresh token is echoed in the body for non-browser clients (mobile, CLI, server-to-server).

The SPA must send `X-Client-Type: browser` on every `POST /auth/login` and `POST /auth/refresh`.

### Argon2id pepper rotation

Password hashing uses Argon2id (`golang.org/x/crypto/argon2`). A server-side pepper is mixed into the password before the KDF so a database dump alone cannot be brute-forced. The composition root wires a `PepperedHasher` when `password.pepper.current` is set in config; otherwise the bare `PasswordHasher` is used (legacy mode).

Rotation: when `password.pepper.previous` is set, hashes minted under the previous pepper still verify, and `VerifyUpgrade` returns `NeedsUpgrade=true`. The auth flow transparently re-mints the hash under the current pepper on the next successful login. A hash minted under an unknown pepper id is rejected with `ErrUnknownPepper`, so operators must complete a rotation within one access-token TTL of retiring a pepper.

The stored PHC string carries an optional seventh segment (`$argon2id$v=19$m=...$salt$hash$pepperid`) so the minting pepper is self-describing. Legacy hashes without the segment are accepted only when the deployment runs without a pepper.

### Account/email distributed rate limiting

Login, password forgot, and password reset are throttled twice:

1. **By IP** (`login_per_ip`, `refresh`, `reset`) — catches drive-by scanners.
2. **By email** (`login_per_email`, `forgot`, `reset`) — an HMAC-SHA-256(secret, normalized_email) subject so a botnet with many IPs still grinds one bucket per account. The raw email is never written to Redis.

Policies are configured under `rate_limit.policies` in `config.yaml`. Security-critical rate limiters fail closed (503) when Redis is unavailable.

### Multi-branch subset semantics

A principal's branch scope is now a *subset* (`[]uuid.UUID`), not just a single home branch. The `user_branch_assignments` table (migration `000017`) lists the branches a user may act on; the union of their home branch (`users.branch_id`) and their assignments forms the effective subset baked into the JWT as the `branches` claim.

The `X-Branch-IDs` header (comma-separated UUIDs) lets a branch-bound principal narrow the request to a subset of their assigned branches. A request for a branch outside the assigned set is denied with `BRANCH_SCOPE_DENIED`. Org-wide roles (`SUPER_ADMIN`, `ADMIN`) may target any subset of the org's branches.

### Durable audit pipeline

The `Audit()` middleware builds a masked `AuditEntry` for every state-changing request. The `PostgresAuditSink` (`internal/platform/audit/sink.go`) writes the entry to the durable `audit_logs` partitioned table in the same request context (so it participates in any active transaction). Sensitive fields (passwords, tokens, OTPs) are redacted before insert. Errors are logged and swallowed so a transient DB outage never breaks the request.

### Idle session timeout

When `session.idle_timeout` is set, the `IdleSession` middleware (mounted after `Auth` in the `Protected` chain) calls `TouchSession` on every authenticated request. The underlying `UPDATE sessions SET last_seen_at = now() WHERE id = $1 AND last_seen_at + $2 > now()` is idempotent: if the session has idled out (0 rows matched), the middleware returns 401 Unauthenticated. A transient touch failure degrades gracefully (the absolute timeout is the hard guard).

### Observability

Dedicated auth/authz/security Prometheus counters and histograms are registered in `internal/platform/telemetry/metrics.go` and wired through the `Middleware.metrics` field. Counters include:

- `auth_login_attempts_total` (`success | invalid_credentials | locked | inactive | unknown_email | mfa_required`)
- `auth_refresh_attempts_total` (`success | invalid_token | expired | reuse | session_inactive | account_inactive | error`)
- `auth_mfa_challenges_total`, `auth_password_changes_total`, `auth_password_resets_issued_total`, `auth_password_resets_redeemed_total`
- `auth_account_lockouts_total`, `auth_account_email_rate_limited_total{bucket}`
- `auth_session_cache_total` (`hit | miss | error | corrupt`), `auth_session_cache_store_total`, `auth_session_cache_invalidated`
- `authz_decisions_total` (`allow | deny | error`), `authz_cache_total` (`hit | miss | stale | error | skip`), `authz_resolve_seconds{source}`, `authz_version_bumps_total{scope}`, `rbac_generation_bumps_total{reason}`, `authz_cache_errors_total`
- `security_rate_limited_total{policy}`, `security_denials_total{reason}`, `security_trusted_proxy_blocked_total{reason}`, `security_step_up_total{outcome}`

All helpers are nil-tolerant so unit tests can omit the registry without panicking.

### Authorization, RBAC, tenants, and branches

Roles, permissions, role permissions, and user roles are stored in PostgreSQL. RBAC denies by default. The enforcer is a hand-rolled native implementation of `config/casbin_model.conf`:

- A `snapshot` is rebuilt from `LoadPolicies` and `LoadGroupings` and atomically swapped in. Enforce is a lock-free atomic load on the snapshot.
- Reloads happen at startup, on a periodic tick, and immediately after any RBAC mutation through `service.reloadEnforcer`.

Two durable authorization epochs (ADR §27, §30) make cached authorization safely replaceable:

- `users.authz_version` — per-user epoch. Bumped transactionally on role assignment/revoke, role permission replace, managed role update/delete. The JWT carries this value as `av`; authentication compares it to PostgreSQL on every request.
- `organizations.rbac_generation` — per-organization epoch (migration `000016`). Bumped transactionally on role create/update/delete, on role permission replacement, and on a fresh role's first commit. Folded into the authorization cache key (see below) so a single role-definition change invalidates every cached snapshot for that org at O(1), without scanning per-user rows.

Organization identity comes from the verified principal, never from a client-supplied organization id. `X-Branch-ID` (legacy single) or `X-Branch-IDs` (multi, comma-separated) may only narrow branch scope and is never trusted as authority:

- `SUPER_ADMIN` and `ADMIN` default to org-wide (`nil`) scope and may target any branch in their org via either header.
- Branch-bound users are restricted to the subset baked into their JWT (`branches` claim, populated from `user_branch_assignments` + `users.branch_id`); they may pass a subset of it, or nothing, but requesting a branch outside it is denied with `BRANCH_SCOPE_DENIED`.
- A principal with no branch assignment is org-wide; a branch-bound principal with no assigned branches is a misconfiguration and is denied.

Tenant-scoped repositories filter by trusted organization/effective branch so foreign ids never match any row.

### AuthorizationResolver (Redis cache + enforcer + Postgres)

`internal/modules/rbac/authorizer.go` is the central resolver wired into the middleware's `Authorizer` port. The decision path is:

1. Read `users.authz_version` (already on the request context) and `organizations.rbac_generation` (one primary read, small scalar, indexed).
2. Build the versioned cache key: `mc:authz:v1:org:{org}:user:{user}:uv:{av}:og:{rbacgen}`.
3. **Redis HIT** with parseable JSON → use the cached `Access` projection directly.
4. **Redis MISS / ERROR / malformed** → count the error (if malformed) and fall through to the resolver.
5. **Singleflight** (`sync.Map`-backed `flight`): concurrent misses for the same key collapse onto a single `enforcer.ResolveAccess` call. This is the ADR §36 cache-stampede protection.
6. **PostgreSQL** is the source of truth: `enforcer.ResolveAccess` reads the in-memory snapshot, which itself is rebuilt from the primary on load/reload.

The cached `Access` projection is `RoleName`, `IsSuperAdmin`, and a sorted list of `module:action` strings. The in-memory check is O(n) over a small list (rarely >100 entries); the per-request cost is one Redis GET (or one resolver call on miss), nothing more.

Cache TTL is 5 minutes. Versioning is the correctness mechanism; TTL is memory hygiene.

Cache corruption handling (ADR §34): a malformed JSON value increments `authz_cache_error_total` (exposed as `rbac.AuthzCacheErrorCount()` for the Prometheus collector), the bad entry is deleted, and the call resolves from the enforcer as a normal miss. It is not silently counted as a hit.

### Per-request authorization enforcement

`middleware.RBAC(module, action)` calls the `Authorizer.Enforce(sub, dom, obj, act)` once per request and attaches the module/action to the request context for Audit. `Enforce` returns `(bool, error)`; the middleware denies on `false` and on any error (treating an enforcer error as a failure-closed "could not be verified"). A user without roles in the domain or a role with no matching grant is denied with a `nil` error (clean 403, not a server error).

`appctx.HasPermission(module, action)` is the in-memory check for code paths that already hold a `Principal` and want to short-circuit a permission query. It is called only in modules that want zero Redis/DB cost on the hot path; the canonical check remains `middleware.RBAC`.

### Mutations that invalidate authorization (atomic)

Every mutation in the rbac service that affects effective authorization advances the appropriate epoch in the same transaction:

- `AssignRole` / `RevokeRole` → `users.authz_version += 1`.
- `SetRolePermissions` → per-user bumps for every current member + `organizations.rbac_generation += 1`.
- `UpdateRole` / `DeleteRole` / `CreateRole` → `organizations.rbac_generation += 1` (and per-user bumps where applicable).

A successful `Refresh` re-snapshots the account from the primary and re-mints the access JWT, picking up any new authz_version naturally on the next rotation.

### Redis, PostgreSQL, failure behavior, and performance

- Redis runs the atomic Lua rate limiter, the session cache, and the authorization cache.
- The security rate-limit policies (`login_per_ip`, `login_per_email`, `refresh`, `reset`, `auth_write`) fail closed with 503 if Redis is absent, disabled, or errors. Other rate-limit policies log and degrade open.
- Redis is **never** authoritative for sessions, refresh-token replay, authz_version, or RBAC decisions. Every Redis path falls through to the PostgreSQL primary on miss/error/corruption.
- PostgreSQL supplies transactions, row locks, sessions, refresh families, password reset state, login attempts, RBAC relations, and authorization epochs. Security reads use primary storage; a read replica (if configured) is only used for cosmetic reads (e.g. the "active devices" list).
- A PostgreSQL error while making a security decision fails closed through the normal database-error path (the middleware logs the error and returns 403/500 as appropriate; the enforcer fails closed on a missing snapshot).

The hot path is: JWT verify (CPU) → session cache Redis GET → cache hit returns the principal projection; cache miss falls through to one primary session row + one primary credential row + one authz-version read + one org-rbac-generation read. The RBAC decision itself is then a Redis GET, with a singleflight + enforcer fallback on miss.

### Errors, audit, observability, and API contract

| Outcome | Code |
|---|---|
| Missing / invalid credentials | 401 `UNAUTHENTICATED` / `INVALID_CREDENTIALS` |
| Bad / missing / expired / wrong-alg JWT | 401 `TOKEN_INVALID` / `TOKEN_EXPIRED` |
| Token reuse detection | 401 `TOKEN_REUSE_DETECTED` (family revoked) |
| Rate-limit rejection | 429 `RATE_LIMITED` |
| Account locked | 423 `ACCOUNT_LOCKED` (with `retry_after_seconds`) |
| Permission denied | 403 `FORBIDDEN` |
| Branch-scope denied | 403 `BRANCH_SCOPE_DENIED` |
| Security rate-limit dependency down | 503 `SERVICE_UNAVAILABLE` |
| Internal failure during security decision | 500 (generic body, cause in logs only) |

Errors do not expose secrets. Validation errors carry field-level details. `WWW-Authenticate` challenges follow RFC 6750: `invalid_token` for token defects, `insufficient_scope` for forbidden/scoped outcomes.

Login attempts are persisted with safe metadata. The middleware audit wrapper records authentication attempts and every protected mutation via the `PostgresAuditSink` (`internal/platform/audit/sink.go`), which writes to the durable `audit_logs` partitioned table in the request context. Sensitive fields (passwords, tokens, OTPs) are redacted before insert. Errors are logged and swallowed so a transient DB outage never breaks the request.

Observability metrics:

- Dedicated Prometheus counters and histograms are registered in `internal/platform/telemetry/metrics.go` and exposed via the `/metrics` endpoint. Counters include `auth_login_attempts_total`, `auth_refresh_attempts_total`, `auth_mfa_challenges_total`, `auth_password_changes_total`, `auth_password_resets_issued_total`, `auth_password_resets_redeemed_total`, `auth_account_lockouts_total`, `auth_account_email_rate_limited_total{bucket}`, `auth_session_cache_total`, `auth_session_cache_store_total`, `auth_session_cache_invalidated`, `authz_decisions_total`, `authz_cache_total`, `authz_resolve_seconds{source}`, `authz_version_bumps_total{scope}`, `rbac_generation_bumps_total{reason}`, `authz_cache_errors_total`, `security_rate_limited_total{policy}`, `security_denials_total{reason}`, `security_trusted_proxy_blocked_total{reason}`, and `security_step_up_total{outcome}`.
- All helpers are nil-tolerant so unit tests can omit the registry without panicking.
- `rbac.AuthzCacheErrorCount()` — the running total of cache-corruption / cache-error events since process start (also exported as `authz_cache_errors_total`).

### Endpoints

| Method | Path | Auth | Notes |
|---|---|---|---|
| POST | `/api/v1/auth/login` | public | IP rate-limited, audit-wrapped |
| POST | `/api/v1/auth/refresh` | public | cookie-first; rotated; audit-wrapped |
| POST | `/api/v1/auth/password/forgot` | public | always returns 200 |
| POST | `/api/v1/auth/password/reset` | public + token | single-use, 1 h |
| POST | `/api/v1/auth/logout` | auth | revokes current session |
| POST | `/api/v1/auth/logout-all` | auth | revokes every session |
| POST | `/api/v1/auth/password/change` | auth | revokes every other session |
| GET  | `/api/v1/auth/me` | auth | principal snapshot (no DB) |
| GET  | `/api/v1/auth/sessions` | auth | active devices, current flagged |
| DELETE | `/api/v1/auth/sessions/{id}` | auth | revokes one other session |
| POST | `/api/v1/auth/mfa/setup` | auth | 501 (reserved) |
| POST | `/api/v1/auth/mfa/verify` | auth | 501 (reserved) |
| POST | `/api/v1/auth/mfa/disable` | auth | 501 (reserved) |

Bearer access tokens use `Authorization: Bearer <token>`. The refresh token rides in the `mc_refresh` HttpOnly cookie (and may also be sent in the JSON body for non-browser clients).

### Deployment, operations, incident response, and tests

- Apply migrations `000015_authz_version`, `000016_rbac_session_generations` (adds `organizations.rbac_generation`, `sessions.security_generation`, and the `bump_organization_rbac_generation()` helper), and `000017_user_branch_assignments` (adds the multi-branch subset table).
- Configure PostgreSQL, Redis, unique high-entropy HS256 secrets (≥32 bytes), issuer/audience, access/refresh TTLs, production `Secure` cookies, Argon2id parameters, and security rate-limit policies. Never deploy development defaults or commit secrets.
- For `TOKEN_REUSE_DETECTED`: investigate the family/session/user and login-attempt trail; revoke all user sessions if broader compromise is suspected. Disable compromised accounts through the user-management flow.
- Redis outage: security rate-limit policies return 503 (fail closed); session and authorization caches fall through to the PostgreSQL primary. Restore Redis before returning to normal traffic.
- For signing-key compromise: remove the key from active and previous configuration, redeploy, then revoke affected sessions if refresh credentials may also be compromised. Active access tokens signed by the removed key will fail signature verification and force a re-login.
- For a discovered role-definition mistake: the org-rbac-generation bump in the same transaction makes the corrected policy visible on the next request; no per-user cache wipe is needed.

Baseline verification: `go test ./...`, `go vet ./...`, formatting checks, and `go test -race ./...` in a supported CI/host. Unit tests cover the HS256 signer, Argon2id hasher, brute-force lockout policy, opaque refresh-token minting, the refresh-cookie manager, and the RBAC enforcer + access resolution. Integration testing requires PostgreSQL/Redis for session rotation/replay, login/lockout, logout, password reset, and tenant/branch paths.

## NOT IMPLEMENTED / FUTURE

- MFA, WebAuthn, TOTP, recovery codes, and step-up authentication (routes reserved, return 501).
- A connected password-reset email provider; never use logs as an alternative delivery channel.
- CSRF tokens/double-submit protection, device binding, DPoP, mTLS, risk scoring, or impossible-travel detection.
- JWT blacklist, Redis pub/sub invalidation channels, or distributed auth locks.
- OAuth/OIDC, JWKS, asymmetric signing, or any signing algorithm other than HS256.
- A retention/cleanup job for expired sessions, refresh tokens, password resets, and audit records (migration `000017` adds `user_branch_assignments`; the schema is ready for the cleanup worker).
- Async audit processing via a message queue (current implementation is synchronous via `PostgresAuditSink`; high-throughput deployments should add a bounded async producer/consumer).
