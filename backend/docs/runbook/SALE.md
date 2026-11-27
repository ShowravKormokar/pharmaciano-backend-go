# Sale Module Runbook

This runbook describes the operational, monitoring, troubleshooting, and release
procedures for the Pharmaciano ERP Sale module (`internal/modules/sale`).

It is the operational companion to `docs/adr/SALE_CONTRACT.md` and
`docs/adr/SALE_IMPLEMENTATION_PLAN.md`. It must be read together with
`docs/runbook/deployment.md` and `docs/runbook/incident_response.md`.

------------------------------------------------------------------------

## 1. Module Overview

### 1.1 Purpose

The Sale module records and orchestrates the commercial sale/dispensing of
medicine and related products while preserving stock, valuation, payment,
accounting, invoice, audit, and tenant consistency.

### 1.2 Ownership

- Sale owns POS checkout orchestration, sale documents and immutable line
  snapshots, pricing/discounts, tenders, invoice issuance, credit-sale
  orchestration, returns, refunds, voids, and cashier sessions.
- Sale does NOT own stock, batch identity, cost layers, GL accounts, journals,
  customer master, medicine identity, or payment-provider secrets.
- Cross-module writes are forbidden. Sale orchestrates; Inventory and Ledger
  expose narrow transaction-aware ports only.

### 1.3 Module Status

**Current state: design-only (not ready for mutation traffic).**

- Persistence models exist (`model.go`), but there are no services, repositories,
  handlers, or tests for mutation workflows.
- Sale is NOT mounted in the API router (`RegisterRoutes` is intentionally not
  wired in `cmd/api/main.go`).
- Existing read endpoints (`GET /sales`, `GET /sales/:id`,
  `GET /sales/:id/invoice`) are registered in `routes.go` but are not served
  until the module is wired behind a release gate.
- No `features.sale_read_api` flag exists yet; the module is entirely unmapped
  until the readiness gates in `SALE_CONTRACT.md §26` pass.

Treat the Sale API surface documented in `docs/api/API.md` and the OpenAPI spec
as proposed, not implemented.

------------------------------------------------------------------------

## 2. Scope and Boundaries

### 2.1 In scope

- POS lookup, calculation/quotation, checkout (cash/offline only in first slice).
- Retail, wholesale, and explicitly configured sale channels.
- Effective-dated price books and branch overrides.
- Discounts, coupons, promotions, and authorized overrides.
- Tax calculation and immutable tax snapshots.
- Cash/offline/split tender and customer-credit sale orchestration.
- Remote payment intents and provider reconciliation (later gate).
- FEFO allocation through Inventory (internal port).
- Sale/line/allocation snapshots.
- Invoice numbering and immutable invoice payloads.
- Controlled void/reversal workflow.
- Returns, dispositions, credit notes, refunds, and refund reconciliation.
- Cashier/drawer sessions and close.
- Minimal prescription review/evidence references.
- Sale history, invoice reads, exports, analytical source facts.

### 2.2 Out of scope (owned by other modules)

- Stock, batch master, balance, reservation, movement, cost layers --- Inventory.
- GL accounts, journal construction, account mapping, period control, reversals --- Ledger.
- Customer master/contact/consent --- Customer.
- Medicine/variant/barcode/package identity --- Medicine.
- Global units, currencies, tax categories/rules --- MasterData/tax owner.
- Authentication, permission evaluation, role definitions --- Auth/RBAC.
- Payment-provider secrets --- platform secret/configuration infrastructure.
- PDF rendering, notifications, analytics projections, export artifact storage
  as synchronous checkout work.

### 2.3 Dependency direction

Sale orchestrates. Inventory and Ledger do NOT import Sale. All transaction-aware
ports use the caller's transaction context (`db.WithTx` / `db.FromCtx`) and
never open or commit an independent transaction. No HTTP self-calls between
modules.

------------------------------------------------------------------------

## 3. Endpoint and Permission Inventory

### 3.1 Routes (registered in `routes.go`, NOT mounted)

| Method | Path | Handler | Rate policy | Permission |
|---|---|---|---|---|
| GET | `/api/v1/sales` | `Handler.List` | `auth_read` | `sales:view` |
| GET | `/api/v1/sales/:id` | `Handler.Get` | `auth_read` | `sales:view` |
| GET | `/api/v1/sales/:id/invoice` | `Handler.GetInvoice` | `auth_read` | `sales:view` |

All three set `Cache-Control: private, no-store`. Mutation endpoints
(checkout, return, refund, void, cash session close, etc.) are not implemented
and must not be mounted.

### 3.2 RBAC permissions

- `sales:view` --- read sale documents and invoices inside authorized scope.
- `sales:export` --- request exports only for datasets the principal is permitted
  to view.
- `sales:update` is retired by the RBAC seeder; do not re-introduce.
- Cross-branch/cross-warehouse access requires a separately cataloged grant
  (`sales:cross_branch_view`, `sales:admin`) and seeded role policy before
  exposure.

### 3.3 Rate policies

- `auth_read` --- read endpoints.
- `auth_write` --- mutation endpoints (when available).
- `pos_checkout` --- 30 requests/minute (default); bound to `RATE_LIMIT_POS_CHECKOUT_RPM`.
- `export` --- export endpoints (when available).

### 3.4 Authorization invariants

- `organization_id` comes only from trusted Principal/context, never request
  body/query.
- `branch_id` supplied by client is a filter selector, not authority; intersect
  with effective authorized branch scope.
- Omitted `branch_id` means only the Principal's effective scope (single branch,
  authorized subset, or org-wide only when explicitly allowed). Never default to
  unrestricted.
- `SUPER_ADMIN` cross-branch access remains organization-scoped; not a
  cross-tenant bypass.
- Warehouse filters must resolve warehouse -> branch -> organization and be
  intersected with caller scope.
- Apply tenant predicates in repository SQL as defense-in-depth, not only
  handler checks.

------------------------------------------------------------------------

## 4. Configuration

### 4.1 Feature gating

Sale has no `features.sale_read_api` flag today. The module is not wired into
`cmd/api/main.go`. To enable even read traffic, add an explicit feature flag
following the `features.purchase_read_api` / `features.analytics_read_api`
pattern, default `false`, and gate registration in `main.go`.

### 4.2 Relevant config keys

| Key | Default | Env override | Purpose |
|---|---|---|---|
| `rate_limit.policies.pos_checkout.limit` | 30 | `RATE_LIMIT_POS_CHECKOUT_RPM` | POS checkout rate limit |
| `idempotency.ttl` | 24h | `IDEMPOTENCY_TTL` | HTTP idempotency cache TTL |
| `idempotency.key_max_length` | 128 | `IDEMPOTENCY_KEY_MAX_LENGTH` | Max idempotency key length |
| `pagination.default_limit` | 20 | --- | Default page size |
| `pagination.max_limit` | 100 | --- | Max page size |
| `outbox.enabled` | false | `OUTBOX_ENABLED` | Outbox dispatcher enablement |
| `audit.enabled` | true | --- | Durable audit sink |

### 4.3 Environment variables

See `internal/platform/config` `bindEnvVars` for the full mapping. Sale-specific
overrides follow the existing `RATE_LIMIT_*`, `IDEMPOTENCY_*`, and `OUTBOX_*`
conventions. No Sale-specific env vars exist today.

------------------------------------------------------------------------

## 5. Deployment and Release Gates

### 5.1 Prerequisites (must pass before mounting Sale routes)

1. Pin repository commit and inspect router/middleware/worker/outbox/db
   abstractions.
2. Freeze source read/event contracts for Sale, Inventory, Ledger, Medicine,
   MasterData, Customer.
3. Confirm timezone, currency/base currency (BDT-only initially), base-unit
   conversion, sale net/return semantics, valuation policy, and Ledger
   profitability dimensions.
4. Add Analytics contribution/checkpoint/reconciliation tables only after event
   semantics are frozen; shared outbox supplies delivery state.
5. Implement one daily-sales projection and prove event replay/idempotency.
6. Add Inventory movement/balance/valuation only after snapshot + watermark
   contract exists.
7. Add profitability/turnover only after Ledger read contract is frozen.
8. Add endpoints progressively; freshness from first release.
9. Reuse Report export lifecycle if available.
10. Shadow-reconcile against source before exposing dashboards.
11. Enable endpoints behind permissions/feature flags.
12. Monitor lag, mismatches, query latency, and backlog.
13. Enable export after security/resource tests.
14. Document rollback: disable consumer/routes, preserve source facts, rebuild
    projection. Never roll back source transactions to fix Analytics.

### 5.2 Cash/offline checkout acceptance (release condition)

The first production checkout slice is restricted to BDT and payment methods
whose settlement can be recorded locally. The following MUST succeed in a single
shared PostgreSQL transaction or all roll back:

- Sale document + immutable line snapshots.
- Stock consumption + cost allocation (Inventory FEFO).
- Tender (cash/offline/split).
- Invoice issuance.
- Balanced journal (Ledger typed source facts).
- Mandatory audit append.
- Outbox event append.

Remote-provider, credit, promotion, and regulated-dispensing workflows remain
disabled until their own gates pass. The first slice may sell only products whose
owner-module policy explicitly confirms no prescription/reviewer workflow is
required; unknown or regulated classification fails closed.

### 5.3 Verification checklist

```powershell
# From backend/
go build ./...
go test ./internal/modules/sale/... -count=1
```

- Confirm `go vet ./internal/modules/sale/...` is clean.
- Confirm migration lineage is resolved (no duplicate `000023` pairs).
- Confirm RBAC seeder includes `sales:view` and `sales:export` without direct
  grants to operational roles until policy is approved.
- Confirm no Sale mutation route is mounted (`RegisterRoutes` not wired in
  `cmd/api/main.go`).

### 5.4 Rollback

- Disable the feature flag and remove the module from `cmd/api/main.go` modules
  list.
- Preserve source facts (sales, stock, journals, invoices) --- never delete.
- Rebuild projections from authoritative source + watermark.
- Do not roll back source transactions to fix Sale/Analytics.

------------------------------------------------------------------------

## 6. Monitoring and Observability

### 6.1 Suggested metrics (follow existing naming)

- `sale_operations_total{operation,outcome}` --- checkout, return, refund, void,
  payment outcomes.
- `sale_operation_duration_seconds{operation}` --- latency per operation.
- `sales_completed_total{branch_id}` --- completed POS sales (bounded label).
- `analytics_projection_events_total{consumer,result}` --- if Sale feeds Analytics.
- `analytics_projection_lag_seconds{consumer}` --- projection freshness.

### 6.2 Structured logs

Include request/trace/correlation IDs, org/scope fingerprint, consumer/event/source,
projection/version, job/attempt/fencing generation, duration, rows/bytes, and
sanitized error class. Redact PII, tokens, payment data, prescription data, and
signed URLs. No user/customer/sale/org IDs in Prometheus labels.

### 6.3 Alerts

- Checkout transaction failure rate spike.
- Stock allocation failures (insufficient stock, FEFO errors).
- Ledger posting failures or reconciliation mismatches.
- Invoice numbering gaps.
- Outbox backlog/lag on Sale events.
- Cash session close anomalies.

------------------------------------------------------------------------

## 7. Troubleshooting

### 7.1 Checkout failures

| Symptom | Likely cause | Action |
|---|---|---|
| `409 CONFLICT` / duplicate operation | Idempotency key replay or operation record conflict | Check `sale_operations` unique constraint; verify idempotency key hash; do not retry blindly. |
| `422` validation | Missing warehouse, items, tenders, or invalid quantity | Validate request against DTO rules; check medicine/variant active status. |
| `403` | Branch/warehouse outside effective scope | Verify principal branch scope and warehouse->branch->org resolution. |
| `500` / DB error | Transaction participant failure (Inventory/Ledger/Audit/Outbox) | Check logs with request ID; inspect which participant rolled back; do not partially retry. |
| Insufficient stock | FEFO eligible stock < requested base quantity | Verify Inventory balances/conditions; check batch expiry/recall/quarantine. |
| Invoice number gap | Counter transaction rolled back with checkout | Expected on failure; no action if consistent. |

### 7.2 Read endpoint issues (when mounted)

- `GET /sales` returns empty: verify organization/branch scope, filters, and
  pagination cursor validity.
- `GET /sales/:id` returns 404: confirm sale exists in same org/branch; check
  soft-delete state.
- `GET /sales/:id/invoice` returns 404: invoice may not be issued yet; check
  `invoice_status`.

### 7.3 Reconciliation

- Sales daily projection vs Sale completed/return facts: compare count, gross,
  discounts, net, quantity by currency/unit.
- Branch/category/cashier totals must reconcile to parent totals.
- COGS/revenue/profitability must reconcile to Ledger posted journals, reversals,
  period, and dimensions.
- Differences create exceptions with source/projected values, scope, time,
  severity, and correlation IDs. Sale never repairs source facts; Analytics never
  repairs Sale.

### 7.4 Common operational checks

```powershell
# Confirm module is NOT mounted (expected pre-release)
Select-String -Path "D:\Codding\pharmaciano-backend-go\backend\cmd\api\main.go" -Pattern "sale" -SimpleMatch

# Build and test Sale package
cd D:\Codding\pharmaciano-backend-go\backend
go build ./internal/modules/sale/...
go test ./internal/modules/sale/... -count=1
go vet ./internal/modules/sale/...
```

------------------------------------------------------------------------

## 8. Data Integrity and Evidence Rules

- Posted sales, invoices, tenders, returns, refunds, stock movements, and journals
  are immutable evidence. Correct through compensating documents/reversals, never
  silent edits or deletes.
- Never delete a completed sale.
- Do not provide generic PATCH semantics for posted financial facts.
- Void is allowed only within configured policy and only if downstream
  settlement/period rules permit.
- Return approval and physical stock disposition are separate decisions.
- External refund settlement may be asynchronous.
- Cash close is immutable; correction uses separately audited adjustment/reopen
  policy.
- Prescription/controlled-medicine rules are server-side policy decisions; store
  minimal restricted evidence, not unnecessary clinical details.

------------------------------------------------------------------------

## 9. Security and Privacy

- Cashier identity comes from authenticated context; never trust request body.
- Prescription evidence is restricted; generic analytics roles must not access
  it.
- Export IDOR protection: reauthorize job/status/download; requester/delegation
  checks.
- Stale privilege: existing authz-version invalidation; re-resolve at
  execution/download.
- SQL injection: parameterized values, allow-listed identifiers/filters.
- Resource exhaustion: rate limits, date bounds, query/worker/export budgets.
- Never log signed URLs, auth headers, or export rows.
- No user/customer/sale/org IDs in Prometheus labels.

------------------------------------------------------------------------

## 10. Release and Rollback Summary

| Action | Procedure |
|---|---|
| Enable (future) | Add `features.sale_read_api` flag (default false), wire `RegisterRoutes` in `cmd/api/main.go`, mount only after gates pass. |
| Disable | Set flag false, remove module from modules list, rebuild/redeploy. |
| Rollback schema | Do not run down migrations automatically; preserve source facts; rebuild projections. |
| Emergency | Disable routes, preserve logs/DB state, follow `incident_response.md`. |

------------------------------------------------------------------------

## 11. Related Documents

- `docs/adr/SALE_CONTRACT.md` --- full architecture and implementation contract.
- `docs/adr/SALE_IMPLEMENTATION_PLAN.md` (if present) --- implementation sequencing.
- `docs/runbook/deployment.md` --- deployment and migration procedures.
- `docs/runbook/incident_response.md` --- incident triage and response.
- `docs/runbook/db_backup_restore.md` --- backup/restore procedures.
- `docs/api/API.md` --- proposed API surface (treat as unimplemented).
- `docs/api/OPENAPI.md` --- OpenAPI-first contract workflow.

------------------------------------------------------------------------

## 12. Document Control

- **Module:** `internal/modules/sale`
- **Status:** Design-only; not mounted.
- **Owner:** Sale domain team.
- **Last updated:** 2026-11-27
- **Review cadence:** Update when readiness gates change, routes are mounted, or
  operational procedures are added.
