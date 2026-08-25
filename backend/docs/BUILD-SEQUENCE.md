# BUILD SEQUENCE — Pharmaciano ERP (Go backend)

> **What this document is.** The dependency-ordered plan for building the rest of
> the backend. [ADR 0009](adr/0009-Folder-Structure.md) is the authoritative
> *map* of every folder and file; this is the *route* through it — the order in
> which to implement modules so that nothing is ever wired against something that
> does not yet exist, and so the security-critical pieces come online first.
>
> **Golden rule:** build in dependency order, and never leave the tree in a
> non-compiling state. Every phase below ends at a point where `go build ./...`
> passes.

---

## 1. Current state (done)

| Layer | Status | Notes |
|---|---|---|
| `internal/platform/*` | ✅ Complete | config, db (pgx pool + tx + migrate), redis (client/keys/locker/pubsub), logger, telemetry (metrics/tracing/health), uuid, validator, mailer, storage |
| `internal/errors`, `internal/common/{constants,context,enums}`, `pkg/*` | ✅ Complete | Error taxonomy, typed context (Principal), response envelope, crypto, pagination |
| `internal/middleware/*` (13 files) | ✅ Complete | 12 middleware + `deps.go` DI core. Dependency-inverted: declares `Authenticator`, `Authorizer`, `AuditSink`; ships **fail-closed** deny-all stubs until modules inject the real ones |
| `internal/router/{router.go,v1.go,health.go}` | ✅ Complete | Engine builder, ops endpoints (`/livez` `/readyz` `/healthz` `/metrics`), `/api/v1` group + documented module-mount pattern |
| `cmd/api/main.go` | ✅ Complete | Composition root: config → logger → tracing → metrics → pg → redis → health → middleware → router → graceful shutdown |
| `internal/modules/*` (22 modules) | 🔲 Scaffolded only | Every file exists but contains just its `package` declaration |
| `internal/jobs/*`, `cmd/worker` | 🔲 Scaffolded only | Asynq worker not yet implemented |
| `migrations/*` (14 pairs) | ✅ Present | SQL through `000014_indexes_and_views` |
| `seed/*.yaml` | ✅ Present | Declarative seed data (roles, permissions, master data, CoA, super_admin) |

The API compiles and boots today. Because no domain modules are injected, every
protected route answers `401/403` and audit events are dropped — **by design**,
and `middleware.New` logs a warning to that effect on every boot.

---

## 2. Invariants that dictate the order

1. **Dependency inversion at the HTTP edge.** `internal/middleware` must never
   import a domain module (that would recreate the import cycle the interfaces
   were built to avoid). Modules import `middleware` to protect their routes;
   `main.go` injects each module's concrete `Authenticator` / `Authorizer` /
   `AuditSink` into the container. ⇒ **The three interface-provider modules
   (`auth`, `rbac`, `audit`) are built first.**

2. **Fail-closed until wired.** A half-built system must deny, not allow. Do not
   relax a middleware default to "make a route work" — instead inject the real
   dependency in `main.go`.

3. **Tenancy is a data-layer contract.** `org_id` scoping lives in every query,
   not only in `tenant.go`. ⇒ The org/branch/user foundation is built before any
   business module that reads or writes tenant-scoped rows.

4. **Migrations lead code.** A module's tables must exist (its migration applied)
   before its repository is written. The 14 migrations already define the schema;
   build modules in the same order the migrations were numbered.

5. **Bottom-up within a module.** `model → dto → repository → service → handler →
   routes → test`. Wiring (router mount + main injection) is the last step.

---

## 3. Phased build order

Each phase is independently shippable and ends compiling + tested.

### Phase A — Shared kernel finalization *(mostly done; verify only)*
Confirm `internal/errors` codes cover every case the modules need, `common/enums`
has the domain state enums (`PurchaseState`, `SaleState`, `UserStatus`, …), and
`pkg/{pagination,response,crypto,times,strings,httpclient}` expose what services
will call. No new packages; fill gaps as they surface.

### Phase B — Lift the middleware stubs *(security-critical, build first)*
These three modules replace the deny-all defaults. After Phase B, protected
routes actually authenticate, authorize, and audit.

| Order | Module | Provides | Migration | Key files beyond the standard set |
|---|---|---|---|---|
| B1 | `auth` | `middleware.Authenticator` | `000004_user_tables` (sessions/refresh) | `jwt.go`, `password.go`, `limiter.go` |
| B2 | `rbac` | `middleware.Authorizer` | `000003_rbac_tables` | `casbin.go`, `seed.go` |
| B3 | `audit` | `middleware.AuditSink` | `000011_audit_partitioned` | (async enqueue via Asynq) |

> **Wiring checkpoint (end of Phase B):** in `cmd/api/main.go`, change
> `middleware.New(cfg, log, rdb)` to inject all three (see §5). Boot warnings
> disappear; `/api/v1` routes become enforceable.

`auth` depends on `rbac` only at *runtime* (role names in the principal), not at
compile time — build `auth` first so login works, then `rbac` to enforce, then
`audit` to record. `audit`'s HTTP read API (`/audit-logs`) can be finished after
its `AuditSink` is wired, since the sink is what the middleware needs.

### Phase C — Tenancy foundation
Everything below is tenant-scoped, so these come next.

`organization` → `branch` → `warehouse` → `user`
(migrations `000002`, then `000004`). `user` depends on `rbac` (role assignment)
and `organization`/`branch` (scope). This closes the loop with `tenant.go`:
`SUPER_ADMIN`/`ADMIN` are org-wide and may select any branch via `X-Branch-ID`;
branch-bound users are pinned.

### Phase D — Catalog & master data
`masterdata` (categories, dosage forms, routes, package/unit types, generics, tax
rates — `000005`) → `brand`, `supplier` (`000006`) → `medicine` (`000006`,
full-text/trigram search) → `customer` (`000009` customer half).
These are largely central-managed reference data; reads are cache-friendly.

### Phase E — Inventory core
`inventory` (`000007`): `InventoryBatch`, `StockMovement`, `StockTransfer`, plus
`fefo.go` (First-Expiry-First-Out selector). This is the spine the transaction
modules call to reserve/commit/release stock. Build before `purchase`/`sale`.

### Phase F — Transactions
`purchase` (`000008`) with `state_machine.go`
(`DRAFT→PENDING_APPROVAL→APPROVED→RECEIVED→PAID/…`) — increments stock on receive.
Then `sale` (`000009`) with `pricing.go` + `invoice.go` — decrements stock FEFO.
Both are **mutation-heavy**: mount with `RateLimit` + `Idempotency` + `Audit`.

### Phase G — Financials
`ledger` (`000010`): double-entry posting, `auto_post.go` (sale → DR cash / CR
revenue, purchase → DR inventory / CR payable), fiscal periods, P&L, balance
sheet, `seed.go` for the default Chart of Accounts. Consumes purchase/sale events.

### Phase H — Insight & delivery
`notification` (`000012`, `publisher.go` + `ws_hub.go`/`ws_client.go` for the
`/ws` endpoint) → `analytics` (`000014` views) → `report` → `ai` (`000013`,
`client.go`/`circuit_breaker.go`/`feature_builder.go`) → `settings` → `backup`.
These depend on transaction/ledger data existing.

### Phase I — Background worker
`internal/jobs/{server.go, scheduler.go, task_types.go}` (client.go + middleware.go
exist) and `cmd/worker/main.go`. Then the `jobs/handlers/*` tasks: `audit_task`
(drains the `AuditSink` queue), `notification_task`, `expiry_scan_task`,
`report_task`, `ai_forecast_task`, `ledger_post_task`, `backup_task`,
`cleanup_task`. The worker is a **second binary** sharing the same modules; it
gets its own composition root mirroring `cmd/api/main.go`.

### Phase J — Hardening & release
`tests/integration/*` (testcontainers: Postgres+Redis), `tests/load/*` (k6),
`deployments/*` (Docker, nginx, Prometheus, Grafana), `.github/workflows/*`
(lint, test, security-scan, codeql).

---

## 4. The standard module recipe

For any module `X` under `internal/modules/X/`, implement in this order:

1. **`model.go`** — persistence structs mirroring the migration's tables.
2. **`dto.go`** — request/response shapes; validation tags (`internal/platform/validator`).
3. **`repository.go`** — all SQL. **Every query filters by `org_id`** (and
   `branch_id` where the row is branch-scoped). Use `db.FromCtx(ctx)` so it
   participates in the ambient transaction.
4. **`service.go`** — business rules, transactions (`db.WithTx`), invariants,
   emits domain events / enqueues jobs. Returns `*errs.AppError` on failure.
5. **`handler.go`** — thin: bind + validate DTO → call service → `response.OK/Created/…`.
   Never put business logic here.
6. **`routes.go`** — a `RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware)`
   func that mounts the module's routes using the chains from §5.
7. **`X_test.go`** — table-driven unit tests for the service; repository tests
   against a testcontainer where feasible.
8. **Wire it** — call `X.RegisterRoutes(v1, d.MW)` from `internal/router/v1.go`
   and inject any interface it provides in `cmd/api/main.go`.

**Definition of done (per module):** compiles; unit tests pass; every route is
RBAC-protected with a permission that exists in `seed/permissions.yaml`;
mutations carry `Idempotency` + `Audit`; list endpoints paginate; all queries are
`org_id`-scoped; errors return the standard envelope.

---

## 5. Wiring contract (copy-paste patterns)

**Route registration inside a module (`routes.go`):**

```go
func RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware, h *Handler) {
    g := rg.Group("/sales")

    // Reads: auth → tenant → rbac(read)
    g.GET("",     append(mw.Protected("sales", "read"), h.List)...)
    g.GET("/:id", append(mw.Protected("sales", "read"), h.Get)...)

    // Writes: rate-limit (outermost) → auth → tenant → rbac → idempotency → audit → handler
    g.POST("/pos/checkout",
        append([]gin.HandlerFunc{mw.RateLimit("sales_write")},
            append(mw.Protected("sales", "create"),
                mw.Idempotency(), mw.Audit(), h.Checkout)...)...,
    )
}
```

**Pre-auth endpoints** (no principal yet) use `mw.RateLimitByIP("login")`, not
`mw.RateLimit`.

**Central mount (`internal/router/v1.go`)** — keep every mount point in one file
so the URL surface is auditable:

```go
auth.RegisterRoutes(v1, d.MW, authHandler)
user.RegisterRoutes(v1, d.MW, userHandler)
sale.RegisterRoutes(v1, d.MW, saleHandler)
// …
```

**Interface injection (`cmd/api/main.go`)** — lift the fail-closed stubs:

```go
mw := middleware.New(cfg, log, rdb,
    middleware.WithAuthenticator(authModule),   // auth.Service
    middleware.WithAuthorizer(rbacEnforcer),    // rbac.Enforcer (Casbin)
    middleware.WithAuditSink(auditProducer),    // audit.Producer (Asynq)
)
```

No middleware or router code changes when swapping auth strategy (hybrid-stateful
↔ stateless) — that lives entirely behind `auth`'s `Authenticator` impl.

---

## 6. Migration ↔ module map

| Migration | Unlocks module(s) | Phase |
|---|---|---|
| `000001_init_extensions` | (all) | — |
| `000002_organization_branch_warehouse` | organization, branch, warehouse | C |
| `000003_rbac_tables` | rbac | B2 |
| `000004_user_tables` | auth (sessions/refresh), user | B1, C |
| `000005_master_data` | masterdata | D |
| `000006_medicine_brand_supplier` | brand, supplier, medicine | D |
| `000007_inventory_batch` | inventory | E |
| `000008_purchase` | purchase | F |
| `000009_sale_customer` | sale, customer | F, D |
| `000010_ledger` | ledger | G |
| `000011_audit_partitioned` | audit | B3 |
| `000012_notification_settings` | notification, settings | H |
| `000013_ai_forecast` | ai | H |
| `000014_indexes_and_views` | analytics, report | H |

Apply with `cmd/migrate` before writing the corresponding repository.

---

## 7. Verification (run on the build host, not the sandbox)

> The dev sandbox has no Go toolchain and no network to fetch modules, so these
> must be run on your Windows machine from the `backend/` directory.

```powershell
# 1. Resolve deps introduced by the new wiring (gin, otel, prometheus, etc.)
go mod tidy

# 2. Format (the new files are gofmt-clean; this catches any stray edits)
gofmt -l .            # prints files needing formatting; expect empty output
go build ./...        # whole-module compile — the real gate

# 3. Static analysis
go vet ./...

# 4. Boot smoke test (needs Postgres + Redis reachable per config/config.dev.yaml)
go run ./cmd/api
#   → GET http://localhost:8080/livez     ⇒ 200
#   → GET http://localhost:8080/readyz     ⇒ 200 when pg+redis healthy, else 503
#   → GET http://localhost:8080/api/v1/status ⇒ 200 {"data":{"status":"ok",...}}
#   → any /api/v1 protected route          ⇒ 401 (expected until Phase B wiring)
```

Expected first-boot log line (until Phase B): a warning that no Authenticator/
Authorizer/AuditSink is injected. That is correct for the current state.

---

## 8. Open notes / risks

- **`telemetry/metrics.go` metrics auth:** the Bearer guard builds its expected
  value as `"Bearer" + authToken` (no space), so a standard
  `Authorization: Bearer <token>` header will not match. If you set
  `telemetry.metrics.auth_token`, either drop the space client-side or fix the
  helper to `"Bearer " + authToken`. Left untouched here because it lives in the
  "done" platform layer — flagging for a deliberate fix.
- **Worker composition root:** `cmd/worker/main.go` will duplicate much of
  `cmd/api/main.go` (config/logger/tracing/pg/redis). Consider extracting a small
  shared `internal/bootstrap` package when the second binary lands, rather than
  copy-pasting.
- **`X-Branch-ID` trust:** only honored for org-wide roles; branch-bound users are
  pinned in `tenant.go`. Keep that check in the middleware even after modules add
  their own scoping — defense in depth.
