# ADR 0004 — Casbin for Authorization (RBAC + PBAC with Domains)

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Product
- **Related:** ADR-0001, ADR-0008

---

## Context

Pharmaciano ERP has strong access-control requirements:

- A hard-wired **SUPER_ADMIN** that can do everything.
- System roles (ADMIN, MANAGER, PHARMACIST, CASHIER, ACCOUNTANT, WAREHOUSE,
  AUDITOR) with default permission sets.
- **Dynamic roles** (`JHON_SALESMAN`, `NIGHT_SHIFT_LEAD`, ...) with any
  admin-chosen subset of the permission catalog.
- **Tenant isolation**: rows are scoped by `organization_id`.
- **Branch scoping**: a Chittagong manager cannot see Feni sales, but SUPER_ADMIN
  and ADMIN can see all branches.
- Permissions in the shape `module:action` (e.g., `sales:create`,
  `purchases:approve`).
- Fast decisions (≤ 5 ms per request) so we can put the check in every
  handler.

Options considered:

1. **In-house RBAC in Postgres** — join tables and hand-rolled queries.
2. **OpenFGA / Ory Keto** — external service, ReBAC model.
3. **Casbin** (`github.com/casbin/casbin/v2`) with the `gorm-adapter/v3`
   backing store (using pgx internally is possible but we accept a small
   secondary dependency here).
4. **OpenPolicyAgent** — sidecar, Rego policy language.

## Decision

We adopt **Casbin v2** with a custom model that combines **RBAC-with-Domains**
and **path-style permission matching** (`keyMatch`), with policies persisted
in Postgres.

Highlights of the model (see `config/casbin_model.conf`):

- Request tuple: `sub (role name), dom (organization_id), obj (module),
  act (action)`.
- `g` grouping: `user_id → role_name` scoped by `dom`.
- `g2` grouping: `role_name → branch_id` for branch-scoped roles.
- Policy row: `p, <role>, <organization_id | "*">, <module>, <action>,
  allow|deny`.
- Matcher combines the domain check, `keyMatch(obj, p.obj)` for wildcards,
  and an explicit `allow`/`deny` effect.

The policy adapter is `gorm-adapter/v3` writing to a Postgres table
`casbin_rule`. Casbin auto-reloads every 30 s and on explicit signal after
policy writes.

## Rationale

- **Casbin** matches every requirement of the model out of the box: RBAC,
  domains (for org isolation), path matching (for future wildcarding like
  `sales:*`), and dynamic policy changes at runtime.
- Casbin's decision path is in-process — no network hop, sub-millisecond in
  practice.
- We keep policy in the same Postgres cluster as the rest of the app; there
  is no new store to back up or fail over.

OpenFGA was rejected because it introduces a new service and a ReBAC model
we do not need (we have roles, not relationships between users). OPA/Rego was
rejected because Rego is a new language for the team and the sidecar is extra
ops surface.

## Enforcement

- All authenticated routes pass through the `rbac` middleware
  (`internal/middleware/rbac.go`) after `auth` and `tenant`.
- The middleware constructs the request tuple from `sub = user's role`,
  `dom = organization_id in JWT`, `obj = <module>`, `act = <action>`. The
  handler chooses `<module>` and `<action>` via route metadata.
- On failure the request returns `403 FORBIDDEN`.

For SUPER_ADMIN we have a shortcut rule:  
p, SUPER_ADMIN, *, *, *, allow  
which is seeded on first boot and is not removable through any API.

## Policy Lifecycle

- Roles and permissions are **seeded** by `cmd/seed` from
  `seed/permissions.yaml` and `seed/roles.yaml`.
- System roles are marked `is_system=true` in the `roles` table and cannot be
  deleted through the API. Their default permission sets **can** be extended
  by SUPER_ADMIN but not shrunk below the required minimum documented in the
  role's constant list.
- Dynamic roles are created by SUPER_ADMIN through `POST /api/v1/roles` with
  any subset of the permission catalog.

## Consequences

### Positive

- Battle-tested library, active maintenance, wide adoption.
- Same engine can express future ABAC/RBAC-with-domain rules.
- Runtime policy changes without a restart.
- Policy is auditable — every `p` row is a database row.

### Negative

- Casbin uses reflection for its enforcer; on hot paths we cache the enforcer
  per request rather than reconstruct it.
- Model changes are subtle. Any change to `config/casbin_model.conf`
  requires a targeted integration test suite (`tests/integration/rbac/`).

### Neutral

- We use gorm-adapter purely as a persistence bridge; there is no GORM in
  the domain code, avoiding conflict with ADR-0002.

## Guardrails

- Never call the enforcer directly from a handler. Always go through the
  `rbac` middleware or the `Enforcer` interface in
  `internal/modules/rbac/service.go`.
- Every new permission (module + action pair) must be added to
  `seed/permissions.yaml` in the same PR that adds the feature.
- Integration tests must cover: cross-branch access denied,
  cross-organization access denied, SUPER_ADMIN always allowed, revoked
  session denied.

## References

- Casbin v2 docs, https://casbin.org/docs/overview
- RBAC-with-domains model example,
  https://casbin.org/docs/rbac-with-domains
- NIST SP 800-162 (ABAC guidance).