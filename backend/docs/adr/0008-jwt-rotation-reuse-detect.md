# ADR 0008 — JWT Auth with Refresh Rotation and Reuse Detection

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Security
- **Related:** ADR-0003, ADR-0004

---

## Context

MediCore ERP is used from multiple devices (POS terminals, back-office
laptops, phones). Sessions must:

- Survive across app restarts on the client.
- Be revocable centrally (SUPER_ADMIN can force-logout any user).
- Detect and respond to refresh-token theft.
- Track each device with metadata (name, OS, browser, IP, geo, last-seen)
  so the user and SUPER_ADMIN can see "My Devices" / "Live sessions" pages.
- Be short-lived on the wire (access tokens) yet ergonomic (few real
  logouts).

Options considered:

1. **Long-lived JWTs** in localStorage — bad on every axis (XSS, no
   revocation).
2. **Server-side sessions** with a cookie — hard to scale statelessly.
3. **Access + refresh JWT pair, refresh token in HttpOnly cookie, rotation
   with reuse detection.**
4. **OAuth2/OIDC with an external identity provider.**

## Decision

We use option **3**: short-lived access JWTs plus rotating refresh tokens,
with **reuse detection at the family level**.

### Token model

| Token | Lifetime | Where it lives | Purpose |
|---|---|---|---|
| Access token | **15 min** | `Authorization: Bearer` header on every request | Stateless authorisation on the API |
| Refresh token | **7 days** (sliding) | `HttpOnly; Secure; SameSite=Strict` cookie on `/api/v1/auth` | Exchange for new access token |
| Session record | = refresh TTL | Redis (hot) + Postgres (durable) | Device tracking, revocation |

### Algorithm

- **Dev**: HS256, secret from `JWT_SECRET`.
- **Prod**: RS256 with keys under `JWT_PRIVATE_KEY_PATH` /
  `JWT_PUBLIC_KEY_PATH`; verifiers need only the public key.
- Header `kid` carries `JWT_KEY_ID`; verifiers pick the right key for
  rotation-friendly transitions.

Claims (validated by `jwt.NewParser` with strict `WithValidMethods`,
`WithIssuer`, `WithAudience`, `WithExpirationRequired`, `WithIssuedAt`):  
iss:  medicore
aud:  medicore-users
sub:  <user_id>
sid:  <session_id>
fid:  <family_id>   -- refresh chain family
org:  <organization_id>
br:   <branch_id>   -- may be null for org-wide roles
role: <role_name>
iat, exp, jti  

Access tokens deliberately do **not** carry email or name — reloaded from DB
on need. This keeps them short and reduces PII in logs.

### Refresh rotation with reuse detection

Every refresh call issues a **new** refresh token, marks the old one
`used_at = now()` in Postgres, and links the pair via `family_id`.

If a used refresh token is presented again (`used_at IS NOT NULL`), that is
**proof of theft**:

1. Revoke the entire `family_id`: mark every session and refresh in that
   family `revoked_at = now(), revoke_reason = 'reuse_detected'`.
2. Emit a high-priority notification to the user and SUPER_ADMIN.
3. Write an audit-log entry with the offending IP and UA.
4. Return `401 TOKEN_REUSE_DETECTED` and clear the refresh cookie.

The user must log in again on every device tied to that family.

### Rate limits

- `POST /api/v1/auth/login`: **5 attempts / 15 min per email**; **20 / 15
  min per IP**.
- `POST /api/v1/auth/refresh`: **60 / min per (user, device_fp)**.
- `POST /api/v1/auth/password/*`: **3 / hour per (email, IP)**.

See ADR-0003 for the hashing budget that shapes these numbers.

### Revocation

Three shapes:

- **Logout this device**: mark this session revoked. Access tokens are
  short-lived so they expire naturally within 15 min; for immediate effect
  the auth middleware checks a Redis blacklist for the token's `jti` on the
  hot path.
- **Logout everywhere**: mark all sessions for the user revoked; broadcast a
  `session.revoked` event on WebSocket to close live connections.
- **Admin force-logout**: SUPER_ADMIN endpoint on any user; audit-logged.

### Cookies

The refresh cookie:

- `HttpOnly` (JS cannot read it → XSS-safe).
- `Secure` (HTTPS-only) in staging/prod.
- `SameSite=Strict` (blocks CSRF for cookie-bearing auth flows).
- `Path=/api/v1/auth` (scope limited to auth endpoints).
- `Domain` unset in Compose so the browser uses the request host.

Access tokens live only in memory in the client (not in localStorage) to
keep them out of reach of XSS.

## Rationale

- Access + refresh pair is the mainstream pattern for stateless SPAs and
  mobile clients.
- Rotation limits the blast radius of a stolen refresh.
- Reuse detection is the specific trick that catches the theft.
- HttpOnly + SameSite=Strict + short-lived access token together mitigate
  the common web attack vectors.

Full OAuth2/OIDC was rejected for MVP because we do not need third-party
identity providers yet. Nothing here prevents adding an OIDC layer later.

## Consequences

### Positive

- Central revocation with fine granularity.
- Live "My Devices" / "Live sessions" experiences are straightforward.
- Theft is detected and the entire family neutered automatically.
- Access tokens are stateless — API scales horizontally.

### Negative

- Refresh path is stateful (Redis + DB). Redis outage forces a fallback to
  DB-only refresh with lower cache hit rate.
- Reuse detection requires strict `used_at` semantics and single-use tokens.
  Concurrency is handled with `SELECT ... FOR UPDATE` around the refresh
  row.

### Neutral

- Cookie-based refresh adds CSRF considerations to `/auth/refresh`. Our
  `SameSite=Strict` cookie plus a dedicated path make CSRF infeasible in
  practice.

## Guardrails

- `auth` middleware **must** call `jwt.NewParser` with
  `WithValidMethods([]string{alg})` — never accept `alg=none`.
- JWT parsing errors must not leak reasons to the client; always return a
  generic `401 UNAUTHENTICATED`.
- Refresh cookie is only ever written by the auth handler; nothing else
  touches it.
- Any code path that reads `used_at` on a refresh row must do so inside a
  transaction that also updates it, to avoid double-spend races.

## References

- IETF RFC 7519 (JSON Web Token).
- Auth0 blog, *Refresh Token Rotation and Automatic Reuse Detection*.
- OWASP Cheat Sheet, *JSON Web Token for Java*.
- OWASP ASVS 4.0, chapter V3 (Session Management).