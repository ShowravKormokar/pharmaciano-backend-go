# Pharmaciano --- Sale Module Contract

**File:** `SALE_CONTRACT.md`\
**Module:** `internal/modules/sale`\
**Status:** Architecture and implementation contract\
**Implementation readiness:** Not ready; Sale is currently design-only\
**Primary consistency store:** PostgreSQL\
**Target:** Production-grade modular monolith

> **Source basis:** Repository inspection of the Sale, Inventory, Ledger,
> Customer, MasterData, Audit, database transaction, migration, router,
> and build-sequence code and schemas. The requirements below are target
> guarantees, not claims that the current implementation already meets
> them. Repository-specific gaps and release gates are recorded in §0.1
> and §29.

------------------------------------------------------------------------

## 0. Executive decisions

1.  Sale owns POS checkout orchestration, sale documents and immutable
    line snapshots, pricing/discounts, tenders, invoice issuance,
    credit-sale orchestration, returns, refunds, voids, and cashier
    sessions.
2.  Inventory owns batch identity, balances, stock condition,
    reservations, movements, and valuation/cost layers. Sale never
    writes Inventory tables directly.
3.  Ledger owns account mappings, journals,
    revenue/tax/COGS/receivable/cash posting, period checks, source
    deduplication, and reversals. Sale never writes Ledger tables or
    submits arbitrary account IDs.
4.  Synchronous cash/offline checkout commits Sale, stock consumption,
    cost allocation, tender, invoice, balanced journal, mandatory audit,
    and outbox in **one shared PostgreSQL transaction**. All succeed or
    all roll back.
5.  PostgreSQL is authoritative. Redis is never authoritative for stock,
    reservations, durable idempotency, invoice numbers, promotion
    quotas, credit exposure, payment settlement, or cash-session facts.
6.  Redis idempotency middleware is only an edge optimization.
    Domain-level PostgreSQL operation identity and unique constraints
    must prevent duplicate financial/stock effects even when Redis fails
    open or is unavailable.
7.  Remote payment providers are called outside database transactions.
    Use durable intent/reservation/provider-event/finalization state
    machines.
8.  Posted sales, invoices, tenders, returns, refunds, stock movements,
    and journals are immutable evidence. Correct through compensating
    documents/reversals, never silent edits or deletes.
9.  Inventory chooses eligible batches using FEFO. Client-supplied batch
    IDs are not trusted for ordinary POS checkout.
10. Returns are bounded by original sale allocations. Returned medicine
    defaults to quarantine/inspection, not immediately sellable stock.
11. Prescription/controlled-medicine rules are server-side policy
    decisions. Store minimal restricted evidence, not unnecessary
    clinical details.
12. Sale documents are branch-scoped. Cashier comes from authenticated
    context; warehouse is resolved and validated server-side.

### 0.1 Current repository readiness

The Sale contract is **not currently implemented or safe to expose for
mutations**. The following is the observed repository state; source
presence alone is not proof that a workflow is transactionally complete.

  ------------------------------------------------------------------------------------------------------
  Area                    Observed state                                              Readiness impact
  ----------------------- ----------------------------------------------------------- -------------------
  Sale package/API        Sale has persistence model definitions, but no services,   Persistence types
                          repositories, handlers, or tests; Sale is not mounted      alone do not enable
                          in the API router.                                          Sale mutations.

  Existing Sale schema    Migration `000009_sale_customer` already creates           Do not create a
                          `sales`, `sale_items`, `sale_payments`, returns, coupons,   parallel set of
                          offers, and redemption tables. The schema has legacy       same-purpose tables
                          mutable/soft-delete behavior, cascading deletes, and       or treat it as
                          lacks the contract's durable command/allocation model.     immutable evidence.

  Inventory              Migration `000026_inventory_core` adds normalized           Schema primitives
                          balances, movements, reservations, and cost layers; the    exist, but current
                          model defines exact `int64` base-unit quantities and         Go service/repository
                          FEFO selection is a pure, non-locking allocator. The        exposes only batch
                          current repository/service expose batch listing only.       listing; no consume,
                                                                                      reserve, or cost
                                                                                      allocation workflow
                                                                                      is available to Sale.

  Ledger                 `AutoPost` and source-posting deduplication exist and use    Useful foundation,
                          the platform transaction context (nested `WithTx` uses      but Sale still needs
                          a savepoint). However, `AutoPostCommand` accepts journal    a Ledger-owned
                          lines containing account IDs; no Sale-facts-to-approved-   posting policy/facts
                          accounts adapter is established here. Credit-balance       boundary and tested
                          lock/upsert primitives exist, but no Sale/customer         credit decision.
                          credit decision workflow is exposed.

  Transactions            `db.WithTx`, `db.FromCtx`, and nested savepoints provide   Available foundation;
                          a caller-owned transaction boundary.                      every participant
                                                                                      must use its context.

  Audit                   `PostgresAuditSink.Record` writes via the context but      Not a mandatory
                          returns no error and logs/swallow failures.                business-audit port.

  Outbox                  No durable outbox table or implementation is present in    Must be delivered
                          the inspected code/migrations.                             before mutations.

  Idempotency             HTTP middleware stores request fingerprints/results in    Edge-only. It can
                          Redis, allows missing keys, and fails open on Redis       fail open; no Sale
                          errors.                                                     PostgreSQL operation
                                                                                      record exists.

  Configuration            Default YAML defines `pos_checkout` at 30 requests per   Environment
                           minute and idempotency defaults; the loader now binds     overrides and
                           `RATE_LIMIT_ENABLED`, `RATE_LIMIT_POS_CHECKOUT_RPM`,       `.env.example` are
                           `IDEMPOTENCY_TTL`, and `IDEMPOTENCY_KEY_MAX_LENGTH`.      aligned; this does
                                                                                       not enable Sale.

  Money/currency          Ledger postings use integer minor units and currently      Initial Sale scope
                          accept BDT functional currency only. Inventory monetary    must be BDT-only
                          movement values are also constrained to BDT.               until a shared
                                                                                      currency contract
                                                                                      exists.

  Schema/migrations       Migration files extend through `000026`, but two distinct Do not add or run
                          migration pairs use version `000023` (`backup_module`    migrations until the
                          and `ledger_contract`).                                    deployed history and
                                                                                      version collision
                                                                                      are resolved.

  System plan             `docs/BUILD-SEQUENCE.md` still describes all domain       Its Sale order is
                          modules as scaffold-only, stops at migration `000014`,     stale: it places
                          and places Ledger after Sale.                              Ledger after Sale
                                                                                       despite the required
                                                                                       atomic posting.

  API/RBAC/rate policy    `docs/api/API.md` documents Sale endpoints that are not    Treat those routes
                          mounted. RBAC permissions use `module:action` values;      as proposed, use
                          configured limiter keys are `auth_read`, `auth_write`,    actual middleware
                          `pos_checkout`, and `export`. Sale action permissions are  keys. Sensitive
                          now cataloged without direct grants to operational roles; actions remain
                          `sales:update` is retired by the RBAC seeder.                gated by policy.

  ------------------------------------------------------------------------------------------------------

The cash/offline checkout guarantee in this contract is a **release
acceptance condition**, not a description of current behavior. No Sale
mutation route may be mounted until the prerequisites in §26 pass their
transactional integration tests. The first production checkout slice is
restricted to BDT and payment methods whose settlement can be recorded
locally; remote-provider, credit, promotion, and regulated-dispensing
workflows remain disabled until their own gates pass.
The first slice may sell only products whose owner-module policy
explicitly confirms that no prescription/reviewer workflow is required;
unknown or regulated classification fails closed.

## 1. Purpose, scope, and ownership

### 1.1 Purpose

Record and orchestrate the commercial sale/dispensing of medicine and
related products while preserving stock, valuation, payment, accounting,
invoice, audit, and tenant consistency.

### 1.2 In scope

-   POS lookup, calculation/quotation, checkout.
-   Retail, wholesale, and explicitly configured sale channels.
-   Effective-dated price books and branch overrides.
-   Discounts, coupons, promotions, and authorized overrides.
-   Tax calculation and immutable tax snapshots.
-   Cash/offline/split tender and customer-credit sale orchestration.
-   Remote payment intents and provider reconciliation.
-   FEFO allocation through Inventory.
-   Sale/line/allocation snapshots.
-   Invoice numbering and immutable invoice payloads.
-   Controlled void/reversal workflow.
-   Returns, dispositions, credit notes, refunds, and refund
    reconciliation.
-   Cashier/drawer sessions and close.
-   Minimal prescription review/evidence references.
-   Sale history, invoice reads, exports, analytical source facts.

### 1.3 Out of scope

-   Stock, batch master, balance, reservation, movement, and cost-layer
    ownership --- Inventory.
-   GL accounts, journal construction, account mapping, period control,
    and reversal semantics --- Ledger.
-   Customer master/contact/consent --- Customer.
-   Medicine/variant/barcode/package identity --- Medicine.
-   Global units, currencies, tax categories/rules --- MasterData or
    designated tax owner.
-   Authentication, permission evaluation, role definitions ---
    Auth/RBAC.
-   Payment-provider secrets --- platform secret/configuration
    infrastructure.
-   PDF rendering, notifications, analytics projections, and export
    artifact storage as synchronous checkout work.
-   Public stock-reservation APIs; reservation primitives remain
    internal unless separately approved.

### 1.4 Ownership matrix

  -----------------------------------------------------------------------
  Fact                    Authority               Sale responsibility
  ----------------------- ----------------------- -----------------------
  Sale and sale lines     Sale                    Own and persist

  Price/promotion         Sale                    Resolve, snapshot,
  redemption                                      enforce

  Tax rule definition     MasterData/tax owner    Resolve and snapshot
                                                  version

  Medicine/variant        Medicine                Validate and snapshot
  identity                                        

  Package conversion      Medicine/Inventory      Use validated
                          contract                conversion

  Batch, expiry, sellable Inventory               Request eligible
  stock                                           allocation

  Stock movement and cost Inventory               Correlate operation and
  layer                                           persist references

  Consumed cost           Inventory valuation     Pass exact facts to
                                                  Ledger

  Revenue, tax, COGS,     Ledger                  Submit typed source
  cash, AR journals                               facts

  Customer identity       Customer                Validate
                                                  reference/eligibility

  Credit exposure         Ledger subledger policy Request locked credit
                                                  decision

  Cashier identity        Auth context            Never trust request
                                                  body

  Prescription evidence   Restricted              Store minimal reference
                          Sale/evidence subdomain and decision

  Audit/outbox            Shared platform         Append in caller
                                                  transaction
  -----------------------------------------------------------------------

### 1.5 Module dependency rules

-   Sale orchestrates; Inventory and Ledger do not import Sale.
-   Inventory exposes narrow transaction-aware ports for
    allocate/consume, reserve/release, and return-to-quarantine.
-   Ledger exposes typed source operations for sale, refund, settlement,
    and reversal. Sale never builds arbitrary journal lines.
-   All transaction-aware ports use the caller's transaction context;
    none opens or commits an independent transaction.
-   No HTTP self-calls between modules in the modular monolith.
-   Avoid cyclic imports. Put small interfaces at the
    application/consuming boundary or shared platform transaction
    contract.
-   Cross-module reads use owner services/projections; do not duplicate
    ownership tables.

## 2. Scope, tenancy, branch, warehouse, and actor security

### 2.1 Scope

-   Sale documents are organization- and branch-scoped.
-   Warehouse allocation is warehouse-scoped and must belong to the
    authorized selling branch by default.
-   Cross-branch fulfillment is not implicitly allowed. It requires a
    separate workflow, permission, Inventory contract, and
    source/destination authorization.
-   Price books may be organization-wide with explicit branch overrides.
-   Customer identity may be organization-wide; that does not grant
    access to every branch's sales.
-   Every tenant-owned repository method requires `organizationID`;
    branch authorization is enforced separately.
-   Never trust `organization_id`, `cashier_id`, `branch_id`,
    `warehouse_id`, approver, or permission fields from request JSON.

### 2.2 Trusted request context

Use existing middleware/context conventions for authenticated user,
organization, effective branch scope, permissions, request/trace IDs,
and operation context. Cashier comes from authenticated context.
Validate requested branch against effective scope and validate warehouse
against organization, branch, and active status.

### 2.3 IDOR protections

-   Detail, invoice, return, refund, cash session, export,
    payment-event, and job reads scope by organization and authorized
    branch before returning data.
-   Apply tenant predicates in SQL, not only in-memory after loading by
    UUID.
-   Preserve existing `NOT_FOUND`/branch-scope error behavior.
-   Use composite tenant foreign keys where practical: child
    `(organization_id, sale_id)` references parent
    `(organization_id, id)`.
-   Exports/jobs re-check authorization and scope at execution and
    download time.
-   Never allow a line, payment, invoice, return, refund, or allocation
    to reference a parent from another organization.

## 3. Domain entities and lifecycle

  -------------------------------------------------------------------------------------
  Entity                  Purpose                               Lifecycle
  ----------------------- ------------------------------------- -----------------------
  Cart                    Optional saved draft                  Disposable,
                                                                owner/branch scoped

  Sale                    Completed commercial transaction      Posted immutable

  Sale item               Product/quantity/price/discount/tax   Immutable after posting
                          snapshot                              

  Stock allocation        Sold quantity mapped to               Immutable
                          batch/movement/cost                   

  Payment intent          Remote payment operation              Durable state machine

  Tender                  Actual cash/offline/provider          Append-only
                          settlement fact                       

  Payment event           Provider/internal settlement evidence Append-only,
                                                                deduplicated

  Invoice                 Number and immutable rendering facts  Immutable once issued

  Return                  Request/approval/posting document     State machine; posted
                                                                immutable

  Return item             Original sale allocation and          Immutable once posted
                          disposition                           

  Refund                  Amount and destination to return      State machine;
                                                                settlement immutable

  Prescription evidence   Minimal dispensing authorization      Restricted
                          evidence                              

  Price book/version      Effective-dated selling policy        Versioned

  Promotion/redemption    Discount policy/quota use             Redemption immutable

  Cash session            Drawer accountability                 Open → closing → closed

  Sale operation          Durable command identity/result       Unique and durable

  Sale transition         Append-only state history             Immutable
  -------------------------------------------------------------------------------------

### 3.1 Independent state dimensions

Do not overload one `status` or `payment_status`.

-   `sale_status`: processing/completed/void_pending/voided/reversed as
    actually needed.
-   `payment_status`: unpaid/partially_paid/paid; derive from settlement
    facts or maintain a reconciled projection.
-   `invoice_status`: not_issued/issued/voided where legally allowed.
-   `return_status`: requested/approved/rejected/posted/cancelled per
    return document.
-   `refund_status`:
    pending/submitted/pending_reconciliation/settled/failed/reversed.
-   `cash_session_status`: open/closing/closed; exceptional reopen only
    through controlled audited workflow.
-   Provider status is not Sale status.

A mutable aggregate `payment_status` is not the settlement authority.

### 3.2 Lifecycle rules

-   Cart/quote is not a stock promise or guaranteed final price.
-   A sale is completed only after stock, tender/credit, journal, audit,
    and invoice rules succeed.
-   Completed sale lines/totals are immutable.
-   Never delete a completed sale.
-   Do not provide generic PATCH semantics for posted financial facts.
    Corrections use return, credit note, void/reversal, or another
    approved compensating document.
-   Void is allowed only within configured policy and only if downstream
    settlement/period rules permit it.
-   Return approval and physical stock disposition are separate
    decisions.
-   External refund settlement may be asynchronous.
-   Cash close is immutable; correction uses separately audited
    adjustment/reopen policy.

## 4. Database contract

This is a logical schema contract, not final migration SQL. Reuse the
repository's actual `BaseModel`, UUID, JSON, money, quantity, timestamp,
and soft-delete conventions. Do not invent duplicate shared types.

### 4.1 Common rules

-   PostgreSQL is authoritative.
-   Tenant-owned rows carry `organization_id`; branch-owned rows carry
    `branch_id`.
-   Store UTC `timestamptz`; business-day rules use configured branch
    timezone.
-   No `float32`/`float64` for price, money, tax, cost, discount,
    refund, tender, or cash.
-   Quantity precision must align with Medicine/Inventory pack
    conversion; never silently truncate fractional units.
-   JSONB snapshots are bounded, validated, and schema-versioned.
-   Use FK, unique/check constraints, and partial indexes for critical
    invariants.
-   No hard delete of posted financial, inventory-linked, or audit
    evidence.
-   Soft delete is for eligible master/configuration records, not posted
    sales/tenders/invoices/movements/journals.
-   Currency minor-unit convention, rounding mode/stage, unit-conversion
    ratio, and timestamps must be explicit and tested.

### 4.2 Recommended tables

  ----------------------------------------------------------------------------------------------------
  Table                          Logical columns                          Constraints/indexes
  ------------------------------ ---------------------------------------- ----------------------------
  `sale_carts`                   org, branch, owner_user_id, expires_at,  Bounded draft; owner/branch
                                 content JSONB, version                   index

  `sales`                        org, branch, warehouse, customer?,       U(org,operation_id);
                                 cashier, cash_session?, operation_id,    I(org,branch,sold_at,id);
                                 invoice_id?, sale_type, states,          nonnegative/consistent
                                 currency, subtotal, discount, tax,       totals
                                 charges, total, sold_at,                 
                                 policy_snapshot, version                 

  `sale_items`                   org, sale_id, line_no, medicine/variant, U(org,sale,line_no);
                                 unit, entered/base quantity, conversion  positive quantity; tenant FK
                                 snapshot, unit price, discount, tax,     
                                 line total, product/unit/tax snapshots   

  `sale_stock_allocations`       org, sale/item IDs, warehouse, batch,    Positive; unique
                                 movement and cost-allocation refs,        movement/allocation source;
                                 base quantity, cost facts/currency        immutable

  `sale_payment_intents`         org, branch, operation_id, sale?,        Unique command;
                                 provider/account, provider key,          provider/account/reference
                                 amount/currency, state, expiry, external uniqueness
                                 ref                                      

  `sale_tenders`                 org, branch, sale, intent?, tender_no,   Append-only
                                 method, amount/currency, received_at,    
                                 settlement ref, actor                    

  `sale_payment_events`          org, intent, provider/account/event ID,  Unique
                                 event type, amount/currency,             provider/account/event;
                                 occurred_at, sanitized evidence          append-only

  `sale_invoices`                org, branch, sale, series, fiscal_key,   Unique invoice identity;
                                 number, issued_at, currency, immutable   U(org,sale_id) if one
                                 snapshot/hash                            invoice

  `sale_invoice_counters`        org, branch, series, fiscal_key,         Composite PK; short
                                 next_number                              PostgreSQL row lock

  `sale_returns`                 org, branch, original sale,              U(org,operation_id); scoped
                                 operation_id, return_no, status, reason, return number
                                 actors/times, credit total, policy       
                                 snapshot                                 

  `sale_return_items`            org, return, original item/allocation,   Positive; cumulative bound
                                 batch, quantity, disposition, credit,    under original allocation
                                 original cost                            lock

  `sale_refunds`                 org, branch, sale, return?, original     Unique command; cumulative
                                 tender?, operation_id,                   refund bound
                                 amount/currency/method/status/provider   
                                 ref                                      

  `sale_prescription_evidence`   org, branch, sale, reviewer,             Restricted access; minimal
                                 reviewed_at,                             clinical data
                                 prescription/prescriber/evidence refs,   
                                 policy version, decision                 

  `sale_price_books`             org, name/code, branch?, currency,       Explicit org/branch
                                 active, version                          uniqueness

  `sale_price_versions`          org, book, variant, unit,                Non-overlap per
                                 price/currency, valid_from/to, version   book/variant/unit

  `sale_promotions`              org, code, kind, typed bounded config,   U(org,normalized_code)
                                 validity, active, quota, per-customer    
                                 limit, version                           

  `sale_promotion_redemptions`   org, promotion, sale, customer?,         Unique redemption; quota
                                 quantity, discount, redeemed_at          locked

  `sale_cash_sessions`           org, branch, cashier/drawer, opening     One active session per
                                 amount/time, state,                      configured drawer policy
                                 counted/expected/variance, close         
                                 actor/time, version                      

  `sale_cash_movements`          org, branch, session, type,              Append-only
                                 amount/currency, source ref, actor, time 

  `sale_transitions`             org, branch, aggregate, dimension,       Append-only
                                 from/to, action, actor, reason, policy   
                                 version, operation_id, time              

  `sale_operations`              org, branch, operation_id, type,         Unique operation and scoped
                                 key/request hashes, state, result        key
                                 resource/response, timestamps            
  ----------------------------------------------------------------------------------------------------

### 4.3 Database invariants

-   Composite tenant FKs for child-to-parent references.
-   Warehouse must belong to same organization and authorized branch.
-   Variant/medicine must be same-tenant and compatible.
-   One sale line may allocate across multiple batches; do not put one
    `batch_id` on the line as the only allocation.
-   Sum of allocation quantities equals required base quantity under
    exact conversion rules.
-   Tender currency matches sale currency unless a separately designed
    FX flow exists.
-   Invoice uniqueness and sequence enforced by PostgreSQL.
-   Return items reference original sale item and allocation in same
    tenant.
-   Refund references original sale and, where applicable, original
    tender/return.
-   Use partial unique indexes for active cash sessions and appropriate
    non-overlap constraints for price versions.
-   `CHECK` constraints reject negative/invalid values; cross-row
    cumulative limits need locks/conditional writes plus constraints.

### 4.4 Exact money and quantity

The initial Sale model sketch uses `float64` for money and `int` for
quantity.
These are not production-safe accounting representations.

-   `common.Money` is currently a string alias, not a validated arithmetic
    or currency policy. Do not treat it as a complete money abstraction.
-   The current Ledger and Inventory value contracts use signed 64-bit
    minor-unit amounts and support BDT only for posting/value movements.
    Keep the initial Sale release BDT-only; do not accept another
    functional currency until all participating modules agree on
    currency exponent, conversion, and rounding.
-   Do not use a Sale-only money type or convert through floating point.
    If per-unit selling price needs precision finer than currency minor
    units, define the shared fixed-scale representation and rounding
    before the Sale migration.
-   `inventory.Quantity` is `int64` in canonical indivisible base units.
    Sale must resolve package conversion through the Medicine owner and
    reject fractional base quantities until a shared fractional-unit
    contract exists.
-   Inventory valuation is authoritative for consumed cost. Preserve
    exact cost-layer references and values it returns; never recompute
    COGS from purchase price or current catalog data.
-   Define rounding mode and stage (line vs invoice) with Ledger and the
    tax owner, and lock the decision with golden tests.
-   Persist applied totals and policy versions. Historical invoices are
    never recalculated with today's rules.

### 4.5 Existing schema and migration compatibility

Migration `000009_sale_customer` already creates Sale and Customer
tables. Its `sales`/`sale_items`/`sale_payments`/`sales_returns`/
`sales_return_items`/`coupons`/`offers`/`coupon_redemptions` schema is a
legacy baseline, not a production-ready implementation of this
contract. For example, a legacy line stores one `batch_id`, several
child rows lack their own organization scope, posted facts are
soft-deletable and can be removed through cascading parent deletes, and
mutable update triggers do not provide append-only financial evidence.
The `customers` table also contains `credit_limit`
and `current_due`; these fields must not become a second receivable
authority alongside Ledger.

-   Never edit or renumber an already-applied migration. Inspect each
    deployed database's migration version/dirty state and actual schema
    before choosing a remediation.
-   Do not create duplicate `sales` or `sale_items` tables. Choose and
    document one safe expand/backfill/cutover strategy for each legacy
    fact: additive columns/tables with an explicit compatibility period,
    or a controlled data migration to the normalized contract.
-   Reconcile existing rows, soft-deleted history, invoice uniqueness,
    parent/child tenant scope, and old Inventory batch references. The
    normalized Inventory batch model uses
    `inventory_legacy_batch_links`; preserve traceability when mapping
    historical sale lines.
-   Make the authoritative write path singular during rollout. Do not
    dual-write legacy and new Sale/Customer credit totals without a
    transactional reconciliation design.
-   Treat old rows as historical facts: do not silently rewrite
    amounts, quantities, returns, or inventory effects to make them fit
    the new model. Report unmappable records and resolve them before
    cutover.
-   The migration tree currently contains both
    `000023_backup_module` and `000023_ledger_contract`. Resolve the
    duplicate version against deployed migration history and the
    migration runner before adding or applying any Sale migration. Do
    not blindly rename or renumber either file.

## 5. Pricing, tax, discounts, and promotions

### 5.1 Server-owned quote calculation

`POST /sales/pos/calculate` is preview only. It validates scope/product
eligibility, resolves price book/version, package conversion, effective
tax rule/version, permitted promotions/coupons, discount order,
rounding, and returns `quoted_at`, optional expiry, and policy versions.

Client totals (`subtotal`, `tax_amount`, `total_amount`, `cost_price`,
`discount_amount`, `change_amount`) are never trusted.

### 5.2 Price policy

-   Organization price book may have explicit branch override.
-   Price versions are effective-dated and immutable once used.
-   Define deterministic precedence (branch-specific before organization
    default, if that is the approved rule).
-   Price override requires `sales:manage-prices`, reason, and policy
    evidence.
-   Reject zero/negative prices unless an explicit
    complimentary/free-sample policy permits them.
-   Historical sale retains price version and product description
    snapshots.

### 5.3 Discounts and promotion

-   Distinguish line discount, order discount, coupon, promotion, and
    manual override.
-   Default to no stacking unless policy explicitly allows it.
-   Enforce max discount amount/percentage and item restrictions
    server-side.
-   Override requires `sales:override-discount`, reason, actor; dual
    approval where configured.
-   Cashier cannot self-approve when policy requires independent
    supervisor.
-   Evaluate validity in configured branch timezone.
-   Quota/per-customer limits require row lock or atomic conditional
    update; pre-check alone is race-prone.
-   Redemption is in the same transaction as sale; failed checkout
    consumes no quota.
-   Redemption history is immutable; correction is compensating.

### 5.4 Tax

-   Use effective-dated MasterData/tax-owner rules.
-   Snapshot rule ID/version, jurisdiction, rate, calculation basis,
    taxable base, rounding, and applied amount.
-   Exemption/zero-rating requires explicit policy and evidence where
    required.
-   Never trust client-provided tax rate.
-   Later tax changes do not change posted invoices.

## 6. Inventory integration: stock, batch, FEFO, movements, cost

### 6.1 Inventory is the only stock authority

Sale must not directly decrement balances, update batch quantities,
insert movements, mutate cost layers, or treat cached availability as
checkout guarantee. Sale calls a transaction-aware Inventory port.

### 6.2 FEFO

-   Inventory selects eligible batches by FEFO (earliest expiry first),
    with deterministic tie-breakers.
-   Eligibility includes tenant, warehouse, variant, batch active/recall
    state, expiry, condition, available balance, and medicine rules.
-   Expired/recalled/quarantined/damaged/inactive batches are excluded.
-   Expiry boundary and branch business date semantics must be explicit.
-   If the whole quantity cannot be allocated, fail atomically unless
    partial fulfillment is explicitly supported.
-   Do not use `SKIP LOCKED` to skip an earlier-expiry locked batch in
    strict FEFO checkout; it changes allocation semantics.
-   Client cannot choose batches in ordinary checkout. Manual batch
    override requires separate permission, reason, and audit.
-   Revalidate eligibility at consume time.

### 6.3 Concurrency and balance invariants

-   Critical reads use PostgreSQL primary.
-   Inventory locks balances in canonical order (e.g. org → warehouse →
    variant → batch).
-   Conditional updates and DB constraints prevent negative quantity.
-   For absent balance rows, unique-key upsert followed by locking is
    required; locking a nonexistent row does not protect creation races.
-   Quantity unit/conversion precision must match Medicine/Inventory.
-   Inventory movement source includes unique Sale operation identity.
-   Any Sale/Ledger/Audit/Outbox failure rolls back consumption in the
    same transaction.

### 6.4 Reservation

-   Cash/offline checkout may allocate+consume in one transaction.
-   Remote gateway flow needs durable reservation with expiry before
    provider call.
-   Reservation is not consumption and does not create COGS or completed
    Sale.
-   Create/extend/consume/release is idempotent and transaction-aware.
-   Expiry worker acts on durable DB state; Redis TTL alone cannot
    release stock.
-   Late provider success after expiry must not force negative stock;
    reconcile/refund/manual resolve.
-   Never delete reservation history.

### 6.5 Cost / COGS

-   Inventory returns actual consumed cost from exact cost layers.
-   Sale stores allocation/cost references; Inventory remains valuation
    authority.
-   Ledger receives net revenue, tax, tender/AR, and actual consumed
    cost facts.
-   COGS uses recorded allocation cost, never current Purchase price or
    client `cost_price`.
-   A sale line consuming multiple batches/cost layers has multiple
    allocation rows.
-   Movement and cost-layer consumption reconcile to allocation totals.
-   Cost correction uses Inventory/Ledger correction workflow, not
    SaleItem mutation.

### 6.6 Return disposition

-   Approval does not mean sellable stock.
-   Default medicine return disposition is quarantine/inspection.
-   Inventory decides quarantine, damaged, expired, recalled, or
    restockable condition.
-   Sale submits original allocation, batch, quantity, and requested
    disposition; Inventory validates physical action.
-   Returned quantity cannot exceed unreturned original allocation.
-   Non-restockable return never increases sellable balance.
-   Later release to sellable stock is a separately authorized Inventory
    transition.

## 7. Ledger integration and financial invariants

### 7.1 Ledger authority

Ledger owns account mapping, journal construction, balance validation,
periods, source deduplication, immutability, reversal, and subledger
exposure. Sale supplies typed economic facts only.

The existing low-level `AutoPost` command accepts journal lines with
account IDs; it is not by itself the Sale posting contract. Before Sale
checkout is enabled, Ledger must provide a Sale-facing operation that
accepts validated revenue, tax, tender/receivable, and Inventory-returned
COGS facts, resolves approved account mappings and dimensions inside
Ledger, enforces policy/version/period rules, and deduplicates by stable
Sale operation identity. Sale must not choose arbitrary journal accounts
or construct journal lines.

### 7.2 Conceptual checkout posting

Depending on configured chart and tax treatment, conceptual entries may
include:

-   Debit cash/bank/clearing for settled tender.
-   Debit AR for approved credit portion.
-   Credit sales revenue for net revenue.
-   Credit output tax payable for tax.
-   Debit COGS for actual consumed cost.
-   Credit inventory asset for actual consumed cost.

These are conceptual directions, not hardcoded Sale account mappings.
Ledger's account-policy adapter chooses configured accounts/dimensions.
Split tender maps each method to its correct settlement/clearing
account. Change due is not revenue.

### 7.3 Credit sale

-   Customer is required.
-   Validate customer active/credit eligibility.
-   Ledger must expose a transaction-aware customer credit decision
    using its subledger lock/update primitives before approving new
    credit. Those low-level primitives alone are not a complete credit
    policy.
-   Check credit limit, outstanding balance, overdue restrictions, and
    branch policy in the same transaction.
-   Never rely on cached balance or unlocked pre-check.
-   Reconcile the legacy `customers.credit_limit` and `customers.current_due`
    fields before cutover; Ledger is the only receivable authority and
    Sale/Customer must not maintain a competing balance.
-   Credit creates AR; later collection is a separate durable operation.
-   Sale does not maintain a second customer receivable balance.

### 7.4 Source identity

Use stable source identity: `source_module=sale`, source type
(checkout/refund/return), source ID, operation ID, and source revision
where required. Ledger enforces unique source posting. A retry after
timeout queries the original source; it never posts a second journal.

### 7.5 Return/refund/void accounting

-   Return/credit note adjusts revenue/tax and inventory/COGS based on
    accepted disposition.
-   Refund settlement differs from return approval.
-   Cash refund may be synchronous only when cash movement and Ledger
    posting share the transaction.
-   External refund is a durable provider workflow outside transaction.
-   Ledger reverses journals with original reference, date, and reason;
    no direct journal mutation.
-   Closed period cannot be bypassed; follow Ledger's approved
    accounting-date/credit-note policy.
-   Void after settlement/period close may require return/credit note
    plus refund/reversal, not deletion.

## 8. Transactions and orchestration

### 8.1 Cash/offline checkout: one atomic transaction

1.  Middleware authenticates, authorizes `sales:create`, enforces POS
    rate policy, attaches request/trace IDs, and applies edge
    idempotency optimization.
2.  Service derives actor/org/branch from trusted context and validates
    server-resolved warehouse.
3.  Begin PostgreSQL transaction and claim durable operation identity.
4.  If completed with same normalized request hash, return stored
    result. Same key/different hash returns `IDEMPOTENCY_KEY_CONFLICT`.
5.  Lock/validate price and promotion versions; validate
    customer/credit.
6.  Recompute all totals server-side.
7.  Validate prescription/controlled medicine policy.
8.  Inventory locks eligible balances, allocates FEFO, consumes stock,
    appends movements, consumes cost layers, and returns exact
    allocation/cost facts using the same transaction.
9.  Persist Sale and immutable SaleItem snapshots.
10. Persist tender facts; validate tender/change/credit allocation.
11. Ledger posts revenue/tax/COGS/cash/AR from typed facts in the same
    transaction.
12. Allocate invoice number under short PostgreSQL counter lock; persist
    immutable invoice snapshot.
13. Append mandatory business audit.
14. Append versioned outbox events.
15. Persist operation result and commit.
16. Return committed Sale/invoice.

Any failure before commit rolls everything back. Do not call
notification, PDF renderer, or payment gateway while stock locks are
held.

### 8.2 Transaction requirements

-   Transaction-aware ports use caller's transaction/connection; no
    independent commits.
-   Use `db.WithTx` for the outer Sale command and pass its transaction
    context to every repository and owner-module port. The platform
    `db.FromCtx` resolves the active transaction, and nested `WithTx`
    calls use savepoints; verify rollback from the outer transaction
    undoes every participant's writes.
-   Do not infer mandatory audit from middleware `AuditSink.Record`:
    the current PostgreSQL sink returns no error and swallows write
    failures. Sale needs a separate error-returning audit append port
    that participates in the caller's transaction.
-   No durable Outbox implementation currently exists. Implement and
    prove the transaction-bound append/delivery path before any Sale
    mutation can be considered production-ready.
-   Do not hold DB locks across network calls.
-   Read Committed plus explicit locks/conditional updates is sufficient
    for defined invariants; Serializable only where explicit protection
    is impossible.
-   Lock in deterministic canonical order.
-   Retry only known transient DB failures with bounded attempts/jitter
    and durable idempotency.
-   Never blindly retry provider calls.
-   Measure transaction duration; avoid remote calls, PDF, exports, or
    per-line network reads inside.

### 8.3 Remote payment state machine

1.  Persist payment intent and operation identity.
2.  Reserve stock through Inventory and persist reservation
    reference/expiry.
3.  Commit intent/reservation.
4.  Call provider outside transaction with stable provider idempotency
    key.
5.  Verify callback/poll: signature, merchant/account, amount, currency,
    reference, replay window.
6.  Deduplicate provider event by provider/account/event ID.
7.  Persist verified event and transition intent.
8.  Finalization transaction locks intent/reservation and verifies
    amount, currency, expiry, and state.
9.  Consume reservation, persist Sale/tender/invoice, post Ledger,
    audit/outbox, complete intent atomically.
10. Unknown outcome becomes `pending_reconciliation`; no blind second
    charge.
11. Late success without fulfillable reservation enters controlled
    refund/manual resolution; never force negative stock.
12. Workers resume from durable DB state/leases, not only Redis queue.

### 8.4 Failure matrix

  -----------------------------------------------------------------------
  Failure                             Required behavior
  ----------------------------------- -----------------------------------
  Insufficient stock                  No Sale/tender/journal/invoice

  Inventory movement failure          Entire checkout rolls back

  Ledger posting failure              Sale and stock roll back

  Audit failure                       Mandatory operation rolls back

  Outbox insert failure               Operation rolls back; no silent
                                      event loss

  DB lost before commit               Resolve via durable operation
                                      identity; do not assume

  DB lost after commit/before         Replay same key returns original
  response                            result

  Redis unavailable                   PostgreSQL idempotency still
                                      prevents duplicate effect

  Notification/PDF failure            Sale remains committed; retry
                                      delivery/render

  Provider timeout                    Pending reconciliation; no blind
                                      second charge

  Duplicate provider callback         Unique event suppresses duplicate
                                      effect

  Reservation expiry/payment race     Lock intent/reservation; one
                                      transition wins

  Concurrent last-unit sale           Exactly one succeeds

  Ledger period closes concurrently   Ledger lock/policy decides; no
                                      closed-period journal
  -----------------------------------------------------------------------

## 9. Durable idempotency

-   Require durable command identity for checkout, return
    request/approval, refund, void, collection, cash close, price
    mutation, and promotion mutation.
-   Scope key by organization, actor/client scope, operation type/route
    according to platform convention.
-   Persist canonical normalized request hash.
-   Same key+same hash returns original result; same key+different hash
    returns existing conflict error.
-   Store operation state (`in_progress`, `completed`,
    retryable/terminal failure as existing convention permits).
-   Operation record and business writes share transaction where
    possible.
-   Unique DB constraints protect concurrent duplicate operation
    creation.
-   Crash recovery must be possible from durable state.
-   Retention must exceed realistic retry/dispute needs; Redis TTL is
    not enough.
-   Do not store sensitive provider payloads in replay response.
-   Existing Redis middleware may reduce duplicate load but is not the
    correctness boundary if it can fail open, lose keys, or expire.

## 10. Invoice numbering and immutability

-   Use PostgreSQL counter keyed by organization, branch, series, fiscal
    key.
-   Lock/update briefly within invoice transaction; unique constraint is
    final guard.
-   Never use Redis, process memory, timestamps, or random values for
    legally sequential numbers.
-   Decide whether failed transactions consume numbers under applicable
    legal rules. Do not promise gapless sequence unless required and
    safely implemented.
-   Invoice snapshot includes seller/branch identity, buyer where
    required, lines, quantities, price, discount, tax, currency, totals,
    date/time, and policy references.
-   Issued invoice is immutable; corrections use credit
    note/void/reissue policy.
-   PDF/receipt rendering reads immutable snapshot and may be async.

## 11. Returns, refunds, and voids

### 11.1 Return request

-   Reference original Sale and original item/allocation IDs.
-   Validate tenant/branch scope.
-   Lock original items/allocations in deterministic order.
-   Compute accepted/posted return quantity from authoritative records.
-   Cumulative return cannot exceed original sold quantity less prior
    accepted returns.
-   Validate return window, reason, medicine eligibility, batch
    traceability, evidence, and regulated-product restrictions.
-   Request alone does not restock or refund.
-   Require idempotency.

### 11.2 Approval/posting

-   Separate requester and approver where policy requires.
-   Approve/reject requires `sales_returns:approve`; rejection reason
    required.
-   Determine disposition per allocation: quarantine, damaged, expired,
    recalled, or approved restock.
-   Inventory owns physical movement/condition.
-   Return posting, Inventory movement, cost handling, Ledger
    credit-note posting, audit, and outbox are atomic when synchronous.
-   External refund can remain separate durable settlement.
-   Preserve original batch/allocation and original cost.

### 11.3 Refund

-   Refund cannot exceed refundable settled original tender after prior
    refunds/credits.
-   Credit-sale balance adjustment is not cash refund; use Ledger
    AR/credit-note flow.
-   External refund uses stable provider idempotency and verified
    callback/reconciliation.
-   Refund destination is constrained by original tender/policy;
    arbitrary destination is a fraud risk.
-   Cash refund requires cash session and cash movement.
-   Ambiguous provider refund remains pending reconciliation, not
    settled.
-   Refund facts are append-only/state-controlled; never overwrite
    amount/provider evidence.

### 11.4 Void

-   Void is compensating workflow, not DELETE.
-   Validate time window, actor, branch, cash session, settlement,
    invoice/legal rules, stock, and Ledger period.
-   Inventory performs compensating movement/return disposition.
-   Settled tender requires refund/reversal policy.
-   Ledger reverses or posts credit note.
-   Preserve original Sale, invoice, tender, movement, and journal.
-   Require reason, strong permission, and dual control where
    configured.

## 12. Cash sessions

-   Session is branch/drawer/cashier scoped by configured policy.
-   Opening float, cash tenders, cash refunds, cash-in/out, paid-in/out,
    and closing count are distinct facts.
-   Expected cash is derived server-side from immutable cash movements;
    document change, cash drop, and refund treatment.
-   Cashier cannot edit tender history to force reconciliation.
-   Close records counted amount, actor, timestamp, evidence/reason.
-   Variance is calculated server-side; threshold breach triggers
    supervisor review.
-   Enforce one active session per configured drawer policy with DB
    uniqueness/locking.
-   Closing locks session against new cash tenders.
-   Reopen only through separately permissioned, reasoned, audited
    exception or use adjustment document.
-   Session close/reconciliation uses Ledger typed facts, never direct
    GL writes.

## 13. Prescription and controlled-medicine controls

-   Enforcement comes from applicable jurisdiction/organization policy;
    do not assume all or no products require prescriptions.
-   Determine prescription-required/controlled categories from
    Medicine/MasterData policy.
-   Validate evidence before dispensing.
-   Record reviewer, decision, time, policy version, secure evidence
    reference.
-   Avoid storing diagnosis, patient history, or unnecessary clinical
    text.
-   Restrict evidence reads with dedicated permission and branch scope;
    redact from ordinary DTOs, logs, exports, and analytics.
-   Controlled medicine may require pharmacist/reviewer authorization,
    quantity limits, patient/prescriber reference, and additional audit
    based on policy.
-   Prevent cashier self-approval when separate reviewer required.
-   Use opaque evidence storage keys; no arbitrary URL/path.
-   Audit access to restricted evidence.

## 14. API contract

Follow existing router versioning, response envelope, AppError,
validation, pagination, and OpenAPI conventions. The paths below are
proposed contract routes; no Sale routes are currently mounted. Register
them only after the applicable transaction and authorization gates pass.

### 14.1 POS and sales

| Method & path | Permission (`module:action`) | `RateLimit` policy key | Behavior |
|---|---|---|---|
| `POST /api/v1/sales/pos/lookup` | `medicines:view` | `auth_read` | Barcode/SKU lookup; availability is a hint, not a stock promise. |
| `POST /api/v1/sales/pos/calculate` | `sales:create` | `auth_read` | Preview quote; no stock promise. |
| `POST /api/v1/sales/pos/checkout` | `sales:create` | `pos_checkout` | Atomic checkout; require `Idempotency-Key`. |
| `POST /api/v1/sales` | `sales:create` | `auth_write` | Non-POS/wholesale command if supported. |
| `GET /api/v1/sales` | `sales:view` | `auth_read` | Cursor list with scoped branch/cashier/customer/status/date filters. |
| `GET /api/v1/sales/{id}` | `sales:view` | `auth_read` | Scoped detail, lines, allocations, tenders, and invoice status. |
| `POST /api/v1/sales/{id}/void` | `sales:void` | `auth_write` | Compensating void; reason and idempotency required. |
| `POST /api/v1/sales/{id}/refund` | `sales:refund` | `auth_write` | Refund workflow; idempotency required. |
| `POST /api/v1/sales/{id}/payments` | `sales:collect-payment` | `auth_write` | Collect credit/remaining balance. |
| `GET /api/v1/sales/{id}/payments` | `sales:view` | `auth_read` | Tender/settlement history. |
| `GET /api/v1/sales/{id}/invoice` | `sales:view` | `auth_read` | Immutable invoice payload. |
| `GET /api/v1/sales/{id}/invoice/pdf` | `sales:view` | `auth_read` | Render/stream immutable snapshot. |
| `POST /api/v1/sales/exports` | `sales:export` | `export` | Asynchronous scoped export job. |

### 14.2 Returns

| Method & path | Permission (`module:action`) | `RateLimit` policy key | Behavior |
|---|---|---|---|
| `POST /api/v1/sales/{sale_id}/returns` | `sales_returns:create` | `auth_write` | Idempotent return request. |
| `GET /api/v1/sales/{sale_id}/returns` | `sales_returns:view` | `auth_read` | Returns for scoped sale. |
| `GET /api/v1/sales-returns` | `sales_returns:view` | `auth_read` | Cursor list. |
| `GET /api/v1/sales-returns/{id}` | `sales_returns:view` | `auth_read` | Scoped detail. |
| `POST /api/v1/sales-returns/{id}/approve` | `sales_returns:approve` | `auth_write` | Idempotent approve/post. |
| `POST /api/v1/sales-returns/{id}/reject` | `sales_returns:reject` | `auth_write` | Idempotent rejection; reason required. |

### 14.3 Price/promotion

| Method & path | Permission (`module:action`) | `RateLimit` policy key | Behavior |
|---|---|---|---|
| `GET /api/v1/sales/price-books` | `sales:view` or a dedicated read action | `auth_read` | Scoped books. |
| `POST /api/v1/sales/price-books` | `sales:manage-prices` | `auth_write` | Create book. |
| `POST /api/v1/sales/price-books/{id}/prices` | `sales:manage-prices` | `auth_write` | Add effective-dated price. |
| `GET /api/v1/sales/promotions` | `coupons:view` | `auth_read` | Promotion list. |
| `POST /api/v1/sales/promotions` | `coupons:create` | `auth_write` | Create typed promotion. |
| `PATCH /api/v1/sales/promotions/{id}` | `coupons:update` | `auth_write` | Versioned update/deactivate. |

### 14.4 Cash session and provider webhook

| Method & path | Authentication/permission | `RateLimit` policy key | Behavior |
|---|---|---|---|
| `POST /api/v1/sales/cash-sessions/open` | `sales:open-cash` | `auth_write` | Idempotent open; new permission must be seeded. |
| `GET /api/v1/sales/cash-sessions/current` | `sales:view` plus own/session scope | `auth_read` | Current session. |
| `POST /api/v1/sales/cash-sessions/{id}/close` | `sales:close-cash` | `auth_write` | Count/variance/close; new permission must be seeded. |
| `POST /api/v1/sales/payment-providers/{provider}/webhook` | Provider signature and allow-listed account | Provider-specific configured policy | Signature, replay, and event dedupe; configure a dedicated limiter before enabling. |

Webhook must not rely on generic user auth. Verify signature,
merchant/account, amount, currency, reference, replay window, and
request size. Never expose provider secrets/raw sensitive payloads.

The policy values above are the exact keys passed to
`mw.RateLimit`; documentation aliases such as `rate_auth_read` are not
configuration keys. Add a configured provider-specific policy before
mounting a webhook route.

### 14.5 API rules

-   Organization and actor come from trusted context. Do not trust
    `cashier_id`.
-   Do not accept trusted cost, stock balance, final totals, invoice
    number, arbitrary batch IDs, journal IDs, or GL account IDs.
-   Use allow-listed DTOs; never bind directly to persistence models.
-   Bound line count, quantity, coupon count, note length, and body
    size.
-   Use cursor/keyset pagination; avoid unbounded offsets.
-   Parameterize search; allow-list sort/filter fields.
-   Re-check authorization on invoice/PDF/export download.
-   Preserve existing errors: `NOT_FOUND`, `BRANCH_SCOPE_DENIED`,
    `ALREADY_EXISTS`, `IDEMPOTENCY_KEY_CONFLICT`, `CONFLICT`,
    `INSUFFICIENT_STOCK`, `BATCH_EXPIRED`, `BATCH_INACTIVE`, and Ledger
    credit/period errors.

## 15. RBAC and separation of duties

Use existing RBAC capabilities. Do not hardcode role names or build a
parallel role engine.

| Permission (`module:action`) | Purpose | Seed state |
|---|---|---|
| `sales:create` | POS checkout/create | Already seeded. |
| `sales:view` | Read permitted sales | Already seeded. |
| `sales:void` | Void/compensate sale | Already seeded. |
| `sales:refund` | Initiate/approve refund per policy | Already seeded. |
| `sales:export` | Export scoped sales | Already seeded. |
| `sales:manage-prices` | Manage price books/versions | Seeded; grant only after policy approval. |
| `sales:override-discount` | Override discount bounds with reason | Seeded; grant only after policy approval. |
| `sales:collect-payment` | Collect credit/remaining balance | Seeded; grant only after policy approval. |
| `sales:open-cash` | Open cashier session | Seeded; grant only after policy approval. |
| `sales:close-cash` | Close cashier session | Seeded; grant only after policy approval. |
| `sales:cash-adjust` | Exceptional cash adjustment | Seeded; grant only after policy approval. |
| `sales:cash-reopen` | Exceptional reopen if policy supports | Seeded; grant only after policy approval. |
| `sales:prescription-view` | View restricted prescription evidence | Seeded; grant only after policy approval. |
| `sales:prescription-review` | Review regulated dispensing evidence | Seeded; grant only after policy approval. |
| `sales_returns:create` | Request return | Already seeded. |
| `sales_returns:view` | View returns | Already seeded. |
| `sales_returns:approve` | Approve/post return | Already seeded. |
| `sales_returns:reject` | Reject return | Already seeded. |
| `coupons:view` | View promotions | Already seeded. |
| `coupons:create` | Create promotions | Already seeded. |
| `coupons:update` | Update/deactivate promotions | Already seeded. |
| `coupons:delete` | Deactivate only; retain redemption history | Already seeded. |

Use the existing RBAC seed/provider and exact `module:action` convention;
services must not check role-name strings. The RBAC seeder retires the
legacy `sales:update` permission and removes its role grants; no generic
update for posted sales is supported. If draft editing is later added,
scope it to a disposable draft/cart workflow and do not use it to mutate
posted evidence.

-   Refund, void, discount override, price management, prescription
    review, and cash adjustment are separate capabilities.
-   Apply dual control for high-risk actions where configured.
-   Do not grant refunds, financial posting, or unrestricted exports to
    every administrator.
-   Workers use narrow service identities/capabilities.
-   Exports require `sales:export` plus relevant source permissions.

## 16. Middleware/platform integration

Use the actual existing chain/order, expected responsibilities:

1.  Request/trace context.
2.  Recovery and safe panic handling.
3.  Security headers/CORS as configured.
4.  Authentication.
5.  Tenant context.
6.  Branch scope.
7.  Protected/RBAC authorization.
8.  Rate limit (`pos_checkout` for checkout; use the configured policy
    key passed to `mw.RateLimit`).
9.  Idempotency middleware (edge optimization).
10. Request validation/body limits.
11. Handler → application service.
12. Transaction-aware orchestration.
13. Middleware audit plus mandatory domain audit.
14. Existing AppError/response mapping.

-   Middleware audit does not replace domain evidence (batch allocation,
    price override, reviewer, refund, cash variance).
-   No alternate unprotected route group.
-   Webhooks use provider signature authentication.
-   Do not log raw checkout/payment/prescription request bodies.
-   Never leak SQL, provider secrets, or cross-tenant resource
    existence.

## 17. Audit, outbox, events

### 17.1 Mandatory business audit

Same-transaction audit for sale completion/void, price/discount override
and approver, material promotion redemption, batch allocation/manual
exception, prescription review decision, return transitions, refund
settlement/reversal, provider reconciliation decision, cash
open/close/variance/adjustment/reopen, invoice issue/void/credit-note,
and sensitive export/download where required.

Audit includes actor, org, branch, action, resource, operation ID,
timestamp, reason, policy version, and safe before/after metadata. Never
copy full prescription/payment payloads into audit. If mandatory audit
append fails, operation rolls back.

The existing middleware audit sink is best-effort and cannot satisfy
this rule: `Record` logs write errors without returning them. Use a
transaction-aware, error-returning domain append operation; do not
change generic middleware audit semantics to force business atomicity.

### 17.2 Outbox

-   Insert outbox envelope in same transaction as
    Sale/Inventory/Ledger/Audit facts.
-   Include event ID/type/schema version, org, branch, aggregate ID,
    operation ID, occurred_at, minimal committed facts/references.
-   Exclude secrets, card data, raw prescription, unnecessary PII.
-   Publish after commit.
-   Consumers are at-least-once and deduplicate by event ID.
-   Worker failure cannot undo Sale.
-   Outbox state/retries are durable in PostgreSQL; Redis may wake
    workers but is not authority.

Recommended events: - `SaleCompleted.v1` - `SaleVoided.v1` -
`SaleReturnRequested.v1` - `SaleReturnApproved.v1` -
`SaleReturnPosted.v1` - `SaleRefundRequested.v1` -
`SaleRefundSettled.v1` - `SalePaymentSettled.v1` -
`SaleInvoiceIssued.v1` - `SaleCashSessionClosed.v1` -
`SaleCashVarianceDetected.v1`

## 18. Redis and cache policy

Allowed: short-lived catalog/search projection, quote cache revalidated
at checkout, saved-cart cache only if persistence contract permits loss,
rate limiting, queue wake-up, non-authoritative versioned read cache.

Never sole authority for: stock/reservation, FEFO, cost layer, durable
idempotency, settlement, invoice counter, promotion quota, credit
exposure, cash session, posted Sale/Invoice/Return/Refund.

Checkout validates critical facts against PostgreSQL primary and
owner-module transaction-aware services.

## 19. Background jobs

  -----------------------------------------------------------------------
  Job                     Durable source          Behavior
  ----------------------- ----------------------- -----------------------
  Payment reconciliation  Payment intents/events  Resolve unknown
                                                  outcomes; never blindly
                                                  charge

  Reservation expiry      Inventory reservation   Release via Inventory
                                                  port

  Late payment resolution Provider event +        Refund/manual
                          reservation             resolution if no safe
                                                  fulfillment

  Invoice rendering       Immutable invoice       Generate PDF/receipt
                          snapshot                

  Abandoned cart cleanup  Persisted carts         Expire disposable
                                                  drafts

  Cash discrepancy review Closed session/variance Notify supervisor;
                                                  preserve facts

  Export generation       Export job              Scoped extraction,
                                                  CSV/XLSX safety,
                                                  private artifact

  Promotion expiry        Promotion validity      Deactivate projection;
                                                  retain redemptions

  Reconciliation          Sale/Inventory/Ledger   Detect differences;
                          references              never auto-edit history
  -----------------------------------------------------------------------

Jobs need durable state, leases/claims, bounded retries,
dead-letter/manual review, idempotent handlers, and metrics. Queue loss
must not lose work.

## 20. Security threat model

  -----------------------------------------------------------------------
  Threat                              Mitigation
  ----------------------------------- -----------------------------------
  Cross-tenant IDOR                   Tenant predicates, composite FKs,
                                      scoped repositories

  Cross-branch read                   Effective branch scope on every
                                      endpoint/export

  Warehouse injection                 Server-resolve and validate branch
                                      relationship

  Price tampering                     Server recomputation and policy
                                      snapshot

  Discount fraud                      Permission, bounds, reason, dual
                                      control

  Coupon race                         Quota lock/atomic update and unique
                                      redemption

  Duplicate checkout                  PostgreSQL idempotency and unique
                                      operation/source IDs

  Redis fail-open                     Domain durable deduplication

  Overselling                         Inventory locks, conditional
                                      updates, nonnegative constraints

  Batch substitution                  Inventory-owned FEFO and immutable
                                      allocation

  Expired/recalled stock              Revalidate eligibility at consume

  False gateway callback              Signature, merchant binding,
                                      replay/event dedupe

  Amount manipulation                 Compare event to persisted intent
                                      amount/currency/account

  Refund fraud                        Original tender binding, cumulative
                                      lock, separate permission

  Return fraud                        Original allocation lock,
                                      cumulative bound, disposition

  Invoice manipulation                PostgreSQL counter, immutable
                                      snapshot, unique constraint

  Prescription leakage                Restricted evidence, minimal
                                      storage, redacted logs/exports

  Cash drawer fraud                   Immutable movements, session state
                                      machine, variance review

  CSV formula injection               Escape cells beginning with `=`,
                                      `+`, `-`, `@`, tabs/newlines

  Mass assignment                     Explicit DTO mapping

  SQL injection                       Parameterized query and
                                      allow-listed sort/filter

  DoS                                 POS rate limit, body/line caps,
                                      bounded DB work

  Replay                              Durable command/provider event
                                      uniqueness

  Insider misuse                      Separation of duties, immutable
                                      audit, alerts

  Sensitive logs                      Redaction and safe structured
                                      fields
  -----------------------------------------------------------------------

## 21. Observability and operations

### 21.1 Logs

Include request_id, trace_id, operation_id, module/action, route
template, outcome, safe org/branch context, sale ID, transaction
duration, retry count, and lock-wait category. Never log secrets, card
data, raw provider payload, or clinical text. Do not put arbitrary IDs
in Prometheus labels.

### 21.2 Metrics

-   checkout count/success/failure and latency;
-   allocation latency/conflicts;
-   insufficient stock, expired/inactive batch rejections;
-   negative stock invariant violations (target zero);
-   transaction duration/lock wait/deadlock;
-   idempotency replay/conflict;
-   duplicate Ledger source posting attempts;
-   invoice counter conflicts;
-   pending payment age/reconciliation lag;
-   provider success/failure/unknown outcomes;
-   return/refund volume and failures;
-   cumulative return/refund bound rejections;
-   cash variance/unresolved age;
-   outbox oldest pending age/consumer lag;
-   Sale↔Inventory and Sale↔Ledger reconciliation differences;
-   prescription review failures/unauthorized evidence access without
    clinical labels.

### 21.3 Alerts

Alert on any committed negative stock/unbalanced journal, duplicate
source posting, audit/outbox append failure, growing pending payment
age, provider signature failure spike, outbox backlog, repeated scope
denials, invoice uniqueness conflict, unexplained cash variance, and
Sale/Inventory/Ledger mismatch.

SLOs must be established by load testing the deployed stack. Do not
infer throughput from local hardware.

## 22. Performance

-   POS checkout is the latency-sensitive hot path.
-   Bulk-load catalog, price, tax, and policy data; avoid per-line N+1.
-   Keep cash/offline transaction bounded and short.
-   Limit locks to selected balances, promotion quota, credit exposure,
    invoice counter, and source rows.
-   No organization-wide locks or remote calls while stock is locked.
-   Cursor/keyset pagination for sales, returns, tenders, and events.
-   Index `(organization_id, branch_id, sold_at, id)` and common scoped
    filters.
-   Batch-fetch details; do not load all payments/returns for a sales
    page.
-   Render PDFs and exports asynchronously.
-   Load-test hot SKU/last-unit contention; do not weaken consistency to
    improve benchmark.

## 23. Testing strategy

### Unit

Money/quantity/rounding, price precedence/effective dates, discount
stacking/caps, tax snapshots, promotion limits, FEFO eligibility
contract, state machines, return/refund bounds, prescription policy,
invoice snapshot.

### PostgreSQL repository

Tenant/branch FKs, operation uniqueness, idempotency, invoice counter
concurrency, active cash-session uniqueness, price non-overlap,
promotion quota race, return/refund races, transaction participation, no
hard-delete of posted facts.

### Integration/concurrency

-   Two sales for last unit: exactly one succeeds.
-   Two checkouts competing for same FEFO batch.
-   Duplicate checkout after Redis flush: one sale, one stock movement,
    one journal.
-   Same key/different body: conflict.
-   Timeout after commit/before response: replay returns original
    result.
-   Ledger failure rolls Sale/Inventory back.
-   Audit failure rolls all required writes back.
-   Outbox failure rolls operation back.
-   Promotion quota and credit limit never exceed limits under
    concurrency.
-   Concurrent return/refund never exceeds original allocation/settled
    tender.
-   Duplicate provider callback has one effect.
-   Provider timeout does not create second charge.
-   Reservation expiry/payment race resolves safely.
-   Quarantine return does not increase sellable stock.
-   Cost allocation reconciles to Inventory cost layers.
-   Closed Ledger period rejects posting.
-   Cross-tenant/branch IDs rejected.
-   Prescription evidence absent from ordinary DTO/logs.
-   Cash close races safely with new tender.

### Property-based

Debits=credits, allocated quantity=sale base quantity, stock never
negative, return/refund bounds, documented total calculation, command
replay creates one effect, reversal preserves evidence.

### Failure injection

PostgreSQL disconnect before/after commit, deadlock/serialization,
Inventory/Ledger/Audit/Outbox failures, Redis loss/fail-open, worker
crash, provider timeout/duplicate/out-of-order callback, renderer crash,
export upload failure.

## 24. Reconciliation

Scheduled reconciliation compares: - Sale allocation quantity ↔
Inventory movements; - allocation cost ↔ Inventory cost-layer
consumption; - Sale revenue/tax/COGS facts ↔ Ledger source postings; -
tender facts ↔ Ledger cash/clearing/AR; - refund facts ↔ Ledger/provider
settlement; - invoice ↔ completed Sale; - cash expected ↔ immutable cash
movements/tenders; - promotion redemptions ↔ quota use.

Reconciliation detects and raises exceptions. It never silently rewrites
posted Sale, Inventory, or Ledger history. Corrections use owning-module
compensating operations with audit/approval.

## 25. Migration and rollout safety

-   Inspect actual migration loader, duplicate versions, migration
    history, and every deployed environment before adding Sale
    migrations.
-   Never renumber/rewrite an applied migration.
-   Resolve existing duplicate migration-version collisions from
    deployed `schema_migrations`, dirty flags, checksums if available,
    and actual schema objects.
-   This checkout already has two distinct migration pairs numbered
    `000023` and migration files through `000026`. Resolve the
    `000023` collision using the deployed schema and migration runner
    before adding a Sale migration. A fresh-database migration test is
    required after resolution; do not assume checked-in filename
    ordering proves deployed compatibility.
-   Migration `000009_sale_customer` already owns the legacy Sale table
    names. Implement the compatibility/cutover strategy in §4.5 before
    creating any normalized Sale tables or writing production data.
-   Inventory migration `000026_inventory_core` adds the normalized
    inventory tables while retaining legacy tables; Sale must use the
    normalized Inventory port and preserve historical links rather than
    reading/writing legacy `inventory_batches` as a new authority.
-   Use forward-only additive production migrations for financial/stock
    facts.
-   Destructive down migrations are not appropriate for posted financial
    evidence.
-   Add indexes/constraints in safe phases; use concurrent index
    creation where supported by migration tooling.
-   Backfill in bounded resumable batches with verification.
-   Use `NOT VALID` then validate constraints where appropriate.
-   Do not deploy code that assumes schema before migrations are
    complete.
-   Do not expose Sale mutation routes until transaction-aware
    Inventory/Ledger/audit/Outbox/PostgreSQL-idempotency prerequisites
    are implemented and proven. A scheduled future task is not a
    production readiness gate.
-   Verify empty DB, populated DB, interrupted migration, and
    application rollback compatibility.

## 26. Implementation sequence

Implement and release in dependency order. A phase is not complete
because a model or interface exists; its stated persistence,
concurrency, and rollback tests must pass before the next dependent
mutation is enabled.

| Phase | Work | Required exit gate |
|---|---|---|
| 0. Repository and rollout gate | Resolve duplicate migration version `000023`; inspect deployed migration history/schema; inventory legacy Sale/Customer rows and `000026` Inventory links; decide initial BDT/cash-only checkout and legal invoice policy. | Approved compatibility plan; fresh and deployed-schema migration strategy proven; no unresolved data ownership or legal-numbering blocker. |
| 1. Shared atomic-write prerequisites | Add the PostgreSQL durable Sale operation record/claim/replay path with mandatory `Idempotency-Key` (or explicitly reuse an existing platform store; do not build a parallel generic framework); implement an error-returning transaction-bound business-audit port and durable Outbox; add Inventory FEFO lock/consume, reservation, return, and exact cost-allocation ports; add Ledger-owned typed Sale posting/account mapping. Preserve `db.WithTx` caller context across all ports. | PostgreSQL integration test proves one outer transaction rolls back Sale, Inventory, Ledger, audit, and Outbox together. Redis outage, duplicate operation, and same-key/different-payload cases have deterministic outcomes. |
| 2. Schema bridge and core Sale records | Add forward-only schema after migration numbering is repaired; define immutable sales, lines, multi-batch allocations, tenders, invoice, and transition history; backfill/bridge legacy data without dual authoritative writes. | Constraints enforce tenant/branch scope, operation uniqueness, quantity/totals, and immutability; legacy row reconciliation is measured and repeatable. |
| 3. Read and calculation path | Implement scoped lookup/list/detail and server-side BDT quote calculation using owner-module product/unit and tax facts. Do not promise stock or price beyond documented quote validity. Start with no promotions unless their transactional quota path is ready. | DTO/scope tests and exact-money/tax golden tests; no client-supplied total, cost, batch, or cashier is trusted. |
| 4. First POS checkout slice | Enable only locally settled BDT checkout for products cleared by dispensing policy. In one outer transaction claim the command, recalculate, lock/consume FEFO stock, save immutable Sale/allocation/tender/invoice facts, post Ledger facts, append mandatory audit and Outbox, store replay result, then commit. | PostgreSQL concurrency tests prove last-unit safety, one stock movement and journal per operation, same-key replay/conflict behavior, and rollback on each participant failure. |
| 5. Price, promotion, credit, and collection | Add versioned effective prices/promotions and quota locks; then Ledger-backed customer credit/AR and later collections. Do not use legacy `current_due` as an authority. | Promotion and credit-limit concurrency tests; audited override/dual-control tests; one ledger authority for receivables. |
| 6. Returns, refunds, and voids | Add allocation-bounded returns, Inventory disposition/quarantine, credit notes/reversals, tender-bound refunds, and separately durable external-refund reconciliation. | Concurrent cumulative-bound tests; immutable history; no refund is represented as a stock return or vice versa. |
| 7. Remote payment providers | Add payment intent/event verification, Inventory reservations, stable provider idempotency, callback dedupe, finalization and late-success reconciliation. Provider calls remain outside DB transactions. | Duplicate/out-of-order callback, timeout, reservation-expiry race, and ambiguous-outcome tests with no blind second charge or forced negative stock. |
| 8. Cash sessions and regulated dispensing | Add drawer open/movements/close/variance and restricted prescription review/evidence only after policy, permissions, storage, and audit requirements are approved. | Cash reconciliation and concurrent close tests; restricted-access/redaction and separation-of-duties tests. |
| 9. Reads, exports, and operations | Add invoice rendering from immutable snapshots, scoped asynchronous exports, reconciliation jobs, metrics, alerts, and runbooks. | Authorization rechecked at execution/download; reconciliation is exception-only and never rewrites posted history. |
| 10. Release gate | Exercise deployed-like migration, failure, security, and load scenarios; review runbooks and feature flags. | Signed release criteria; no Sale mutation enabled if any invariant or reconciliation alert is unresolved. |

## 27. Definition of done

-   [ ] Sale does not directly mutate Inventory or Ledger tables.
-   [ ] Inventory has transaction-aware FEFO consume/reserve/return
    ports.
-   [ ] Ledger has transaction-aware typed Sale/COGS/tax/tender/AR
    posting.
-   [ ] Audit and Outbox append in the same transaction.
-   [ ] PostgreSQL idempotency works when Redis is unavailable.
-   [ ] Exact money/quantity align with Ledger, Inventory, Medicine,
    MasterData.
-   [ ] FEFO is deterministic and concurrency-safe.
-   [ ] Concurrent checkout cannot create negative stock.
-   [ ] Every allocation references exact batch, movement, and cost
    facts.
-   [ ] COGS uses Inventory allocation cost, not current Purchase price.
-   [ ] Ledger journal is balanced and source-deduplicated.
-   [ ] Posted sale/invoice/tender/return/refund facts are immutable.
-   [ ] Return/refund cumulative bounds are race-safe.
-   [ ] Returned medicine enters quarantine by default.
-   [ ] Provider workflows are durable and reconcilable.
-   [ ] Invoice numbering is PostgreSQL-backed and unique.
-   [ ] Cash session close reconciles immutable movement facts.
-   [ ] Prescription evidence is minimized, restricted, and audited.
-   [ ] Tenant/branch scope applies to every endpoint/export/job.
-   [ ] Existing RBAC is used; no hardcoded role checks.
-   [ ] No notification/PDF/provider call inside stock-lock transaction.
-   [ ] Reconciliation detects Sale/Inventory/Ledger differences.
-   [ ] Tests cover duplicate command, Redis loss, commit timeout,
    last-unit sale, refund race, rollback.
-   [ ] Migration history/deployed schema audited before migrations.
-   [ ] Load tests establish realistic checkout SLOs.

## 28. Review of initial Sale model sketch

The initial Sale model sketch is a useful domain outline, not an
implemented or production-ready schema.

  ------------------------------------------------------------------------
  Supplied design         Risk                    Required direction
  ----------------------- ----------------------- ------------------------
  `float64` money         Floating-point          Shared exact
                          accounting errors       Money/Decimal

  `int` quantity          Platform-size and no    Shared quantity
                          package precision       scale/base-unit
                                                  conversion

  `SaleItem.CostPrice`    Sale is not cost        Inventory allocation
                          authority               cost reference/result

  One `BatchID` on line   One line may span FEFO  Separate allocation rows
                          batches                 

  `BatchNo` as truth      Inventory owns batch    Display snapshot +
                          identity                Inventory FK

  Free-form `PackType`    Unvalidated units       Medicine unit/variant
                                                  conversion

  Mutable paid/change     Split tender/refunds    Append-only
  amounts                 not represented         tender/refund facts

  Single mutable payment  Multiple                Derive from settlement
  status                  tender/provider states  facts
                          conflated               

  Generic string status   Lifecycle dimensions    Separate typed states
                          conflated               

  PATCH completed sale    Rewrites financial      Draft-only edit;
                          evidence                compensating document

  Payment/coupon deletion Erases evidence         Reverse/deactivate;
                                                  retain history

  Return with refund      Return, credit, refund  Separate state machines
  fields                  are separate            

  `Restockable bool`      Sale cannot determine   Inventory
                          sellable stock          disposition/inspection

  Coupon `UsageCount`     Race/drift risk         Transactional quota
                                                  lock/atomic redemption

  Untyped `JSONMap` offer Unsafe/unvalidated      Typed, versioned,
  config                  config                  bounded rules

  Client-supplied cashier Actor spoofing          Auth context

  Optional warehouse      Ambiguous stock source  Resolve/validate
                                                  required warehouse for
                                                  stock-consuming sale

  Direct invoice number   Race/sequence/legal     Invoice table +
                          issues                  PostgreSQL counter

  Plain prescription      Sensitive evidence      Restricted evidence
  string                  unclear                 reference/policy
                                                  snapshot
  ------------------------------------------------------------------------

Do not create duplicates if equivalent owner-module structures already
exist. Do not duplicate Inventory
batch/balance/movement/reservation/cost tables, Customer credit balance,
or Ledger journal model.

## 29. Decisions still required before full production scope

Repository implementation facts and technical blockers are recorded in
§0.1. The following are business/accounting/compliance decisions; record
each approved policy and version before implementing the dependent
feature. Do not invent defaults in code.

| Decision | Required owner/input | Blocks |
|---|---|---|
| Tax calculation basis, exemption evidence, line/invoice rounding and effective-date rules | Accounting/tax owner; coordinate with MasterData | Quote and checkout |
| Invoice series, fiscal-year reset, legal fields, void/credit-note and number-gap rules for the operating jurisdiction | Finance/legal | Invoice issuance and void |
| Allowed initial tender methods, change handling, refund destinations, and settlement evidence | Finance/operations | Checkout and refund |
| Price-book precedence, price override limits, discount stacking/caps, and supervisor approval policy | Product/finance | Price management and promotions |
| Return window, restock eligibility evidence, refund timing, and regulated-product exceptions | Operations/compliance; coordinate with Inventory | Return/refund |
| Customer credit-limit source, overdue policy, customer/subledger identity mapping, and legacy balance cutover | Finance; coordinate with Ledger and Customer | Credit sale and collection |
| Prescription/controlled-medicine jurisdiction rules, reviewer qualifications, evidence retention, and access policy | Pharmacy/compliance/legal | Regulated dispensing |
| Drawer identity, one-active-session scope, opening float, cash drop/paid-in/out, variance threshold, and reopen process | Finance/operations | Cash sessions |
| Branch business timezone and business-date cutoff | Operations/accounting | FEFO expiry boundary, pricing, tax and invoice date |
| Actual deployed migration versions, dirty state, schema, and legacy row quality | Database/release owner | Any Sale migration or production write |

Until a decision is approved, keep its feature out of the enabled Sale
scope. The first checkout slice is limited to the explicit constraints
in §0.1; it does not imply that returns, credit, promotions, remote
payments, cash sessions, or prescription workflows are ready.

------------------------------------------------------------------------

**End of contract.**
