# Inventory Module Runbook

This runbook describes the operational, monitoring, troubleshooting, reconciliation,
and release procedures for the Pharmaciano ERP Inventory module
(`internal/modules/inventory`).

It is the operational companion to `docs/adr/INVENTORY_CONTRACT.md` and must be read
together with `docs/runbook/deployment.md` and `docs/runbook/incident_response.md`.

------------------------------------------------------------------------

## 1. Module Overview

### 1.1 Purpose

Inventory owns the authoritative operational record of physical stock:
organization-level batch identity, warehouse-level stock balances, immutable
movements, reservations, cost layers and allocations, adjustments, transfers,
recalls, stock counts, and reorder policies. It is a transactional domain, not
CRUD around a quantity field.

### 1.2 Ownership

- Inventory owns batch identity, balances, movements, reservations, cost
  layers, adjustments, transfers, recalls, stock counts, reorder policies.
- Inventory does NOT own medicine identity, purchase/sale documents, supplier/
  customer identity, COA/journals, or notifications.
- Purchase receipt and Sale checkout orchestrate their workflows and call
  Inventory through transaction-aware ports; Inventory never independently
  begins/commits a transaction inside those workflows.

### 1.3 Module Status

Implemented and mounted in the API router. Provides batch listing with cursor
pagination and FEFO allocation logic. The module is the stock authority; Purchase
and Sale depend on it for stock/cost effects.

------------------------------------------------------------------------

## 2. Scope and Boundaries

### 2.1 In scope

- Batch identity (organization-level lot identity with expiry/recall status).
- Warehouse-level stock balances by condition (sellable, quarantined, damaged,
  expired, recalled, in_transit).
- Immutable stock movements with operation identity.
- FEFO allocation.
- Reservations, cost layers, adjustments, transfers, recalls, stock counts,
  reorder policies (per contract).

### 2.2 Out of scope (owned by other modules)

| Concern | Owning module |
|---|---|
| Medicine/variant/base-unit conversion | Medicine |
| Purchase orders/receipts/invoices | Purchase |
| Sale/checkout/customer returns | Sale |
| COA, journals, account balances | Ledger |
| Organization/branch/warehouse master | Foundation |
| Auth/tenant/RBAC | Auth / RBAC |
| Audit/idempotency/outbox | Shared infrastructure |

### 2.3 Dependency direction

```
Purchase -> Inventory application port (receipt/return)
Sale     -> Inventory application port (allocate/consume/return)
Inventory -> Medicine (unit conversion), Ledger (adjustment valuation),
            shared platform, common, errors, PostgreSQL repositories
```

------------------------------------------------------------------------

## 3. Endpoint and Permission Inventory

### 3.1 Routes (registered in `routes.go`, mounted under `/api/v1/inventory`)

| Method | Path | Handler | Rate policy | Permission |
|---|---|---|---|---|
| GET | `/inventory/batches` | `ListBatches` | `auth_read` | `inventory:view` |

### 3.2 RBAC permissions

- `inventory:view` --- read batch/stock data.

### 3.3 Rate policies

- `auth_read` --- read endpoints.

### 3.4 Authorization invariants

- `organization_id` comes only from trusted Principal/context.
- Branch scope enforced separately from organization scope.
- All repository methods are tenant-scoped.
- Warehouse ID supplied by client is only a requested resource identifier;
  resolve and verify organization/branch/actor scope on every operation.

------------------------------------------------------------------------

## 4. Configuration

### 4.1 Feature gating

Inventory is not behind a feature flag; it is mounted unconditionally in
`cmd/api/main.go`. It is a core platform module.

### 4.2 Relevant config keys

| Key | Default | Purpose |
|---|---|---|
| `pagination.default_limit` | 20 | Default page size |
| `pagination.max_limit` | 100 | Maximum page size |
| cursor signing key | required | Cursor pagination signing |

------------------------------------------------------------------------

## 5. Data Model Summary

### 5.1 Core entities

- `inventory_batch_identities` --- organization-level lot identity with
  display lot number, expiry precision, recall status, status.
- `inventory_balances` --- one row per org + warehouse + batch + condition:
  on_hand_quantity, reserved_quantity, version.
- `stock_movements` --- immutable movement events (append-only).
- Reservations, cost layers, adjustments, transfers, recalls, stock counts,
  reorder policies per contract.

### 5.2 Quantity and money types

- `Quantity` is `int64` in canonical base unit (no float).
- Monetary amounts use integer minor units + ISO currency code.

### 5.3 Key invariants

- `available = on_hand - reserved`; `0 <= reserved <= on_hand`; `on_hand >= 0`.
- Negative stock forbidden.
- Movements are append-only and immutable after commit.
- Batch identity is organization-level; balances are warehouse-scoped.
- FEFO: earliest expiry first, batch ID as tie-breaker; only sellable,
  active, non-recalled, non-expired batches eligible.

------------------------------------------------------------------------

## 6. Operational Procedures

### 6.1 Health checks

- `GET /health` --- general liveness.
- `GET /ready` --- readiness (DB connectivity).

### 6.2 Monitoring

- Batch list request rate/latency.
- DB query latency.
- FEFO allocation success/failure rates.

------------------------------------------------------------------------

## 7. Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| Empty batch list | No balances in scope or branch filter | Check branch scope and data |
| Slow list queries | Missing indexes on `(organization_id, warehouse_id)` | Review migration indexes |
| FEFO insufficient stock | No eligible sellable batches | Check expiry/recall/status |
| Cursor decode errors | Invalid/tampered cursor | Re-issue query without cursor |

------------------------------------------------------------------------

## 8. Reconciliation

- Balance aggregates must reconcile to movement sums per batch/warehouse.
- `accepted_quantity` on PO lines vs receipt lines (Purchase coordination).
- Cost layer remainders must not be negative.

------------------------------------------------------------------------

## 9. Release Procedure

1. Apply all Inventory migrations.
2. Run `go build ./...` and `go test ./internal/modules/inventory/...`.
3. Smoke test `GET /api/v1/inventory/batches` with auth token.
4. Verify cursor pagination and FEFO logic in staging.

------------------------------------------------------------------------

## 10. Incident Response

- If stock inconsistency is detected, investigate via movement history;
  do not manually edit balances. Use compensating movements/reversals.
- Negative stock indicates a bug; treat as P1.

------------------------------------------------------------------------

## 11. Related Documents

- `docs/adr/INVENTORY_CONTRACT.md` --- architecture and implementation contract.
- `docs/runbook/LEDGER.md` --- Ledger operational runbook.
- `docs/runbook/PURCHASE.md` --- Purchase operational runbook.
- `docs/runbook/SALE.md` --- Sale operational runbook.
- `docs/runbook/deployment.md` --- deployment procedures.
- `docs/runbook/incident_response.md` --- incident response procedures.
