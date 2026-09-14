# ADR 0004 — Native RBAC Enforcer with Branch-Scoped Grants

- **Status:** Accepted (supersedes original Casbin design — updated 2026-09-14)
- **Date:** 2026-07-15 (original) · 2026-09-14 (revision)
- **Deciders:** Backend Lead, Product
- **Related:** ADR-0001, ADR-0008, ADR-0015, ADR-0017, ADR-0019, ADR-0022

---

## Context

Pharmaciano ERP has strong access-control requirements:

- A **SUPER_ADMIN** that can do everything (resolved from role grants,
  not hardcoded bypass).
- System roles (ADMIN, MANAGER, PHARMACIST, CASHIER, ACCOUNTANT, WAREHOUSE,
  AUDITOR) with default permission sets.
- **Dynamic roles** (`JHON_SALESMAN`, `NIGHT_SHIFT_LEAD`, ...) with any
  admin-chosen subset of the permission catalog.
- **Tenant isolation**: rows are scoped by `organization_id`.
- **Branch scoping**: a Chittagong manager cannot see Feni sales, but
  org-wide roles see all branches.
- Permissions in the shape `module:action` (e.g., `sales:create`,
  `purchases:approve`).
- Fast decisions (sub-millisecond per request) so the check runs in every
  handler.
- Zero external dependencies for the authorization path.

Original options considered (2026-07-15):

1. **In-house RBAC in Postgres** — join tables and hand-rolled queries.
2. **OpenFGA / Ory Keto** — external service, ReBAC model.
3. **Casbin** (`github.com/casbin/casbin/v2`) with the `gorm-adapter/v3`
   backing store.
4. **OpenPolicyAgent** — sidecar, Rego policy language.

**Updated decision:** We rejected Casbin (option 3) during implementation
in favor of a **native, hand-rolled RBAC enforcer** with zero external
dependencies. The reasons and details are below.

## Decision

We use a **native Go RBAC enforcer** (`internal/modules/rbac/authorizer.go`)
with:

- An **immutable, atomically-swapped snapshot** of the permission map.
- **Branch-scoped grants** (optional branch subset per permission).
- An **authz_version epoch** for instant stale-token rejection.
- **In-memory resolution** with singleflight dedup for concurrent cache misses.
- **PostgreSQL persistence** for roles, permissions, and grant rows.

### Why Not Casbin

During implementation we discovered several issues with Casbin for our use
case:

1. **Reflection overhead:** Casbin uses reflection for its enforcer, adding
   overhead on the hot path that cannot be optimized away.
2. **Model complexity:** The RBAC-with-domains model (`casbin_model.conf`)
   was subtle to maintain; model changes required targeted integration tests
   and were error-prone.
3. **gorm-adapter dependency:** Introduced GORM as a transitive dependency,
   conflicting with our pgx-only data access (ADR-0002).
4. **Branch scoping:** Casbin's domain model does not natively support the
   "permission carries a branch subset" pattern we need (ADR-0019/0022).
5. **Snapshot semantics:** We needed an immutable snapshot that could be
   atomically swapped for zero-alloc reads — Casbin's internal model
   doesn't provide this.

The native enforcer is ~300 lines of Go, has zero external dependencies,
and runs in sub-millisecond time on the hot path.

### Permission model

Permissions are `module:action` strings:

```
medicines:view
medicines:create
medicines:update
medicines:delete
warehouse:view
warehouse:create
sales:create
sales:approve
organizations:view
organizations:update
branches:view
branches:create
users:view
users:create
users:update
users:delete
users:assign
users:revoke
```

The permission catalogue is seeded from `seed/permissions.yaml` via
`cmd/seed` and stored in the `permissions` table.

### Branch-scoped grants

Each permission grant can optionally carry a **branch subset**:

- `NULL` / empty → wildcard (all branches the user has access to)
- `[uuid1, uuid2]` → only those specific branches

At enforcement time:

1. Resolve the user's effective branch subset (home branch + assignments
  from `user_branch_assignments`).
2. Intersect with the permission's allowed branches.
3. If `branchID` is in the intersection → allowed.
4. If no intersection → `BRANCH_SCOPE_DENIED`.

This is the **ADR-0019/0022 branch-scope model**.

### Snapshot architecture

```go
type Enforcer struct {
    snapshot atomic.Value  // *Snapshot (immutable, atomically swapped)
}

type Snapshot struct {
    mu       sync.RWMutex
    roles    map[string]map[string][]BranchGrant  // role → module:action → branches
    version  int64                                 // rbac_generation epoch
}
```

- The snapshot is **immutable** after creation. Readers never block.
- When roles/permissions change, a new snapshot is built and CAS-swapped
  in via `atomic.Value.Store`.
- Concurrent readers see either the old or new snapshot — never a partial
  update.
- The snapshot is rebuilt on startup from PostgreSQL and updated when
  `rbac_generation` bumps.

### Authz version epoch

Every security-sensitive mutation bumps the user's `authz_version` counter
in the `users` table:

- Role grant/revoke → `BumpAuthzVersion`
- Permission change → `BumpAuthzVersion`
- MFA enable/disable → `BumpAuthzVersion`
- Branch assignment → `BumpAuthzVersion`

This epoch is checked in `Authenticate` on every request:

1. Cached session entry's `authz_version` must match JWT's `authz_version`.
2. Current DB `authz_version` must match JWT's `authz_version` (indexed
   scalar read, sub-millisecond).
3. Mismatch → deny (forces re-authentication with fresh claims).

This ensures permission changes take effect **immediately** — no waiting
for token expiry.

### In-memory singleflight

When multiple concurrent requests need to resolve the same user's
permissions, `singleflight.Group` deduplicates the DB reads:

```go
func (e *Enforcer) ResolveAccess(orgID, userID string) AccessView {
    key := orgID + ":" + userID
    v, _, _ := e.group.Do(key, func() (any, error) {
        return e.resolveFromDB(orgID, userID)
    })
    return v.(AccessView)
}
```

This prevents thundering herd on cache misses while keeping the resolution
logic simple.

## Enforcement flow

```
Request → Auth middleware (JWT parse + session check → Principal)
       → Tenant middleware (org + BranchScope from Principal)
       → RBAC middleware → enforcer.Enforce(userID, orgID, module, action, branchID)
          → snapshot lookup → permission check → branch scope check → allow/deny
```

The middleware constructs the request from:
- `sub = Principal.UserID`
- `dom = Principal.OrgID`
- `obj = module name (from route metadata)`
- `act = action name (from route metadata)`

On failure: `403 FORBIDDEN` with `BRANCH_SCOPE_DENIED` or `PERMISSION_DENIED`.

### SUPER_ADMIN resolution

SUPER_ADMIN is **not** a hardcoded bypass rule. It is a regular role in the
`roles` table with the `is_system=true` flag. Its permissions are seeded
from `seed/roles.yaml` and include all `module:*` grants. The enforcer
resolves SUPER_ADMIN's permissions exactly like any other role — through
the snapshot lookup. This means:

- SUPER_ADMIN's permissions can be audited (they are database rows).
- SUPER_ADMIN can theoretically be restricted (by modifying the role's
  grants, though the UI prevents this).
- The enforcement path is identical for all roles — no special cases.

## Route registration

Every module registers its routes with explicit `(module, action)` pairs:

```go
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
    grp := rg.Group("/medicines")
    grp.GET("", mw.Protected("medicines", "view"), h.List)
    grp.POST("", mw.Protected("medicines", "create"), h.Create)
    grp.PATCH("/:id", mw.Protected("medicines", "update"), h.Update)
    grp.DELETE("/:id", mw.Protected("medicines", "delete"), h.Delete)
}
```

The `mw.Protected()` chain is: `Auth → IdleSession → Tenant → RBAC`.

Module/action strings come from `constants.Module*` and `constants.Action*`
— the same source the seed uses to build the permission catalogue. A route
can never be gated on a `(module, action)` pair with no backing permission
row.

## Policy lifecycle

- Roles and permissions are **seeded** by `cmd/seed` from
  `seed/permissions.yaml` and `seed/roles.yaml`.
- System roles are marked `is_system=true` in the `roles` table and cannot
  be deleted through the API. Their default permission sets **can** be
  extended by SUPER_ADMIN.
- Dynamic roles are created by SUPER_ADMIN through `POST /api/v1/roles`
  with any subset of the permission catalog.
- Branch-scoped grants are managed via `POST /users/:id/roles` with an
  optional `branches` array.
- Every mutation bumps `rbac_generation` (for snapshot refresh) and
  `authz_version` (for stale-token rejection).

## Consequences

### Positive

- Zero external dependencies — no Casbin, no OpenFGA, no sidecar.
- Sub-millisecond enforcement (in-memory snapshot, atomic read).
- Immutable snapshot eliminates race conditions on the hot path.
- Branch-scoped grants are a first-class concept, not bolted on.
- `authz_version` epoch provides immediate permission propagation.
- Singleflight prevents thundering herd on concurrent cache misses.
- The enforcement path is identical for all roles — no special cases
  for SUPER_ADMIN.

### Negative

- Hand-rolled enforcer means we maintain the code ourselves (~300 lines).
- Snapshot rebuild on `rbac_generation` bump has a brief window where the
  old snapshot is in use — bounded by the time to build the new snapshot
  (typically <10ms).
- Branch-scope intersection adds a small cost to each enforcement call
  (linear scan of branch lists, typically 1-5 entries).

### Neutral

- The permission model (`module:action`) is simple and well-understood.
  More complex models (ABAC, ReBAC) are not needed at this stage.
- PostgreSQL as the persistence layer means policy changes are auditable
  and transactional.

## Guardrails

- Never call the enforcer directly from a handler. Always go through the
  `rbac` middleware or the `Enforcer` interface.
- Every new permission (module + action pair) must be added to
  `seed/permissions.yaml` in the same PR that adds the feature.
- Integration tests must cover: cross-branch access denied,
  cross-organization access denied, SUPER_ADMIN always allowed, revoked
  session denied, branch-scope enforcement on all CRUD operations.
- The `authz_version` check in `Authenticate` must read from the primary
  (not replica) to avoid stale reads.

## References

- NIST SP 800-162 (ABAC guidance).
- Internal ADR-0019 (Branch-Scoped RBAC Enforcement).
- Internal ADR-0022 (Branch-Scope Repository Pattern).
- Internal ADR-0015 (Authz Version Epoch).
- Internal ADR-0017 (Session Cache with Generation Keying).