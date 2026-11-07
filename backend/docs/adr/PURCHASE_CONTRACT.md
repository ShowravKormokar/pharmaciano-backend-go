# PURCHASE_CONTRACT.md

# Pharmaciano ERP --- Purchase Module Contract

**Module:** `internal/modules/purchase`\
**API base:** `/api/v1`\
**Datastore:** PostgreSQL\
**Consistency:** Strong consistency for procurement documents, receipts,
invoices, payment facts, inventory posting, and ledger posting.\
**Status:** Architecture and implementation contract.

---

## 1. Purpose and guarantee

Purchase owns procurement commitments, approvals, goods receipts,
supplier invoices, three-way matching, supplier payment records,
supplier returns, and procurement closure.

Purchase coordinates Inventory and Ledger. It is not a second inventory
engine, accounting ledger, supplier master, or notification system.

**Critical guarantee:** A committed goods receipt creates exactly one
Purchase receipt, exactly one Inventory stock/cost effect, exactly one
Ledger GRNI effect, and required Audit/outbox evidence---or none of
them. Retries, concurrency, and process crashes must not duplicate
stock, cost, payable, or cash/bank effects.

## 2. Normative architecture rules

1.  PostgreSQL is the source of truth for Purchase business facts.
2.  Reuse existing authentication, tenant resolution, branch scope,
    RBAC, rate limiting, idempotency, audit, transaction, pagination,
    error, observability, and job infrastructure.
3.  Purchase writes only Purchase-owned tables. Inventory and Ledger
    write only their own tables.
4.  Cross-module writes occur through narrow transaction-aware service
    ports, never direct cross-module SQL.
5.  Purchase's top-level application workflow owns the outer transaction
    for Purchase-originated receipt, invoice, payment, and return
    commands.
6.  All participating services receive the same transaction-aware
    context/runner. No participant independently commits.
7.  Mandatory Audit and outbox records are inserted in the same
    transaction as the business mutation.
8.  Notifications, exports, reminders, analytics, and external provider
    calls happen asynchronously/outside business locks.
9.  Posted commercial, stock, and accounting facts are immutable.
    Corrections use compensating documents/reversals.
10. Redis may support cache, queue transport, or rate limiting; it is
    never authoritative for stock, invoice uniqueness, settlement, or
    durable idempotency.
11. Authorization is checked in middleware and revalidated in
    services/repositories.
12. Never introduce duplicate transaction, audit, idempotency, queue,
    logging, or RBAC frameworks.

## 3. Source alignment and deliberate corrections

This contract is based on the Purchase section of
`answer_in_markdown.md`, its common cross-module rules, the Purchase →
Inventory → Ledger workflow, and the model/routes supplied with the
request.

### Preserved from Astra

- Purchase is organization-owned; orders/receipts are branch-scoped;
  receipt lines target warehouses.
- Dependencies: Supplier, Medicine, Inventory, Ledger, Settings,
  Audit.
- Purchase owns procurement commitments, approvals, receipts, supplier
  invoices, payment obligations, and supplier returns.
- Goods receipt posts Inventory and GRNI/accrued receipt value.
- Supplier invoice matching clears GRNI and recognizes
  payable/tax/variance under accounting policy.
- Three-way matching compares ordered, received, and invoiced
  quantities/prices.
- Receipt, stock increase, cost layer, journal, audit, and outbox are
  atomic.
- Supplier payment is a separate settlement transaction.
- Provider ambiguity becomes reconciliation state, not a blind second
  payment.
- Approval/receive/invoice/payment permissions are separated.

### Corrections to the supplied model sketch

---

Supplied design Contract decision Reason

---

One `Status` and `PaymentStatus` Independent order, receipt, Partial
invoice, payment, and receipt/invoice/payment
closure dimensions coexist

`float64` money and rates Existing exact Money/Decimal Binary floating point is
abstraction; otherwise fixed unsafe for accounting
decimal or integer minor  
units

One batch/expiry on `PurchaseItem` Batch identity is One PO line can arrive in
created/selected per receipt multiple lots
line by Inventory

`QuantityReceived` freely updated Derive from posted receipt Prevent double increments
lines or maintain  
transactionally guarded  
aggregate

Payment `PATCH`/`DELETE` Immutable settled payment; Preserve financial
explicit reversal command history

`received_at` on PO Receipt timestamps live on Multiple partial
each receipt; optional deliveries
derived last-received  
timestamp

Purchase directly creates Inventory Call Inventory receipt port Inventory owns stock
batches identity and rules

Mutable Derive or transactionally Avoid inconsistent
`TotalAmount/PaidAmount/DueAmount` maintain from immutable parallel balances
lines/invoices/allocations
-------------------------------------------------------------------------------------------

Do not implement simplified fields and normalized entities as competing
sources of truth.

## 4. Ownership boundaries

---

Concern Owner Purchase interaction

---

Supplier identity, Supplier Validate and snapshot
verification, bank required facts
destination

Medicine/variant/base Medicine Validate identity and unit
unit conversion

Batch, expiry, stock Inventory Transaction-aware
condition, balances, receipt/return ports
movements

Cost layers and stock Inventory Receive valuation and
valuation provenance

COA, journal, GRNI, AP, Ledger Posting/exposure/settlement
cash/bank, fiscal ports
periods, supplier  
balances

Tax policy MasterData/configured tax Resolve and snapshot policy
owner

Tenant/branch/warehouse Existing core Resolve and validate scope
access modules/middleware

Audit Audit/shared port Append in shared transaction

Event Outbox/Notification Persist event now, deliver
delivery/notifications later

Exports Existing Async, private, scoped
jobs/report/storage

Analytics/reporting Analytics/Report/Ledger Consume source events/read
contracts
---------------------------------------------------------------------------------

Purchase must never: - write Inventory or Ledger tables directly; -
calculate authoritative supplier balance or stock valuation; - own
retail selling-price policy; - mutate Supplier, Medicine, Organization,
Branch, or Warehouse master data; - use Redis as proof a business
operation happened; - make notifications/export success part of
receipt/payment success.

## 5. Tenant, branch, and warehouse scope

---

Entity Scope

---

Supplier relationship Organization

Purchase order/lines Organization + branch

Approval evidence Organization + branch

Receipt/receipt lines Organization + branch + warehouse

Supplier invoice/matches Organization + branch

Payment/allocations Organization + branch /
organization settlement account

Supplier return/lines Organization + branch + warehouse

Transition history Organization + branch

Export job/artifact Organization + requesting user +
immutable authorized scope
-----------------------------------------------------------------------

Rules: - Every repository method receives `organizationID`. - Branch
comes from authenticated tenant context/effective branch scope. A
request branch ID is only a selector. - `X-Branch-ID` override follows
the existing platform rule; do not create a Purchase bypass. - Receiving
warehouse must belong to the same organization and PO branch. - Supplier
organization-wide identity does not grant access to every branch's
documents. - A user authorized for Branch A cannot access Branch B by
changing an ID. - Use tenant-safe composite FKs
`(organization_id, parent_id)` wherever possible. - Use RESTRICT/NO
ACTION for posted history; no cascade deletion of posted
procurement/financial facts. - Historical reads must still resolve
deactivated suppliers/products.

## 6. Domain model and database design

Names below are logical target entities. Reconcile against existing
migrations before creating tables.

### 6.1 `purchase_orders`

Fields: - `id`, `organization_id`, `branch_id`, `supplier_id` -
`purchase_no`, optional supplier reference - `order_status`,
`receipt_status`, `invoice_status`, `closure_status` - currency
identity/code snapshot under shared Money policy - requester/submission
actors and timestamps - expected delivery, ordered, cancelled, closed
timestamps - notes, version, policy snapshot

Constraints: - Unique `(organization_id, branch_id, purchase_no)`. -
Tenant-safe supplier FK. - `version > 0`; status CHECK constraints. - Do
not use mutable total fields as accounting truth.

### 6.2 `purchase_order_lines`

Fields: - organization, PO ID, line number - canonical Medicine/Variant
identity matching Inventory's contract - ordered quantity, unit ID and
unit/base conversion snapshot - exact unit price, discount, tax
snapshot - product display/description snapshot - expected delivery and
version

Rules: - Unique `(organization_id, purchase_order_id, line_no)`. -
Ordered quantity \> 0. - Quantities use Inventory's exact canonical
base-unit precision. - Client-submitted totals are never
authoritative. - Snapshot unit, tax, and product display facts for
historical reproducibility.

### 6.3 `purchase_approvals`

Fields: organization, PO, revision/stage, decision, actor, reason,
approval policy version, decision time.

Rules: - Append-only and unique per revision/stage. - Enforce
maker-checker when policy requires. - Role/permission policy comes from
existing RBAC; do not hardcode role names. - Material draft changes
after approval require a new revision and approval.

### 6.4 `purchase_receipts`

Fields: - organization, branch, warehouse, PO - receipt number, durable
operation ID, status - supplier delivery/challan reference - receiver,
received/posted timestamps and actors - notes/evidence references

Rules: - One PO can have multiple receipt documents. - One receipt
represents one physical delivery event. - Posted receipt is immutable. -
Unique `(organization_id, operation_id)` and scoped receipt number. -
Duplicate challan/delivery evidence must be rejected or require an
explicit exception policy.

### 6.5 `purchase_receipt_lines`

Fields: - organization, receipt, PO line, line number - Inventory batch
ID - accepted quantity, rejected quantity, rejection
reason/disposition - provisional unit acquisition cost and currency -
valuation evidence and Inventory movement/cost-layer references -
lot/expiry evidence snapshot

Rules: - One PO line can map to many receipt lines and batches. -
Accepted/rejected quantities are nonnegative; delivered quantity is
positive. - Accepted quantity cannot exceed remaining ordered quantity
plus approved tolerance. - Rejected goods do not increase sellable
stock. - Batch identity and movement are Inventory-owned. - Never store
one batch number on the PO line as the only lot.

### 6.6 Supplier invoices

`purchase_invoices`: - organization, branch, supplier, optional PO - raw
and normalized supplier invoice number - invoice/received/due dates -
currency and exact subtotal/discount/tax/freight/total - status,
evidence reference, operation ID, posting actor/time, version

`purchase_invoice_lines`: - invoice ID, line number, description/product
snapshot - optional PO line - invoiced quantity, unit price, discount,
tax, line total - unit/tax snapshots

`purchase_invoice_matches`: - invoice line, optional PO line, optional
receipt line - matched quantity/amount, match type, exception
actor/reason/evidence

Uniqueness: - Unique
`(organization_id, supplier_id, normalized_supplier_invoice_no)` unless
documented supplier numbering requires fiscal year/series in the key. -
Preserve raw invoice number; normalize according to explicit policy. -
Database unique constraint is final duplicate defense.

Three-way matching: - Ordered facts from PO. - Accepted quantities from
posted receipts. - Claimed quantity/price from invoice. - Lock rows and
enforce cumulative match bounds. - Invoiced quantity cannot exceed
eligible accepted quantity unless an explicit advance/prepayment
workflow exists. - One receipt quantity cannot be matched twice. -
Price/tax/freight/discount/rounding variance must be classified and
tolerance-controlled. - Exceptions require permission, actor, reason,
evidence, and audit. - Posted invoice/match facts are immutable;
corrections use credit/debit notes or reversal.

**Initial policy:** invoice-before-receipt is rejected. If advances are
needed, model supplier prepayment explicitly; never create stock from an
invoice.

### 6.7 Purchase payments

`purchase_payments`: - organization, branch, operation ID, supplier,
currency - exact positive amount, payment method reference, settlement
account reference - payment/value date, status, external reference -
paid-by, settled-at, reversal-of ID, evidence, Ledger posting reference

`purchase_payment_allocations`: - payment ID, invoice ID, allocated
amount, operation identity

Rules: - Sum allocations \<= settled payment amount. - Lock
invoice/payable exposure to prevent over-allocation. - Unallocated
advances must be explicitly modeled as supplier advances. -
Pending/failed payment does not reduce payable. - Settled payment is
immutable; no PATCH/DELETE. - Reversal is a new operation with reason,
actor, timestamp, and compensating Ledger journal. - Validate payment
method/account against organization and branch policy. - Supplier bank
destination comes from verified Supplier data, never arbitrary client
input.

### 6.8 Supplier returns

`purchase_returns`: - organization, branch, warehouse, supplier -
original receipt reference, return number, operation ID, status -
reason/evidence, actors/timestamps, Ledger credit/claim reference

`purchase_return_lines`: - original receipt line, batch, return
quantity - original acquisition cost/valuation reference - Inventory
movement reference and disposition

Rules: - Cumulative return quantity cannot exceed eligible accepted
quantity less prior returns. - Lock original receipt lines before
checking return totals. - Inventory validates current
batch/condition/available quantity. - Return uses original cost-layer
valuation, not retail selling price. - A return request does not itself
reduce stock. - Supplier credit note is distinct from physical dispatch
and cash refund. - If returned goods were already consumed/sold, use an
exception workflow; never force negative stock.

### 6.9 `purchase_transitions`

Append-only: organization, branch, PO ID, from/to dimension state,
action, actor, reason, operation ID, policy version, timestamp.

Transition history is business evidence; generic audit logs do not
replace it.

## 7. Independent lifecycle dimensions

Do not compress all purchase lifecycle facts into one enum.

### Order status

`draft → pending_approval → approved → ordered`; with `rejected` and
`cancelled` branches.

`ordered` means supplier order/confirmation was recorded. If the
business does not distinguish approval from supplier order placement,
document that and collapse only those states.

### Receipt status

Derived from posted accepted receipt lines: - `not_received` -
`partially_received` - `fully_received`

### Invoice status

- `not_invoiced`
- `partially_invoiced`
- `fully_invoiced`
- `disputed`

### Payment status

Invoice-level: `unpaid`, `partially_paid`, `settled`, with overdue
derived from due date/business timezone and Ledger outstanding balance.

Payment-operation-level: `pending`, `settled`, `failed`,
`pending_reconciliation`, `reversed`.

### Closure status

`open` or `closed`. Close only after unfulfilled quantity is received or
explicitly cancelled, material invoice/receipt discrepancies are
resolved or accepted, and closure permission is verified.

### Reconciliation with supplied state machine

The supplied
`draft → pending_approval → approved → dispatched → partially_received → received → completed`
is inadequate as one persisted status: - `dispatched` is ambiguous
(supplier dispatch vs buyer order placement). - Partial receipts,
invoices, and payments coexist. - `completed` must not imply fully
paid. - Cancellation cannot erase receipts/payables.

A legacy API `status` may be derived for compatibility, but it is not
the authoritative persisted lifecycle.

### Cancellation

Cancellation cancels only unfulfilled commitment: - Draft:
archive/delete according to API policy. - Pending approval: withdraw. -
Approved/ordered: cancel remaining unfulfilled quantity with
reason/approval. - Posted receipts, invoices, payments, and returns
remain. - After physical dispatch, use receipt/rejection/return
handling. - Lock PO/lines and write transition/audit/outbox atomically.

## 8. Critical goods receipt workflow

### 8.1 Command input

- PO ID and expected version.
- Receiving warehouse.
- Supplier delivery/challan reference.
- Receipt lines referencing PO line IDs.
- Delivered, accepted, rejected quantities.
- Lot/batch number, expiry date/precision, manufacturing date if
  available.
- Cost evidence and rejection reason/disposition.
- Evidence references and `Idempotency-Key`.

Client must not submit authoritative stock balances, movement IDs,
cost-layer IDs, Ledger accounts, journal IDs, or supplier balances.

### 8.2 Validation

Inside the transaction: 1. Resolve organization and effective branch
scope. 2. Claim durable operation identity. 3. Lock PO and affected
lines in deterministic sorted order. 4. Confirm PO is approved/ordered
and not cancelled/closed. 5. Validate supplier, warehouse, branch,
organization, product, variant, and unit. 6. Validate lot/expiry against
Medicine/Inventory policy. 7. Recalculate accepted quantity remaining
from committed receipts/guarded aggregate. 8. Apply only configured
overreceipt tolerance; require explicit approval/reason above it. 9.
Validate accepted/rejected quantity and cost/currency policy. 10. Reject
duplicate delivery/challan identity according to configured policy.

### 8.3 One shared transaction

Purchase service owns the outer transaction and invokes all ports with
the same transaction handle:

1.  Claim durable receipt operation.
2.  Lock PO and affected lines.
3.  Insert receipt and receipt lines.
4.  Call Inventory receipt port.
5.  Inventory validates batch/warehouse/condition and writes batch
    identity, balance increase, immutable stock movement, cost layer,
    and valuation provenance.
6.  Inventory returns batch/movement/cost-layer references and accepted
    valuation.
7.  Purchase persists returned references.
8.  Call Ledger GRNI posting port with validated receipt valuation.
9.  Ledger posts balanced journal: **Debit Inventory asset / Credit GRNI
    (accrued receipts)**.
10. Append mandatory Audit evidence in the same transaction.
11. Insert `GoodsReceived` outbox event in the same transaction.
12. Update receipt aggregate/status and PO version.
13. Append immutable transition evidence and complete durable operation
    result.
14. Commit once.

Any failure rolls back Purchase, Inventory, Ledger, Audit, and outbox
database writes.

**Inventory/Ledger must not open and commit independent transactions in
this workflow.** They receive the transaction-aware runner/context from
existing platform infrastructure.

### 8.4 Accounting sequence

---

Event Debit Credit

---

Goods receipt Inventory asset GRNI/accrued receipts

Supplier invoice GRNI; eligible input Accounts payable
tax; approved variance  
accounts

Payment settled Accounts payable Cash/bank/settlement
account

Supplier return/credit Ledger-defined Inventory/return
payable/claim account valuation account per
policy
-----------------------------------------------------------------------

PO approval normally creates no journal. Do not credit Accounts Payable
solely because physical goods arrived if the accounting policy uses
GRNI.

Ledger owns account selection, journal balancing, fiscal-period
validation, and payable balances. Purchase sends source facts, not
arbitrary client-selected accounts.

### 8.5 Valuation

- Purchase records agreed acquisition price and procurement charge/tax
  snapshots.
- Inventory owns cost layers and stock valuation.
- Ledger owns financial balances.
- Freight/discount/tax allocation must be deterministic and versioned.
- Recoverable input tax is not automatically inventory cost;
  nonrecoverable tax treatment follows accounting policy.
- Inventory returns accepted valuation breakdown to Ledger.
- Ledger receipt value must reconcile to Inventory posted value under
  shared currency/rounding policy.
- Purchase must not silently average cost if Inventory uses
  FIFO/weighted average/another method.

### 8.6 Partial receipt concurrency

- Lock PO header and affected lines with `SELECT ... FOR UPDATE` in
  deterministic ID order.
- Re-read cumulative accepted quantities after acquiring locks.
- Insert receipt and Inventory movement in same transaction.
- Use guarded updates/version checks for aggregates.
- Unique operation IDs prevent replay.
- Concurrent receiver either succeeds within remaining quantity or
  receives a clear conflict.
- Do not use `SKIP LOCKED` for strict receiving; silently skipping a
  PO line is not a valid receipt result.

### 8.7 Rejected goods and batches

- Rejected/damaged/expired goods never enter sellable stock.
- Track delivered, accepted, and rejected quantities separately.
- Temporary physical holding uses Inventory quarantine/receiving-hold
  capability if available.
- Goods taken away immediately are rejection evidence, not stock.
- Inventory owns canonical batch identity/uniqueness and expiry
  precision.
- A batch becomes sellable only after Inventory's receipt validation
  succeeds.

## 9. Supplier invoice matching and posting

Lifecycle: 1. Create invoice draft and evidence. 2. Claim normalized
supplier invoice uniqueness. 3. Add lines and PO/receipt references. 4.
Calculate exact totals server-side. 5. Run three-way match. 6. Resolve
tolerance/exception policy. 7. Lock invoice and matched PO/receipt lines
in canonical order. 8. Enforce cumulative match bounds and operation
uniqueness. 9. Call Ledger invoice-posting port in same transaction. 10.
Append audit, transition, and outbox. 11. Mark posted/immutable and
commit.

Rules: - Invoiced quantity cannot exceed eligible accepted quantity
unless advance workflow exists. - Cumulative invoice match cannot exceed
receipt quantity. - Multiple invoices may match one receipt and one
invoice may match multiple receipts if partial billing is supported. -
Duplicate supplier invoice number is rejected by PostgreSQL
constraint. - Variances require reason, actor, permission, evidence, and
policy snapshot. - Never mutate approved PO prices just to make invoice
matching pass. - Posted invoice corrections use credit/debit notes or
compensating reversal, not edits.

## 10. Supplier payment and settlement

### Internal cash/bank settlement

Within one transaction: 1. Claim durable payment operation. 2. Lock
relevant invoice/payable rows in deterministic order. 3. Recalculate
authoritative outstanding via Ledger exposure/posting port. 4. Validate
tenant, branch, supplier, currency, payment method, settlement account,
period, and allocation. 5. Insert immutable payment fact. 6. Call Ledger
settlement port using the same transaction. 7. Ledger posts Debit AP /
Credit Cash or Bank. 8. Insert allocations. 9. Append audit/outbox and
transition evidence. 10. Complete operation and commit.

### External provider/bank workflow

Never call external provider while DB locks are held.

1.  Persist payment intent with stable provider idempotency key.
2.  Commit intent.
3.  Call provider outside transaction.
4.  Authenticate callback/poll result and deduplicate provider event by
    provider/account/event ID.
5.  On verified settlement, open a new transaction, lock intent/payable,
    post Ledger settlement once, append audit/outbox, commit.
6.  Unknown outcome becomes `pending_reconciliation`; query/reconcile
    using same provider key.
7.  Never create a second charge blindly.
8.  Reversal/refund is a separate durable operation.

Payment states may include `pending`, `submitted`,
`pending_reconciliation`, `settled`, `failed`, `reversal_pending`,
`reversed`.

### Reversal

- No physical delete or edit of posted amount/method/date.
- Require dedicated reversal permission and reason.
- Lock original payment and allocations.
- Prevent reversal beyond unreversed settled amount.
- Ledger creates compensating journal; original journal remains
  immutable.
- Reverse/reallocate invoice allocations consistently.
- Audit/outbox and immutable `reversal_of_id`.
- Reversal command is idempotent.

## 11. Supplier returns

1.  Create draft against original posted receipt lines.
2.  Validate organization/branch/warehouse/supplier/batch/reason.
3.  Lock receipt lines and return aggregates.
4.  Enforce cumulative return bounds.
5.  Obtain approval if policy requires.
6.  Dispatch/hand over goods using explicit state transition.
7.  Invoke Inventory return/dispatch primitive and Ledger credit/claim
    posting according to actual accounting event.
8.  Persist immutable return lines/movement references.
9.  Append audit/outbox and commit.

A return request alone does not reduce stock. Return valuation uses
original acquisition cost/layer. Supplier credit note, physical
dispatch, and cash refund are distinct events. Do not force negative
stock when returned goods were already consumed.

## 12. API contract

All paths are relative to `/api/v1`. Preserve existing router, response
envelope, validation, error mapping, pagination, and OpenAPI
conventions.

### 12.1 Purchase orders

---

Method/path Permission Rate Behavior

---

`POST /purchases` `purchases:create` `rate_auth_write` Create draft;
idempotency required

`GET /purchases` `purchases:view` `rate_auth_read` Scoped list and
filters

`GET /purchases/{id}` `purchases:view` `rate_auth_read` Detail with bounded
related summaries

`PATCH /purchases/{id}` `purchases:update` `rate_auth_write` Draft only;
version/If-Match

`DELETE /purchases/{id}` `purchases:delete` `rate_auth_write` Draft-only
archive/delete

`POST /purchases/{id}/submit` `purchases:submit` `rate_auth_write` Submit revision

`POST /purchases/{id}/approve` `purchases:approve` `rate_auth_write` Policy/maker-checker
validation

`POST /purchases/{id}/reject` `purchases:reject` `rate_auth_write` Reason required

`POST /purchases/{id}/order` `purchases:order` `rate_auth_write` Record supplier order
confirmation

`POST /purchases/{id}/receive` `purchases:receive` `rate_auth_write` Partial/full receipt;
idempotency required

`POST /purchases/{id}/cancel` `purchases:cancel` `rate_auth_write` Cancel unfulfilled
remainder

`POST /purchases/{id}/close` `purchases:close` `rate_auth_write` Close after
reconciliation

`GET /purchases/{id}/items` `purchases:view` `rate_auth_read` Bounded/paginated
lines

`GET /purchases/{id}/receipts` `purchases:view` `rate_auth_read` Receipt history

`GET /purchases/{id}/audit-trail` `audit:view` `rate_auth_read` Scoped
audit/transition
history

`GET /purchases/export?format=csv\|xlsx` `purchases:export` `rate_export` Async export under
existing convention
-----------------------------------------------------------------------------------------------------------

### 12.2 Supplier invoices (required extension)

---

Method/path Permission Behavior

---

`POST /purchases/{id}/invoices` `purchases:invoice` Record invoice and
matches; idempotent

`GET /purchases/{id}/invoices` `purchases:view` List invoices

`GET /purchase-invoices/{invoice_id}` `purchases:view` Scoped detail

`POST /purchase-invoices/{invoice_id}/post` `purchases:invoice` Match and Ledger
posting

`POST /purchase-invoices/{invoice_id}/dispute` `purchases:invoice` Reason/evidence

`POST /purchase-invoices/{invoice_id}/reverse` `purchases:invoice_reverse` Compensating correction
------------------------------------------------------------------------------------------------------

### 12.3 Payments

---

Method/path Permission Behavior

---

`POST /purchases/{id}/payments` `purchase_payments:create` Payment/settlement;
idempotent

`GET /purchases/{id}/payments` `purchase_payments:view` List

`GET /purchase-payments/{payment_id}` `purchase_payments:view` Scoped detail

`POST /purchase-payments/{payment_id}/reverse` `purchase_payments:reverse` Immutable reversal;
idempotent
------------------------------------------------------------------------------------------------------

**Supplied PATCH/DELETE correction:**
`PATCH /purchase-payments/{payment_id}` and
`DELETE /purchase-payments/{payment_id}` must not edit/delete settled
payment facts. If compatibility is temporarily required, deprecate them;
never silently reinterpret arbitrary PATCH/DELETE as reversal.

### 12.4 Supplier returns (required extension)

---

Method/path Permission Behavior

---

`POST /purchases/{id}/returns` `purchases:return` Create draft;
idempotent

`GET /purchases/{id}/returns` `purchases:view` List

`GET /purchase-returns/{return_id}` `purchases:view` Scoped detail

`POST /purchase-returns/{return_id}/approve` `purchases:return_approve` Policy approval

`POST /purchase-returns/{return_id}/dispatch` `purchases:return` Physical dispatch

`POST /purchase-returns/{return_id}/post` `purchases:return` Inventory/Ledger
posting

`POST /purchase-returns/{return_id}/cancel` `purchases:cancel` Draft/pre-dispatch only
----------------------------------------------------------------------------------------------------

### 12.5 API conventions

- Every mutation uses existing request/correlation IDs, validation,
  error envelope, and idempotency conventions.
- Idempotency required for create, submit, approve/reject, order,
  receive, cancel, close, invoice post, payment, reversal, and return
  posting.
- GET is read-only.
- Use existing pagination package; default 20/max 100 unless project
  policy differs.
- `from` inclusive, `to` exclusive; persist UTC instants.
- Allow-list sorting and filters; stable ID tie-breaker.
- Use version/If-Match for mutable drafts.
- DTOs are explicit; never expose persistence models.
- Async export returns `202 Accepted`, job ID, and status URL.
- Do not load all receipts/payments/invoices for every row in list
  responses.

## 13. Permissions and separation of duties

Suggested permissions (align names to actual RBAC seed convention before
migration):

`purchases:create`, `purchases:view`, `purchases:update`,
`purchases:delete`, `purchases:submit`, `purchases:approve`,
`purchases:reject`, `purchases:order`, `purchases:receive`,
`purchases:cancel`, `purchases:close`, `purchases:invoice`,
`purchases:invoice_reverse`, `purchases:return`,
`purchases:return_approve`, `purchases:export`,
`purchase_payments:create`, `purchase_payments:view`,
`purchase_payments:reverse`, `audit:view`.

Rules: - Requester cannot self-approve where maker-checker policy
applies. - Receiver cannot approve own overreceipt exception where
policy requires separation. - Payment creation does not imply reversal
permission. - Purchase view does not imply audit or sensitive
cost/payment visibility. - SUPER_ADMIN/ADMIN/OWNER policy comes from
existing RBAC; do not hardcode role names in services. - Supplier bank
changes remain Supplier-owned and verified.

Authorization order: 1. Existing auth middleware. 2. Tenant/organization
resolution. 3. Effective branch scope. 4. Rate limit. 5. Permission
check. 6. DTO binding/validation. 7. Service-level
tenant/branch/warehouse/resource revalidation. 8. Repository
tenant/scope predicates. 9. Audit sensitive actions.

## 14. Idempotency and durable operation identity

Use two layers: - Existing idempotency middleware for request replay
ergonomics. - Durable PostgreSQL operation identity for stock/money
correctness.

Reuse existing shared operation/idempotency storage if semantics
suffice. If not, propose a narrow shared extension through ADR; do not
create a Purchase-only generic framework.

Required semantics: - Same key + same canonical request + completed:
return original result. - Same key + different request:
`409 idempotency_key_reused`. - Same operation identity with different
key: DB uniqueness prevents second effect. - Transaction rollback: no
committed business side effect or completed result. - Client timeout
after commit: retry returns original committed result. - Worker/provider
callback retry: event dedupe and operation uniqueness prevent double
settlement.

Canonical hash includes tenant/resource and all effect-bearing fields;
excludes trace IDs; uses deterministic JSON normalization; never stores
credentials/secrets.

## 15. Transactions, locks, constraints

### Transaction ownership

Purchase service opens one outer transaction. All ports/repositories use
that exact transaction. No independent commits, cross-module SQL writes,
or nested transaction as a substitute. External network calls occur
outside.

### Canonical lock order

Document and follow one deterministic order: 1. durable operation
identity; 2. Purchase aggregate/header; 3. Purchase lines sorted by
UUID; 4. receipt/invoice/payment/return rows; 5. Inventory-owned rows
through Inventory port's documented order; 6. Ledger exposure/period
rows through Ledger port's documented order.

Avoid reverse dependency calls that create lock cycles.

### Isolation and writes

- Start with PostgreSQL READ COMMITTED plus explicit row
  locks/constraints.
- Use `SELECT ... FOR UPDATE` for cumulative receipt, invoice match,
  payment allocation, and return limits.
- Use optimistic version checks for draft updates.
- Use guarded updates for aggregate quantities.
- Use stronger isolation only for a proven invariant not protected by
  locks/constraints.
- Retry only known deadlock/serialization errors, bounded with jitter
  and same operation identity.
- Never blindly repeat external provider calls.

### Minimum constraints/indexes

- Unique organization/branch purchase number.
- Unique organization/operation ID per posted command class.
- Unique normalized supplier invoice number under documented
  namespace.
- Unique line numbers per parent.
- Positive ordered/invoiced/accepted/returned/payment
  quantities/amounts as appropriate.
- Nonnegative rejected quantities/allocations.
- Status CHECK constraints.
- Tenant-safe composite FKs.
- Scoped list index
  `(organization_id, branch_id, created_at DESC, id DESC)`.
- Selective indexes for supplier, status, due date, PO, receipt,
  invoice, payment based on real query plans.
- Exact numeric types with bounds/finite checks.
- RESTRICT for posted financial/stock references.
- No cascade deletion of posted history.

Cross-row invariants require locks/guarded writes; ordinary row CHECK
constraints cannot validate arbitrary other rows.

## 16. Failure handling

---

Failure Required behavior

---

Inventory receipt port fails Entire receipt rolls back

Ledger GRNI fails Purchase and Inventory roll back

Mandatory Audit fails Critical mutation rolls back

Outbox insert fails Critical mutation rolls back

Process crashes before commit PostgreSQL rolls back

Crash after commit before HTTP Retry returns original result
response

Notification fails Business transaction remains
committed

Export worker fails Job retry/failure state only

Concurrent overreceipt One succeeds; other receives
quantity conflict

Duplicate invoice Unique constraint rejects

Provider timeout Pending reconciliation; no blind
second payment

Closed Ledger period Deterministic Ledger policy error;
never silently backdate

Supplier inactive after approval Apply documented receiving policy;
preserve history

Return exceeds eligible quantity Reject under lock

Redis unavailable No loss of authoritative business
correctness

Outbox worker down Durable backlog; committed business
facts remain

Deadlock/serialization Bounded safe retry with same
operation identity
-----------------------------------------------------------------------

## 17. Audit and outbox

Mandatory audit actions: - draft create/update/archive; -
submit/approve/reject/withdraw/order; - receipt posted and overreceipt
exception; - invoice created/matched/disputed/posted/reversed; - payment
initiated/settled/failed/reconciled/reversed; - return
created/approved/dispatched/posted/cancelled; - PO cancelled/closed; -
sensitive export requested/downloaded where policy requires.

Audit includes actor, org, branch, resource, action, operation ID,
request/correlation ID, timestamp, reason, policy version, and minimal
before/after metadata. Never log secrets or full bank credentials.

Versioned outbox events: - `PurchaseSubmitted` - `PurchaseApproved` -
`PurchaseRejected` - `PurchaseOrdered` - `GoodsReceived` -
`SupplierInvoicePosted` - `SupplierInvoiceDisputed` -
`PurchasePaymentSettled` - `PurchasePaymentReversed` -
`SupplierReturnPosted` - `PurchaseCancelled` - `PurchaseClosed`

Use existing shared event envelope: event ID/type/schema version,
org/branch, aggregate ID/version, occurred-at, operation ID,
correlation/causation IDs, minimal stable payload. Outbox insert is
transactional; delivery is asynchronous and at-least-once. Consumers
must deduplicate.

## 18. Redis, jobs, exports

### Redis

Allowed only for non-authoritative cache, queue transport, and existing
rate limiting. Never cache authoritative remaining PO quantity, invoice
uniqueness, outstanding payable, settlement, stock, cost layer, or
durable idempotency. Read caches must be invalidatable/versioned and
PostgreSQL remains fallback.

### Jobs

Use existing Asynq/job infrastructure for approval reminders, overdue
delivery/payables, provider reconciliation, match exceptions, export
generation, and outbox delivery. Jobs are at-least-once safe, bounded
retry/backoff, and invoke normal application commands with durable
operation identity for mutations.

### Export

- Permission-checked and audited; immutable filters/scope captured at
  request time.
- Recheck authorization at download time.
- Private storage and short-lived signed URLs.
- Escape CSV formula injection (`=`, `+`, `-`, `@`, leading control
  chars).
- Sensitive cost/payment data requires corresponding permission.
- Row/size/time budgets and `rate_export`.
- Async `202` job; never generate large export in request handler.

## 19. Security threat model

---

Threat Control

---

Cross-tenant ID guessing Tenant predicates, composite FKs,
negative tests

Cross-branch receiving Effective branch scope + warehouse
ownership

Unauthorized approval Dedicated permission, policy,
maker-checker

Fictitious/inflated receipt PO bounds, evidence, tolerance
approval

Duplicate receipt replay Middleware + durable operation +
unique constraints

Duplicate supplier invoice Normalized DB uniqueness

Invoice/receipt overmatch Locks and cumulative allocation
constraints

Payment duplication Durable operation and provider
event dedupe

Payment redirection Verified Supplier bank data,
restricted changes

Forged callback Signature, timestamp/replay checks,
account binding

Mass assignment Explicit DTO allow-list

Price/tax tampering Server calculation and immutable
policy snapshot

Batch/expiry manipulation Inventory validates batch and
expiry

SQL injection Parameterized queries and
allow-listed sorting

Export exfiltration Scope snapshot, permission recheck,
private storage

CSV formula injection Safe cell escaping

Sensitive log leakage Structured allow-list and redaction

Race conditions Locks, guarded writes, unique
constraints

Denial of service Existing rate limits, pagination,
payload/export budgets

Privileged misuse Least privilege, audit, approval
separation
-----------------------------------------------------------------------

Also enforce body/line-count limits, UUID/enum validation, safe
notes/reasons, secret redaction, evidence retention policy, and
consistent not-found behavior for foreign-tenant IDs. Never accept
arbitrary ledger account IDs from clients.

## 20. Observability

Reuse existing structured logging, metrics, tracing, request/correlation
IDs, and health instrumentation.

Log bounded context: organization, branch,
purchase/receipt/invoice/payment/return ID, operation ID, actor, request
ID, action, result/error code, duration. Never log credentials or full
document payloads.

Suggested low-cardinality metrics: - `purchase_order_created_total` -
`purchase_order_transition_total{from,to,action}` -
`purchase_receipt_post_total{result}` -
`purchase_receipt_duration_seconds` -
`purchase_receipt_conflict_total{reason}` -
`purchase_overreceipt_rejected_total` -
`purchase_invoice_match_total{result}` -
`purchase_invoice_duplicate_total` -
`purchase_payment_settlement_total{state,method}` -
`purchase_payment_reconciliation_pending` -
`purchase_payment_reversal_total` -
`purchase_return_post_total{result}` - `purchase_outbox_lag_seconds` -
`purchase_export_job_duration_seconds`

Never use tenant/user/supplier/document IDs as metric labels. Trace
middleware → Purchase → transaction → Inventory → Ledger → Audit →
outbox. Alert on outbox lag, pending reconciliation growth, duplicate
invoice spikes, receipt conflicts, rollback spikes, reconciliation
mismatch, and export backlog.

## 21. Performance and query design

- Bounded pagination; cursor for large histories where shared package
  supports it.
- Avoid N+1 supplier/product/receipt/payment loads.
- List endpoints do not load all child rows per PO.
- Explicit selected columns; allow-listed include expansions.
- Index from observed queries and EXPLAIN plans, not every column.
- Batch insert receipt lines/outbox only where transaction semantics
  remain clear.
- Keep locks short; no provider call, export rendering, or
  notification inside transaction.
- No Redis cache until measured need.
- Use existing statement/transaction timeouts.
- Large reconciliation runs as checkpointed jobs, not API requests.

## 22. Reconciliation invariants

For each PO line: -
`ordered_quantity >= accepted_quantity + cancelled_unfulfilled_quantity`
(with explicit policy for rejected/replacement quantities). -
`accepted_quantity = SUM(posted receipt accepted quantities)`. - Cached
aggregate, if present, is updated in the same transaction and reconciles
to source rows.

For receipt lines: - accepted/rejected quantities are nonnegative; -
delivered = accepted + rejected when delivered quantity is captured; -
rejected quantity never increases sellable stock.

For invoice matches: - positive match quantity/amount; - cumulative
match never exceeds eligible receipt quantity absent explicit approved
exception.

For supplier returns: - cumulative posted return \<= eligible accepted
quantity less prior posted returns and other dispositions.

For payments: - allocation sum \<= settled payment amount; - net invoice
allocations \<= invoice payable after credits/reversals; - reversal \<=
original unreversed settled amount; - pending/failed payment does not
reduce payable.

For Inventory: - accepted receipt quantity equals corresponding
Inventory movements; - accepted movements have cost-layer provenance; -
batch/warehouse/branch/org relationship is valid; - duplicate receipt
cannot create a second movement.

For Ledger: - every journal balances; - source operation identity is
unique per posting purpose; - posted journals immutable; - fiscal period
valid; - receipt/invoice/payment journal values reconcile to source
facts.

Periodic reconciliation jobs compare Purchase ↔ Inventory ↔ Ledger ↔
outbox. They report exceptions; they never silently rewrite posted
history or auto-adjust balances.

## 23. Testing strategy

### Unit

- State transition matrix for each dimension.
- Exact money/quantity and currency rounding.
- Unit conversion snapshots.
- Tax/discount/freight allocation.
- Overreceipt tolerances.
- Three-way matching and variance thresholds.
- Invoice normalization.
- Payment allocation/reversal bounds.
- Supplier return eligibility.
- RBAC/policy and DTO allow-list.

### PostgreSQL integration

1.  Draft create/update and stale version.
2.  Submit/approve/reject and immutable evidence.
3.  Maker-checker and cross-tenant/branch/warehouse injection.
4.  Partial/full receipt with correct batch/balance/movement/cost layer.
5.  Two concurrent receipts cannot over-receive.
6.  Same key returns original; same key/different request conflicts.
7.  Different key/same operation cannot duplicate.
8.  Inventory failure rolls back Purchase/Ledger.
9.  Ledger failure rolls back Purchase/Inventory.
10. Audit/outbox failure rolls back critical mutation.
11. Rejected goods never become sellable stock.
12. Duplicate invoice blocked by unique constraint.
13. Three-way match rejects overmatch.
14. Price variance requires approval.
15. Partial invoice matching across receipts.
16. Concurrent payment allocation cannot over-settle.
17. Provider timeout remains pending reconciliation.
18. Duplicate provider callback settles once.
19. Payment reversal creates compensating journal and preserves
    original.
20. Supplier return cumulative quantity cannot exceed eligible receipt.
21. PO cancellation affects only unfulfilled remainder.
22. Closed PO cannot receive.
23. Export scope/download authorization rechecked; CSV injection
    escaped.
24. Outbox duplicate delivery is consumer-idempotent.

### Property/race/failure injection

- Random receipt partitions never exceed ordered quantity.
- Invoice allocation never exceeds accepted receipt.
- Payment allocation never exceeds settlement/payable.
- Journal debit equals credit.
- Inventory movement sum reconciles to balance change.
- Cost-layer remaining quantity never negative.
- Kill process after Inventory write but before Ledger: all roll back.
- Commit then kill before HTTP response: retry returns original.
- Concurrent final-quantity receipt: exactly one succeeds.
- Concurrent final-payable settlement: no overpayment.
- Deadlock/serialization retry does not duplicate operation.

## 24. Suggested module structure

Follow actual repository conventions; do not create empty
files/directories.

```text
internal/modules/purchase/
├── model.go
├── dto.go
├── handler.go
├── service.go
├── repository.go
├── routes.go
├── errors.go
├── mapper.go          # only if mapping is non-trivial
├── ports.go           # narrow required ports if not colocated
├── state.go           # if state logic is substantial
├── calculation.go     # exact calculations if useful
└── *_test.go
```

Optional focused files: invoice/receipt/payment/return service, export,
events. Do not split mechanically.

Dependency direction: - Purchase defines narrow interfaces it needs. -
Composition root wires existing Inventory, Ledger, Audit, transaction,
and outbox implementations. - No cyclic imports. - No concrete
Inventory/Ledger repository imports. - No module writes another module's
tables.

## 25. Implementation sequence

Prerequisites: Supplier, Medicine, Inventory, Ledger, Settings, Audit,
transaction-aware shared infrastructure, and outbox/job foundation.

---

Phase Deliverable Exit criteria

---

0 Repository Confirm go.mod paths, transaction runner, Money/Quantity,
reconnaissance Inventory receipt/return ports, Ledger GRNI/AP/settlement
ports, RBAC, idempotency, audit/outbox

1 Schema/domain Tenant-safe constraints, exact types, independent status
dimensions

2 Draft/order workflow Create/update/list/detail/submit/approve/reject/order/cancel;
versioning

3 Atomic receipt Purchase + Inventory + GRNI + Audit + outbox commit together

4 Partial receipt Parallel receipt and replay tests pass
concurrency

5 Invoice and matching Duplicate invoice and overmatch prevented

6 Ledger AP integration GRNI clearing, AP, tax/variance reconcile

7 Payment Allocation, settlement, durable idempotency, immutable reversal

8 External provider Only if in scope; no provider call in transaction
reconciliation

9 Supplier returns Inventory cost/movement and Ledger credit/claim consistent

10 Closure/reconciliation Remainders and discrepancies resolved

11 Export/events Existing jobs/outbox/storage reused

12 Hardening Security, race, failure, load, observability
----------------------------------------------------------------------------------------------------------------

Do not implement receipt until Inventory's transaction-aware receipt
port and Ledger's GRNI port are proven with integration tests.

## 26. Definition of done

- [ ] Tenant/branch/warehouse isolation is enforced.
- [ ] Existing middleware/platform infrastructure reused.
- [ ] Granular permissions and maker-checker work.
- [ ] Exact money/quantity representations match shared contracts.
- [ ] Independent lifecycle dimensions work.
- [ ] Partial receipt is concurrency-safe.
- [ ] Receipt, Inventory movement, cost layer, GRNI journal, Audit,
      outbox are atomic.
- [ ] Invoice uniqueness and three-way match are protected.
- [ ] Payment settlement/allocation is atomic and idempotent.
- [ ] Posted payments/invoices/receipts/returns are immutable;
      corrections are compensating.
- [ ] Supplier return preserves stock/cost/Ledger consistency.
- [ ] No direct Purchase writes to Inventory/Ledger tables.
- [ ] Redis is not source of truth.
- [ ] Provider calls are outside transactions and reconcilable.
- [ ] Export is async, private, scoped, and CSV-safe.
- [ ] Integration, race, property, and failure-injection tests pass.
- [ ] Reconciliation detects mismatches without rewriting history.
- [ ] Observability is useful and does not leak sensitive data.
- [ ] OpenAPI and implementation remain aligned.

## 27. Decisions to confirm before migrations

These are repository/business facts, not assumptions to hardcode:

1.  Does the project already have a shared Money/Decimal type and
    currency exponent policy?
2.  Does Medicine/Inventory use integer base units or fixed-precision
    quantity?
3.  Is canonical stock identity MedicineID, VariantID, or another SKU
    identity?
4.  What exact transaction runner/context is used by repositories and
    cross-module services?
5.  What is the Inventory receipt/return port contract (batch, balance,
    movement, cost layer, valuation)?
6.  What is the Ledger
    GRNI/AP/tax/variance/settlement/supplier-credit/reversal contract?
7.  Does existing idempotency storage support durable operation
    semantics?
8.  What exact Audit and outbox append interfaces participate in a
    transaction?
9.  Is supplier invoice uniqueness per supplier, fiscal year, series, or
    another namespace?
10. What is overreceipt tolerance and rejected-goods quarantine policy?
11. Are supplier advances/invoice-before-receipt required?
12. Is external payment-provider integration in initial scope?
13. When is supplier credit recognized: dispatch, supplier acceptance,
    or credit note?
14. Do actual RBAC seeds use colon or dot permission naming?
15. What is the existing asynchronous export/job/storage convention?

No migration should be generated until these are reconciled with the
repository and existing Inventory/Ledger contracts.

---

## Appendix A --- Goods receipt sequence

```mermaid
sequenceDiagram
    participant API
    participant P as Purchase
    participant DB as Shared PostgreSQL transaction
    participant I as Inventory
    participant L as Ledger
    participant A as Audit
    participant O as Outbox

    API->>P: Receive + Idempotency-Key
    P->>DB: Begin; claim operation; lock PO/lines
    P->>DB: Insert receipt facts
    P->>I: Receive stock with same transaction
    I->>DB: Batch + balance + movement + cost layer
    I-->>P: Inventory references + valuation
    P->>L: Post GRNI with same transaction
    L->>DB: Balanced journal + source identity
    P->>A: Append audit in same transaction
    A->>DB: Audit evidence
    P->>O: Append GoodsReceived
    O->>DB: Outbox row
    P->>DB: Update aggregates, transition, operation result
    P->>DB: Commit once
    P-->>API: Receipt result
```

Any failure before commit rolls back all participating database writes.

## Appendix B --- External payment sequence

```mermaid
sequenceDiagram
    participant API
    participant P as Purchase
    participant DB as PostgreSQL
    participant EXT as Provider/Bank
    participant L as Ledger

    API->>P: Create payment intent
    P->>DB: Persist intent + stable provider key
    P->>DB: Commit
    P->>EXT: Submit outside transaction
    EXT-->>P: Response/callback
    P->>DB: Deduplicate provider event
    alt Verified settlement
        P->>DB: Begin settlement transaction
        P->>L: Post AP settlement in same transaction
        L->>DB: Journal + source identity
        P->>DB: Mark settled + allocations + audit/outbox
        P->>DB: Commit
    else Unknown outcome
        P->>DB: Mark pending_reconciliation
        Note over P,EXT: Reconcile same provider key; never blindly charge again
    end
```

## Appendix C --- Initial-release non-goals

Unless separately approved: - Autonomous AI purchase
creation/approval. - External payment integration without provider
security/reconciliation design. - Intercompany procurement across
organization boundaries. - Full supplier portal. - OCR extraction as
authoritative invoice posting. - Automatic supplier bank-detail
changes. - Editing/deleting posted receipts, invoices, payments,
returns, or journals. - Purchase-owned stock adjustment endpoints. -
Purchase-owned supplier balance or inventory valuation calculations.
