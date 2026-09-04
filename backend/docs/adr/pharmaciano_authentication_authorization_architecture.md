# Pharmaciano:ERP — Production-Grade Authentication & Authorization

> **Status:** Greenfield architecture plan  
> **Scope:** Authentication + Authorization for a high-security, multi-tenant pharmacy ERP  
> **Target:** Fast, reliable, horizontally scalable, auditable, secure under high concurrency and large user volume  
> **Architecture:** Hybrid — short-lived signed access JWT + stateful server session + opaque rotating refresh token + PostgreSQL authorization source of truth + Redis cache

---

## 1. Executive Architecture

Pharmaciano should **not** use purely stateless JWT authentication and should **not** put permissions inside the access JWT.

Recommended architecture:

```text
REQUEST
   │
   ▼
TLS / Reverse Proxy / Request Limits
   │
   ▼
CORS / CSRF / Rate Limiting / Security Headers
   │
   ▼
JWT Verification
   │
   ▼
Session Validation
   │
   ▼
Principal Creation
   │
   ▼
Effective Organization + Branch Context
   │
   ▼
Current Authorization Versions
   │
   ▼
┌─────────────────────────────────────────┐
│         AuthorizationResolver            │
│                                         │
│ Redis cache ──MISS/ERROR──► PostgreSQL  │
│                         PRIMARY         │
└──────────────────────┬──────────────────┘
                       │
                       ▼
              Effective PermissionSet
                       │
                       ▼
              Enforce(resource, action)
                   │           │
                 ALLOW        DENY
                   │           │
                   ▼           ▼
                Service     403/404
                   │
                   ▼
          Scoped Repository Query
                   │
                   ▼
              PostgreSQL
```

### Core principles

1. **JWT authenticates; it does not authorize.**
2. **A server-side session provides revocation and session control.**
3. **PostgreSQL is the security source of truth.**
4. **Redis is a performance cache, never the authority.**
5. **Authorization is resolved server-side.**
6. **Permissions are not stored in the JWT.**
7. **Organization and branch scope are part of authorization context.**
8. **`authz_version`/authorization generations make cached authorization safely replaceable.**
9. **Refresh tokens are opaque, high-entropy, hashed, rotated and family-protected.**
10. **Security-sensitive reads use the PostgreSQL primary.**
11. **Every effective authorization mutation changes the relevant invalidation/version state atomically.**
12. **Default deny.**
13. **Security-source failures fail closed.**
14. **Repository tenant/branch predicates remain defense-in-depth, not the main RBAC engine.**

---

# 2. Authentication vs Authorization

## Authentication

Authentication answers:

> **Who is this caller?**

It verifies:

- password/credential proof,
- token signature,
- token lifetime,
- issuer/audience,
- session validity,
- account lifecycle,
- revocation state.

It produces a trusted **Principal**.

Example:

```go
type Principal struct {
    UserID         uuid.UUID
    OrganizationID uuid.UUID
    SessionID      uuid.UUID
    // nil = organization-wide; non-nil = explicit allowed branch subset.
    BranchIDs      []uuid.UUID
    AuthzVersion   int64
    Authenticated  bool
}
```

## Authorization

Authorization answers:

> **Is this authenticated principal allowed to perform this action on this resource in this organization and branch?**

Conceptually:

```text
Can(
    user,
    organization,
    branch,
    resource,
    action
)
```

Example:

```text
Can(
    U1,
    O1,
    B1,
    "inventory",
    "update"
)
```

returns:

```text
ALLOW
```

or:

```text
DENY
```

Authentication must happen before authorization.

---

# 3. Why Hybrid Authentication

A purely stateless design would be:

```text
JWT → verify signature → trust everything in token
```

This is problematic when roles/permissions change.

A large JWT containing permissions also creates:

- stale authorization,
- large request headers,
- difficult immediate revocation,
- repeated permission data,
- more proxy/header overhead.

The hybrid model separates responsibilities:

```text
Access JWT
    ↓
identity + session + security context

Server session
    ↓
revocation + session lifecycle

Refresh token
    ↓
long-lived authentication continuity

PostgreSQL
    ↓
security truth

Redis
    ↓
fast authorization/session/rate-limit cache

AuthorizationResolver
    ↓
current effective permissions
```

This provides the speed of JWT verification while retaining server-side control.

---

# 4. Authentication Lifecycle

## Login

```text
CLIENT
  │
  │ POST /auth/login
  │ email + password
  ▼
IP / account rate limit
  │
  ├── exceeded → 429
  │
  ▼
Normalize email
  │
  ▼
Credential lookup on PRIMARY
  │
  ├── unknown
  │     └── dummy Argon2id verification
  │
  ▼
Check lock state
  │
  ├── locked → 423 + bounded retry information
  │
  ▼
Argon2id verification
  │
  ├── FAIL
  │    ├── atomic failure increment
  │    ├── lock if threshold reached
  │    ├── audit event
  │    └── generic 401
  │
  ▼
Check account status
  │
  ├── disabled/suspended → deny
  │
  ▼
BEGIN TRANSACTION
  ├── create session
  ├── create refresh-token family
  ├── store refresh-token hash
  ├── reset login failure state
  └── audit success
  │
  ▼
COMMIT
  │
  ▼
Mint short-lived access JWT
  │
  ▼
Set refresh token as HttpOnly cookie
  │
  ▼
Return access token / response
```

---

# 5. Login Failure and Lockout

Never reveal whether an email exists.

Bad:

```text
Email not found
```

Good:

```text
Invalid credentials
```

For unknown accounts, a dummy Argon2id verification can reduce timing-based enumeration.

## Atomic failure tracking

Do not use:

```text
SELECT failed_attempts
failed_attempts++
UPDATE failed_attempts
```

because concurrent requests can overwrite increments.

Use atomic SQL:

```sql
UPDATE credentials
SET failed_login_attempts = failed_login_attempts + 1,
    updated_at = now()
WHERE id = $1;
```

Locking decisions must be consistent with the same transaction/atomic state model.

## Layered defense

Do not rely only on account lockout:

```text
IP rate limit
+
account/email rate limit
+
progressive delay
+
atomic failure counter
+
temporary lock
+
MFA
```

Otherwise an attacker can deliberately lock legitimate users.

---

# 6. Detailed Failure Flow

```text
REQUEST
  │
  ▼
Request validation
  ├── malformed → 400
  │
  ▼
Rate limit
  ├── exceeded → 429
  │
  ▼
JWT
  ├── missing → 401
  ├── malformed → 401
  ├── bad signature → 401
  ├── unexpected algorithm → 401
  ├── bad kid → 401
  ├── expired → 401
  ├── bad issuer → 401
  └── bad audience → 401
  │
  ▼
SESSION
  ├── missing → 401
  ├── revoked → 401
  ├── expired → 401
  ├── wrong user/session → 401
  │
  ▼
ACCOUNT
  ├── disabled → deny
  └── suspended → deny
  │
  ▼
EFFECTIVE BRANCH
  ├── invalid → 403
  └── outside scope → 403
  │
  ▼
AUTHORIZATION RESOLVER
  ├── Redis HIT → continue
  ├── Redis MISS → PostgreSQL PRIMARY
  ├── Redis ERROR → PostgreSQL PRIMARY
  └── PostgreSQL failure → FAIL CLOSED
  │
  ▼
ENFORCE
  ├── permission absent → 403
  │
  ▼
SERVICE
  ├── business-rule failure → 4xx
  │
  ▼
REPOSITORY
  ├── scoped resource absent → 404
  └── DB failure → 5xx
  │
  ▼
SUCCESS
```

---

# 7. Access JWT Design

The access JWT should be a **compact signed authentication artifact**.

Target:

```text
~700–1500 bytes
```

Hard application limit:

```text
4096 bytes
```

A token approaching the limit should be treated as a design warning.

## Recommended claims

Example:

```json
{
  "iss": "pharmaciano-api",
  "aud": "pharmaciano-web",
  "sub": "3b7c2f7a-7d5d-4d2f-a4f1-2b9a3c9f7e21",
  "sid": "7e2a5d65-8e15-4b5b-b2a0-cc8f0f4c4b31",
  "org": "2bb7c5a2-5d6c-46c7-96dd-2d8e7f7d1e11",
  "azv": 12,
  "jti": "9d3b5f...",
  "typ": "access",
  "iat": 1788510000,
  "nbf": 1788510000,
  "exp": 1788510900,
  "kid": "access-2026-09"
}
```

### Claim purposes

| Claim | Purpose |
|---|---|
| `iss` | issuer |
| `aud` | intended audience |
| `sub` | user identity |
| `sid` | server-side session identity |
| `org` | organization context |
| `azv` | authorization-version snapshot |
| `jti` | token identifier |
| `typ` | token type |
| `iat` | issued-at |
| `nbf` | not-before |
| `exp` | expiration |
| `kid` | signing-key identifier |

Do **not** store:

```text
permissions[]
password
password hash
refresh token
large profile
menus
UI configuration
large branch lists
unnecessary PII
```

---

# 8. JWT Security

For this architecture:

```text
HS256
```

with:

```text
current secret
+
previous secret during controlled rotation
+
kid
```

The JWT parser must pin the expected algorithm.

Reject:

```text
alg = none
unexpected algorithms
unknown kid
invalid signature
```

Verification order:

```text
1. token size limit
2. parse token structure
3. inspect header
4. validate typ
5. validate kid
6. select only configured key
7. validate expected algorithm
8. verify signature
9. validate issuer
10. validate audience
11. validate exp
12. validate nbf
13. validate iat policy
14. validate sub
15. validate sid
16. validate claim types
17. create Principal
```

Parsing a JWT is not the same as trusting it.

---

# 9. JWT Size

A JWT consists of:

```text
Base64URL(header)
.
Base64URL(payload)
.
Base64URL(signature)
```

Typical compact token:

```text
Header      ≈ 60–120 bytes
Payload     ≈ 300–700 bytes
Signature   = 32 raw bytes for HMAC-SHA256
            ≈ 43 Base64URL characters in compact JWT form
```

A well-designed token can therefore remain around:

```text
600–1500 bytes
```

An access JWT around 800 bytes is excellent.

A 20 KB token is a major architectural smell because it increases:

- every request's header size,
- bandwidth,
- proxy work,
- load-balancer work,
- parsing cost,
- risk of maximum-header failures,
- accidental logging exposure.

---

# 10. Why Permissions Must Not Be in JWT

Suppose a manager has hundreds of permissions.

Bad:

```json
{
  "sub": "...",
  "permissions": [
    "users:read",
    "users:update",
    "inventory:read",
    "...hundreds..."
  ]
}
```

Consequences:

```text
large JWT
+
stale permissions
+
hard invalidation
+
repeated data
```

Correct:

```text
JWT
 ├── identity
 ├── session
 ├── organization
 └── authorization version
       │
       ▼
AuthorizationResolver
       │
       ├── Redis
       └── PostgreSQL
```

---

# 11. Token Transport

## Browser access token

If the browser directly calls the API as a bearer-token client:

```http
Authorization: Bearer <access-token>
```

Keep the short-lived access token in memory rather than persistent browser storage where practical.

Avoid:

```text
localStorage.access_token
```

for a high-security browser application because an XSS vulnerability can expose it.

If the architecture uses a backend-for-frontend/session layer, an HttpOnly cookie-based browser session can be preferable.

## Refresh token

Use an HttpOnly cookie:

```http
Set-Cookie: mc_refresh=<opaque-token>;
HttpOnly;
Secure;
SameSite=Lax;
Path=/api/v1/auth;
Max-Age=<bounded-lifetime>
```

Production:

```text
Secure=true
HttpOnly=true
```

Prefer a host-only cookie unless cross-subdomain operation is explicitly required.

Do not return the refresh token in JSON for normal browser clients.

---

# 12. CSRF

`HttpOnly` does **not** prevent CSRF.

For cookie-authenticated flows use:

```text
SameSite=Lax/Strict
+
Origin validation
+
CSRF token where required
```

For unsafe methods, validate the expected origin/site policy.

Do not use:

```text
Access-Control-Allow-Origin: *
```

with credentialed requests.

---

# 13. Refresh Token

Use a cryptographically secure opaque random value.

Conceptually:

```text
256-bit random token
```

Database stores:

```text
SHA-256(raw_refresh_token)
```

not the raw token.

Never log the raw refresh token.

---

# 14. Refresh Rotation

Every successful refresh:

```text
OLD TOKEN
   │
   ▼
lock row
   │
   ▼
validate
   │
   ▼
mark old token used
   │
   ▼
create new token
   │
   ▼
create new access JWT
```

The old token becomes unusable.

## Reuse detection

If an already-used refresh token is presented:

```text
reuse detected
   │
   ├── revoke token family
   ├── revoke associated session
   ├── audit security event
   └── return generic authentication error
```

Concurrent refresh must be tested so only one request can consume the old token.

---

# 15. Session Architecture

Session is the server-side authentication state.

Conceptual fields:

```text
id
user_id
organization_id
device metadata
IP metadata
created_at
last_seen_at
expires_at
revoked_at
deleted_at
```

JWT contains:

```text
sid
```

Server verifies:

```text
session.id == sid
session.user_id == sub
session active
session not expired
session not revoked
```

This provides immediate server-side revocation.

---

# 16. Session Validation

Protected request:

```text
JWT
 │
 ▼
cryptographically valid?
 │
 ├── NO → 401
 │
 ▼
session lookup
 │
 ├── missing → 401
 ├── revoked → 401
 ├── expired → 401
 ├── deleted → 401
 └── mismatch → 401
 │
 ▼
account lifecycle
 │
 ├── disabled/suspended → deny
 │
 ▼
Principal
```

Do not remove session validation merely to improve performance.

Optimize it with indexing, careful caching and measurement.

---

# 17. Session Cache

Use **PostgreSQL PRIMARY + Redis from the initial implementation**.

PostgreSQL remains the authoritative source of session security state. Redis is the fast, distributed session-state cache.

```text
Request
  │
  ▼
JWT verification
  │
  ▼
Redis session lookup
  │
  ├── HIT + current security version + active
  │        └── continue
  │
  ├── MISS
  │        └── PostgreSQL PRIMARY
  │
  └── ERROR / invalid cached state
           └── PostgreSQL PRIMARY
```

Possible cached state:

```text
active
user_id
organization_id
expires_at
revocation/version information
session security generation
```

Redis must never become the source of truth.

For immediate revocation, the session model must include a version/revocation-generation mechanism or another correctness mechanism that makes stale Redis state unusable.

A safe model is:

```text
PostgreSQL session state
        │
        ├── session active/revoked
        └── session security generation
                    │
                    ▼
             Redis cached state
```

When a session is revoked:

```text
PostgreSQL:
    revoked_at = now()
    session_generation = session_generation + 1

Redis:
    invalidate old generation
```

If Redis invalidation is delayed or unavailable, the request must fall back to PostgreSQL PRIMARY whenever the cached security state cannot be proven current.

Therefore:

```text
Redis available + cache is valid/current
    → fast path

Redis miss
    → PostgreSQL PRIMARY

Redis error
    → PostgreSQL PRIMARY

Security-state uncertainty
    → PostgreSQL PRIMARY

PostgreSQL unavailable
    → fail closed
```

This gives the system a fast path from day one without making Redis authoritative.

# 18. Principal

The Principal should be compact:

```go
type Principal struct {
    UserID         uuid.UUID
    OrganizationID uuid.UUID
    SessionID      uuid.UUID
    BranchID       *uuid.UUID
    AuthzVersion   int64
    Authenticated  bool
}
```

It is the trusted identity/security context created after authentication.

It should not contain a huge permission array.

---

# 19. Effective Branch Resolution

Branch is security-sensitive.

A client may send:

```http
X-Branch-ID: B2
```

but that does not grant access to B2.

The principal must support three states:

```text
single-branch user
multi-branch subset user
organization-wide user
```

## Branch scope model

```go
type Principal struct {
    UserID         uuid.UUID
    OrganizationID uuid.UUID
    SessionID      uuid.UUID

    // nil means organization-wide.
    // non-nil means explicitly restricted to this branch set.
    BranchIDs      []uuid.UUID

    AuthzVersion   int64
    Authenticated  bool
}
```

Semantics:

```text
BranchIDs == nil
    → organization-wide scope

BranchIDs = [B1]
    → single-branch scope

BranchIDs = [B1, B2, B7]
    → explicit multi-branch subset scope
```

Do not use an empty slice ambiguously. Recommended canonical representation:

```text
nil     = unrestricted within organization
non-nil = explicitly restricted branch set
```

If an empty non-nil set is ever possible, treat it as **no branch access**, not organization-wide access.

## Multi-branch subset example

A Regional Manager may have:

```text
Organization = O1

Allowed branches:
    B1
    B3
    B7
```

The manager can operate only within:

```text
{B1, B3, B7}
```

They cannot access B2, B4, B5 or B6 unless their effective authorization changes.

## Effective branch

For a branch-specific request:

```text
requested branch ∈ Principal.BranchIDs
```

must be true.

For organization-wide users:

```text
Principal.BranchIDs == nil
```

a requested branch is valid only when it belongs to the principal's organization, the principal has the required permission, and the endpoint supports branch selection.

Never treat a client branch header as authority. The branch header is only a **requested context**.

## Multiple branch query

For endpoints that operate across the user's allowed branch subset, the service/repository may use:

```sql
WHERE organization_id = $1
  AND branch_id = ANY($2)
```

where `$2` is the server-derived allowed branch set. Never construct this list from an untrusted client request.

## Principal branch scope and authorization cache

Because permissions/scope now depend on branch subset, the authorization cache key must account for the effective context.

For a branch-specific request:

```text
authz:...:org:O1:user:U1:branch:B1:version:...
```

For a multi-branch operation, use a stable server-derived scope identifier/version rather than serializing an unbounded branch list into every key.

The branch-scope version should change whenever the user's effective branch membership changes.

# 20. Authorization Resolver

Create one centralized component:

```text
AuthorizationResolver
```

Its responsibilities:

- resolve effective permissions,
- enforce organization scope,
- enforce branch scope,
- use Redis as cache,
- fall back to PostgreSQL primary,
- apply versioning,
- default deny,
- produce metrics,
- produce security-relevant logs.

Conceptual interface:

```go
type AuthorizationResolver interface {
    Resolve(
        ctx context.Context,
        principal Principal,
        branchID *uuid.UUID,
    ) (AuthorizationContext, error)

    Enforce(
        ctx context.Context,
        principal Principal,
        branchID *uuid.UUID,
        resource string,
        action string,
    ) error
}
```

---

# 21. Authorization Context

Example:

```go
type AuthorizationContext struct {
    UserID         uuid.UUID
    OrganizationID uuid.UUID
    BranchIDs      []uuid.UUID
    Permissions    PermissionSet
    UserVersion    int64
    RBACVersion    int64
}
```

Permission set should be represented in memory as a hash set:

```go
map[string]struct{}
```

so:

```text
Contains("inventory:update")
```

is approximately O(1).

---

# 22. Authorization Resolution

Conceptually:

```text
EffectivePermissions(
    user,
    organization,
    effective_branch,
    current_user_version,
    current_rbac_version
)
```

The resolver combines:

```text
user direct permissions
+
effective role assignments
+
role permissions
+
organization scope
+
branch scope
+
multi-branch subset scope
+
active/expiry state
```

For an explicit branch subset, a branch-scoped role is effective only when its branch is `NULL` (org-wide assignment) or belongs to the principal's server-derived `BranchIDs`. A requested branch must first be validated against that set.

Branch-scoped role condition is conceptually:

```sql
ur.branch_id IS NULL
OR ur.branch_id = :effective_branch
```

A role assigned in B2 must never become effective for B1 merely because the user owns the same role elsewhere.

---

# 23. PostgreSQL as Security Source of Truth

PostgreSQL owns:

```text
users
credentials
sessions
refresh tokens
roles
permissions
user_roles
role_permissions
authorization versions/generations
audit/security records
```

Redis does not own these decisions.

Never implement:

```text
Redis says ALLOW
→ automatically trust forever
```

Redis stores a previously resolved result under a security-specific, versioned key.

---

# 24. Security-Sensitive Reads Use Primary DB

Primary DB:

```text
credential verification
account status
session revocation state
refresh token state
password reset state
authorization version
role authority
permission resolution
role assignment authority
```

Do not use a lagging replica for these decisions.

Replicas are suitable for non-security reads such as:

```text
analytics
reports
historical UI data
```

where replication lag is acceptable.

---

# 25. Redis Authorization Cache

Redis can cache:

```text
effective permissions
authorization context
safe session projections
distributed rate-limit counters
idempotency keys
short-lived authentication challenges
temporary security state
```

Authorization cache example:

```text
authz:u12:r91:org:O1:user:U1:branch:B1
```

The cache key must include every security dimension affecting the decision.

At minimum:

```text
user
organization
effective branch/context
user authorization version
RBAC/global authorization version
branch-scope version
```

Example single-branch key:

```text
authz:org:O1:user:U1:branch:B1:uv:12:rv:91:bsv:4
```

For multi-branch operations, use a stable server-derived branch-scope version/identifier rather than placing an arbitrarily large branch list into the key.

For organization-wide scope:

```text
authz:org:O1:user:U1:scope:org-wide:uv:12:rv:91:bsv:4
```

---

# 26. Why Versioned Cache Keys

Suppose:

```text
user version = 12
RBAC version = 91
```

Cache:

```text
authz:u12:r91:org:O1:user:U1:branch:B1
```

A user-specific authorization mutation:

```text
user version 12 → 13
```

automatically makes the old key obsolete.

A role-definition mutation:

```text
RBAC version 91 → 92
```

makes old role-derived entries obsolete without scanning every user.

This is substantially more scalable than deleting thousands or millions of user cache entries synchronously.

---

# 27. Authorization Versioning Strategy

Use two levels:

```text
user_authz_version
+
organization/RBAC generation
```

Example:

```text
user_authz_version = 12
rbac_generation    = 91
```

User-specific changes increment the user version.

Role-definition changes increment the organization/RBAC generation.

This avoids expensive fan-out invalidation for large organizations.

If authorization rules can be shared globally across organizations, use an additional global generation.

---

# 28. Atomic Authorization Mutation

Example role assignment:

```text
BEGIN

validate actor
validate target
validate scope
validate role

INSERT/UPDATE user_role

UPDATE users
SET authz_version = authz_version + 1
WHERE id = target_user

COMMIT
```

The authorization mutation and version change must be atomic.

Never allow:

```text
role changed
but version not changed
```

---

# 29. Mutations That Must Invalidate Authorization

Review every operation affecting effective authorization:

```text
role assignment
role removal
role permission grant
role permission removal
role activation
role deactivation
role deletion
direct user permission grant
direct user permission removal
user branch scope change
organization scope change
role scope change
permission activation/deactivation
authorization-policy changes
```

Every mutation must update the appropriate version/generation atomically.

---

# 30. Role-Level Changes at Large Scale

A role may affect:

```text
10 users
10,000 users
1,000,000 users
```

Do not synchronously scan every affected user on each role permission mutation.

Instead:

```text
organization RBAC generation
```

changes once.

Old cache:

```text
r91
```

New cache:

```text
r92
```

Old entries naturally expire.

This gives:

```text
O(1) invalidation event
```

instead of:

```text
O(number of affected users)
```

---

# 31. Redis Hit Path

```text
REQUEST
  │
  ▼
current versions
  │
  ▼
build versioned cache key
  │
  ▼
Redis GET
  │
  ├── HIT
  │    └── validate/deserialize
  │          └── PermissionSet
  │
  ▼
Enforce
```

Warm-cache authorization requires no RBAC database query.

---

# 32. Redis Miss Path

```text
Redis GET
   │
   └── MISS
        │
        ▼
PostgreSQL PRIMARY
        │
        ▼
resolve effective permissions
        │
        ▼
construct PermissionSet
        │
        ▼
Redis SETEX
        │
        ▼
Enforce
```

---

# 33. Redis Failure

If Redis is unavailable:

```text
Redis ERROR
   │
   ▼
PostgreSQL PRIMARY
   │
   ▼
resolve authorization
   │
   ▼
ALLOW / DENY
```

Redis is a performance dependency.

Therefore Redis failure should not automatically disable the entire API.

If PostgreSQL/source-of-truth authorization resolution fails:

```text
FAIL CLOSED
```

The API can return:

```text
503 Service Unavailable
```

because the system cannot safely determine whether the caller is authorized.

Never:

```text
DB failure → assume allow
```

---

# 34. Cache Corruption

If cached JSON is malformed:

```text
Redis value
   │
   ├── valid → use
   │
   └── invalid
        │
        ▼
delete invalid entry
        │
        ▼
PostgreSQL PRIMARY
```

Count this as:

```text
authz_cache_error_total
```

not simply a normal miss.

---

# 35. Cache TTL

Start with:

```text
authorization cache = 1–5 minutes
```

Example:

```text
5 minutes
```

Versioning provides correctness/invalidation.

TTL provides:

- memory control,
- cleanup,
- recovery from abandoned entries.

Do not rely on TTL alone for security invalidation.

---

# 36. Cache Stampede Protection

Without protection:

```text
1000 requests
    │
    ▼
same cache MISS
    │
    ▼
1000 PostgreSQL queries
```

Use request coalescing/singleflight:

```text
1000 requests
    │
    ▼
same key
    │
    ▼
ONE PostgreSQL resolution
    │
    ▼
Redis populated
    │
    ▼
remaining requests reuse result
```

This is important during traffic spikes.

---

# 37. Negative Caching

Caching DENY results can improve performance but must be versioned.

Bad:

```text
deny:user:123
TTL = 1 hour
```

because permission may be granted during that hour.

If negative caching is used:

```text
deny:u12:r91:org:O1:user:U1:branch:B1
```

and use a short bounded TTL.

For the first implementation, positive permission-set caching is simpler and safer.

---

# 38. Authorization Enforcement

After resolution:

```text
PermissionSet
```

contains:

```text
users:read
users:update
inventory:read
inventory:update
sales:read
sales:create
```

Then:

```go
Enforce("inventory", "update")
```

does an in-memory lookup.

No new Redis/DB call is required for each permission check in the same request.

---

# 39. Resolve Once Per Request

Bad:

```text
middleware → resolve
handler    → resolve
service    → resolve
repository → resolve
```

Good:

```text
request
  │
  ▼
authentication
  │
  ▼
principal
  │
  ▼
effective context
  │
  ▼
authorization context
  │
  ▼
request context
      │
      ├── handler
      └── service
```

Multiple authorization checks in one request reuse the same `PermissionSet`.

---

# 40. Authorization vs Repository Scope

The user's concern about repository inefficiency is correct, but repository isolation should **not** be removed.

Separate responsibilities:

### Authorization

```text
Can the caller perform this operation?
```

### Service

```text
Are business rules satisfied?
Is the resource transition valid?
```

### Repository

```text
Does the SQL remain inside the organization/branch boundary?
```

Example:

```sql
UPDATE inventory
SET quantity = $1
WHERE id = $2
  AND organization_id = $3
  AND branch_id = $4;
```

The repository does not need to resolve RBAC.

It simply uses the already trusted:

```text
organization_id
effective_branch_id
```

from the request/service context.

---

# 41. Resource-Level Authorization

There are two separate decisions.

### Capability

```text
Does the user have inventory:update?
```

Resolved by AuthorizationResolver.

### Resource scope

```text
Does inventory item X belong to the authorized org/branch?
```

Enforced by scoped query/business rules.

Therefore:

```text
permission
+
organization scope
+
branch scope
+
resource scope
```

must all be satisfied.

---

# 42. Avoiding IDOR

Bad:

```text
GET /users/{arbitrary-id}
```

then:

```text
SELECT * FROM users WHERE id = $1
```

Correct:

```text
authenticate
→ authorize users:read
→ establish org/branch context
→ scoped resource query
```

Example:

```sql
SELECT ...
FROM users
WHERE id = $1
  AND organization_id = $2
  AND (
      branch_id = $3
      OR branch_id IS NULL
  );
```

Exact branch semantics must follow the domain policy.

---

# 43. Permission Naming

Use canonical resource/action identifiers:

```text
users:read
users:create
users:update
users:delete

inventory:read
inventory:create
inventory:update
inventory:delete

sales:read
sales:create
sales:return

purchases:read
purchases:create
purchases:update

reports:read
analytics:read
accounting:read
```

Avoid inconsistent forms such as:

```text
user_read
Users:Read
USERS_READ
read_users
```

Unknown resource/action combinations default to DENY.

---

# 44. Role Hierarchy

If role priority is used:

```text
SUPER_ADMIN = 100
ADMIN       = 90
MANAGER     = 70
ACCOUNTANT  = 60
SALESMAN    = 40
STAFF       = 20
```

Priority should support explicit administrative authority.

It must not replace permission checks.

Recommended assignment policy:

```text
SUPER_ADMIN → explicit override
otherwise actor_priority > target_priority
```

Do not silently allow equal-priority delegation unless the product explicitly wants that behavior.

---

# 45. Role Assignment Security

For:

```text
AssignRole(actor, target, role, branch)
```

perform:

```text
1. Authenticate actor.
2. Resolve current actor authorization.
3. Verify role-management permission.
4. Load target user from PRIMARY.
5. Verify target organization.
6. Verify target branch scope.
7. Load requested role.
8. Verify target role priority.
9. Verify requested branch belongs to organization.
10. Verify actor's branch authority.
11. Perform mutation transactionally.
12. Increment user version / RBAC generation.
13. Audit.
```

Never use stale in-memory role state as the security authority.

---

# 46. Enforcer/Policy Engine

If an in-process policy/enforcer component is retained, it must not decide live security authority if its state can become stale.

It can be used for:

```text
static policy mapping
boot-time configuration
development helpers
```

But current authority should be:

```text
PostgreSQL primary
→ AuthorizationResolver
→ Redis cache
```

Do not allow:

```text
stale enforcer
→ actor authority
→ role assignment
→ authorization allow
```

---

# 47. Security Headers

Recommended API headers:

```http
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
Cache-Control: no-store
Pragma: no-cache
```

For production HTTPS:

```http
Strict-Transport-Security: max-age=31536000
```

Only enable HSTS aggressively when HTTPS is guaranteed.

For a JSON API, CSP is less central than TLS, CORS, CSRF, authentication, authorization and response-cache controls.

---

# 48. Request Headers

Example:

```http
POST /api/v1/inventory/items HTTP/1.1
Host: api.pharmaciano.com
Authorization: Bearer eyJ...
Content-Type: application/json
Accept: application/json
Origin: https://app.pharmaciano.com
X-Request-ID: 6e4...
X-Correlation-ID: 6e4...
X-Branch-ID: 7a...
```

Rules:

- `Authorization` is never logged.
- `Cookie` is never logged.
- `X-Branch-ID` is requested context, not authority.
- `Origin` is validated for browser security policy.
- request IDs are observability identifiers, not authentication.

---

# 49. Proxy/IP Security

Do not blindly trust:

```http
X-Forwarded-For
X-Real-IP
```

Only accept forwarded client IP from configured trusted proxies.

Otherwise an attacker can send:

```http
X-Forwarded-For: fake-ip
```

and bypass IP-based rate limits.

---

# 50. CORS

For credentialed requests:

```text
Access-Control-Allow-Credentials: true
```

must use explicit origins.

Never:

```http
Access-Control-Allow-Origin: *
```

for credentialed browser traffic.

Production should contain only required frontend origins.

---

# 51. IP, Location, Device and Login-Attempt Rate Limiting

Authentication abuse protection should use **multiple independent signals**. IP-only limiting is insufficient because attackers can rotate addresses; account-only limiting can be abused for lockout denial-of-service.

Use layered controls:

```text
IP
+
account/email
+
device/session signal
+
coarse geographic/network anomaly
+
route
+
global service protection
```

## 51.1 IP-based rate limiting

Apply distributed limits to:

```text
/login
/refresh
/forgot-password
/reset-password
MFA verification endpoints
```

Use Redis atomic counters with TTL. Conceptual key:

```text
rl:login:ip:<normalized-ip>:<window>
```

The client IP must come from a configured trusted reverse proxy chain when proxies are used. Never blindly trust arbitrary `X-Forwarded-For` or `X-Real-IP` values.

## 51.2 Account/email-based rate limiting

An attacker can use many IPs against one account, so maintain a separate account/email limiter.

Normalize the identifier consistently and prefer a privacy-safe keyed hash/HMAC in Redis keys:

```text
rl:login:account:<hmac(normalized-email)>:<window>
```

This is independent from account lockout.

## 51.3 Login failure attempts

Persist security state such as:

```text
failed_login_attempts
locked_until
last_failed_at
```

Use atomic database updates; do not use an unsafe read-increment-write sequence.

```sql
UPDATE credentials
SET failed_login_attempts = failed_login_attempts + 1,
    updated_at = now()
WHERE id = $1;
```

Rate limiting protects the service/account from excessive requests; lockout changes the account's authentication state. Use both.

## 51.4 Avoid lockout DoS

Prefer temporary lockout and progressive delay rather than permanent lockout. Exact thresholds should be configuration-driven and measured.

```text
few failures       → normal
more failures      → increasing delay
threshold reached  → temporary lock
continued attack   → stronger throttling/challenge
```

A legitimate user's successful authentication should reset the account failure counter, while IP/device abuse counters decay independently.

## 51.5 Device-based controls

Use a server-issued opaque device/session identifier for rate limiting and risk signals. Do not treat a browser fingerprint as an authentication credential.

Use device information for:

```text
rate limiting
risk scoring
anomaly detection
session management
security notifications
```

Conceptual key:

```text
rl:login:device:<opaque-device-id>
```

## 51.6 Location/geographic controls

Location is a risk signal, not normally an authentication factor. Useful signals include:

```text
country/region
ASN/network
sudden geographic change
impossible-travel pattern
```

For example, a successful privileged login followed minutes later by a distant anomalous login can trigger:

```text
step-up MFA
additional throttling
security alert
or denial
```

IP geolocation is approximate and can be distorted by VPNs, mobile networks, NAT and cloud proxies. Store only the coarse security metadata required by the threat model and retention policy.

## 51.7 Combined risk model

Conceptually:

```text
Risk =
    IP request rate
  + account failure rate
  + device history
  + geographic/network anomaly
  + session history
  + MFA state
```

Then:

```text
LOW RISK    → normal authentication
MEDIUM RISK → password + MFA/step-up
HIGH RISK   → throttle / challenge / deny
```

Keep the first implementation understandable and auditable rather than building an opaque scoring engine.

## 51.8 Distributed rate limiting

Because API nodes are horizontally scaled, local memory counters are insufficient:

```text
API-1 ─┐
API-2 ─┼──► Redis shared rate-limit state
API-3 ─┘
```

Use atomic increments and TTLs. If Redis is unavailable for a high-risk authentication endpoint and the service cannot reliably enforce abuse controls, apply a conservative local fallback or temporarily fail closed rather than silently removing protection.

## 51.9 Suggested login-attempt layers

```text
Layer 1 — global endpoint protection
Layer 2 — IP limiter
Layer 3 — account/email limiter
Layer 4 — device/session signal
Layer 5 — account lockout
Layer 6 — MFA/risk challenge
```

Counters should have independent decay semantics. A successful password proof can reset account failures without necessarily resetting an IP abuse counter.

## 51.10 Observability

Track bounded metrics such as:

```text
login_attempt_total
login_failure_total
login_lockout_total
login_rate_limit_ip_total
login_rate_limit_account_total
login_rate_limit_device_total
mfa_challenge_total
mfa_failure_total
```

Do not use raw IPs, emails, device identifiers or tokens as Prometheus labels.

---

# 51. Password Security

Use Argon2id.

A starting configuration can be:

```text
memory      = 64 MiB
iterations  = 3
parallelism = 2
salt        = 16 bytes
key         = 32 bytes
```

Benchmark and tune this against real production hardware.

Canonical policy should be shared by:

```text
registration
password change
password reset
admin reset
```

Example:

```text
minimum = 8
maximum = 128
```

Do not create contradictory endpoint-specific policies.

## 51A. Argon2id Pepper and Pepper Rotation

If a server-side pepper is used, it must be treated as a secret separate from the password hash and stored outside PostgreSQL, preferably in a secrets manager/KMS-backed configuration system.

Conceptually:

```text
password + pepper
      │
      ▼
   Argon2id
      │
      ▼
 password hash
```

### Pepper rotation

A pepper cannot be rotated exactly like a normal database field because existing password hashes were generated with the previous pepper. Use a key-ring model:

```text
current pepper: P2
previous pepper(s): P1
```

Each credential hash must identify which pepper generation was used, for example:

```text
password_hash
pepper_version = 2
```

Verification strategy:

```text
1. Read stored pepper_version.
2. Load that pepper from the server-side pepper key ring.
3. Verify Argon2id(password + selected pepper).
4. If valid with an old pepper, immediately rehash using the current pepper.
5. Persist the new hash + current pepper_version atomically.
```

Rotation therefore becomes:

```text
P1 active
   │
   ▼
introduce P2 as current
   │
   ▼
verify old P1 hashes during migration window
   │
   ▼
successful login → rehash with P2
   │
   ▼
remaining P1 hashes gradually disappear
   │
   ▼
retire P1 only after migration/forced-reset policy completes
```

Do not silently discard the old pepper while users still have credentials derived from it. If an old pepper is permanently unavailable, those users require a password reset rather than a bypass.

Never log peppers or place them in JWTs, Redis values, database rows, audit logs or application responses.

---

# 52. Password Reset

Forgot password:

```text
POST /auth/forgot-password
```

Always use a generic response:

```text
If the account exists, reset instructions will be sent.
```

Reset token:

```text
high entropy
single use
short TTL
hashed in database
```

Successful reset should atomically:

```text
consume reset token
+
update password
+
revoke sessions as policy requires
+
revoke refresh families
+
audit
```

---

# 53. Logout

Single session:

```text
authenticate
→ revoke session
→ revoke/retire refresh family
→ clear refresh cookie
```

Because the access JWT is short-lived and session validation exists, a revoked session can stop the JWT from being accepted immediately.

---

# 54. Logout All

```text
authenticated user
   │
   ▼
revoke all sessions
   │
   ▼
revoke refresh families
   │
   ▼
clear current refresh cookie
```

---

# 55. Password Change

Recommended:

```text
verify current password
        │
        ▼
update Argon2id hash
        │
        ▼
revoke other sessions
        │
        ▼
revoke other refresh families
        │
        ▼
audit
```

Whether the current session survives should be an explicit product security policy.

---

# 56. Account Lifecycle

States may include:

```text
ACTIVE
DISABLED
SUSPENDED
DELETED
PENDING
```

Authentication must respect account state.

When a security-relevant account state changes:

```text
session invalidation
+
refresh invalidation
+
authorization invalidation
+
audit
```

should occur according to the chosen lifecycle policy.

---

# 57. MFA

MFA is **mandatory for the production security baseline** for Pharmaciano.

Because this ERP can expose controlled/high-risk medicine inventory, financial/accounting information, customer or patient-related PII, organization administration and RBAC administration, MFA must not remain an optional future feature.

## Required baseline

At minimum:

```text
SUPER_ADMIN → MFA required
ADMIN       → MFA required
```

A stronger production policy may also require MFA for MANAGER, ACCOUNTANT and other privileged roles.

High-risk operations should support step-up authentication even when the user already has an authenticated session.

## Initial factor

Implement:

```text
TOTP
```

with secure recovery codes.

Future support can include WebAuthn/passkeys and hardware-backed credentials.

## MFA login flow

```text
password valid
    │
    ▼
account active
    │
    ▼
MFA required?
    │
    ├── NO → create authenticated session
    │
    └── YES
          │
          ▼
      MFA challenge
          │
          ├── invalid → deny
          ├── replay → deny
          └── valid → create authenticated session
```

Do not issue a normal fully privileged access token before mandatory MFA is successfully completed. An intermediate `MFA_PENDING` state must not authorize normal protected API access.

# 58. Rate Limiting

Use multiple dimensions:

```text
IP
account/email
session
route
```

High-risk endpoints:

```text
/login
/refresh
/forgot-password
/reset-password
```

Redis is appropriate for distributed counters.

Conceptual keys:

```text
rl:login:ip:<ip>
rl:login:user:<normalized-email>
rl:refresh:session:<session-id>
```

Use atomic increments and TTLs.

---

# 59. Audit Logging

Record security events:

```text
login success
login failure
account lock
logout
logout-all
refresh reuse
password change
password reset
role assignment
role removal
permission mutation
branch scope change
organization scope change
account disable
security-relevant authorization denial
```

Never log:

```text
password
access token
refresh token
reset token
Authorization header
Cookie header
secret keys
```

---

# 60. Observability

Prometheus metrics:

```text
auth_login_success_total
auth_login_failure_total
auth_lockout_total

auth_refresh_success_total
auth_refresh_failure_total
auth_refresh_reuse_total

authz_allow_total
authz_deny_total
authz_cache_hit_total
authz_cache_miss_total
authz_cache_error_total
authz_resolution_error_total
authz_version_change_total
```

Use bounded labels such as:

```text
resource
action
reason
```

Do not use arbitrary:

```text
user_id
email
token
permission-list
```

as Prometheus labels.

---

# 61. Authorization Logging

Do not log every successful authorization at Info.

Use:

```text
Debug → normal decisions
Warn  → suspicious repeated denials
Error → authorization infrastructure failures
```

Example:

```json
{
  "event": "authorization_denied",
  "resource": "inventory",
  "action": "update",
  "reason": "missing_permission"
}
```

Do not include unnecessary sensitive data.

---

# 62. `/auth/me`

Keep it small.

Example:

```json
{
  "id": "...",
  "organization_id": "...",
  "branch_id": "...",
  "role": "MANAGER"
}
```

Do not return the entire permission matrix on every request.

---

# 63. `/auth/me/permissions`

Provide:

```http
GET /api/v1/auth/me/permissions
```

Example:

```json
{
  "permissions": [
    "users:read",
    "inventory:read",
    "inventory:update",
    "sales:create"
  ],
  "authz_version": 12
}
```

This endpoint is for UI capability rendering.

It is **not** the security authority.

The backend independently enforces permissions for every protected operation.

---

# 64. Frontend Authorization

Frontend can use permissions to:

```text
hide menu
hide button
disable action
```

But frontend JavaScript is attacker-controlled.

Therefore:

```text
frontend permission check = UX
backend permission check = SECURITY
```

---

# 65. Resource and Module Coverage

Authorization must be audited across every module that can cross organization/branch/security boundaries:

```text
users
roles
organizations
branches
medicines/master data
inventory
warehouses
suppliers
customers
sales
sales returns
purchases
purchase returns
reports
analytics
accounting
ledger
notifications
settings
```

Do not secure only the obvious `/users` routes.

---

# 66. Error Semantics

Recommended:

```text
401 Unauthorized
→ authentication is missing/invalid

403 Forbidden
→ authenticated but not authorized

423 Locked
→ account/session security lock where appropriate

429 Too Many Requests
→ rate limit exceeded

503 Service Unavailable
→ security source of truth unavailable
```

For sensitive resource existence, returning 404 instead of 403 may be appropriate.

Do not leak permission details unnecessarily.

---

# 67. High-Availability Architecture

```text
                    LOAD BALANCER
                 /       |                       /        |                     API-1     API-2     API-3
                \        |        /
                 \       |       /
                    Redis Cluster
                         │
                         │
                   PostgreSQL
                  Primary + replicas
```

Application nodes should be effectively stateless.

Do not depend on local memory for:

```text
session truth
revocation truth
authorization truth
```

Local caches may exist only as performance optimizations whose loss cannot break security.

---

# 68. Database Indexes

Review indexes for:

```text
users(id)
users(organization_id)
users(organization_id, branch_id)

credentials(user_id)
credentials(email)

sessions(id)
sessions(user_id)
sessions(user_id, revoked_at, expires_at)

refresh_tokens(token_hash)
refresh_tokens(session_id)
refresh_tokens(family_id)

user_roles(user_id)
user_roles(role_id)
user_roles(user_id, branch_id)

role_permissions(role_id)
permissions(module, action)
```

Use actual query plans and production-like data to validate index choices.

---

# 69. Database Constraints

Use DB constraints for security correctness:

```text
unique normalized email where appropriate
unique role-permission association
unique user-role association where appropriate
unique token identifiers
foreign keys
check constraints
```

Application validation should complement, not replace, database integrity.

---

# 70. Transaction Boundaries

Security mutations should be atomic.

Examples:

```text
role assignment
+
authorization version update
```

```text
refresh consume
+
new refresh insert
+
session update
```

```text
password reset token consume
+
password update
+
session revocation
```

Use:

```text
FOR UPDATE
atomic UPDATE
unique constraints
conditional UPDATE
```

to prevent races.

---

# 71. Concurrency

Explicitly test:

```text
concurrent login attempts
concurrent refresh
concurrent password reset
concurrent role assignment
concurrent role mutation
concurrent permission mutation
```

Expected:

```text
no lost failure increments
one refresh consumption
reuse detection
consistent authorization versions
no duplicate security mutation
```

---

# 72. Security Threat Model

The system must defend against:

### Credential attacks

```text
brute force
credential stuffing
password spraying
user enumeration
```

Defenses:

```text
Argon2id
rate limits
progressive delays
generic errors
MFA
audit
```

### Token attacks

```text
tampering
replay
refresh-token theft
refresh reuse
algorithm confusion
expired token use
wrong audience/issuer
```

Defenses:

```text
HTTPS
short JWT TTL
algorithm pinning
kid
issuer/audience checks
session validation
refresh rotation
family reuse detection
```

### Authorization attacks

```text
IDOR
horizontal privilege escalation
vertical privilege escalation
cross-organization access
cross-branch access
stale authorization
role-assignment escalation
```

Defenses:

```text
central AuthorizationResolver
organization scope
branch scope
versioning
primary DB
repository defense-in-depth
default deny
explicit role hierarchy
```

### Web attacks

```text
XSS
CSRF
CORS abuse
clickjacking
header injection
request smuggling
```

Defenses:

```text
secure cookies
SameSite
Origin/CSRF controls
explicit CORS
security headers
trusted proxy
request limits
```

---

# 73. Performance Model

The optimized request path is:

```text
JWT verification
→ session validation
→ effective context
→ current authorization versions
→ Redis authorization hit
→ O(1) permission check
→ scoped business SQL
```

Avoid:

```text
large JWT
+
RBAC DB query for every repository operation
+
repeated user/org/branch loading
+
repeated permission resolution
```

The objective is not "zero database queries".

The objective is:

> **No unnecessary repeated security resolution inside the same request.**

---

# 74. Concrete Protected Request

Request:

```http
PATCH /api/v1/inventory/items/123
Authorization: Bearer <~800-byte-JWT>
X-Branch-ID: B1
```

JWT:

```text
sub = U1
org = O1
sid = S1
azv = 12
```

Server:

```text
1. Verify JWT.
2. Validate signature.
3. Validate algorithm/kid.
4. Validate issuer/audience.
5. Validate exp/nbf.
6. Validate session S1.
7. Confirm session belongs to U1/O1.
8. Resolve effective branch B1.
9. Read current user/RBAC versions.
10. Redis GET authorization key.
11. Cache hit.
12. PermissionSet contains inventory:update.
13. ALLOW.
14. Service validates business rules.
15. Repository executes scoped UPDATE.
16. Return response.
```

Repository:

```sql
UPDATE inventory
SET quantity = $1
WHERE id = $2
  AND organization_id = $3
  AND branch_id = $4;
```

There is no RBAC query during the warm-cache path.

---

# 75. What Hits PostgreSQL

Authentication:

```text
credential verification
account lifecycle
session state
refresh token state
password reset state
```

Authorization:

```text
cache miss
current version/generation
role/permission resolution
authorization mutations
role assignment authority
```

Business:

```text
inventory
sales
purchases
customers
suppliers
accounting
etc.
```

---

# 76. What Redis Is For

Good candidates:

```text
authorization cache
rate-limit counters
distributed temporary state
idempotency keys
safe session projections
temporary authentication state
```

Not authoritative for:

```text
password
role truth
permission truth
refresh-token truth
revocation truth
authorization mutation truth
```

---

# 77. Cache Consistency Model

Use:

```text
PostgreSQL = strong source of truth
Redis      = versioned cached projection
TTL        = cleanup
Version    = invalidation correctness
```

This is a much stronger design than TTL-only caching.

---

# 78. Request-Scoped Architecture

The final request architecture should be:

```text
REQUEST
  │
  ▼
Authentication
  │
  ▼
Principal
  │
  ▼
Effective branch/context
  │
  ▼
Current authorization versions
  │
  ▼
AuthorizationResolver
  │
  ├── Redis
  │
  └── PostgreSQL PRIMARY
  │
  ▼
AuthorizationContext
  │
  ▼
Enforce(resource, action)
  │
  ▼
Handler / Service
  │
  ▼
Scoped Repository
```

This is more efficient than performing full authorization resolution inside repositories.

---

# 79. Implementation Order From Scratch

Because the previous implementation has been deleted, build in this order.

## Phase 1 — Database

Implement:

```text
users
credentials
sessions
refresh_token_families
refresh_tokens
password_reset_tokens
roles
permissions
user_roles
role_permissions
authorization versions/generations
security audit events
```

Add:

```text
foreign keys
unique constraints
indexes
```

---

## Phase 2 — Password Authentication

Implement:

```text
Argon2id
password policy
login
dummy verification
atomic failed-attempt counter
lockout
account status
```

---

## Phase 3 — Sessions

Implement:

```text
create
validate
revoke
logout
logout-all
expiration
idle/absolute lifetime
```

---

## Phase 4 — JWT

Implement:

```text
HS256
kid
current/previous keys
minimal claims
strict parser
4096-byte hard limit
10–15 minute TTL
```

Tests:

```text
no permissions in token
token under 4096 bytes
wrong algorithm rejected
wrong issuer rejected
wrong audience rejected
expired rejected
```

---

## Phase 5 — Refresh Tokens

Implement:

```text
secure random token
SHA-256 hash
row locking
single-use
rotation
family
reuse detection
session revocation
absolute expiry
```

---

## Phase 6 — Principal

Implement:

```text
Principal
request context
identity
organization
session
effective branch
```

---

## Phase 7 — Authorization Data

Implement:

```text
roles
permissions
user_roles
role_permissions
branch scope
organization scope
role priority
```

---

## Phase 8 — Authorization Resolver

Implement:

```text
Resolve
Enforce
PermissionSet
default deny
branch-aware filtering
organization-aware filtering
primary DB
```

---

## Phase 9 — Versioning

Implement:

```text
user_authz_version
organization/RBAC generation
atomic updates
versioned cache keys
```

---

## Phase 10 — Redis

Implement:

```text
authorization cache
TTL
cache miss fallback
cache error fallback
corruption handling
singleflight
distributed rate limits
```

---

## Phase 11 — Middleware

Implement:

```text
security headers
trusted proxy
rate limits
JWT
session
principal
branch context
authorization
```

---

## Phase 12 — Module Enforcement

Apply to:

```text
users
roles
organizations
branches
medicines
inventory
warehouses
suppliers
customers
sales
sales returns
purchases
purchase returns
reports
analytics
accounting
ledger
notifications
settings
```

---

# 80. Testing Strategy

## Authentication

```text
valid login
wrong password
unknown email
locked account
disabled account
invalid JWT
wrong signature
wrong algorithm
wrong kid
wrong issuer
wrong audience
expired JWT
revoked session
expired session
```

## Refresh

```text
valid refresh
expired refresh
revoked refresh
used refresh
concurrent refresh
reuse detection
family revocation
session expiry
cookie clearing
```

## Authorization

```text
allowed permission
missing permission
cross-org denial
cross-branch denial
branch-bound actor
org-wide actor
role priority escalation
equal-priority assignment
role deactivation
role deletion
role-permission mutation
direct permission mutation
branch-scope mutation
user version change
RBAC generation change
Redis miss
Redis failure
Redis corruption
PostgreSQL authorization failure
```

---

# 81. Mandatory Adversarial Tests

### Cross-branch

```text
User B1
→ resource B2
→ DENY
```

### Cross-organization

```text
User O1
→ resource O2
→ DENY
```

### Stale authorization

```text
cached version = 12
current version = 13
→ old cache not accepted
→ current authorization resolved
```

### Redis outage

```text
Redis DOWN
→ PostgreSQL PRIMARY
→ correct decision
```

### PostgreSQL outage

```text
authorization DB unavailable
→ no allow fallback
→ fail closed
```

### Role mutation

```text
role changed
→ RBAC generation changes
→ old cache obsolete
```

---

# 82. Production Readiness Checklist

## Authentication

```text
[ ] HTTPS production-only
[ ] Secure refresh cookie
[ ] HttpOnly refresh cookie
[ ] SameSite configured
[ ] CSRF strategy
[ ] explicit CORS
[ ] trusted proxy
[ ] request limits
[ ] login rate limiting
[ ] account rate limiting
[ ] Argon2id
[ ] generic login errors
[ ] atomic lockout
[ ] short access JWT
[ ] algorithm pinning
[ ] kid validation
[ ] issuer validation
[ ] audience validation
[ ] exp validation
[ ] nbf validation
[ ] session validation
[ ] refresh rotation
[ ] refresh reuse detection
[ ] token-family revocation
[ ] password reset single-use
[ ] logout revocation
[ ] logout-all
[ ] account lifecycle enforcement
[ ] secrets never logged
```

## Authorization

```text
[ ] centralized AuthorizationResolver
[ ] default deny
[ ] canonical permissions
[ ] organization isolation
[ ] branch isolation
[ ] effective branch resolution
[ ] no permissions in JWT
[ ] server-side permission resolution
[ ] PostgreSQL PRIMARY for security reads
[ ] Redis cache only
[ ] branch in cache key
[ ] organization in cache key
[ ] user authorization version
[ ] RBAC/global generation
[ ] all effective mutations invalidate state
[ ] role deactivation invalidates
[ ] role deletion invalidates
[ ] role permission mutation invalidates
[ ] direct permission mutation invalidates
[ ] branch scope mutation invalidates
[ ] role authority from current DB state
[ ] stale enforcer cannot grant authority
[ ] cache corruption fallback
[ ] Redis failure fallback
[ ] DB failure fail closed
[ ] request-scoped authorization context
[ ] repository scope defense-in-depth
```

---

# 83. Final Reference Architecture

```text
                         ┌───────────────────┐
                         │      CLIENT       │
                         └─────────┬─────────┘
                                   │
                                HTTPS
                                   │
                                   ▼
                         ┌───────────────────┐
                         │ LB / Reverse Proxy│
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │ Security Layer    │
                         │ CORS / CSRF / RL  │
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │   JWT Verifier    │
                         │ HS256 / kid / exp │
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │ Session Validator │
                         │ DB / safe cache   │
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │     Principal     │
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │ Effective Context │
                         │ Org + Branch      │
                         └─────────┬─────────┘
                                   │
                                   ▼
                         ┌───────────────────┐
                         │ Current Versions  │
                         │ User + RBAC       │
                         └─────────┬─────────┘
                                   │
                                   ▼
                  ┌────────────────────────────────┐
                  │     AuthorizationResolver      │
                  └───────────────┬────────────────┘
                                  │
                         ┌────────┴────────┐
                         ▼                 ▼
                   ┌───────────┐    ┌─────────────┐
                   │   Redis   │    │ PostgreSQL  │
                   │   CACHE   │    │   PRIMARY   │
                   └─────┬─────┘    └──────┬──────┘
                         │                 │
                         └────────┬────────┘
                                  │
                                  ▼
                         Effective PermissionSet
                                  │
                                  ▼
                         Enforce(resource,action)
                           │                 │
                         ALLOW              DENY
                           │                 │
                           ▼                 ▼
                        Handler          403/404
                           │
                           ▼
                         Service
                           │
                           ▼
                  Scoped Repository Query
                           │
                           ▼
                       PostgreSQL
```

---

# 84. Final Design Rules

```text
JWT answers:
    "Who presented a valid signed identity?"

Session answers:
    "Is this login session still valid?"

Principal answers:
    "What authenticated security context is this request using?"

Effective branch answers:
    "Which organization/branch context is this request operating in?"

AuthorizationResolver answers:
    "What can this principal do in this context?"

Redis answers:
    "Do we already have a current cached authorization result?"

PostgreSQL answers:
    "What is the actual security truth?"

Enforce answers:
    "Is this resource/action allowed?"

Repository scope answers:
    "Even if application code has a bug, can this query cross the tenant/branch boundary?"
```

## The key optimization

Do **not** do this for every repository operation:

```text
repository
→ find user
→ find organization
→ find branch
→ find role
→ find permission
→ authorize
```

Instead:

```text
REQUEST
→ authenticate once
→ create Principal once
→ resolve effective branch once
→ resolve authorization once
→ cache by user + org + branch + versions
→ keep PermissionSet in request context
→ O(1) permission checks
→ execute scoped repository query
```

This preserves security while removing unnecessary repeated RBAC resolution.

The final architecture is therefore:

```text
Authentication
    ↓
Session
    ↓
Principal
    ↓
Effective Organization + Branch
    ↓
Current Authorization Versions
    ↓
AuthorizationResolver
    ↓
Redis cache / PostgreSQL primary
    ↓
Effective PermissionSet
    ↓
Enforce(resource, action)
    ↓
Service
    ↓
Scoped Repository
    ↓
PostgreSQL
```

**This is the recommended A–Z foundation for rebuilding Pharmaciano:ERP authentication and authorization from scratch.**
