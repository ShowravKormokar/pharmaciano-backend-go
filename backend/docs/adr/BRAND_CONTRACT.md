# Brand Module Contract

**Project:** Pharmaciano:ERP — Go backend  
**Module:** `internal/modules/brand`  
**Status:** Architecture and implementation contract  
**API base:** `/api/v1`  
**Storage:** PostgreSQL  
**Scope:** Organization-owned Brand catalog, shared across the organization's branches

---

## 1. Executive decisions

This contract guides implementation against the existing Pharmaciano foundation. It does not authorize rewriting completed infrastructure.

1. **Brand is not a MasterData resource.** The completed `MASTERDATA_IMPLEMENTATION_CONTRACT.md` explicitly excludes Brand, Manufacturer, and Supplier from MasterData's eleven reference-data resources.
2. **The `brands` table already exists** in `migrations/000005_master_data.up.sql`. Reuse it. Do not create a second table or re-create its schema.
3. **`internal/modules/brand/model.go` is correct and necessary.** It is the owning Go model for the existing `brands` table, not a duplicate MasterData model. Brand must remain absent from `masterdata/model.go`.
4. **Existing migrations are authoritative.** Inspect the real migration before finalizing struct fields, tags, SQL, indexes, FK actions, and nullability. Never edit an already-applied migration. Add a new forward migration only for a verified schema gap.
5. **Implement only the published Brand API** (`/brands`). Do not silently add Manufacturer CRUD endpoints or absorb Manufacturer ownership into this module. Validate the existing manufacturer reference through an approved, tenant-safe contract.
6. **Brand is organization-scoped**, shared across branches within that organization, and never globally shared across tenants by default.
7. **All database access is tenant-scoped.** `organization_id` comes from trusted application context, never from request body/query/header input.
8. **Soft delete only.** A referenced Brand cannot be deleted while medicines depend on it; return `409 CONFLICT` and direct the caller to deactivate it.
9. **Reuse the foundation:** existing auth, RBAC, middleware, `appctx`, `platform/db`, `common.BaseModel`, pagination, validator, HTTP response helpers, error mapping, logger, and router.
10. **No Redis cache initially.** PostgreSQL is authoritative. The inspected foundation has no established generic cache-aside/invalidation contract for this catalog.
11. **No new event/outbox mechanism just for Brand.** Astra's architecture notes that outbox/event infrastructure is not implemented in the inspected repository. Add events only after the shared contract exists.
12. **Use actual configured rate-limit names.** The MasterData contract found API-draft names `rate_auth_read`/`rate_auth_write` differ from configured names `auth_read`/`auth_write`; unknown policy names may mean no effective limit.

### Contract authority

If documentation conflicts, use: (1) current code/migrations for existing infrastructure, (2) this approved Brand contract, (3) `MASTERDATA_IMPLEMENTATION_CONTRACT.md` for shared conventions and boundaries, (4) Astra's architecture document for ownership/dependencies, then (5) older `API.md` unless explicitly superseded.

---

## 2. Purpose and ownership

Brand owns the commercial identity of a medicine brand as maintained by one organization.

### Owns

- Brand model and access to the existing `brands` table.
- Brand request/query/response DTOs.
- Brand validation, lifecycle, and duplicate-code handling.
- Tenant-scoped repository and application service.
- HTTP handlers, route registration, OpenAPI definitions, and tests.
- Brand-specific operational logs/metrics where supported by existing conventions.

### Does not own

| Concern | Owner |
|---|---|
| Global reference dictionaries: routes, dosage forms, units, countries, etc. | `masterdata` |
| Manufacturer lifecycle/API | Separate approved contract; not added here |
| Suppliers and supplier relationships | `supplier` |
| Medicine identity, ingredients, strength, packaging | `medicine` |
| Batches, stock, movement, warehouse balances | `inventory` |
| Purchase, sale, returns, journals, ledger | `purchase`, `sale`, `ledger` |
| Authentication and sessions | `auth` |
| Permissions and roles | `rbac` |
| Database pool/transaction context | `platform/db` |
| Shared request audit sink | Existing middleware/platform audit |
| Business audit/outbox | Shared `audit` contract when implemented |

A manufacturer produces a brand; a supplier distributes/sells products to the pharmacy. They are not interchangeable. Do not use `manufacturer_id` as `supplier_id`.

---

## 3. Scope and tenant isolation

### 3.1 Organization-owned, branch-shared

- Brand belongs to exactly one organization.
- The same real-world Brand may be represented separately by different organizations.
- Branches in the same organization share the Brand catalog; do not add `branch_id` to Brand unless a separately approved requirement changes ownership.
- Branch-scoped staff may read brands when `brands:view` is granted. Branch scope does not grant Brand write permissions.
- Every repository operation must receive a trusted `orgID` from the application context.
- Never accept `organization_id` from a create/patch DTO.
- Never treat `nil`/zero org ID as a global-access escape hatch.
- Manufacturer references must be validated as belonging to the same organization unless a future, approved manufacturer contract explicitly defines a global reference model.

For every read or mutation, enforce predicates equivalent to:

```sql
WHERE organization_id = $trusted_org_id
  AND id = $brand_id
  AND deleted_at IS NULL
```

Manufacturer lookups must also be tenant-scoped. A caller-provided UUID is not proof of ownership.

### 3.2 IDOR and enumeration

- Detail, patch, and delete must not reveal whether a Brand exists in another tenant.
- Cross-tenant Brand IDs should behave like not-found resources.
- Cross-tenant manufacturer IDs must be rejected without leaking the other tenant's data.
- Never trust tenant identity from a raw header, body, query, or path parameter.
- Test cross-tenant list/detail/patch/delete paths explicitly.

---

## 4. Existing schema and `model.go`

### 4.1 Why a Brand model is needed

The MasterData contract removed `Brand`, `Manufacturer`, and `Supplier` structs from `masterdata/model.go`, while preserving their existing tables because other modules reference them. This is the intended ownership boundary:

- `masterdata/model.go`: only its eleven approved reference resources.
- `brand/model.go`: the Brand model mapped to the existing `brands` table.
- `medicine/model.go`: medicine's own model and Brand ID reference; do not duplicate mutable Brand fields there.
- Supplier/manufacturer models belong to their separately approved owning boundaries.

A Go struct maps a table; it does not create a table. The Brand model is required for Brand's own repository, service, and response mapping.

### 4.2 Mandatory inspection before implementation

Read the checked-out versions of:

- `migrations/000005_master_data.up.sql` and its down migration.
- `migrations/000006_medicine_brand_supplier.up.sql`.
- `common.BaseModel` / `AuditColumns` / UUID types and actual package paths.
- `warehouse/model.go`, `repository.go`, `service.go`, `handler.go`, and `routes.go`.
- `masterdata/model.go` only to verify the boundary.
- `ModuleBrands` and the current permission/action constants.
- Router registration, configured rate-limit names, pagination, validator, error mapper, and DB transaction helpers.
- Existing Brand indexes, triggers, FK actions, and unique index predicates.

Do not trust old documentation line numbers; inspect the actual checked-out revision.

### 4.3 Model design rule

Exact columns and tags must match the current migration. The existing schema is documented as including organization scope, code/name, `manufacturer_id`, active state, lifecycle timestamps/deletion metadata, and indexes; verify all of these rather than assuming.

Illustrative shape only:

```go
type Brand struct {
    common.BaseModel // reuse only if its fields/tags match the real table
    OrganizationID   uuid.UUID
    Code             string
    Name             string
    ManufacturerID   uuid.UUID
    IsActive         bool
}
```

This is not copy-ready until the actual migration has been inspected. Do not invent fields such as logo URL, website, country, aliases, registration numbers, or arbitrary metadata without an approved requirement and migration.

`model.go` must not contain DTOs, SQL, Gin types, authorization logic, a duplicate BaseModel, or unrelated Manufacturer/Supplier structs.

### 4.4 Migration policy

- Do not create a duplicate `brands` table.
- Do not rename existing columns for stylistic preference.
- Never edit migrations that may have run.
- Migration `000024_brand_tenant_ownership` adds organization ownership and a tenant-safe manufacturer FK to the formerly global Brand/Manufacturer tables. It deliberately aborts if either catalog already has rows; production rollout must provide an explicit, reviewed ownership mapping instead of silently assigning legacy rows.
- Its down migration also refuses to discard tenant ownership while either catalog contains rows; restore such deployments with a reviewed forward migration.
- Add a new migration only for a verified missing constraint/index/column.
- Check existing data before adding uniqueness constraints.
- Preserve existing `manufacturer_id` nullability and FK behavior unless a reviewed migration explicitly changes them.
- Test migrations against a disposable PostgreSQL database; record whether a live environment has actually applied them.

---

## 5. Domain invariants

1. Brand UUID is the stable technical identity; `code` is an organization-local business identifier; `name` is a display/search label.
2. Required values cannot be empty or whitespace-only.
3. Trim/normalize code and name consistently with project conventions and database uniqueness semantics.
4. Bound lengths to the actual SQL column sizes and OpenAPI schema.
5. Database unique constraints are the final authority for code uniqueness. A pre-insert existence check alone is race-prone.
6. Translate PostgreSQL unique violations into the project's existing duplicate/conflict error.
7. Name similarity is a warning or separately approved policy, not proof of identity. Never auto-merge brands.
8. Manufacturer reference must exist and be valid within the approved tenant boundary.
9. Inactive Brands remain resolvable for historical views but should not be newly selectable where active-only selection is required.
10. Brand changes must not rewrite historical invoice/document snapshots.
11. Brand never mutates medicine, stock, purchase, sale, or ledger records.
12. No cascade delete from Brand into dependent domain data.
13. Every mutation preserves the existing timestamp/version conventions.
14. Manufacturer reassignment after medicine references exist should be rejected by default unless an explicit correction workflow is approved.
15. Do not add a Brand alias table or fuzzy-duplicate service in this CRUD phase.

---

## 6. Lifecycle and safe deletion

### 6.1 Active versus deleted

- `is_active = false`: reversible operational deactivation; row remains addressable and references remain valid.
- `deleted_at IS NOT NULL`: soft-deleted and excluded from normal list/detail APIs.
- Neither operation physically deletes the database row through the API.

Use deactivation when a Brand remains referenced but should no longer be selectable. Use DELETE only when policy allows a genuinely unused Brand to be soft-deleted.

### 6.2 Reference-aware deletion

The existing migration contract states `medicines.brand_id` is required and restrictive for physical deletion. A soft delete does not trigger the database FK's `ON DELETE RESTRICT`; service policy must therefore protect the relationship.

Recommended sequence:

1. Start a short transaction using existing `db.WithTx` conventions.
2. Load the Brand by `(organization_id, id)` with the appropriate lock.
3. Check for live medicine references using the actual table/FK column.
4. If referenced, return `409 CONFLICT` and recommend `PATCH {is_active:false}`.
5. Otherwise soft-delete with tenant and `deleted_at IS NULL` predicates.
6. Make reference creation and deletion safe against races through a documented lock order or database-enforced invariant. A non-locking `SELECT EXISTS` followed by UPDATE is insufficient under concurrency.
7. Confirm whether the existing partial unique index permits code reuse after soft delete; do not assume.
8. Do not probe unrelated tables that do not actually reference Brand; confirm inbound FKs in migrations.

Medicine must independently reject assigning an inactive/deleted Brand for a new medicine. Historical medicine records must continue resolving an inactive Brand.

---

## 7. API contract

All paths are relative to `/api/v1`.

| Method | Path | Permission | Rate policy in draft | Behavior |
|---|---|---|---|---|
| POST | `/brands` | `brands:create` | `rate_auth_write` | Create; return 201 |
| GET | `/brands` | `brands:view` | `rate_auth_read` | List/filter/paginate |
| GET | `/brands/{id}` | `brands:view` | `rate_auth_read` | Tenant-scoped detail |
| PATCH | `/brands/{id}` | `brands:update` | `rate_auth_write` | Partial update |
| DELETE | `/brands/{id}` | `brands:delete` | `rate_auth_write` | Reference-aware soft delete |

**Rate limit:** the MasterData implementation contract found the draft's `rate_auth_read`/`rate_auth_write` names differ from actual configured `auth_read`/`auth_write`. Verify current config and route signatures; use a registered policy. Do not silently mount a nonexistent policy or alter global middleware to accommodate this module.

### 7.1 List filters

- `manufacturer_id`: optional UUID.
- `q`: optional bounded search string.
- `is_active`: optional boolean.
- Existing pagination DTO/defaults/max page size.
- Sorting only on allow-listed supported columns, with stable `id` tie-breaker.

All predicates must compose conjunctively. A manufacturer filter must not be ignored when other filters or scopes exist. Exclude soft-deleted rows. Use parameterized SQL and database-side filtering. Avoid N+1 manufacturer queries; use a tenant-safe join/batch load if the response requires manufacturer details.

### 7.2 DTOs

**Create DTO:** only approved client-controlled fields that exist in the schema, likely `code`, `name`, `manufacturer_id` after migration verification.

**Patch DTO:** only approved mutable fields. Preserve “omitted” versus “provided zero value” semantics. Do not allow clients to set `id`, `organization_id`, timestamps, `deleted_at`, audit fields, or server-controlled version. Use existing validator helpers and tags.

**Query DTO:** `manufacturer_id`, `q`, `is_active`, and established pagination/sort fields. Validate UUIDs, booleans, length bounds, page size, and sort allow-list.

**Response DTO:** explicit public fields such as `id`, `code`, `name`, `manufacturer_id`, `is_active`, `created_at`, `updated_at`, and `version` only if supported by schema/API. Include a manufacturer summary only if the API contract explicitly requires it. Do not serialize database models blindly.

### 7.3 Status and errors

| Condition | Result |
|---|---|
| Valid create | `201 Created` |
| Valid reads/patch | Existing shared success envelope |
| Successful soft delete | `204 No Content` if consistent with existing convention |
| Malformed UUID/query/body | `400 VALIDATION_ERROR` |
| Missing/deleted/cross-tenant Brand | `404 NOT_FOUND` |
| Duplicate code | Existing canonical `409` duplicate/conflict error |
| Referenced Brand delete | `409 CONFLICT` |
| Invalid/cross-tenant manufacturer | Existing validation/business/not-found mapping without tenant leakage |
| Missing permission | `403 FORBIDDEN` |
| Authentication failure | Existing auth middleware response |
| DB failure | Generic `500 DATABASE_ERROR`; details only in structured logs |
| Stale version | Existing conflict/precondition behavior |

Reuse `internal/errors` and the current HTTP error mapper. Add no parallel error framework. Add a new code only when necessary and update every required status mapping.

---

## 8. Authentication, RBAC, middleware, and audit

### 8.1 Required request flow

Follow the exact order and signatures in the current router and `warehouse/routes.go`. Conceptually:

1. Existing versioned router.
2. Authentication/session middleware.
3. Tenant context middleware.
4. `Protected(ModuleBrands, action)` / RBAC.
5. Registered read/write rate limiter.
6. Existing idempotency middleware where required by route conventions.
7. Existing audit middleware on mutations.
8. Thin handler and DTO validation.
9. Service with trusted organization context and domain rules.
10. Tenant-scoped repository.

Do not create local auth, a second RBAC system, or a Brand-specific middleware chain.

### 8.2 Permission matrix

| Caller | List/detail | Create | Patch | Delete |
|---|---:|---:|---:|---:|
| `SUPER_ADMIN` with relevant permission | Yes | Only with `brands:create` | Only with `brands:update` | Only with `brands:delete` |
| Organization ADMIN with explicit grant | Yes | If granted | If granted | If granted |
| Manager/cashier with `brands:view` only | Yes | No | No | No |
| Authenticated caller without permission | No | No | No | No |
| Anonymous caller | No | No | No | No |

Do not assume SUPER_ADMIN bypasses permissions unless current RBAC explicitly provides that behavior.

### 8.3 Service defense

The service derives `orgID` using the existing trusted `appctx` helper. Repository methods require `orgID`. This prevents future internal service callers from accidentally issuing unscoped queries.

### 8.4 Audit semantics

Mount existing `mw.Audit()` on mutation routes according to established convention. The inspected middleware is technical/request audit, not a complete transactional before/after business history.

- Keep existing redaction behavior.
- Never log bearer tokens, credentials, or full arbitrary request bodies.
- Do not create a Brand-specific audit table.
- Do not claim middleware audit can roll back a Brand write if it records after the handler transaction has committed.
- If the Audit module later exposes a transactional business-audit port, use it only through that approved interface.

---

## 9. Idempotency and concurrency

### Create

Use the existing `mw.Idempotency()` according to route conventions, but do not rely on Redis request replay as the sole correctness guarantee if the middleware can fail open. Database uniqueness protects duplicate codes under concurrent requests. If durable domain command identity is already available, use it; otherwise document the limitation rather than inventing a separate idempotency framework.

### Update

- If the existing Brand schema has a `version` column, use the established optimistic concurrency convention.
- If no version exists, do not add one solely for symmetry without migration review; use an atomic update or short row-lock transaction where required.
- Keep transactions short; never call external services while holding a lock.

### Delete/reference race

A concurrent medicine creation must not attach to a Brand while another request deletes it. Coordinate the locking protocol with Medicine's service contract. Use a consistent lock order or DB-enforced invariant; test the race. Do not assume a plain reference probe is sufficient.

---

## 10. Repository contract

Repository owns SQL, tenant predicates, scanning, filters, pagination, locking, and persistence error inputs. It does not own authorization or business policy.

Expected capabilities, named to match local conventions:

- `Create(ctx, orgID, input)`
- `FindByID(ctx, orgID, brandID)`
- `FindByIDForUpdate(ctx, orgID, brandID)` where locking is required
- `List(ctx, orgID, filter, pagination)`
- `Update(ctx, orgID, brandID, patch)`
- `SoftDelete(ctx, orgID, brandID)`
- `IsReferencedByMedicine(ctx, orgID, brandID)` based on the actual FK
- Narrow manufacturer resolution only if needed by the approved manufacturer contract

Do not expose a generic repository accepting arbitrary table/column names or SQL fragments. A small unexported helper is acceptable only if consistent with existing project patterns.

### Database access

- Use `db.FromCtx(ctx)` for writes/transaction-bound work and `db.ReadFromCtx(ctx)` for reads if these are the actual current APIs.
- Participate in the caller's transaction; never commit a parent business transaction.
- Use parameterized SQL.
- Use existing helpers for unique/FK/check violations and canonical constraint names.
- Never expose raw PostgreSQL errors to clients.
- Do not load all brands into Go to apply filters.

Conceptual query shape (adapt to actual columns and pagination helpers):

```sql
WHERE organization_id = $1
  AND deleted_at IS NULL
  -- optional manufacturer_id
  -- optional is_active
  -- optional bounded q
ORDER BY <allow-listed-column>, id
LIMIT $n OFFSET $n
```

---

## 11. Indexes, constraints, and migration rules

Inspect existing migrations/indexes first. Likely query needs include:

- Organization-local unique code, matching existing partial-index and normalization semantics.
- `(organization_id, manufacturer_id)` for manufacturer-filtered lists if no equivalent index exists.
- Existing name trigram index for `q`, if present.
- Index support for the actual medicine reference probe if needed at scale.
- Tenant-safe FK support for manufacturer references.

Do not add speculative indexes. Validate with realistic `EXPLAIN (ANALYZE, BUFFERS)` and weigh read speed against write/storage overhead.

Constraints should enforce relational invariants where possible. Preserve restrictive FK behavior; no cascade deletes. If a composite tenant FK requires a unique `(organization_id, id)` target, inspect all current references and migration order first. Check existing data before creating new unique indexes. Use a new migration number and test up/down against a disposable database. After production data exists, forward repair is generally safer than destructive rollback.

---

## 12. Service layer

### Responsibilities

- Derive trusted organization scope.
- Normalize and validate input.
- Validate manufacturer eligibility and tenant ownership.
- Enforce active/deleted rules.
- Coordinate reference-aware deletion.
- Apply concurrency controls.
- Map database errors to existing domain errors.
- Write only Brand-owned tables.
- Use existing structured logging/correlation IDs.

### Must not

- Import Gin/HTTP types.
- Execute SQL or access raw pools directly.
- Trust client-supplied tenant ID.
- Write another module's tables.
- Mutate medicine, inventory, purchase, sale, or ledger state.
- Introduce Redis caching or an unimplemented outbox.
- Log sensitive request data.
- Make network calls inside a DB transaction.
- Treat every DB failure as “not found.”

### Transaction guidance

| Operation | Transaction |
|---|---|
| Single insert with DB-enforced constraints | Usually no multi-statement transaction needed |
| Single atomic patch | One `UPDATE` may suffice |
| Read-modify-write/row lock | Required |
| Reference probe followed by soft delete | Required with locking strategy |
| Manufacturer reassignment plus validation | Required if check/write must be atomic |
| List/detail read | No write transaction |
| Future outbox insert | Must share business transaction, once shared outbox exists |

Use the project's normal isolation level. Do not default to serializable transactions without a demonstrated write-skew risk.

---

## 13. Handler, routes, and OpenAPI

### Thin handlers

1. Bind JSON/query using existing `httpx` helpers and validator.
2. Parse `:id` using the same helper as existing modules; malformed UUID should be `400`, not `404`.
3. Call the service with `c.Request.Context()`.
4. Render via shared response/error helpers.
5. Contain no SQL, tenant policy, duplicate logic, or cross-module orchestration.

### Routes

Use the existing module registrar and `constants.ModuleBrands` (verify actual constant path/name). Register through the existing router. Apply `Protected`, configured rate-limit names, `Audit`, and idempotency according to local signatures and route conventions.

```text
POST   /brands      -> brands:create + write limit + audit + idempotency per convention
GET    /brands      -> brands:view + read limit
GET    /brands/:id  -> brands:view + read limit
PATCH  /brands/:id  -> brands:update + write limit + audit
DELETE /brands/:id  -> brands:delete + write limit + audit
```

This is a policy sketch; copy middleware signatures from the checked-out source.

### OpenAPI

The project uses split contract-first YAML and existing lint/bundle/codegen/drift checks. Add Brand schemas and paths in the established directories; add the tag and references to root OpenAPI; reuse shared pagination/error/UUID schemas; ensure stable operation IDs and correct path/query schemas. Regenerate generated artifacts through project commands; never hand-edit generated code or introduce Swagger annotations/another server stack.

---

## 14. Observability and performance

Use existing structured logging and metrics, not a new telemetry stack.

Log module/action, request/correlation ID, duration, error class, and permitted organization/Brand IDs. Never log tokens, credentials, or full arbitrary bodies. Avoid high-cardinality metric labels such as raw Brand IDs, names, and search queries.

If the existing metrics system supports module metrics, useful signals include request count by route/status, latency, duplicate-code conflicts, invalid manufacturer references, reference-aware delete conflicts, and database failures. Avoid duplicate HTTP metrics if middleware already records them.

Brand is read-heavy. Prioritize tenant-scoped queries, indexed manufacturer filtering, bounded name search, deterministic pagination, and avoiding N+1 lookups. Add no Redis cache until measurement justifies it and tenant-safe invalidation/versioning is defined.

---

## 15. Testing contract

### Unit tests

- Code/name normalization and bounds.
- Invalid create DTOs.
- Patch omitted-vs-zero semantics.
- Duplicate error mapping.
- Invalid manufacturer handling.
- Active/inactive behavior.
- Referenced Brand deletion conflict.
- Safe error responses.
- Stale-version conflict if versioning exists.

### Repository/integration tests

- Organization A can create/list/get its Brand.
- Organization B cannot list/get/update/delete A's Brand.
- Cross-tenant manufacturer reference is rejected.
- Soft-deleted rows are excluded from normal reads.
- Duplicate code is rejected by DB even under concurrent creates.
- Manufacturer filter composes with `q` and `is_active`.
- Sorting is allow-listed and stable.
- Reference probe matches the actual `medicines.brand_id` FK.
- Delete and medicine creation cannot race into an invalid state.
- Existing FK constraints and migrations remain valid.
- SQL errors are mapped without leaking query text.
- No duplicate Brand table/model is introduced into MasterData.

### Handler/security tests

- Each route requires the correct permission.
- Missing auth is rejected by existing middleware.
- Missing permission returns the shared forbidden response.
- Invalid UUID returns 400.
- Cross-tenant ID returns not found.
- Query binding and pagination work correctly.
- Actual configured rate policy is used.
- Audit middleware is mounted on mutation routes.
- Idempotency matches shared middleware semantics.
- Error envelope matches the rest of the API.
- OpenAPI schema and handlers agree.

| Threat | Mitigation/test |
|---|---|
| IDOR | Cross-tenant list/detail/patch/delete tests |
| Mass assignment | Strict DTO allow-list |
| SQL injection | Parameterized SQL and sort allow-list |
| Forged manufacturer ID | Tenant-safe reference validation |
| Duplicate race | DB uniqueness and concurrent test |
| Delete/reference race | Locking/constraint test |
| Enumeration | Uniform not-found for cross-tenant IDs |
| Oversized search | Bounded query DTO |
| Log leakage | Assert secrets are absent |
| Unauthorized mutation | RBAC route tests |
| Stale update | Version/lock tests if applicable |

---

## 16. Failure handling

| Failure | Behavior |
|---|---|
| Duplicate code | Stable 409; no partial write |
| Invalid/cross-tenant manufacturer | Reject without tenant data leakage |
| Missing/deleted Brand | 404 according to shared convention |
| Brand referenced by medicine | 409; recommend deactivate |
| Database unavailable | Generic DB error; detailed internal log |
| Unique/FK/check violation | Existing DB/error helper mapping |
| Audit middleware failure | Follow actual shared middleware semantics; don't claim rollback unless guaranteed |
| Redis unavailable | Brand must not add a new Redis dependency |
| Stale version | Existing conflict/precondition response |
| OpenAPI drift | CI fails until spec/generated output are reconciled |
| Migration failure | Stop rollout; don't bypass or edit historical migration |

A failed mutation must not leave partially updated Brand-owned state.

---

## 17. File structure

Start with the smallest useful structure:

```text
internal/modules/brand/
├── model.go       # maps Brand to existing brands table
├── dto.go         # create, patch, query/filter, response DTOs
├── repository.go  # tenant-scoped PostgreSQL access
├── service.go     # domain rules, scope, lifecycle, transactions
├── handler.go     # thin HTTP handlers
├── routes.go      # existing middleware + registration
└── brand_test.go  # unit tests; split when useful
```

Add optional files only when needed:

- `mapper.go`: only if mapping is nontrivial or local conventions favor it.
- `manufacturer_port.go`: only for a stable approved read interface; don't duplicate Manufacturer's model.
- Separate repository/handler test files when test size justifies them.
- `events.go` only after shared outbox contract exists.
- `seed.go` only for an approved import/seed requirement; never seed fictitious production brands.
- `aliases.go` is out of scope until alias behavior is approved.

Do not create empty directories or speculative abstractions.

---

## 18. Implementation phases

### Phase 01 — Schema and contract reconciliation
- Inspect current migration, indexes, triggers, FK actions, and actual columns.
- Confirm manufacturer ownership/reference contract.
- Freeze deletion and manufacturer-reassignment rules.
- Verify current RBAC actions and configured rate-limit names.
- Record verified schema gaps before coding.

**Exit:** no duplicate table, no unclear tenant boundary, no unresolved middleware assumptions.

### Phase 02 — Model and DTO
- Implement `Brand` in `brand/model.go` mapped to the existing table.
- Reuse `common.BaseModel` only if fields/tags align.
- Implement create, patch, list/filter, and response DTOs.
- Use existing UUID, pagination, validator, and time conventions.
- Add no repository/business logic yet.

**Exit:** model matches migration; DTOs prevent mass assignment; validation tests pass.

### Phase 03 — Repository
- Implement tenant-scoped create/get/list/update/soft-delete.
- Use existing DB context/transaction helpers.
- Compose filters correctly and allow-list sorting.
- Add reference probe only after confirming actual FK.
- Map unique/FK/check violations through existing helpers.

**Exit:** tenant isolation and query correctness tests pass.

### Phase 04 — Service and lifecycle
- Derive organization ID from trusted context.
- Validate inputs/manufacturer reference.
- Implement duplicate mapping, safe patch, reference-aware deletion.
- Use short transactions/locks for multi-statement invariants.
- Use existing error taxonomy.

**Exit:** domain and concurrency invariants tested.

### Phase 05 — Handler, routes, middleware
- Add thin handlers and registrar.
- Use existing `Protected`, real configured rate limits, audit, and idempotency conventions.
- Register in current router.
- Add route permission and security tests.

**Exit:** no duplicate auth/RBAC logic; routes behave consistently.

### Phase 06 — OpenAPI
- Add split YAML schemas/paths and root references.
- Run the existing lint/bundle/codegen/drift commands.
- Confirm actual response/error/pagination behavior.

**Exit:** contract and handlers agree; compatibility gates pass.

### Phase 07 — Hardening
- Run `gofmt`, focused tests, `go test ./...`, `go vet ./...`, and project lint.
- Run DB integration tests if the configured test database is available.
- Review migration/query plans, tenant isolation, logging, rate limits, and delete/reference races.
- Verify MasterData files/routes were not changed to reintroduce Brand.
- Report unrun checks honestly.

**Exit:** all applicable checks pass and deferred limitations are documented.

---

## 19. Definition of done

- [ ] Existing `brands` table reused; no duplicate table or destructive historical migration.
- [ ] Brand remains absent from MasterData model/API/routes.
- [ ] Every repository operation requires trusted organization scope.
- [ ] Cross-tenant IDs cannot be read or mutated.
- [ ] Manufacturer reference follows approved ownership and tenant rules.
- [ ] Five published endpoints match the API/OpenAPI contract.
- [ ] Duplicate codes are enforced by PostgreSQL and mapped consistently.
- [ ] Referenced Brand cannot be soft-deleted silently.
- [ ] Active/deactivated/deleted semantics are distinct.
- [ ] Existing auth/RBAC/rate-limit/audit/idempotency conventions are reused.
- [ ] No new Redis or unimplemented outbox dependency.
- [ ] DTOs prevent mass assignment and response leakage.
- [ ] Filters/pagination/sorting are bounded and deterministic.
- [ ] Tests cover authorization, tenant isolation, duplicate races, and lifecycle.
- [ ] OpenAPI and Go checks pass.
- [ ] Migration status and limitations are documented.

---

## 20. Explicit non-goals and decisions to freeze

Not included: Manufacturer CRUD endpoints, supplier relationships, global cross-tenant catalog, aliases/fuzzy merge, external catalog sync, logo/image upload, Redis cache, outbox publishing before shared infrastructure exists, hard delete, automatic duplicate merging, or any medicine/inventory/purchase/sale/ledger side effects.

| Decision | Default |
|---|---|
| Brand scope | Organization-owned, shared across branches |
| Model location | `internal/modules/brand/model.go` |
| Database | Reuse existing `brands` table |
| Manufacturer API | Out of scope; validate reference through approved contract |
| Delete when medicine references exist | 409; deactivate instead |
| Manufacturer reassignment after references | Reject unless correction workflow is approved |
| Cache | None initially |
| Events | None until shared outbox exists |
| Similar names | Do not auto-merge |
| Code uniqueness | Existing PostgreSQL unique index is authoritative |
| Rate-limit names | Use registered names in actual config |
| Schema changes | Additive migration only for verified gaps |

---

## Final implementation instruction

Implement Brand as a small, schema-aligned, tenant-safe module in the existing modular monolith. The critical mistakes to avoid are reintroducing Brand into MasterData, duplicating the existing table, trusting client-supplied tenant/manufacturer IDs, copying stale rate-limit names, and soft-deleting a Brand while medicines still reference it.

**Inspect the checked-out migrations and module patterns first. Implement only what the actual schema and this contract justify.**
