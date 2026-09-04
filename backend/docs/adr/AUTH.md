## Pharmaciano:ERP — Production RBAC, Tenant, Authentication & Security Readiness

> **Purpose:** This document is the current implementation/security review and the final hardening plan for the Pharmaciano:ERP backend authentication, authorization, RBAC, tenant isolation, branch scope, sessions, Redis, JWT, and security controls.
>
> **Scope:** Only the security/authentication/authorization foundation is being evaluated here. Business modules such as Inventory, Sales, Purchases, Accounting, Suppliers, Customers, Reports, etc. are **not implemented yet and are intentionally out of scope**.
>
> **Important current-state correction:** There is currently **no `/permission` API and no permission list inside `/auth/me`**. The absence of these endpoints is not a security defect by itself. Backend authorization must remain server-side and authoritative. A future permission endpoint can be added as a UI capability/UX endpoint, but it must never become the security authority.

---

## 1. Executive Assessment

## Current overall condition

| Area | Rating | Status | Short description |
|---|---:|---|---|
| JWT design | **9.3/10** | 🟢 Done | Compact HS256 access JWT, algorithm/key pinning, `kid`, standard validation, no permissions |
| JWT size | **9.8/10** | 🟢 Done | Current token is ~800 B; comfortably below 4 KB hard cap |
| Access-token TTL | **9/10** | 🟢 Done | 15-minute access-token lifetime |
| Refresh-token design | **9.2/10** | 🟢 Done | Opaque high-entropy token, hash-at-rest, rotation, family reuse detection |
| Session authority | **8.8/10** | 🟢 Done | PostgreSQL authoritative session state with Redis cache |
| Immediate revocation | **9/10** | 🟢 Done | Session revocation invalidates otherwise-valid JWT sessions |
| Tenant isolation | **9/10** | 🟢 Done | Organization identity comes from verified principal; repository scoping is defense-in-depth |
| Branch isolation | **8/10** | 🟡 Verify/harden | Current branch-bound/org-wide model is good; multi-branch subset semantics need final code verification |
| RBAC default deny | **9/10** | 🟢 Done | Authorization denies by default |
| RBAC cache invalidation | **9.2/10** | 🟢 Done | Per-user `authz_version` + organization `rbac_generation` |
| Authorization resolver | **8.5/10** | 🟢 Done / verify | Redis → enforcer snapshot → PostgreSQL source-of-truth architecture |
| Login brute-force defense | **8/10** | 🟡 Improve | Dummy Argon2id + durable lockout + IP rate limit; account/email distributed limiter still needed |
| Password hashing | **8.5/10** | 🟢 Done / verify | Argon2id is in place; pepper rotation must be verified |
| Refresh cookie | **8.5/10** | 🟡 Harden | HttpOnly/SameSite/path scoped; production `Secure=true` required |
| CSRF strategy | **7/10** | 🟡 Harden | SameSite is the current browser defense; explicit strategy should be finalized |
| Security headers | **9/10** | 🟢 Done | CSP, HSTS, frame protection, MIME/referrer/permissions policies |
| Security audit trail | **6.5/10** | 🟡 Incomplete | Security events are logged, but durable audit producer is not fully wired |
| Auth/Authz metrics | **7/10** | 🟡 Incomplete | Platform telemetry exists; dedicated auth/authz metrics still need completion |
| MFA | **2/10** | 🔴 Incomplete | Routes exist but return 501; mandatory MFA is not implemented |
| 404/403 information policy | **8/10** | 🟡 Verify | Policy exists conceptually; resource-family behavior needs consistent implementation |
| Operational readiness | **7.8/10** | 🟡 Improve | Strong foundation; several production controls remain |
| **Overall security foundation** | **8.7/10** | 🟢 Strong | Good architecture; finish the listed hardening work before claiming 9+ |

---

## 2. What Is Already Implemented

The current implementation uses:

```text
PostgreSQL
    = durable security authority

Redis
    = distributed rate limiting
    + session cache
    + authorization cache

Argon2id
    = password hashing

Opaque random refresh token
    = refresh credential

HS256 JWT
    = short-lived access credential

In-memory RBAC enforcer
    = derived policy snapshot, NOT the durable authority

Middleware
    = authentication + authorization enforcement

Repository predicates
    = tenant/branch defense-in-depth

The protected request path is:

Request
  ↓
TLS / Reverse Proxy
  ↓
CORS / CSRF strategy / Rate Limit / Security Headers
  ↓
JWT Verification
  ↓
Session Validation
  ├── Redis session cache
  └── PostgreSQL PRIMARY on miss/error
  ↓
Principal
  ↓
Effective organization + branch scope
  ↓
Current authorization versions
  ↓
Authorization Resolver
  ├── Redis authorization cache
  └── derived enforcer snapshot
       ↓
     PostgreSQL-backed policy data
  ↓
RBAC Enforce(resource, action)
  ↓
Handler
  ↓
Service
  ↓
Repository tenant/branch predicates
  ↓
PostgreSQL
3. JWT — Current Condition
Implemented claim model

Current access tokens contain a compact security context similar to:

{
  "iss": "pharmaciano",
  "sub": "ca38286b-b262-4e08-92d0-c35fd786d845",
  "aud": "pharmaciano-users",
  "exp": 1788515479,
  "nbf": 1788514579,
  "iat": 1788514579,
  "jti": "eb470aca-1524-449e-adf0-1e15f829d604",
  "typ": "access",
  "av": 1,
  "org": "4c2baab3-4dbc-486f-a3d7-9f7331b01e6f",
  "sid": "6409ae36-8167-49e9-8cd7-d54517bdf9e7",
  "role": "SUPER_ADMIN",
  "stage": "verified",
  "status": "active"
}
Correct properties
alg=HS256 is pinned.
JOSE typ=JWT is required.
kid is required.
Issuer and audience are validated.
exp, nbf, and iat are validated.
sub and jti are present.
typ=access is checked.
authz_version is carried as av.
Organization and session identity are carried.
Permissions are not carried.
Passwords/secrets/refresh tokens are not carried.
Access token TTL is 15 minutes.
Hard token-size limit is 4096 bytes.
Token size

Current measured access token is approximately:

~800 bytes

Target:

Preferred: 600–1500 bytes
Hard limit: 4096 bytes

This is excellent.

Important JWT rule

Do not put this into the JWT:

{
  "permissions": [
    "... potentially hundreds/thousands of permissions ..."
  ]
}

Authorization is dynamic state and must not be frozen into a long-lived authorization document.

JWT authenticates a compact identity/session/security context.

4. /auth/me and Permission API — Current Decision
Current state

There is currently:

GET /auth/me

but:

NO /permission API
NO permissions array in /auth/me

Current /auth/me returns a small principal snapshot such as:

{
  "user_id": "...",
  "organization_id": "...",
  "session_id": "...",
  "role": "SUPER_ADMIN",
  "stage": "verified",
  "status": "active"
}

This is good.

Do not make /auth/me large just to expose permissions.

Recommended future endpoint

If the frontend needs permissions for UI decisions, add:

GET /api/v1/auth/me/permissions

Example:

{
  "success": true,
  "data": {
    "permissions": [
      "users:read",
      "users:create",
      "users:update",
      "rbac:read"
    ],
    "authz_version": 12
  }
}

For a multi-branch principal, the response should also expose the effective scope when useful to the frontend, for example:

{
  "permissions": [
    "users:read",
    "inventory:read"
  ],
  "authz_version": 12,
  "scope": {
    "organization_id": "...",
    "branch_ids": ["...", "..."]
  }
}
Critical security rule

The frontend may use this endpoint for:

show/hide menu
disable button
hide page
display navigation

but the backend must NEVER trust:

frontend permission state

The real check remains:

JWT
→ session
→ tenant/scope
→ current authorization
→ RBAC enforce

A malicious client can simply ignore the UI and call the API directly.

5. Refresh Token — Current Condition

Implemented:

crypto/rand
    ↓
256-bit opaque token
    ↓
Base64URL

PostgreSQL stores:

SHA-256(token)
user_id
session_id
family_id
expiry
used/revoked state
replacement state
reuse_detected_at

Refresh rotation uses a database transaction and row locking.

A reused token causes:

refresh token replay
      ↓
family revoked
      ↓
session revoked
      ↓
reuse_detected_at recorded
      ↓
TOKEN_REUSE_DETECTED

This is strong.

6. Refresh Cookie — Current Condition

Current browser cookie characteristics:

HttpOnly
SameSite=Strict
Path=/api/v1/auth

Good.

Required production state

Production must use:

Secure=true

Do not use development HTTP cookie configuration in production.

Prefer a host-only cookie unless a cross-subdomain architecture genuinely requires a domain attribute.

Important improvement

For browser clients, do not unnecessarily return the refresh token in JSON.

Prefer:

{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 900
}

with:

Set-Cookie: mc_refresh=...; HttpOnly; Secure; SameSite=Strict; ...

The JavaScript application should not need access to the refresh credential.

For non-browser clients, provide a deliberately designed API-client flow rather than accidentally exposing browser credentials.

7. Session Architecture — Current Condition

The session is the authority for:

"Is this authenticated session still valid?"

The access JWT can be cryptographically valid while the session has already been revoked.

Therefore:

Valid JWT
+
Revoked session
=
DENY
Current cache model
mc:sess:{session_id}:g{security_generation}

Redis is a performance layer.

PostgreSQL remains authoritative.

Correct failure behavior
Redis HIT
    ↓
use projection

Redis MISS
    ↓
PostgreSQL PRIMARY

Redis ERROR
    ↓
PostgreSQL PRIMARY

Malformed Redis data
    ↓
delete bad cache
    ↓
PostgreSQL PRIMARY

PostgreSQL security read fails
    ↓
FAIL CLOSED

This is the correct model.

8. Tenant Isolation — Current Condition

The authenticated principal supplies:

organization_id

The client must never be able to switch tenant by sending:

X-Organization-ID: another-org

or:

{
  "organization_id": "another-org"
}

and having the server accept it as authority.

Repository operations must continue to apply organization predicates:

WHERE organization_id = $trusted_org_id

Defense in depth is required:

Middleware
    ↓
Service authorization
    ↓
Repository tenant predicate

A bug in one layer should not automatically become cross-tenant data exposure.

9. Branch Scope — Current Condition and Required Final Model

The current implementation supports branch-bound users and organization-wide users.

The final production model should explicitly support:

type Principal struct {
    UserID         uuid.UUID
    OrganizationID uuid.UUID
    SessionID      uuid.UUID
    BranchIDs      []uuid.UUID
    AuthzVersion   int64
    Authenticated  bool
}

Semantics:

BranchIDs == nil
    → organization-wide scope

BranchIDs == [B1]
    → only B1

BranchIDs == [B1, B2, B7]
    → only B1/B2/B7

BranchIDs == empty non-nil
    → no branch access

Never derive authorization from:

X-Branch-ID

alone.

X-Branch-ID is a requested/narrowing context, not authority.

Example:

Principal:
  B1, B2

Request:
  X-Branch-ID: B2

Effective:
  B2

But:

Principal:
  B1, B2

Request:
  X-Branch-ID: B7

Effective:
  DENY

For an organization-wide principal:

Principal:
  nil

Request:
  X-Branch-ID: B2

Effective:
  B2

If no branch header exists:

org-wide principal
→ organization-wide operation

branch-bound principal
→ assigned branch scope

The exact behavior should be endpoint-specific where an operation requires a branch.

10. RBAC — Current Condition

RBAC currently follows:

Role
  ↓
Permissions
  ↓
User-role assignments
  ↓
Effective access

The policy engine is an in-memory derived snapshot.

That is acceptable.

The important invariant is:

PostgreSQL = authority
Enforcer = derived snapshot
Redis = cache

Never:

Redis = authority

and never:

Enforcer = independent authority
11. RBAC Cache Versioning — Current Condition

The system uses two authorization epochs:

users.authz_version

and:

organizations.rbac_generation

Cache identity includes:

organization
+
user
+
user authz version
+
organization RBAC generation

This is a strong design.

Example:

User:
authz_version = 8

Organization:
rbac_generation = 31

Cache:

mc:authz:v1:
org:O1:
user:U1:
uv:8:
og:31

If the user loses a role:

authz_version:
8 → 9

Old cache is automatically irrelevant.

If a role's permissions change:

rbac_generation:
31 → 32

Every old authorization cache entry for that organization becomes irrelevant without scanning every user.

12. Critical RBAC Invalidation Matrix

Every effective authorization mutation must invalidate authorization.

Mutation	Required invalidation
Assign role to user	User authz_version++
Revoke role	User authz_version++
Change user's branch scope	User authz_version++
Change user's organization scope	User authz_version++
Replace role permissions	Affected users + org rbac_generation++
Role activation/deactivation	Affected users + org generation
Role deletion	Affected users + org generation
Role priority change	Affected users + org generation
Role scope change	Affected users + org generation
Direct user permission change	User authz_version++
Permission activation/deactivation	All affected authorization contexts / org generation
Permission deletion	All affected authorization contexts / org generation

The update and the invalidation epoch must be in the same PostgreSQL transaction.

Never:

UPDATE role
COMMIT

then later
bump generation

because a crash between the two operations can leave stale authorization state.

Prefer:

BEGIN
  update role
  bump generation
COMMIT
13. SUPER_ADMIN Fast Path

Do not build a massive permission list for SUPER_ADMIN.

Preferred decision flow:

Authenticate
   ↓
Session valid?
   ↓
Account active?
   ↓
Tenant valid?
   ↓
Effective branch/scope valid?
   ↓
Current role/authority from trusted authorization state
   ↓
SUPER_ADMIN?
   ↓
Privileged allow path

Not:

if role == "SUPER_ADMIN" {
    return true
}

before authentication/session/scope checks.

SUPER_ADMIN should still be organization-scoped unless the architecture explicitly introduces a separate platform-global authority.

The fast path should be observable:

decision_path=super_admin

but never leak sensitive details.

14. RBAC Assignment Security

Role assignment is a security-sensitive operation.

Required sequence:

Actor authenticated
    ↓
Actor session valid
    ↓
Actor organization trusted
    ↓
Actor branch scope trusted
    ↓
Actor has RBAC management permission
    ↓
Target user belongs to actor's organization
    ↓
Target user is within actor's branch authority
    ↓
Requested role exists
    ↓
Target role is within actor's authority
    ↓
Requested branch scope is allowed
    ↓
Assign role + bump authz_version
    ↓
Commit atomically
Privilege hierarchy

Do not allow a normal administrator to grant a role that exceeds their authority.

A safe default rule is:

actor_priority > target_priority

for ordinary delegation, with an explicit SUPER_ADMIN override.

If equal-priority delegation is desired, it must be an explicit policy, not an accidental result of:

actorPriority < targetPriority
15. 403 vs 404 Policy

Use a consistent information-disclosure policy.

Recommended:

Unauthenticated
    → 401

Authenticated but capability denied
    → 403

Known resource but insufficient permission
    → 403

Resource outside tenant
    → 404

Resource outside branch scope
    → 404 where existence must be hidden

Resource does not exist
    → 404

For sensitive resources, avoid allowing an attacker to distinguish:

resource exists in another tenant

from:

resource does not exist

through a different status or error message.

Do not expose:

"You are forbidden from viewing user 123 because it belongs to organization X."

Prefer generic behavior.

16. Login Security and Lockout

Current strong controls include:

Unknown email
    ↓
dummy Argon2id verification

This helps reduce timing-based account enumeration.

Also:

wrong password
    ↓
durable failure counter
    ↓
temporary lockout

and:

IP rate limiting
Required final layered model

Use multiple independent controls:

                    Login
                      │
          ┌───────────┼───────────┐
          ↓           ↓           ↓
       IP limit   Account limit  Device/session
          │           │           │
          └───────────┼───────────┘
                      ↓
                 Risk signals
                      ↓
                Authentication
                      ↓
              Durable lockout
IP

Use Redis distributed counters.

Account/email

Never use raw email as a public Redis key if privacy is a concern.

Prefer:

HMAC(server_secret, normalized_email)

as the identifier.

Example:

rl:login:acct:{hmac_email}
Device

A device identifier may be used as a risk/rate-limit signal, but it must not become an authentication credential.

Location/network

IP geolocation is a risk signal, not proof of identity.

Do not block legitimate users solely because location changed.

17. Login Lockout Concerns

Permanent/simple lockouts create an account-denial-of-service problem.

An attacker should not be able to intentionally lock a victim's account forever by repeatedly submitting wrong passwords.

Use:

progressive temporary lockout

rather than an unbounded permanent lock.

Example policy:

5 failures
→ 1 minute

10 failures
→ 5 minutes

15 failures
→ 15 minutes

repeated abuse
→ progressively longer delay

Exact thresholds should be configurable and tested against the expected user population.

Successful authentication should reset the account's failure state.

Do not reset abuse controls merely because another dimension was successful.

18. Password Hashing

Argon2id is the correct family for password hashing.

Recommended credential model:

password_hash
algorithm
parameters
salt
pepper_version
created_at
updated_at
Pepper rotation

If pepper rotation is part of the target architecture, implement:

current pepper: P2
previous pepper: P1

Credential stores:

pepper_version = 1

During login:

verify using stored pepper version
        ↓
success?
        ↓
old pepper?
        ↓
rehash using current pepper
        ↓
store pepper_version=2

Never store the pepper in:

JWT
Redis
database
logs
API response

If an old pepper is unavailable, do not bypass verification. Use the defined password-reset/recovery path.

19. MFA — Main Remaining Authentication Gap

Current state:

/auth/mfa/setup   → 501
/auth/mfa/verify  → 501
/auth/mfa/disable → 501

This is the biggest remaining authentication gap.

Production recommendation

At minimum:

SUPER_ADMIN
ADMIN

must require MFA.

A stronger policy can include:

MANAGER
ACCOUNTANT

depending on business risk.

Recommended first implementation

TOTP:

Password
   ↓
TOTP
   ↓
MFA verified
   ↓
Session

Also issue one-time recovery codes.

Later:

WebAuthn / Passkeys

can provide stronger phishing-resistant authentication.

Important state

Do not let:

MFA_PENDING

obtain a normal fully-authorized session.

Use an explicit authentication state:

PASSWORD_VERIFIED
MFA_REQUIRED
MFA_VERIFIED
20. Step-Up Authentication

For highly sensitive operations, a normal 15-minute access token may not be sufficient.

Future high-risk operations could require recent MFA:

Change SUPER_ADMIN role
      ↓
recent MFA required

or:

Change organization security settings
      ↓
recent MFA required

or:

Disable MFA
      ↓
current password + recent MFA

This should be implemented as a separate policy layer rather than scattering password/MFA checks across handlers.

21. CSRF

Current browser strategy:

HttpOnly refresh cookie
+
SameSite=Strict

This is a strong baseline for a same-site browser architecture.

However, document the deployment topology explicitly.

If the frontend/API become cross-site and require:

SameSite=None

then:

Secure=true

is mandatory and a dedicated CSRF strategy should be considered.

For sensitive cookie-authenticated state-changing operations, a robust option is:

SameSite
+
Origin/Referer validation where appropriate
+
CSRF token/double-submit token where cross-site requirements exist

Do not add complexity without a real deployment need, but do not assume SameSite alone remains sufficient if the architecture changes.

22. Security Headers

Current security headers are strong.

Current examples include:

Content-Security-Policy: default-src 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: geolocation=(), microphone=(), camera=(), payment=(), usb=()
X-Permitted-Cross-Domain-Policies: none
X-XSS-Protection: 0

HSTS:

Strict-Transport-Security:
max-age=31536000; includeSubDomains; preload

is appropriate only when the production domain and all included subdomains are guaranteed HTTPS.

Do not use aggressive HSTS/preload settings casually for development.

23. Trusted Proxy / Client IP

Rate limiting and security auditing depend on correct client IP.

Never blindly trust:

X-Forwarded-For
X-Real-IP

from arbitrary clients.

Only trusted reverse proxies should be allowed to establish the client IP chain.

Example:

Internet
   ↓
Trusted Nginx / Load Balancer
   ↓
Go API

The application must know which proxy networks are trusted.

Otherwise an attacker can send:

X-Forwarded-For: 1.2.3.4

and bypass IP rate limiting.

24. Redis — Production Rules

Redis is appropriate for:

rate limiting
session cache
authorization cache
singleflight coordination/local stampede reduction

Redis must NOT be authoritative for:

session revocation
refresh token replay state
password state
authz_version
RBAC source of truth
tenant authority
Failure policy
Performance cache failure
Redis down
   ↓
PostgreSQL fallback
Security source failure
PostgreSQL unavailable
   ↓
FAIL CLOSED

Do not silently allow requests because the security database cannot be reached.

25. Authorization Cache Design

Recommended cache key:

mc:authz:v1:
org:{organization_id}:
user:{user_id}:
uv:{authz_version}:
og:{rbac_generation}:
scope:{effective_scope_id}

If branch scope affects permissions, the cache identity MUST distinguish it.

Do not use:

org + user

alone when the same user can operate under different branch scopes.

If branch IDs can be many, prefer a canonical/stable scope identifier or normalized scope hash rather than producing unbounded key lengths.

26. Authorization Cache Corruption

Current intended behavior is correct:

Redis value
   ↓
JSON parse
   ↓
invalid
   ↓
increment cache error metric
   ↓
delete corrupted value
   ↓
resolve from authoritative/derived policy source

Never treat malformed authorization data as:

ALLOW

or silently count it as a successful cache hit.

27. Authorization Performance

Target request path:

JWT verify
    ↓
session Redis GET
    ↓
authz state/version
    ↓
authorization Redis GET
    ↓
enforce

A cache hit should avoid repeated expensive authorization queries.

On cache miss:

singleflight
    ↓
one resolver operation

rather than:

100 concurrent requests
    ↓
100 identical DB/policy queries

This is important under horizontal scaling.

28. Database Rules for Security Reads

Security-sensitive reads should use PostgreSQL PRIMARY.

Examples:

session status
authz_version
RBAC authority
role assignment
credential state
refresh token state
password reset token state
account status

Do not use an eventually-consistent read replica for a security decision.

A replica can be used for cosmetic data such as:

active devices display

when a small amount of staleness is acceptable.

29. Transaction Rules

Security state changes must be atomic.

Example:

BEGIN

assign role
bump users.authz_version
possibly bump organizations.rbac_generation

COMMIT

Not:

assign role
COMMIT

later:
bump authz version

The same rule applies to:

role permission updates
role activation/deactivation
branch scope changes
organization changes
session revocation
refresh token rotation
password reset
30. Security Audit

Current application logging exists, but durable audit persistence is incomplete.

Implement a durable security audit pipeline.

Recommended events:

LOGIN_SUCCESS
LOGIN_FAILURE
ACCOUNT_LOCKED

SESSION_CREATED
SESSION_REVOKED
LOGOUT
LOGOUT_ALL

REFRESH_SUCCESS
REFRESH_FAILURE
REFRESH_REUSE_DETECTED

PASSWORD_CHANGED
PASSWORD_RESET_REQUESTED
PASSWORD_RESET_COMPLETED

MFA_ENABLED
MFA_DISABLED
MFA_FAILED

ROLE_ASSIGNED
ROLE_REVOKED
ROLE_PERMISSION_CHANGED
ROLE_CREATED
ROLE_UPDATED
ROLE_DELETED

USER_BRANCH_CHANGED
USER_STATUS_CHANGED

AUTHZ_DENIED
HIGH_RISK_OPERATION

Audit records should include safe context:

request_id
timestamp UTC
actor user ID
organization ID
session ID where appropriate
event type
result
resource type
resource ID where safe
client IP / network metadata according to privacy policy
user-agent/device metadata where appropriate
reason code

Never store:

password
access token
refresh token
reset token
MFA secret
pepper
signing secret
31. Authentication/Authorization Metrics

Add dedicated bounded-cardinality metrics.

Recommended:

auth_login_success_total
auth_login_failure_total
auth_login_locked_total

auth_refresh_success_total
auth_refresh_failure_total
auth_refresh_reuse_total

auth_session_cache_hit_total
auth_session_cache_miss_total
auth_session_cache_error_total

authz_allow_total
authz_deny_total

authz_cache_hit_total
authz_cache_miss_total
authz_cache_error_total

authz_resolution_error_total

authz_version_bump_total
rbac_generation_bump_total

Good labels:

module
action
result
decision_path
reason_code

Bad labels:

email
IP
user_id
session_id
JWT
refresh token

High-cardinality labels can destroy Prometheus performance.

32. Tracing

Add security spans around:

auth.jwt.verify
auth.session.lookup
auth.session.cache
auth.authorization.resolve
auth.authorization.enforce
auth.refresh.rotate
auth.password.verify

Do not put secrets into trace attributes.

Safe:

authz.result = deny
authz.module = users
authz.action = update
authz.decision_path = cache

Unsafe:

jwt = ...
refresh_token = ...
password = ...
33. /auth/me/permissions Recommended Design

This endpoint is optional from a security perspective but useful for the frontend.

Recommended:

GET /api/v1/auth/me/permissions
Authorization: Bearer <access_token>

Server:

JWT
 ↓
session validation
 ↓
current user/authz version
 ↓
effective branch context
 ↓
authorization resolver
 ↓
return current UI permission snapshot

Response:

{
  "success": true,
  "data": {
    "permissions": [
      "users:read",
      "users:create",
      "users:update"
    ],
    "authz_version": 12
  }
}
Do not cache this response in a way that defeats authorization changes

The backend should always use current version-aware authorization state.

The frontend can cache it until:

authz_version changes

or:

session changes

but the backend remains authoritative.

34. No Business Modules Yet — Correct Decision

Do not delay the security foundation until Inventory/Sales/etc. are implemented.

Build the security platform first.

Recommended order:

1. Authentication
2. Session management
3. Tenant context
4. Branch scope
5. RBAC
6. Authorization resolver
7. Rate limiting
8. Audit
9. MFA
10. Security observability
11. Security integration tests
12. Then business modules

When business modules arrive, every module should plug into the same authorization contract:

middleware.RBAC("inventory", "read")
middleware.RBAC("sales", "create")
middleware.RBAC("sales", "refund")
middleware.RBAC("accounting", "post")

The exact business permissions can be added later.

35. Standard Authorization Contract for Future Modules

Use canonical permission identifiers:

{resource}:{action}

Examples:

users:read
users:create
users:update
users:delete

inventory:read
inventory:create
inventory:update

sales:read
sales:create
sales:return

purchases:read
purchases:create

accounting:read
accounting:post

reports:read

Avoid inconsistent names such as:

user.read
USER_READ
users-read
read_users

Canonical naming prevents policy drift.

36. Repository Defense-in-Depth

Even if middleware says:

ALLOW

the repository should still enforce tenant/branch constraints where the domain requires them.

Example:

SELECT ...
FROM users
WHERE id = $1
  AND organization_id = $trusted_org
  AND (
       branch_id IS NULL
       OR branch_id = ANY($trusted_branch_ids)
  );

Never:

SELECT * FROM users WHERE id = $client_id;

for tenant-scoped data.

The repository must not accept organization/branch authority directly from an untrusted request body.

37. Testing Required Before 9+
Authentication tests
valid login
invalid password
unknown email
dummy Argon2 timing path
lockout
lockout expiry
successful login resets failure state
inactive account
unverified account
deleted account
expired JWT
invalid JWT signature
wrong algorithm
missing kid
wrong issuer
wrong audience
invalid typ
invalid nbf
oversized JWT
key rotation
previous-key verification
retired-key rejection
Refresh tests
valid refresh
rotation
old token reuse
concurrent refresh
expired refresh
revoked refresh
revoked session
revoked user
family revocation
session expiry
replacement-token consistency
Session tests
cache hit
cache miss
cache error
malformed cache
revoked session with valid JWT
security-generation change
Redis unavailable
PostgreSQL unavailable
RBAC tests
allow
deny
no role
role with no permission
permission removed
role removed
role disabled
user authz version change
organization generation change
cache invalidation
cache corruption
cache stampede
SUPER_ADMIN fast path
non-SUPER_ADMIN cannot exceed authority
equal-priority behavior
cross-organization assignment
branch-bound assignment
multi-branch subset
organization-wide scope
Tenant tests

For every tenant-scoped repository:

Org A user
→ Org A object = visible

Org A user
→ Org B object = invisible

Do not rely only on service-level tests.

Branch tests
B1 user
→ B1 object = allowed

B1 user
→ B2 object = denied/404 according to policy

B1+B2 user
→ B1 = allowed
→ B2 = allowed
→ B3 = denied

org-wide
→ authorized branch = allowed
38. Concurrency Tests

Security code must be tested under race conditions.

Important tests:

100 concurrent refresh requests

Expected:

exactly one refresh wins
remaining replay submissions
→ reuse detection

Also test:

concurrent role assignment
concurrent role revoke
concurrent authz mutation
concurrent login failures
concurrent session revocation

Run:

go test -race ./...
39. Operational Security

Production configuration must ensure:

TLS enabled
Secure cookies
strong secrets
no committed credentials
PostgreSQL TLS where required
Redis authentication/TLS where required
metrics endpoint protected/internal
debug endpoints disabled
development defaults disabled
trusted proxy configured
CORS restricted
HSTS production-only
structured logging
audit retention
backup/restore tested
database migrations controlled
secret rotation documented
40. Secrets

Never commit:

JWT secret
refresh secret
Argon2 pepper
DB password
Redis password
field-encryption key
initial admin password
SMTP credentials

Use:

environment secret manager
cloud secret manager
Vault/KMS

depending on deployment.

A development .env.example should contain placeholders, never real credentials.

41. Access Token Key Rotation

Current design:

active key
+
previous verification-only keys

Rotation:

Deploy new key ID + secret
        ↓
new tokens use new key
        ↓
old key remains verification-only
        ↓
wait > maximum access-token TTL
        ↓
remove old key

For emergency compromise:

remove compromised key immediately
        ↓
redeploy
        ↓
affected JWTs fail verification
        ↓
revoke sessions if refresh/session compromise is suspected
42. Refresh Token and Session Rotation Relationship

The important security chain is:

Session
   │
   └── Refresh Token Family
           │
           ├── Token 1
           ├── Token 2
           ├── Token 3
           └── Token N

If an old token is reused:

Token 1 reused
     ↓
family compromised
     ↓
revoke family
     ↓
revoke session
     ↓
record reuse

This prevents a stolen refresh token from silently remaining useful after legitimate rotation.

43. Failure-Closed Rules

The system should fail closed for security decisions.

Examples:

JWT invalid
→ deny

session cannot be verified
→ deny

authorization cannot be resolved
→ deny

tenant cannot be established
→ deny

branch authority cannot be established
→ deny

PostgreSQL security state unavailable
→ deny

malformed authorization cache
→ ignore/delete/fallback

Redis unavailable
→ use PostgreSQL where safe

Never:

"Redis is down, therefore allow."
44. What Should NOT Be Added

Do not add technologies merely for appearance.

Not required:

OAuth/OIDC
Casbin package
microservices
JWT blacklist for every request
Kafka
service mesh
Kubernetes
another database
another JWT library

unless a real requirement appears.

The current architecture can scale horizontally without these.

45. Current Remaining Gaps
🔴 Critical
1. MFA not implemented

Current:

501

Required:

TOTP
recovery codes
MFA enforcement for privileged accounts
MFA-aware login/session state
2. Browser refresh token returned in JSON

Change browser flow to:

refresh token → HttpOnly cookie only
3. Multi-branch subset semantics need final implementation verification

Ensure:

Principal.BranchIDs

and authorization cache/resolver identity correctly represent multiple branches.

🟠 High priority
4. Account/email distributed rate limiter

Current:

IP limit
+
account lockout

Target:

IP
+
account/email
+
device/risk
+
route
5. Argon2 pepper rotation

Verify/implement:

pepper_version
current pepper
previous pepper
login-time rehash
6. Durable audit pipeline

Finish:

audit event
→ queue
→ durable audit storage
7. Dedicated authentication/authorization metrics

Add the counters listed above.

8. Explicit CSRF production policy

Document deployment topology and implement additional CSRF protection if cross-site cookie usage becomes necessary.

🟡 Medium priority
9. Step-up authentication

For highly sensitive administration.

10. WebAuthn/passkeys

After TOTP is stable.

11. Risk scoring

Later.

12. Security cleanup/retention jobs

Expired:

sessions
refresh tokens
password reset tokens
audit records

must have documented retention/cleanup policies.

46. Recommended Implementation Order

Do not implement everything simultaneously.

Phase 1 — Security foundation freeze
1. Finalize Principal
2. Finalize tenant context
3. Finalize BranchIDs/effective branch scope
4. Finalize RBAC permission naming
5. Finalize authorization resolver
6. Finalize Redis cache keys
7. Finalize authz invalidation matrix
Phase 2 — Authentication hardening
8. Remove browser refresh token JSON exposure
9. Verify cookie production settings
10. Implement account/email rate limiter
11. Verify Argon2 pepper rotation
12. Implement MFA
Phase 3 — Authorization hardening
13. Verify SUPER_ADMIN fast path
14. Verify role hierarchy
15. Verify cross-org denial
16. Verify branch subset
17. Verify 403/404 policy
18. Verify every RBAC mutation increments correct epoch
Phase 4 — Observability
19. Dedicated auth metrics
20. Dedicated authz metrics
21. Security tracing
22. Durable audit pipeline
Phase 5 — Security verification
23. Integration tests
24. Concurrency tests
25. Race tests
26. Failure-injection tests
27. Tenant isolation tests
28. Branch isolation tests
29. Redis failure tests
30. PostgreSQL failure tests
Phase 6 — Production gate
31. Secret audit
32. TLS audit
33. Cookie audit
34. Proxy/IP audit
35. CORS/CSRF audit
36. Header audit
37. Migration audit
38. Backup/restore test
39. Load test
40. Security review

Only after this should business modules be added.

47. Final 9+/10 Checklist
Authentication
 Argon2id configured and benchmarked
 Password policy centralized
 Argon2 pepper current/previous rotation implemented
 Dummy verification for unknown accounts
 Durable progressive lockout
 IP rate limiting
 Account/email distributed rate limiting
 Device/risk signal where appropriate
 Generic authentication errors
 Mandatory MFA for SUPER_ADMIN
 Mandatory MFA for ADMIN
 Recovery codes
 Step-up authentication policy for critical operations
 Inactive/deleted accounts rejected
JWT
 HS256 algorithm pinned
 kid required
 Key rotation implemented
 Previous-key verification window
 iss validated
 aud validated
 exp validated
 nbf validated
 iat validated
 jti present
 typ=access
 authz_version
 session ID
 organization ID
 no permissions
 no secrets
 no refresh token
 <4 KB hard limit
 regression test for token size
Refresh Tokens
 cryptographically random
 256-bit entropy
 opaque
 SHA-256 hash at rest
 HttpOnly browser cookie
 Secure production cookie
 SameSite policy documented
 refresh rotation
 token family
 replay detection
 reuse revokes family
 reuse revokes session
 FOR UPDATE
 guarded token consumption
 concurrent refresh test
Sessions
 PostgreSQL authority
 Redis cache
 generation/version
 immediate revocation
 absolute expiry
 idle/last-used policy where required
 session listing
 logout current session
 logout all
 revoke individual other session
 Redis failure fallback
 PostgreSQL failure fail-closed
 cookie expiry synchronized with session lifetime
Tenant Isolation
 organization derived from trusted principal
 no client-selected organization authority
 service-level scope checks
 repository-level tenant predicates
 cross-tenant tests
 404 masking for sensitive cross-tenant resources
 organization-scoped RBAC
 organization generation invalidation
Branch Isolation
 Principal has effective branch scope
 single-branch users supported
 multi-branch subsets supported
 organization-wide scope supported
 X-Branch-ID only narrows scope
 client cannot expand branch scope
 repository branch predicates
 branch-aware authorization cache
 branch-aware RBAC role assignment
 cross-branch tests
 branch-to-org-wide transitions tested
 branch removal invalidates authorization
RBAC
 default deny
 canonical resource:action
 centralized authorization resolver
 PostgreSQL source of truth
 enforcer is derived snapshot only
 Redis is cache only
 SUPER_ADMIN fast path after security validation
 role hierarchy
 role assignment authority check
 equal-priority policy explicit
 cross-org role assignment denied
 branch-scope role assignment denied where inappropriate
 authz_version
 rbac_generation
 every effective mutation invalidates
 cache corruption fallback
 singleflight stampede protection
/auth/me
 small response
 identity/session context only
 no huge permission array
 no secret
 no refresh token
Permission API
 /auth/me/permissions only if frontend needs it
 current server-side permissions
 authz version included
 branch scope represented if needed
 frontend never trusted for enforcement
 backend still enforces every protected operation
Security Headers
 CSP
 HSTS production-only
 X-Content-Type-Options
 X-Frame-Options
 Referrer-Policy
 Permissions-Policy
 correct CORS
 trusted proxy configuration
Observability
 auth login metrics
 auth refresh metrics
 session cache metrics
 authorization metrics
 cache metrics
 authz version metrics
 RBAC generation metrics
 security traces
 bounded Prometheus labels
 no secrets in logs/traces
Audit
 authentication events
 session events
 password events
 MFA events
 RBAC events
 branch/tenant scope events
 security denials
 refresh replay events
 durable persistence
 retention policy
 no secrets/tokens in audit
Reliability
 PostgreSQL primary for security reads
 Redis fallback behavior
 fail-closed security decisions
 transactionally atomic authorization mutation
 concurrency tests
 go test ./...
 go test -race ./...
 go vet ./...
 integration tests
 failure-injection tests
 load tests
 migration tests
48. 9+/10 Definition of Done

Pharmaciano:ERP should be considered 9+/10 security foundation only when all of the following are true:

JWT
  ✓ compact
  ✓ signed
  ✓ rotated
  ✓ validated
  ✓ no permissions

Authentication
  ✓ password hardened
  ✓ lockout
  ✓ layered rate limiting
  ✓ MFA for privileged users

Session
  ✓ PostgreSQL authority
  ✓ Redis acceleration
  ✓ immediate revocation
  ✓ refresh rotation
  ✓ replay detection

Tenant
  ✓ organization isolation
  ✓ repository defense-in-depth

Branch
  ✓ explicit effective scope
  ✓ multi-branch subset
  ✓ no client escalation

RBAC
  ✓ default deny
  ✓ centralized enforcement
  ✓ privilege hierarchy
  ✓ SUPER_ADMIN fast path
  ✓ versioned invalidation
  ✓ cache-safe authorization

Security
  ✓ secure cookies
  ✓ CSRF strategy
  ✓ trusted proxy
  ✓ security headers
  ✓ secrets management

Observability
  ✓ auth metrics
  ✓ authz metrics
  ✓ tracing
  ✓ durable audit

Testing
  ✓ concurrency
  ✓ tenant isolation
  ✓ branch isolation
  ✓ RBAC mutation invalidation
  ✓ Redis failures
  ✓ PostgreSQL failures
  ✓ race detector
49. Final Target Architecture

The final security architecture should be:

                         INTERNET
                            │
                            ▼
                  ┌───────────────────┐
                  │ TLS / Proxy / WAF │
                  └─────────┬─────────┘
                            │
                            ▼
              ┌─────────────────────────────┐
              │ Gin Security Middleware     │
              │                             │
              │ CORS                        │
              │ Security Headers             │
              │ Trusted Proxy                │
              │ Rate Limiting                │
              │ CSRF strategy                │
              └──────────────┬──────────────┘
                             │
                             ▼
                  ┌────────────────────┐
                  │ JWT Verification   │
                  │ HS256 + kid        │
                  └─────────┬──────────┘
                            │
                            ▼
                  ┌────────────────────┐
                  │ Session Validation │
                  │                    │
                  │ Redis               │
                  │    ↓ miss/error     │
                  │ PostgreSQL PRIMARY │
                  └─────────┬──────────┘
                            │
                            ▼
                    ┌───────────────┐
                    │   Principal   │
                    │               │
                    │ user          │
                    │ organization  │
                    │ session       │
                    │ branch scope  │
                    │ authz version │
                    └───────┬───────┘
                            │
                            ▼
               ┌──────────────────────────┐
               │ Effective Scope Resolver│
               │                          │
               │ organization             │
               │ branch subset            │
               └────────────┬─────────────┘
                            │
                            ▼
               ┌──────────────────────────┐
               │ Authorization Resolver   │
               │                          │
               │ Redis AuthZ Cache        │
               │        ↓ miss            │
               │ Enforcer Snapshot        │
               │        ↓                 │
               │ PostgreSQL-derived state │
               └────────────┬─────────────┘
                            │
                            ▼
                    ┌───────────────┐
                    │ RBAC Enforce  │
                    │ resource:act  │
                    └───────┬───────┘
                            │
                   ┌────────┴────────┐
                   │                 │
                  DENY              ALLOW
                   │                 │
                 403/404             ▼
                              Handler / Service
                                      │
                                      ▼
                              Repository Layer
                                      │
                            ┌─────────┴─────────┐
                            │                   │
                      Tenant Predicate    Branch Predicate
                            │                   │
                            └─────────┬─────────┘
                                      │
                                      ▼
                               PostgreSQL
50. Final Recommendation

Do not start Inventory/Sales yet solely to test RBAC.

First finish the security platform.

The immediate engineering priority should be:

1. Finalize multi-branch Principal/scope
2. Verify every RBAC invalidation path
3. Verify PostgreSQL is the only authorization authority
4. Verify SUPER_ADMIN fast path
5. Implement account/email rate limiting
6. Implement Argon2 pepper rotation if absent
7. Remove browser refresh token from JSON
8. Implement mandatory MFA
9. Complete durable audit
10. Complete auth/authz metrics
11. Finish tenant + branch adversarial tests
12. Run concurrency/failure/race testing
13. Security review

After these are complete, the authentication + authorization foundation should reasonably move from the current ~8.7/10 into the 9+/10 production-security range.

The absence of a permission endpoint today is not a problem. Keep /auth/me small. Add /auth/me/permissions only when the frontend needs a current UI permission snapshot; it must remain a convenience/read model, never the backend authorization mechanism.