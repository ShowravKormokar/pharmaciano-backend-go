# Purchase Module Runbook

This runbook describes the operational, monitoring, troubleshooting, reconciliation,
and release procedures for the Pharmaciano ERP Purchase module
(`internal/modules/purchase`).

It is the operational companion to `docs/adr/PURCHASE_CONTRACT.md` and must be read
together with `docs/runbook/deployment.md` and `docs/runbook/incident_response.md`.

------------------------------------------------------------------------

## 1. Module Overview

### 1.1 Purpose

The Purchase module owns procurement commitments, approvals, goods receipts,
supplier invoices, three-way matching, supplier payment records, supplier returns,
and procurement closure. It coordinates Inventory (stock/cost effects) and Ledger
(GRNI/AP/cash effects) through narrow transaction-aware service ports.

### 1.2 Ownership

- Purchase owns procurement documents and their lifecycle state.
- Purchase does NOT own supplier master, medicine identity, stock, cost layers,
  COA, journals, or notifications.
- Cross-module writes occur through transaction-aware service ports, never
  direct cross-module SQL.
- The Purchase application workflow owns the outer transaction for
  Purchase-originated receipt, invoice, payment, and return commands.

### 1.3 Module Status

Partially implemented. Only read endpoints (`GET /purchases`, `GET /purchases/:id`)
are mounted. Write mutations exist in service code but are intentionally NOT
registered because the normalized schema, audit, outbox, and durable idempotency
integrations required to expose them safely are not yet in place. The module is
gated behind the `features.purchase_read_api` flag (default false).

------------------------------------------------------------------------

## 2. Scope and Boundaries

### 2.1 In scope

- Purchase order draft creation and update (service-level).
- Read/query of purchase orders with lines.
- Purchase lifecycle dimensions (order, receipt, invoice, payment, closure).
- Approval evidence (append-only).
- Goods receipt DTOs and validation.
- State machine for order transitions and receipt derivation.

### 2.2 Out of scope (owned by other modules)

| Concern | Owning module |
|---|---|
| Supplier identity/contact/bank data | Supplier |
| Medicine/variant identity | Medicine |
| Batch, stock balances, movements, cost layers | Inventory |
| COA, journals, GRNI, AP, cash/bank | Ledger |
| Tax policy resolution | Settings / MasterData |
| Authentication, sessions, RBAC | Auth / RBAC |
| Generic audit infrastructure | Audit |
| Notifications/exports | Notification / Export |

### 2.3 Dependency direction

```
Purchase -> Inventory application port (receipt/return)
Purchase -> Ledger application port (GRNI/AP/cash posting)
Purchase -> Supplier / Medicine / Settings / Audit / Outbox (validation/snapshot)
```

Do not call Purchase through its own HTTP endpoint from another module inside
the same process. Internal calls should use the application service and the
caller's transaction context.

------------------------------------------------------------------------

## 3. Endpoint and Permission Inventory

### 3.1 Routes (registered in `routes.go`, mounted under `/api/v1/purchases`)

| Method | Path | Handler | Rate policy | Permission |
|---|---|---|---|---|
| GET | `/purchases` | `List` | `auth_read` | `purchases:view` |
| GET | `/purchases/:id` | `Get` | `auth_read` | `purchases:view` |

Writes are intentionally not registered. Service methods `Create`, `Update`,
and (planned) submit/approve/reject/cancel/order/receive/invoice/pay/return/close
are implemented only at service/repository level and must not be exposed until
schema + audit + outbox + durable idempotency are wired.

### 3.2 RBAC permissions

- `purchases:view` --- read purchase orders and lines.

### 3.3 Rate policies

- `auth_read` --- read endpoints.

### 3.4 Authorization invariants

- `organization_id` comes only from trusted Principal/context, never request
  body/query.
- Branch scope is enforced separately from organization scope.
- All repository methods are tenant-scoped (`organization_id` predicate in SQL).
- Row-level locks (`FOR UPDATE`) used for mutable draft updates.

------------------------------------------------------------------------

## 4. Configuration

### 4.1 Feature gating

Purchase read API is gated behind `features.purchase_read_api` (default `false`).
Enable only after verifying the normalized Purchase schema is installed and
migrations applied. See `backend/docs/adr/PURCHASE_CONTRACT.md` §6.

### 4.2 Relevant config keys

| Key | Default | Purpose |
|---|---|---|
| `features.purchase_read_api` | false | Enable purchase read endpoints |
| `pagination.default_limit` | 20 | Default page size |
| `pagination.max_limit` | 100 | Maximum page size |

------------------------------------------------------------------------

## 5. Data Model Summary

### 5.1 Core entities

- `purchase_orders` --- procurement document header with independent lifecycle
  dimensions (order_status, receipt_status, invoice_status, closure_status).
- `purchase_order_lines` --- line items with canonical medicine/variant, ordered
  quantity, unit conversion snapshot, pricing, tax, and discount.
- `purchase_approvals` --- append-only approval evidence per revision/stage.
- `purchase_receipts` / `purchase_receipt_lines` --- goods receipt documents with
  accepted/rejected quantities, batch evidence, Inventory references.
- `purchase_invoices` / `purchase_invoice_lines` / `purchase_invoice_matches` ---
  supplier invoices and three-way matching.
- `purchase_payments` / `purchase_payment_allocations` --- supplier settlement.
- `purchase_returns` / `purchase_return_lines` --- supplier returns.
- `purchase_transitions` --- append-only lifecycle transition history.

### 5.2 Money and quantity types

- `Quantity` is `int64` in canonical base unit (no float).
- `AmountMinor` is `int64` in currency smallest unit.
- `RateBasisPoints` is `int32` (percentage * 100).

### 5.3 Lifecycle dimensions

Order status: `draft -> pending_approval -> approved -> ordered` (with `rejected`,
`cancelled` branches). Receipt/invoice/payment/closure are tracked independently.

------------------------------------------------------------------------

## 6. Operational Procedures

### 6.1 Enabling purchase read API

1. Verify normalized Purchase schema is installed (migrations applied).
2. Set `features.purchase_read_api=true` in config.
3. Restart API process.
4. Verify `GET /api/v1/purchases` returns 200 with auth token.

### 6.2 Health checks

- `GET /health` --- general liveness.
- `GET /ready` --- readiness (DB connectivity).

### 6.3 Monitoring

- Request rate and latency for `/purchases` endpoints.
- Error rate (4xx/5xx) by endpoint.
- DB query latency for purchase order queries.

------------------------------------------------------------------------

## 7. Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| 404 on `/purchases` | Feature flag disabled or routes not mounted | Check `features.purchase_read_api` and `main.go` wiring |
| 401/403 | Missing `purchases:view` permission | Verify RBAC assignment |
| Empty list | No purchase orders in scope or branch filter | Check branch scope and data |
| Slow list queries | Missing indexes on `(organization_id, branch_id)` | Review migration indexes |

------------------------------------------------------------------------

## 8. Reconciliation

- Purchase order `accepted_quantity` on lines is a transactionally maintained
  read optimization. Posted receipt lines are the source of truth. Reconcile
  periodically: `SUM(accepted)` on receipt lines vs `accepted_quantity` on PO lines.
- Three-way matching: ordered vs received vs invoiced quantities must be
  consistent. Discrepancies surface as `disputed` invoice status.

------------------------------------------------------------------------

## 9. Release Procedure

1. Apply all Purchase migrations (normalized schema).
2. Run `go build ./...` and `go test ./internal/modules/purchase/...`.
3. Enable `features.purchase_read_api` in staging; smoke test read endpoints.
4. Promote to production with flag enabled.
5. Do NOT enable write endpoints until audit/outbox/idempotency are wired.

------------------------------------------------------------------------

## 10. Incident Response

- If data inconsistency is detected, stop mutations (flag already disables
  writes), investigate via transition history and audit logs.
- Do not manually mutate posted procurement/financial facts; use compensating
  documents/reversals once write paths are available.

------------------------------------------------------------------------

## 11. Related Documents

- `docs/adr/PURCHASE_CONTRACT.md` --- architecture and implementation contract.
- `docs/runbook/LEDGER.md` --- Ledger operational runbook.
- `docs/runbook/SALE.md` --- Sale operational runbook.
- `docs/runbook/deployment.md` --- deployment procedures.
- `docs/runbook/incident_response.md` --- incident response procedures.
