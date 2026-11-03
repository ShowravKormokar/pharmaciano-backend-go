# INVENTORY_CONTRACT.md

> **Project:** Pharmaciano ERP\
> **Module:** `internal/modules/inventory`\
> **Status:** Architecture and implementation contract\
> **Primary authority:** PostgreSQL\
> **API:** `/api/v1/inventory`

## 1. Purpose

Inventory owns the authoritative operational record of physical stock:
organization-level batch identity, warehouse-level stock balances,
immutable movements, reservations, cost layers and allocations,
adjustments, transfers, recalls, stock counts and reorder policies.

Inventory is a transactional domain, not CRUD around a quantity field.
Every committed operation must preserve quantity conservation, batch
traceability, valuation provenance, tenant/branch/warehouse isolation,
auditability and Ledger consistency.

**PostgreSQL is the source of truth.** Redis may support
non-authoritative read caches and existing infrastructure, but it must
never be the sole record of stock, reservations, FEFO eligibility,
cost-layer remainder, transfer state or idempotency.

## 2. Authority and reconciliation of source material

This contract is grounded in the Inventory section of Astra's
`answer_in_markdown.md`, the route table and model sketch supplied for
this request, and Pharmaciano's established modular-monolith
conventions.

Astra's required principles:

-   Batch identity is organization-owned; balances and reservations are
    warehouse-scoped.
-   Transfers span two warehouses/branches within one organization.
-   Inventory owns allocation, stock eligibility, unit conversion, cost
    consumption, reservations and movement creation.
-   Purchase and Sale orchestrate their commercial workflows and call
    Inventory through transaction-aware ports.
-   Inventory-originated adjustments/write-offs/opening corrections
    coordinate Ledger facts.
-   Reservation APIs should initially be internal service ports, not
    generic public stock-lock endpoints.
-   FEFO, non-negative stock, immutable movements, durable idempotency
    and stock-to-Ledger reconciliation are mandatory.

The supplied structs are domain sketches, not a final schema. They
combine batch identity with location quantities, use floating-point
money, and omit reservations, cost layers, condition balances, approval
evidence, recalls and stock counts. Implement the normalized model below
rather than copying them literally.

The supplied routes are the initial compatibility baseline. Astra
additionally requires adjustment approval, recall, stock-count and
reorder-policy workflows; these are included below as required API
capabilities. Add them deliberately to OpenAPI/router rather than
implementing undocumented endpoints.

**Reuse existing infrastructure.** Do not create parallel
authentication, tenant middleware, RBAC, transaction manager,
idempotency framework, audit system, outbox, worker framework,
pagination, error mapping or observability stack. Inspect completed
modules and use their actual APIs.

## 3. Ownership and module boundaries

  -----------------------------------------------------------------------
  Fact                                Owner
  ----------------------------------- -----------------------------------
  Medicine, variant, package and      Medicine
  base-unit conversion definition     

  Batch/lot identity, expiry,         Inventory
  physical quantity, condition,       
  movement                            

  Purchase order, goods receipt,      Purchase
  supplier invoice/return             

  Sale, invoice, tender, customer     Sale
  return/refund                       

  Chart of accounts, journals,        Ledger
  journal lines and account balances  

  Organization, branch, warehouse     Existing foundation modules
  master data                         

  Authentication, tenant/branch       Existing auth/middleware/RBAC
  scope, RBAC                         

  Audit, idempotency, outbox,         Existing shared infrastructure
  tracing, metrics, jobs              
  -----------------------------------------------------------------------

Inventory must not create duplicate medicine, supplier, customer,
warehouse, journal, user or permission master data.

### 3.1 Transaction ownership

Purchase owns purchase receipt. Sale owns checkout. Their orchestrator
must pass the **same PostgreSQL transaction context** to Inventory,
Ledger, mandatory Audit and Outbox writes.

-   Purchase receipt: Purchase validates the receipt; Inventory
    increases stock and creates cost layers; Ledger posts
    inventory/GRNI; Audit and Outbox append evidence; Purchase commits.
-   Sale checkout: Sale validates commercial facts/tender; Inventory
    allocates and consumes stock/cost; Ledger posts revenue/tax/COGS;
    Audit and Outbox append evidence; Sale commits.

Inventory must never independently begin/commit a transaction inside
these workflows. Otherwise stock could commit while the receipt/sale or
journal fails.

Inventory-owned adjustments, expiry write-offs and opening corrections
use one transaction covering adjustment state, balances, movements, cost
evidence, Ledger journal, Audit, Outbox and idempotency result.

## 4. Scope and tenancy

  -----------------------------------------------------------------------
  Resource                            Scope
  ----------------------------------- -----------------------------------
  Batch identity                      Organization

  Stock balance                       Organization + branch + warehouse +
                                      batch + condition

  Movement                            Organization + branch + warehouse +
                                      batch

  Reservation                         Organization + branch + warehouse +
                                      owning operation

  Cost layer                          Organization + branch + warehouse +
                                      batch + currency

  Adjustment                          Organization + branch + warehouse

  Transfer                            One organization; source and
                                      destination branch/warehouse

  Recall                              Organization; optional batch or
                                      variant target

  Stock count                         Organization + branch + warehouse

  Reorder policy                      Organization + branch + warehouse +
                                      variant
  -----------------------------------------------------------------------

Every tenant-owned repository operation must receive resolved `orgID`.
Do not trust `organization_id`, `branch_id`, `warehouse_id`,
`performed_by`, `approved_by` or ownership fields from request bodies.

Expected request flow follows existing router conventions:
authentication → session/security checks → tenant resolution → branch
scope → RBAC → rate policy → idempotency for mutations → audit context →
handler. Actual composition must match the existing `mw.Protected`
implementation.

`X-Branch-ID` override is permitted only through the existing trusted
SUPER_ADMIN override mechanism. It is not an arbitrary branch selector
for ordinary users.

A warehouse ID supplied by a client is only a requested resource
identifier. Resolve it from PostgreSQL and verify organization, branch
and actor scope on every operation.

### 4.1 Transfer authorization

-   Create requires permission for the requested transfer under
    organization policy.
-   Dispatch requires source branch/warehouse authorization.
-   Receive requires destination branch/warehouse authorization.
-   Destination-only users cannot dispatch source stock.
-   Source-only users cannot confirm destination receipt unless
    separately authorized.
-   Both warehouses must belong to the same organization.
    Cross-organization movement is not an Inventory transfer; it needs a
    separate intercompany purchase/sale design.

## 5. Non-negotiable invariants

### 5.1 Quantity

``` text
available_quantity = sellable_on_hand_quantity - active_reserved_quantity
0 <= reserved_quantity <= sellable_on_hand_quantity
on_hand_quantity >= 0
```

-   Negative stock is forbidden in service logic and database
    constraints.
-   Reservations cannot exceed available sellable quantity.
-   Damaged, quarantined, expired, recalled, blocked or in-transit stock
    is not sellable/reservable.
-   All balances use the Medicine variant's canonical base unit.
-   Package conversion comes from Medicine's authoritative conversion
    definition; snapshot the applied factor on the source operation
    where required.
-   Never mix package quantities and base-unit quantities in a balance.
-   Decide whether all supported base units are integral. If fractional
    units are required, use fixed-precision numeric quantities
    consistently in DB and DTOs. Never use binary floating point for
    quantity arithmetic.

### 5.2 Movement

-   Movements are append-only and immutable after commit.
-   Each movement references one durable operation and operation line.
-   Replaying an operation cannot append another movement.
-   Corrections use compensating movements, never edits/deletes.
-   Record quantity delta, operation identity, movement kind, location,
    batch, actor, timestamp and resulting balance/version evidence.
-   Non-zero quantity delta is required except for an explicitly
    modelled value-only adjustment.

### 5.3 Batch

-   Unique within organization + medicine variant + normalized lot
    number.
-   Same lot cannot silently acquire conflicting expiry/manufacture
    metadata.
-   Batch identity has no branch, warehouse, quantity or
    location-specific price fields.
-   Identity fields become protected after use; correction requires an
    audited workflow.
-   Expiry is checked synchronously against branch business date. A
    nightly job is not an eligibility control.
-   Preserve source expiry precision. Month-only expiry requires an
    approved documented interpretation; do not fabricate a day silently.

### 5.4 Cost

-   Inventory valuation is separate from selling price and MRP.
-   Acquisition cost comes from accepted receipt or approved
    opening/adjustment valuation.
-   Use immutable cost layers and immutable cost allocations.
-   Sale COGS uses recorded consumed-layer cost, never current purchase
    price or selling price.
-   Cost-layer remainder cannot be negative; allocations cannot exceed
    remainder.
-   Use the project's shared money/currency representation. Never use
    `float32`/`float64` for money.
-   If Ledger is currently BDT-only minor units, align exactly with
    Ledger and document that limitation. Do not create a competing money
    type or assume every currency has the same minor-unit scale.

## 6. Domain model

### 6.1 `InventoryBatch`

Organization-level lot identity:

-   `id`, `organization_id`, `variant_id`
-   `normalized_lot_no`, `display_lot_no`
-   optional `manufacturer_id`
-   `manufactured_on`, `expires_on`, `expiry_precision`
-   controlled `recall_status`
-   bounded source metadata where necessary

Do not put `branch_id`, `warehouse_id`, stock quantities or selling
prices here.

### 6.2 `InventoryBalance`

One row per organization + warehouse + batch + condition:

-   `id`, `organization_id`, `branch_id`, `warehouse_id`, `batch_id`
-   `condition`: `sellable`, `quarantined`, `damaged`, `expired`,
    `recalled`, `in_transit`
-   `on_hand_quantity`, `reserved_quantity`, `version`
-   timestamps

Unique key: `(organization_id, warehouse_id, batch_id, condition)`.
Branch must match the warehouse. Do not store independently mutable
`available_quantity`; derive it from on-hand minus active reservations.

### 6.3 `StockMovement`

Immutable event:

-   organization, branch, warehouse, batch, condition
-   controlled `movement_kind`
-   signed `quantity_delta`; optional value delta in shared money type
-   operation ID, operation-line ID and line number
-   source module/type/ID where applicable
-   balance-after/version-after evidence
-   actor, timestamp, optional reversal reference and bounded reason

Kinds must cover receipt, sale consumption, sale return, purchase
return, adjustment increase/decrease, damage, expiry write-off, transfer
dispatch/receipt/loss, opening balance and correction/reversal. Do not
accept arbitrary strings.

### 6.4 Reservation

`InventoryReservation` contains organization/branch/warehouse, unique
operation ID, owner type/ID, status, expiry and lifecycle
timestamps/version.

`InventoryReservationLine` links reservation to balance/batch and
records reserved, consumed and released quantities. Reconcile line
quantities and enforce bounds.

Reservation states:

``` text
active -> consumed
active -> released
active -> expired
active -> cancelled
```

Exactly one terminal transition wins. Expiry worker and checkout
finalizer must compete through the same PostgreSQL row lock/state
transition. Redis TTL is not the reservation lifecycle.

### 6.5 Cost layers

`InventoryCostLayer` contains organization/branch/warehouse/batch,
receipt operation/line, original and remaining quantity, unit
cost/currency, acquisition timestamp and immutable origin.

`InventoryCostAllocation` links a consumption movement to one or more
layers with allocated quantity and cost. It is immutable.

### 6.6 Adjustment

Header: organization/branch/warehouse, operation ID/number, kind,
status, reason/evidence, requested/approved/posted actor and timestamps,
workflow version.

Lines: line number, batch, condition, signed non-zero delta, valuation
basis, expected balance version and line-level reason/evidence.

Drafts may be edited under version control. Posted adjustments are
immutable.

### 6.7 Transfer

Header: organization, source/destination branch and warehouse, operation
ID/number, status/version, requested/approved/dispatched/received actors
and timestamps.

Lines: variant, batch, requested/dispatched/received/lost quantities,
cost provenance and line number. Batch number and expiry are read from
the batch identity; any snapshot is evidence only, not a second source
of truth.

### 6.8 Recall, count, reorder

-   `InventoryRecall`: organization, variant, optional batch,
    reason/source evidence, effective time, status and lifecycle actors.
-   `InventoryStockCount`: organization/branch/warehouse, scope, state,
    cutoff strategy, opened/approved actors and timestamps.
-   `InventoryStockCountLine`: balance identity, expected
    quantity/version snapshot, observed quantity/time, variance, counter
    and evidence.
-   `InventoryReorderPolicy`: organization/branch/warehouse/variant,
    minimum, target, safety quantity, lead time, version and
    active/effective dates.

## 7. Database contract

Suggested tables:

  -----------------------------------------------------------------------
  Table                               Required role
  ----------------------------------- -----------------------------------
  `inventory_batches`                 Organization-level lot identity

  `inventory_balances`                Location/batch/condition quantity

  `inventory_movements`               Immutable stock journal

  `inventory_reservations`            Durable reservation header

  `inventory_reservation_lines`       Batch-level reserved quantity

  `inventory_cost_layers`             Acquisition valuation

  `inventory_cost_allocations`        Immutable consumption cost evidence

  `inventory_adjustments` / `_lines`  Adjustment workflow

  `inventory_transfers` / `_lines`    Transfer workflow

  `inventory_recalls`                 Recall lifecycle

  `inventory_stock_counts` / `_lines` Count and observations

  `inventory_reorder_policies`        Location-specific replenishment
                                      settings
  -----------------------------------------------------------------------

Use shared UUID/BaseModel and migration conventions already present in
the repository.

### 7.1 Constraints

At minimum:

-   Unique `(organization_id, variant_id, normalized_lot_no)` for
    batches.
-   Unique `(organization_id, warehouse_id, batch_id, condition)` for
    balances.
-   `on_hand_quantity >= 0`.
-   `reserved_quantity >= 0 AND reserved_quantity <= on_hand_quantity`.
-   Non-sellable conditions cannot have active reservations.
-   Unique durable operation/line/movement identity.
-   Reservation operation uniqueness within organization.
-   Cost layer `0 <= remaining_quantity <= original_quantity`.
-   Positive cost allocation quantity; allocation cannot exceed
    remaining layer quantity.
-   Transfer source != destination.
-   `0 <= received + lost <= dispatched <= requested`.
-   Adjustment delta is non-zero.
-   Reorder `target >= minimum >= 0`.
-   State-dependent actor/timestamp consistency.

Use tenant-safe composite foreign keys where practical. A child must not
reference a batch/warehouse belonging to another organization. Validate
branch/warehouse/org consistency in service logic and add database
constraints where possible.

Posted movements, cost allocations, approval decisions, transfer
transitions and posted adjustments cannot be updated/deleted through
normal repository methods. Use append-only boundaries and, where
appropriate, database role/trigger protections.

### 7.2 Indexes

Index actual access paths:

-   unique balance key
    `(organization_id, warehouse_id, batch_id, condition)`
-   FEFO/expiry lookup by organization, warehouse, variant/batch expiry
    and stable ID
-   movement history
    `(organization_id, warehouse_id, occurred_at DESC, id DESC)`
-   operation lookup `(organization_id, operation_id)`
-   partial active-reservation index
    `(organization_id, status, expires_at, id)`
-   adjustment/transfer/count status queues
-   reorder `(organization_id, warehouse_id, variant_id)`
-   high-volume foreign-key joins

Use the project's safe migration strategy for large indexes. Do not add
speculative indexes without query-plan evidence.

## 8. FEFO, expiry and sellability

FEFO order:

1.  Earliest valid expiry date.
2.  Stable deterministic tie-breaker (e.g. received timestamp then batch
    ID).
3.  Any additional approved policy tie-breaker.

Eligibility must filter organization, authorized warehouse, variant,
sellable condition, positive available quantity, branch business date,
recall status and batch sale-block state.

Expiry is checked synchronously during allocation/reservation. Nightly
jobs are projections only. Month-only expiry must preserve source
precision and use one approved interpretation policy confirmed against
applicable pharmacy/regulatory rules.

Do not use `SKIP LOCKED` for strict FEFO checkout: skipping a locked
earliest-expiry batch changes allocation semantics. Wait within bounded
lock timeout, then re-evaluate.

## 9. Reservations

Reservations reduce available quantity, not physical on-hand. They are
durable PostgreSQL records linked to stable owner operation IDs.
Consume/release/expire exactly once. Consume must recheck expiry,
recall, condition and available quantity.

Recall activation must block new allocation and force
revalidation/release/invalidation of affected active reservations
according to a deterministic policy.

Initially expose reservation functions only as internal Sale/payment
orchestration ports. Do not expose generic public stock-lock endpoints.

## 10. Costing and Ledger coordination

FEFO selects physical stock by expiry. Cost-layer policy values the
consumed quantity; these are separate decisions.

Initial recommendation: specific receipt-layer provenance, with a
documented deterministic layer order (e.g. FIFO by receipt time within a
batch when multiple receipts create separate layers). Never overwrite
original acquisition cost. Allocate rounding residual deterministically
so allocation sum equals posted cost exactly.

  -----------------------------------------------------------------------
  Event                   Inventory               Ledger
  ----------------------- ----------------------- -----------------------
  Accepted purchase       Increase stock; create  Purchase posts Dr
  receipt                 cost layer; append      Inventory / Cr GRNI or
                          movement                accrued receipts

  Sale                    Consume stock and cost  Sale posts Dr COGS / Cr
                          layers                  Inventory

  Sale return             Return to quarantine by Sale posts commercial
                          default; preserve       credit and appropriate
                          original cost           inventory/COGS
                                                  correction

  Purchase return         Decrease original batch Purchase posts
                          quantity/cost           payable/GRNI and
                                                  inventory correction

  Damage/expiry write-off Move/remove quantity    Dr loss/expense / Cr
                          through movement        Inventory

  Opening/positive        Add quantity with       Dr Inventory / Cr
  adjustment              approved valuation      approved
                                                  opening/variance
                                                  account

  Transfer dispatch       Source decreases;       Dr Stock-in-Transit /
                          in-transit              Cr Inventory if policy
                          quantity/value          requires
                          increases               

  Transfer receive        In-transit clears;      Clear in-transit to
                          destination increases   destination inventory

  Transfer loss           Resolve missing         Approved transfer-loss
                          quantity/value          journal
  -----------------------------------------------------------------------

Inventory never hardcodes account IDs. Ledger owns account mapping and
journal semantics. Confirm transfer accounting against the Ledger
contract and branch accounting policy before implementation.

For Inventory-owned financial operations, one transaction must include
adjustment/operation state, balance updates, movements, cost-layer
changes, Ledger journal, Audit, Outbox and durable idempotency result.
If Ledger fails, all Inventory writes roll back.

For Purchase/Sale, the owning module passes its transaction context.
Inventory must not commit independently. No external API, notification,
PDF or object-storage call while stock locks are held.

## 11. Adjustments

Types: `count_variance`, `damage`, `expiry_write_off`,
`opening_balance`, `correction`, `other_approved`.

State machine:

``` text
draft -> submitted -> approved -> posted
                    \-> rejected
draft -> cancelled
```

High-risk/value adjustments require maker-checker: requester and
approver are different actors. Thresholds must come from explicit
policy, not hardcoded role names.

Posting validates tenant/warehouse/batch, state/version,
reason/evidence, valuation, stock availability and condition. Lock
balances canonically; apply guarded updates; append movements; update
cost layers; post Ledger; append Audit/Outbox; persist idempotency
result; commit once.

Posted adjustments are immutable. Correct them with a compensating
adjustment.

## 12. Transfers

States:

``` text
draft -> approved -> dispatched -> partially_received -> received
  \          \
   \          -> cancelled (before dispatch only)
    -> cancelled (before dispatch only)
```

After dispatch, cancellation is forbidden. Use return transfer or an
approved loss/exception workflow.

Dispatch is its own transaction: lock transfer/lines, validate source
authorization and destination ownership, lock source balances/layers in
canonical order, verify available quantity, decrement source, represent
in-transit stock explicitly, append movements/transitions, preserve cost
provenance, post configured in-transit Ledger facts,
audit/outbox/idempotency, commit.

Receive is separate: lock transfer/lines, validate destination
authorization, allow partial receipt, ensure received/lost does not
exceed dispatched, move accepted stock from in-transit to destination
preserving batch/cost, route damaged stock to quarantine, keep missing
quantity in discrepancy state until approved loss resolution, post
configured Ledger clearing/loss, append
movement/transition/audit/outbox, commit.

For each line:

``` text
0 <= received + lost <= dispatched <= requested
```

For a fully resolved line: `received + approved_lost = dispatched`.

Lock source/destination rows in canonical order independent of transfer
direction to avoid opposite-transfer deadlocks. Never leave stock
counted in source and destination simultaneously.

## 13. Stock counts

A count cannot overwrite live stock with a stale observed number.

Prefer version/cutoff reconciliation for a live pharmacy: capture
balance version and count time, then account for movements committed
after the count snapshot. A freeze strategy is acceptable only if
operationally required and enforced.

At posting, calculate expected quantity at the count's logical cutoff
and reconcile intervening movements. Record expected, observed,
variance, counter, reviewer, reason and evidence. Post normal adjustment
movements and Ledger facts. Never blindly set `on_hand = observed`.

## 14. Recall and batch deactivation

Recall activation blocks new FEFO allocation/reservation immediately.
Checkout rechecks recall inside its transaction; stale cache cannot
bypass it. Existing reservations must be
revalidated/released/invalidated. Recall does not delete stock or
movements. Closure does not automatically restore sellability; a
separate authorized inspection/release action is required.

Batch deactivate blocks allocation but does not erase quantity. Activate
only when expiry, recall, quarantine and other restrictions permit it.

## 15. Reorder policy

Reorder policy is per organization/branch/warehouse/variant. Enforce
`target >= minimum >= 0`, nonnegative safety quantity and bounded lead
time. Low-stock recommendations are advisory; they do not create/approve
Purchase Orders. Incoming supply is included only through an explicit
Purchase read contract. AI cannot mutate stock directly.

## 16. API contract

All paths are relative to `/api/v1`. Preserve the supplied route names
as compatibility baseline. Astra uses dotted permission notation while
the supplied table uses colon notation. Use the convention already
established by RBAC seed data; do not create duplicate aliases.

### 16.1 Supplied baseline

  --------------------------------------------------------------------------------------------------------------------
  Method & path                               Rate                Permission               Contract
  ------------------------------------------- ------------------- ------------------------ ---------------------------
  `GET /inventory/batches`                    `rate_auth_read`    `inventory:view`         Cursor list; branch,
                                                                                           warehouse,
                                                                                           medicine/variant,
                                                                                           status/condition, expiry,
                                                                                           low-stock and query filters

  `GET /inventory/batches/{id}`               `rate_auth_read`    `inventory:view`         Batch identity + authorized
                                                                                           balances + movement history

  `PATCH /inventory/batches/{id}`             `rate_auth_write`   `inventory:adjust`       Only Inventory-owned
                                                                                           metadata; never stock
                                                                                           quantity or historical
                                                                                           identity

  `POST /inventory/batches/{id}/deactivate`   `rate_auth_write`   `inventory:deactivate`   Block new allocation; audit

  `POST /inventory/batches/{id}/activate`     `rate_auth_write`   `inventory:adjust`       Re-enable only if all
                                                                                           restrictions cleared

  `POST /inventory/adjust`                    `rate_auth_write`   `inventory:adjust`       Adjustment command;
                                                                                           `Idempotency-Key` required

  `GET /inventory/stock-movements`            `rate_auth_read`    `inventory:view`         Cursor list; batch,
                                                                                           variant, kind, source and
                                                                                           date filters

  `GET /inventory/stock-summary`              `rate_auth_read`    `inventory:view`         Aggregate by variant in
                                                                                           authorized scope

  `GET /inventory/low-stock`                  `rate_auth_read`    `inventory:view`         Under configured minimum

  `GET /inventory/expiring`                   `rate_auth_read`    `inventory:view`         Default 30 days; branch
                                                                                           business date

  `POST /inventory/transfers`                 `rate_auth_write`   `inventory:transfer`     Draft; idempotent

  `GET /inventory/transfers`                  `rate_auth_read`    `inventory:view`         Cursor list and
                                                                                           status/source/destination
                                                                                           filters

  `GET /inventory/transfers/{id}`             `rate_auth_read`    `inventory:view`         Scoped detail

  `POST /inventory/transfers/{id}/dispatch`   `rate_auth_write`   `inventory:transfer`     Dispatch; idempotency +
                                                                                           version

  `POST /inventory/transfers/{id}/receive`    `rate_auth_write`   `inventory:transfer`     Partial/full receive;
                                                                                           idempotency + version

  `POST /inventory/transfers/{id}/cancel`     `rate_auth_write`   `inventory:transfer`     Before dispatch only

  `GET /inventory/export`                     `rate_export`       `inventory:export`       Async CSV/XLSX; bounded
                                                                                           filters and secure download
  --------------------------------------------------------------------------------------------------------------------

**Ownership correction:** supplied `PATCH /batches/{id}` mentions
selling price, tax and discount. Pharmaciano ownership assigns selling
price books to Sale and tax classification to MasterData/Medicine.
Inventory must not become a second pricing/tax authority. Keep
compatibility only for Inventory-owned fields; delegate/deprecate
foreign-owned fields through their owning module.

### 16.2 Astra-required additional capabilities

  -----------------------------------------------------------------------------------------------------
  Method & path                                Permission                       Contract
  -------------------------------------------- -------------------------------- -----------------------
  `GET /inventory/adjustments` and `GET /{id}` `inventory:view`                 Scoped adjustment
                                                                                history/detail

  `POST /inventory/adjustments/{id}/approve`   `inventory:approve-adjustment`   Maker-checker, expected
                                                                                version

  `POST /inventory/adjustments/{id}/reject`    `inventory:approve-adjustment`   Reason required

  `POST /inventory/recalls`                    `inventory:recall`               Activate recall;
                                                                                idempotent

  `GET /inventory/recalls` and `GET /{id}`     `inventory:view`                 Scoped recall read

  `POST /inventory/recalls/{id}/close`         `inventory:recall`               Evidence; no automatic
                                                                                stock release

  `POST /inventory/counts`                     `inventory:count`                Create count session;
                                                                                idempotent

  `GET /inventory/counts` and `GET /{id}`      `inventory:view`                 Count/variance read

  `POST /inventory/counts/{id}/submit`         `inventory:count`                Submit
                                                                                observations/version

  `POST /inventory/counts/{id}/approve`        Dedicated count approval or      Approve variance
                                               adjustment approval permission   

  `POST /inventory/counts/{id}/post`           `inventory:adjust`               Movements + Ledger
                                                                                atomically

  `GET /inventory/reorder-policies`            `inventory:view`                 Scoped policies

  `PUT /inventory/reorder-policies/{id}`       `inventory:configure`            Versioned configuration

  `POST /inventory/exports` /                  `inventory:export`               Use existing export/job
  `GET /inventory/exports/{id}`                                                 framework
  -----------------------------------------------------------------------------------------------------

Use existing export route conventions; do not build a second export/job
framework.

### 16.3 Internal service ports, not public endpoints

`ReserveStock`, `ReleaseReservation`, `ConsumeReservation`,
`AllocateFEFO`, `ConsumeStock`, `ReceiveStock`,
`ReturnStockToQuarantine`, `ConsumeCostLayers`,
`CreateAdjustmentWithinTx`, `DispatchTransferWithinTx`,
`ReceiveTransferWithinTx`, `ActivateRecallWithinTx`,
`ReconcileCountWithinTx`.

Each mutation port accepts transaction context, organization ID,
resolved scope and stable operation identity. No stock mutation without
durable operation identity.

## 17. DTO and validation

-   Use explicit request/response DTOs; never bind persistence models
    from JSON.
-   Never accept organization, actor, approver, poster or trusted
    ownership fields from client.
-   Resolve actor and tenant from context.
-   Validate UUIDs, enum values, string lengths, lot normalization,
    quantity precision and date ranges.
-   Use shared pagination/cursor utilities.
-   Use expected-version / `If-Match` according to existing project
    convention.
-   Bound page sizes, date ranges, export rows and file sizes.
-   Do not expose cost layers/purchase cost through ordinary stock DTOs
    unless RBAC policy permits it.
-   Use shared error envelope; do not leak SQL, stack traces, table
    names, secrets or cross-tenant existence.

Distinguish validation, not-found (including foreign tenant), forbidden
scope, insufficient stock, expired/recalled batch, stale version,
idempotency conflict, invalid state, approval required, Ledger failure,
retryable DB conflict and database unavailable.

## 18. Idempotency

Every stock-changing public command requires existing idempotency
middleware/platform support and durable PostgreSQL operation identity:

-   adjustment create/submit/approve/post
-   transfer create/dispatch/receive/cancel
-   recall activation/closure
-   stock count create/submit/approve/post
-   batch state mutation
-   export job creation
-   internal Purchase/Sale stock commands

Semantics:

-   Same key + same canonical request + same actor/tenant/scope returns
    original result.
-   Same key + different request returns conflict.
-   Keys/results are tenant-scoped.
-   Redis loss/flush/expiry cannot duplicate stock effects.
-   Durable operation and movement uniqueness is enforced in PostgreSQL.
-   Unknown commit outcome is resolved by querying the durable operation
    before retry.
-   Retention follows existing platform contract; do not invent a
    shorter Inventory TTL.

Purchase/Sale source receipt/checkout IDs remain durable source
identities even if HTTP idempotency records expire.

## 19. Transaction and concurrency

Use existing transaction manager. PostgreSQL `READ COMMITTED` with
explicit row locks and guarded updates is appropriate; do not make every
transaction SERIALIZABLE by default.

Critical operations:

-   `SELECT ... FOR UPDATE` for balances, reservations, operation
    headers, transfer lines and cost layers.
-   Lock rows in canonical order: warehouse UUID, batch UUID, condition,
    then cost-layer order.
-   Use guarded
    `UPDATE ... WHERE on_hand >= requested ... RETURNING ...`.
-   For absent balance rows, unique-key insert/upsert then lock the
    created row. A lock cannot lock a nonexistent row.
-   Keep transactions short and scoped to affected rows.
-   Use bounded lock/statement timeouts.
-   Retry only known deadlock/serialization failures by restarting the
    entire transaction with the same operation ID.
-   On uncertain commit, read durable operation state before retrying.
-   Never retry business validation errors.

Atomic stock mutation:

1.  Begin using existing transaction context.
2.  Resolve/validate organization, branch and warehouse.
3.  Claim/validate durable operation identity.
4.  Lock operation aggregate and affected balances canonically.
5.  Re-read availability, expiry, recall, condition and reservations
    from PostgreSQL primary.
6.  Lock cost layers if valuation changes.
7.  Validate quantities and state.
8.  Apply guarded balance updates.
9.  Append immutable movements.
10. Append cost allocations/update layer remainder.
11. Post Ledger if operation owns financial posting.
12. Append mandatory Audit and Outbox.
13. Persist idempotency result.
14. Commit once.

Never call payment providers, notifications, AI, PDF rendering, object
storage or external services while stock locks are held.

## 20. Purchase and Sale integration

### Purchase receipt

Within Purchase's transaction:

1.  Lock receipt and ordered quantities.
2.  Validate supplier, order, warehouse and authorization.
3.  Create/find batch identity; reject conflicting lot metadata.
4.  Add accepted quantity to the correct balance/condition.
5.  Create cost layer tied to receipt line.
6.  Append movement.
7.  Ledger posts Dr Inventory / Cr GRNI (or configured mapping).
8.  Purchase records receipt completion.
9.  Audit/Outbox append.
10. Commit.

Rejected goods never enter sellable stock. Duplicate receipt cannot
increase stock twice, even after Redis loss.

### Sale checkout

Within Sale's transaction:

1.  Sale validates price/tax/discount/customer/tender.
2.  Inventory locks FEFO balances.
3.  Rechecks expiry, recall, condition and reservation ownership.
4.  Consumes available quantity or matching reservation.
5.  Allocates actual cost layers and appends movements.
6.  Sale persists batch allocations.
7.  Ledger posts revenue/tax and Dr COGS / Cr Inventory.
8.  Audit/Outbox append.
9.  Commit.

Never hold stock locks during external payment. Online payment uses
durable intent + reservation, provider call outside transaction,
verified finalization. Late success after reservation expiry requires
refund/manual reconciliation, never forced negative stock.

### Returns

Sale return must reference original sale allocation; cumulative returned
quantity cannot exceed sold quantity. Default disposition is quarantine.
Only verified eligible stock becomes sellable. Preserve original batch
and cost provenance.

Purchase return uses exact original batch/receipt cost provenance.
Inventory must not accept arbitrary client-supplied cost.

## 21. Redis, events and jobs

Redis is never authoritative for stock, checkout availability,
reservation state, FEFO, recall, cost-layer remainder, transfer state,
idempotency or Ledger outcome.

Optional short-lived stock-summary cache is non-authoritative and for
browsing only. All mutations/checkout use PostgreSQL primary. Invalidate
cache after commit through existing outbox/event flow. Redis failure
cannot cause overselling.

Use existing transactional outbox and worker framework. Suggested
events:

-   `inventory.stock_received`
-   `inventory.stock_consumed`
-   `inventory.stock_adjusted`
-   `inventory.transfer_dispatched`
-   `inventory.transfer_received`
-   `inventory.recall_activated`
-   `inventory.reservation_expired`
-   `inventory.stock_count_posted`
-   `inventory.cost_adjusted`

Payloads carry IDs, committed quantities, operation IDs and versions;
avoid unnecessary PII.

Jobs: reservation expiry, expiry-risk scan, expired-stock
classification, low-stock scan, stock/cost reconciliation, in-transit
aging and count exception processing. Jobs must be retry-safe and use
durable operation identity. Reconciliation jobs diagnose by default;
they never silently rewrite balances or journals.

## 22. Security and audit

Threat controls:

  -----------------------------------------------------------------------
  Threat                              Control
  ----------------------------------- -----------------------------------
  Cross-tenant data                   Mandatory org scope, tenant-safe
                                      FKs, no client org trust

  Cross-branch/warehouse              Branch scope + warehouse ownership
                                      checks in service/repository

  Overselling                         PostgreSQL locks, guarded updates,
                                      DB checks

  Duplicate receipt/checkout          Durable idempotency + unique
                                      operation/movement keys

  Lot substitution                    Protected batch identity and
                                      original allocation references

  Expired sale                        Synchronous branch-date check

  Recall race                         Recall recheck in stock transaction

  Stock theft via adjustment          Reason/evidence, maker-checker,
                                      audit, thresholds

  Transfer fraud                      Independent source/destination
                                      authorization

  Cost leakage                        Separate cost-view permission and
                                      DTO

  Mass assignment                     Explicit DTO allowlists

  CSV formula injection               Escape spreadsheet-leading formula
                                      characters

  Export exfiltration                 Bounded filters, scoped job,
                                      reauthorize download, short-lived
                                      signed URL via existing file
                                      service

  Unsafe repair                       Separate authorized audited repair
                                      procedure
  -----------------------------------------------------------------------

Use existing RBAC permission convention for `inventory:view`,
`inventory:adjust`, `inventory:approve-adjustment`,
`inventory:deactivate`, `inventory:transfer`,
`inventory:receive-transfer`, `inventory:recall`, `inventory:count`,
`inventory:configure`, `inventory:export` and cost visibility where
needed. Seed permissions through existing RBAC process. Do not hardcode
role names in services.

Audit every movement, adjustment lifecycle, transfer transition/loss,
recall lifecycle, count variance, batch state change, valuation
correction and privileged export. Mandatory audit append belongs in the
same transaction as the business mutation. Do not create a parallel
Audit module.

## 23. Observability and performance

Use existing structured logs, metrics and tracing.

Logs: request/trace ID, operation ID, module/action/outcome, safe
org/branch/warehouse IDs, batch/transfer IDs, retry count and failure
category. Never log tokens or secrets.

Low-cardinality metrics:

-   mutation success/failure by operation type
-   FEFO allocation latency
-   lock wait and transaction duration
-   insufficient-stock/negative-stock guard rejections
-   reservation count/age and expiry lag
-   movement/balance mismatch
-   cost-layer/allocation mismatch
-   in-transit age
-   count variance buckets
-   recall activation latency
-   outbox/worker lag

Never put arbitrary tenant/user/batch/operation UUIDs in Prometheus
labels.

Performance rules:

-   Narrow, indexed balance rows; append-heavy movements.
-   Cursor pagination on stable `(occurred_at, id)`.
-   Bounded date ranges and page sizes.
-   Batch-load related metadata; avoid N+1.
-   Do not recalculate organization-wide totals per POS line.
-   Dashboard projections may be cached; checkout availability may not.
-   Avoid branch/organization-wide locks.
-   Keep stock transactions short.
-   Partition movement tables only when measured volume/query plans
    justify it.
-   Async exports use existing jobs, bounded rows/date range, streaming
    and download reauthorization.
-   Read-after-write uses PostgreSQL primary / existing consistency
    policy.

## 24. Reconciliation and recovery

Quantity:

``` text
current_balance = opening_quantity + sum(committed_movement_quantity_delta)
```

The replay equation must account for condition transitions and
in-transit representation.

Reservation:

``` text
balance.reserved_quantity = sum(active reservation-line remaining quantities)
```

Cost layer:

``` text
remaining_quantity = original_quantity - sum(committed allocation quantities)
```

Cost allocations must tie to consumption movements and Ledger COGS
evidence. Inventory valuation must reconcile to Ledger inventory control
accounts under the Ledger contract.

Reconciliation is diagnostic by default. Differences become exceptions.
Repair only through a separately authorized, reasoned, audited
compensating operation; never rewrite historical movements/journals to
force agreement.

Failure outcomes:

-   DB transaction failure: no partial stock/cost/ledger/audit/outbox
    commit.
-   Ledger or mandatory Audit/Outbox failure: whole operation rolls
    back.
-   Redis failure: correctness unaffected.
-   Worker crash: durable operation remains retryable.
-   Partial transfer: received quantity moves; unresolved quantity
    remains in transit/discrepancy.
-   Unknown commit: resolve operation record before retry.
-   Deadlock: bounded whole-transaction retry with same operation ID.

## 25. Testing contract

Use unit, service, real-PostgreSQL repository, API, integration,
concurrency and failure-injection tests.

Mandatory concurrency tests:

1.  Two sales for last unit: exactly one succeeds.
2.  Two reservations for last unit: reserved never exceeds on-hand.
3.  Expiry races with consume: exactly one terminal state.
4.  Recall races with checkout: recalled stock is not consumed after
    recall wins.
5.  Opposite transfers: no persistent deadlock; quantity conserved.
6.  Duplicate receipt after Redis flush: one stock increase and one cost
    effect.
7.  Duplicate dispatch/receive: no duplicate movement.
8.  Concurrent adjustment with stale version: stale command rejected.
9.  Count posting during movements: cutoff reconciliation correct.
10. Concurrent cost consumption: no negative layer remainder.

Mandatory integration tests:

-   Purchase receipt + Inventory + Ledger + Audit + Outbox all commit or
    all roll back.
-   Sale checkout + Inventory + Ledger + Audit + Outbox all commit or
    all roll back.
-   Sale return restores original batch to quarantine and preserves cost
    provenance.
-   Purchase return consumes original receipt cost basis.
-   Transfer dispatch/receive posts configured in-transit journal
    effects.
-   Adjustment/write-off creates matching movement and journal.
-   Cross-tenant/branch/warehouse requests are denied.
-   Export job and download enforce scope.

Release gates: no committed negative stock; no duplicate operation
effects; synchronous expiry/recall checks; reservation and cost
reconciliation; transfer conservation; atomic Ledger integration; shared
transaction context for Purchase/Sale; OpenAPI/router/permission seeds
synchronized.

## 26. Implementation order

  -----------------------------------------------------------------------
  Phase                               Deliverable
  ----------------------------------- -----------------------------------
  0                                   Audit existing transaction,
                                      BaseModel, UUID, pagination, app
                                      context, branch scope, RBAC,
                                      idempotency, Audit, Outbox, worker,
                                      money and quantity abstractions

  1                                   Freeze
                                      quantity/expiry/cost/transfer
                                      policy; migrations, FKs, checks and
                                      indexes

  2                                   Batch identity, balances and
                                      immutable movements

  3                                   FEFO allocation and durable
                                      reservation ports

  4                                   Cost layers, allocations and
                                      reconciliation

  5                                   Adjustments, approvals and atomic
                                      Ledger adapter

  6                                   Transfers, partial receipt,
                                      in-transit and loss resolution

  7                                   Recall, stock counts and reorder
                                      policies

  8                                   DTOs, handlers, routes, OpenAPI,
                                      permissions and error mapping

  9                                   Existing jobs/outbox,
                                      observability, exports and
                                      reconciliation

  10                                  Purchase/Sale integration with
                                      shared transaction

  11                                  Load, security, failure-injection
                                      and recovery verification
  -----------------------------------------------------------------------

Do not start with dashboard CRUD before balance, movement, locking and
costing invariants are implemented.

## 27. Repository architecture expectations

Mirror completed modules and actual repository conventions. Typical
responsibilities:

-   `model.go`: persistence/domain types and typed constants.
-   `dto.go`: explicit request/response DTOs.
-   `mapper.go`: explicit persistence/domain ↔ DTO mapping.
-   repository: tenant-scoped SQL, transaction-aware persistence.
-   service: invariants, state transitions and orchestration.
-   handler: HTTP binding/validation/response mapping only.
-   routes: existing protected router conventions.
-   ports: narrow transaction-aware cross-module interfaces.
-   events: outbox payload contracts.
-   migrations: existing migration system.
-   OpenAPI: established `/api` contract-first structure.

This is a responsibility guide, not a requirement to create files that
conflict with the established module layout.

Repository rules:

-   Every tenant-owned method requires `orgID`.
-   Mutations accept existing transaction handle/context.
-   Critical reads use PostgreSQL primary.
-   Apply branch/warehouse scope to list, detail, mutation and export.
-   No unscoped tenant `GetByID`.
-   Use explicit SQL column lists.
-   Never return persistence models directly.
-   Avoid N+1 and hidden cross-module repository imports.
-   Service owns business invariants; repository does persistence;
    handler does HTTP only.

## 28. Explicit non-goals

Inventory does not own Medicine catalog/package conversion definitions,
Purchase/Sale documents, customer/supplier records, price books, tax
master, Chart of Accounts/journals, payment gateways, notification
delivery, AI model execution, a new
queue/cache/outbox/audit/idempotency/auth framework, cross-organization
transfer or offline stock authority.

## 29. Decisions to confirm before implementation

1.  Quantity precision: integral base units or fractional quantities?
2.  Month-only expiry interpretation against applicable pharmacy rules?
3.  Cost layer policy and rounding: specific receipt provenance / FIFO
    within batch?
4.  Currency: BDT-only or multi-currency, aligned with Ledger's exact
    money type?
5.  Transfer accounting: Stock-in-Transit journal for every transfer or
    another approved branch accounting policy?
6.  Adjustment approval thresholds and maker-checker rules?
7.  Existing RBAC permission for cost visibility?
8.  Who may inspect/release returned or quarantined stock?
9.  Inventory posting behavior after Ledger period close?
10. Actual APIs for transaction context, idempotency, Audit, Outbox,
    jobs, pagination, money and quantity in the repository?

Do not guess policies that affect stock eligibility, financial valuation
or legal traceability.

## 30. Definition of done

Inventory is production-ready only when:

-   PostgreSQL is the stock correctness authority.
-   Batch identity is organization-scoped; quantities are
    warehouse-scoped.
-   Every stock change has immutable movement evidence.
-   Balances are nonnegative, concurrency-safe and reconcilable.
-   FEFO is deterministic; expiry/recall checks are synchronous.
-   Reservations are durable, idempotent and race-safe.
-   Cost layers preserve acquisition provenance.
-   Adjustments, counts, recalls and transfers have controlled states
    and audit evidence.
-   Transfers conserve quantity across in-transit, partial receipt and
    loss resolution.
-   Inventory financial effects are atomic with Ledger.
-   Purchase/Sale invoke Inventory inside their outer transaction.
-   Existing auth, tenant, branch, RBAC, rate, idempotency, Audit,
    Outbox, pagination, observability and worker infrastructure is
    reused.
-   Cross-tenant/branch/warehouse access is denied at API and
    service/repository boundaries.
-   Concurrency, rollback, replay, reconciliation and recovery tests
    pass.
-   OpenAPI, generated interfaces, router, permission seeds and
    implementation agree.

**Final rule:** Never trade stock correctness for cache speed or
endpoint convenience. A failed stock operation is acceptable; silently
incorrect committed stock, cost or Ledger state is not.
