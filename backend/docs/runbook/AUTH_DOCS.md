# Authentication and Authorization Runbook

This document describes the code currently implemented in this repository, not a target architecture.

## IMPLEMENTED

### Architecture, authentication, and login

The Go/Gin backend uses PostgreSQL as the durable security authority, Redis for distributed rate limiting, Argon2id password hashing, opaque refresh tokens, HS256 access JWTs, and in-memory RBAC policy resolution. The protected path is `Auth -> Tenant -> RBAC -> handler`. JWT claims alone never authorize a request: PostgreSQL verifies session liveness and authorization version on every protected request.

`POST /api/v1/auth/login` accepts email/password and optional device metadata. It is IP rate-limited, uses dummy Argon2id verification for an unknown email, returns generic invalid-credential failures, applies durable account lockout, validates login-capable account status, then creates the session and first refresh token in one transaction. It snapshots RBAC and returns access/refresh tokens. Login success resets failure counters and records a login attempt.

### Access tokens, signing, and key rotation

Access tokens are compact HS256 JWSs, short-lived by `jwt.access_token_ttl`. Verification pins `alg=HS256`, requires JOSE `typ=JWT` and configured `kid`, compares HMACs in constant time, and validates issuer, audience, expiry, not-before, subject, JWT ID, token type, and authorization version.

Claims are `iss`, `sub`, `aud`, `exp`, `nbf`, `iat`, `jti`, `typ=access`, `av`, `org`, `branch`, `sid`, `role`, `perms`, `stage`, and `status`. Tokens contain no passwords, refresh tokens, or signing secrets.

Only HS256 is supported. New tokens use `jwt.secret` and `jwt.key_id`. `jwt.previous_secrets` maps retiring key IDs to verification-only secrets. Rotation procedure: deploy a new active key ID/secret while retaining the old pair as previous; wait longer than the maximum access-token TTL; remove the old pair. Startup rejects an invalid algorithm, missing key ID, short secret, or malformed previous-key configuration. Emergency removal of a compromised key invalidates all access tokens signed with it.

### Refresh tokens, rotation, replay, and sessions

Refresh tokens are random opaque credentials. PostgreSQL stores only their SHA-256 hashes plus session/user/family IDs, expiry, use/revocation/replacement state, and reuse timestamp. `POST /auth/refresh` takes the refresh cookie first and can take a body token for non-browser clients.

Refresh runs in one transaction with `SELECT ... FOR UPDATE`. It validates token, session, and account; creates a replacement; atomically marks the old token used; touches the session; and mints an access token. Session expiry is absolute and is not extended by rotation. A spent or revoked token is a replay: the whole family and session are revoked, reuse timestamps are persisted, and the result is `TOKEN_REUSE_DETECTED`. Concurrent refresh submissions are safe: one succeeds and a competitor triggers replay handling.

A session is created at login and records user/family IDs, device metadata, last use, absolute expiry, and revocation reason. Logout uses the authenticated principal's session ID, revokes that session and its refresh tokens transactionally, and returns 204. Logout-all revokes all sessions/tokens for that user transactionally. Access JWTs stop immediately after revocation because the session row is checked.

### Passwords, reset, MFA, and cookies

Passwords use the shared Argon2id hasher and are never returned or logged. Password change verifies the current password, stores a new hash, and revokes every other session. Password reset stores a single-use, one-hour hashed credential, consumes it under a row lock, changes the password, and revokes all sessions. Forgot-password always returns the same success response for known and unknown accounts. Raw reset tokens are not logged.

Refresh cookie attributes come from `refresh_cookie`: name, domain, path, Secure, HttpOnly, and SameSite. Production requires Secure. Unset SameSite defaults to Lax. Cookie-based refresh relies on SameSite as the currently implemented CSRF strategy; there is no separate CSRF-token middleware.

MFA is not implemented: MFA endpoints return 501 and `mfa_enabled` does not cause a second-factor challenge.

### Authorization, RBAC, tenants, and branches

Roles, permissions, role permissions, and user roles are stored in PostgreSQL. RBAC denies by default. The enforcer is loaded at startup and reloaded after RBAC writes. After valid authentication, a verified permission snapshot may allow a route; otherwise the authoritative enforcer evaluates it and errors deny access.

Migration `000015_authz_version` adds `users.authz_version`. Tokens carry this value as `av`; authentication compares it to PostgreSQL every request. It increments transactionally when role membership is assigned/revoked, role permissions are replaced, or a managed role is updated/deleted. Thus an old permission snapshot is rejected after the security change commits.

Organization identity comes from the verified principal, never a client-supplied organization ID. `X-Branch-ID` can only narrow branch scope. SUPER_ADMIN and ADMIN may select a branch within their own organization. Branch-bound users are restricted to their assigned branch. Tenant-scoped repositories must filter by trusted organization/effective branch; foreign IDs must not match.

### Redis, PostgreSQL, failure behavior, and performance

Redis runs the atomic Lua rate limiter. Security policies `login_per_ip`, `login_per_email`, `refresh`, `reset`, and `auth_write` fail closed with 503 if Redis is absent, disabled, or errors. Other rate-limit policies log and degrade open. Redis is not authoritative for sessions, refresh-token replay, or authorization versions.

PostgreSQL supplies transactions, row locks, sessions, refresh families, password reset state, login attempts, RBAC relations, and authorization epochs. Security reads use primary storage. A PostgreSQL error while making a security decision fails closed through the normal database error path.

Each protected request performs JWT verification plus primary PostgreSQL session and authz-version reads. This intentionally favors immediate revocation over a zero-I/O hot path. RBAC evaluation is in memory after policy reload; refresh and mutations are comparatively infrequent. Size PostgreSQL and Redis pools for these paths.

### Errors, audit events, observability, and API contract

Missing/invalid authentication and dead sessions return 401; expired JWTs have an expired-token challenge; valid users without permission return 403; rate-limit rejection is 429; required Redis security-control failure is 503. Errors do not expose secrets.

Login attempts are persisted with safe metadata. Middleware audit wraps authentication and protected mutations, but the currently wired audit sink is a no-op, so those events are not durably persisted by the application process. Zap logs security dependency and enforcement failures. Prometheus/OpenTelemetry platform components exist, but dedicated auth/authz counters and spans are not currently emitted.

Endpoints: public `POST /auth/login`, `/auth/refresh`, `/auth/password/forgot`, `/auth/password/reset`; authenticated `POST /auth/logout`, `/auth/logout-all`, `/auth/password/change`; and `GET /auth/me`. Bearer access tokens use `Authorization: Bearer <token>`. Logout also accepts `{ "all": true }`.

### Deployment, operations, incident response, and tests

Apply migration `000015_authz_version`. Configure PostgreSQL, Redis, unique high-entropy HS256 secrets, issuer/audience, access/refresh TTLs, production Secure cookies, Argon2 parameters, and security rate-limit policies. Never deploy development defaults or commit secrets.

For `TOKEN_REUSE_DETECTED`, investigate the family/session/user and login-attempt trail; revoke all user sessions if broader compromise is suspected. Disable compromised accounts through the user-management flow. Redis outage intentionally makes security auth endpoints return 503; restore and investigate it before normal traffic resumes. For signing-key compromise, remove that key from active/previous configuration and redeploy, then revoke affected sessions if refresh credentials may also be compromised.

Baseline verification is `go test ./...`, `go vet ./...`, formatting checks, and `go test -race ./...` in a supported CI/host. Unit tests cover password hashing, signer behavior, lockout, opaque credentials, cookies, and RBAC. Integration testing requires PostgreSQL/Redis for session, rotation/replay, logout, and tenant/branch paths.

## NOT IMPLEMENTED / FUTURE

- MFA, WebAuthn, TOTP, recovery codes, and step-up authentication.
- A connected password-reset email provider; never use logs as an alternative delivery channel.
- Mounted, privacy-preserving per-account login rate limiting; durable account lockout is implemented.
- CSRF tokens/double-submit protection, device binding, DPoP, mTLS, risk scoring, or impossible-travel detection.
- Redis session/authz caching, JWT blacklist, Redis pub/sub invalidation, or distributed auth locks.
- Dedicated authentication/authorization metrics, tracing spans, or a durable middleware audit producer.
- OAuth/OIDC, JWKS, asymmetric signing, or any signing algorithm other than HS256.
- A documented automatic cleanup job for expired sessions/tokens; establish retention operations for production volume.
