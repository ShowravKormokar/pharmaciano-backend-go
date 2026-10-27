# Pharmaciano:ERP --- Supplier Module Contract

**File:** `SUPPLIER_CONTRACT.md`\
**Module:** `internal/modules/supplier`\
**Status:** Architecture contract; reconcile the current schema before
implementation.\
**Architecture:** Go modular monolith, Gin, PostgreSQL, existing native
RBAC, shared middleware, OpenAPI 3.1 contract-first.

**Sources:** Astra `answer_in_markdown.md` (Supplier module and
cross-module ownership), `MASTERDATA_IMPLEMENTATION_CONTRACT.md`, the
supplied Supplier model/API draft, and documented migration
dependencies.

> This document distinguishes existing facts from target design. It does
> not claim that proposed banking encryption, qualification tables,
> outbox or business-audit ports already exist. Inspect source and
> migrations before coding.

## 1. Executive decisions

1.  **Supplier is not a MasterData resource.** `suppliers` already
    exists in `000005_master_data.up.sql`, but migration-file grouping
    does not determine module ownership. MasterData owns its eleven
    approved reference resources; Supplier owns supplier identity and
    purchasing eligibility.
2.  **Move Go model ownership to `internal/modules/supplier/model.go`.**
    Do not keep competing `Supplier` structs/repositories in MasterData
    and Supplier. Reuse the existing `suppliers` table; do not create a
    second supplier table.
3.  **Do not edit applied migrations.** If the existing schema lacks
    required tenant columns or constraints, add a forward migration only
    after examining deployed schema, existing data, and inbound foreign
    keys.
4.  **Target supplier identity is organization-scoped.** The supplier
    profile may be visible across that organization's branches, while
    purchase history, documents and payables obey their own
    branch/ledger access rules. Never expose one tenant's suppliers to
    unrelated organizations.
5.  **Tenant scope is a blocking schema question.** The MasterData
    contract documents the existing table's
    code/name/contact/address/license/tax and balance fields,
    soft-delete timestamps, partial unique code index and trigram name
    index; it does not establish that `organization_id` exists. Do not
    pretend the current table is tenant-safe.
6.  **`OpeningBalance` and `CurrentBalance` are not Supplier-owned
    financial truth.** Opening balances must be represented by Ledger
    opening entries; current payables must come from Ledger's
    authoritative read contract. Never independently update supplier
    balances.
7.  **Credit limit and credit period are commercial terms, not
    balances.** Purchase/Ledger enforce exposure and payment
    obligations. Supplier must not maintain a parallel payable
    calculation.
8.  **Separate contacts, branch eligibility, qualifications and bank
    destinations only when their lifecycle/security requires it.**
    Sensitive bank data must never be casually embedded in the base
    supplier row or returned by ordinary detail endpoints.
9.  **Supplier is distinct from Manufacturer and Brand.** A supplier may
    distribute many manufacturers' products; a manufacturer can also be
    a supplier, but that relationship is explicit, not assumed.
10. **Use existing auth, native RBAC, rate limiter, idempotency, audit,
    response/error, transaction and observability infrastructure.**
    Verify actual configured rate-policy names; draft labels are not
    proof they exist.
11. **No Redis cache by default.** PostgreSQL is authoritative. Do not
    introduce a new queue, event bus, encryption framework or generic
    abstraction without verified shared infrastructure and an approved
    need.
12. **Purchase history and payables delegate to Purchase and Ledger.**
    Supplier must not mutate purchase documents, query private module
    tables ad hoc, or write journal entries.
13. **Archive/inactivate; do not physically delete.** Purchases and
    inventory rows reference suppliers. Preserve identity for historical
    purchases, returns, reconciliation and settlement. Inactive
    suppliers cannot be used for new commitments but must remain
    available for existing obligations.
14. **Bank destination changes are high-risk financial operations.**
    Require a separate permission, verification, versioned records,
    strict redaction and audit. A changed destination must not silently
    redirect existing payments.

## 2. Purpose and boundaries

### Supplier owns

-   Organization-scoped supplier/trading-counterparty identity, code,
    name, status and business contact information.
-   Commercial terms such as default payment period and optional credit
    limit.
-   Qualification/license evidence and purchasing eligibility where
    required.
-   Optional branch purchasing eligibility.
-   Versioned payment-destination references and verification status,
    when banking is implemented.
-   Supplier-specific business-change evidence under the approved audit
    contract.

### Supplier does not own

-   Manufacturer, brand or medicine catalog identity.
-   Purchase orders, receipts, purchase invoices, returns, payments or
    receiving quantities.
-   Accounts payable balances, journal entries, opening balance
    postings, allocations or reconciliation.
-   Inventory batches, stock quantity, expiry or valuation.
-   Payment execution/provider credentials.
-   Cross-tenant global supplier directory.

## 3. Scope model

``` text
Organization
  ├── Supplier profile (shared across its branches)
  │     ├── Contacts
  │     ├── Qualifications / licenses
  │     ├── Optional branch eligibility
  │     └── Restricted, versioned payment destinations
  ├── Branch A: purchases and receipts
  ├── Branch B: purchases and receipts
  └── Ledger: authoritative supplier payables/subledger projections
```

**Visibility:** supplier identity may be organization-wide. Purchase
history is filtered by the caller's effective branch scope; payables
require ledger permission and ledger scope. Supplier-view permission
does not grant access to every purchase or payable.

**Tenant rule:** derive `organization_id` from trusted authenticated
context, not JSON, query parameters or client-controlled headers. Every
tenant-scoped repository method must include organization scope.
Cross-tenant references fail closed.

## 4. Existing schema and migration reconciliation

### 4.1 Established by project documents

-   `suppliers` was created in `migrations/000005_master_data.up.sql`.
-   Inbound FKs are documented from `inventory_batches.supplier_id`
    (`000007_inventory_batch.up.sql`, `ON DELETE SET NULL`) and
    `purchases.supplier_id` (`000008_purchase.up.sql`, `NOT NULL`,
    `ON DELETE RESTRICT`).
-   MasterData contract records Supplier as out of scope for MasterData
    APIs and states that its Go struct was removed from
    `masterdata/model.go`.
-   The existing table has soft-delete support, a partial unique index
    on non-deleted `code`, and a trigram index on `name`, according to
    the migration review.
-   The supplied model includes `OpeningBalance` and `CurrentBalance`;
    these must not remain authoritative mutable financial state.

### 4.2 Required discovery before implementation

Inspect the complete supplier DDL, all later migrations and inbound FKs;
`common.BaseModel`/`AuditColumns`; current MasterData models;
organization/branch scope helpers; Purchase and Ledger modules; native
RBAC constants and permission seeds; actual rate limiter keys;
idempotency/audit behavior; response/error DTOs; and split OpenAPI
conventions. Check existing supplier rows and whether safe organization
ownership can be established.

### 4.3 Migration policy

-   Never modify `000005` or another applied migration.
-   If `organization_id` is missing, create a forward migration only
    after reviewing a safe backfill. **Do not assign existing supplier
    rows to an arbitrary organization.**
-   If ownership cannot be mapped reliably, stop and resolve data
    ownership before enabling tenant APIs.
-   Do not change globally unique supplier-code semantics to
    per-organization uniqueness without duplicate analysis and an
    explicit compatibility decision.
-   Do not create a second `supplier_profiles` table to avoid
    reconciling the existing `suppliers` table.
-   Preserve inbound FK targets and historical rows. Any balance-column
    retirement must wait until Ledger migration and reconciliation are
    ready.

## 5. Domain model and persistence target

  --------------------------------------------------------------------------------
  Entity                         Responsibility          Scope/lifecycle
  ------------------------------ ----------------------- -------------------------
  `Supplier`                     Counterparty identity   Organization;
                                 and basic commercial    active/inactive/archive
                                 terms                   

  `SupplierContact`              Named contact           Supplier child;
                                 channels/purpose        mutable/redacted

  `SupplierBranchEligibility`    Branches permitted to   Optional; audited
                                 make new commitments    

  `SupplierQualification`        License/approval and    Effective-dated,
                                 validity evidence       verified/revoked

  `SupplierPaymentDestination`   Restricted payment      Versioned,
                                 destination reference   verified/unverified

  `SupplierTermsRevision`        Optional immutable      Append-only
                                 terms history           

  Business audit evidence        Sensitive supplier      Use approved shared audit
                                 changes                 contract
  --------------------------------------------------------------------------------

### 5.1 Existing `suppliers` table

Retain existing columns when compatible with the deployed schema. The
logical target is: ID/base timestamps, approved tenant scope, code,
canonical name/legal name (choose one canonical stored field),
compatible contact/address fields, credit limit, credit period, active
state and soft-delete timestamp. Add `version` or creator/updater
columns only if the project convention and an approved migration support
them.

**Do not expose or maintain `opening_balance` or `current_balance` as
financial truth.** If the columns exist, plan their eventual retirement
separately; first reconcile non-zero values into Ledger opening entries
and verify them. Do not silently drop or ignore legacy balances.

### 5.2 Contacts

If the current single `contact_person`/`phone`/`email` fields meet the
first-release requirement, retain them temporarily rather than
duplicating truth. Add `supplier_contacts` only when multiple contacts,
role, preferred channel or contact history are real requirements. Use
tenant-safe supplier FK, index `(organization_id, supplier_id)`,
minimize personal data, and redact contact details from logs.

### 5.3 Branch eligibility

Optional. If all suppliers are available to every branch in the
organization, do not insert a row for every branch. Model the
default-all policy explicitly and introduce an allow-list only when the
business requires restrictions. Validate branch and supplier ownership
in the same organization. A client-supplied `branch_id` is a selector,
not authority.

### 5.4 Qualifications

If licenses gate procurement, store type, reference,
issuer/jurisdiction, issued/expiry dates, verification status,
verifier/time, revocation reason and private evidence reference. A
license number alone does not prove validity. Only an authorized
verification action can mark evidence verified. Use approved private
object storage and generated object keys; never store arbitrary
filesystem paths or public evidence URLs.

### 5.5 Payment destinations

Implement only with an approved tokenization or
encryption/key-management design: - Separate destination ID, supplier
ID, type, encrypted payload/token reference, key reference, masked
display fields, verification state, verifier/time, effective dates and
version. - Never return full bank details from ordinary supplier
list/detail endpoints. - New destinations start unverified and cannot be
used for payment. - Verification and activation are separate
transitions; a verified destination is not mutated in place---create a
new version. - Require a dedicated permission and, where possible,
separation of duties (requester cannot verify their own change). - If
the foundation lacks approved KMS/envelope encryption or provider
tokenization, defer this feature. Never store plaintext as a shortcut.

## 6. Model and DTO ownership

The Supplier struct shown in the earlier MasterData model should be
owned by:

``` text
internal/modules/supplier/model.go   // maps the existing suppliers table
internal/modules/supplier/dto.go     // explicit API request/response DTOs
internal/modules/supplier/mapper.go  // only if mapping is non-trivial
```

This is **not** a new database table. Do not keep two competing Supplier
models or repositories.

  -----------------------------------------------------------------------
  Supplied field                      Decision
  ----------------------------------- -----------------------------------
  `BaseModel`                         Reuse only if its fields/tags match
                                      actual SQL

  `Code`, `Name`                      Retain if matching existing
                                      columns; enforce approved
                                      uniqueness in PostgreSQL

  Contact/address fields              Retain compatible fields for first
                                      release; normalize into contacts
                                      only with an explicit migration/API
                                      plan

  `DrugLicenseNo`                     May be a display/reference field,
                                      but a production qualification
                                      requires status, issuer, expiry and
                                      verification evidence

  `TIN`                               Sensitive tax identifier; minimize
                                      exposure and redact logs

  `CreditLimit`                       Precise decimal, nullable if no
                                      limit configured; define currency
                                      and null semantics

  `CreditPeriodDays`                  Non-negative integer; material
                                      changes may need version/audit

  `OpeningBalance`                    Remove from Supplier API/model as
                                      financial truth; represent through
                                      Ledger

  `CurrentBalance`                    Remove from Supplier API/model as
                                      financial truth; read through
                                      Ledger

  `IsActive`                          Keep; inactive blocks new
                                      commitments but not
                                      history/settlement

  `OrganizationID`                    Required in the target if table is
                                      tenant-owned; derived from trusted
                                      context

  `Version`                           Add only if supported by
                                      schema/concurrency convention
  -----------------------------------------------------------------------

Do **not** write copy-ready model code until the exact SQL columns and
nullability are verified. A `db` tag that disagrees with deployed schema
will break queries.

DTOs should include separate `CreateSupplierRequest`,
`PatchSupplierRequest`, `SupplierListQuery`, redacted
`SupplierResponse`, and restricted banking/qualification DTOs only when
those features exist. Never bind JSON directly into a persistence model.
Do not accept organization ID, balances, actor IDs, verification state
or bank verification status from ordinary create/patch payloads. PATCH
must define omitted versus explicit-null semantics.

## 7. Business invariants

1.  Supplier code uniqueness follows the approved existing schema;
    PostgreSQL handles concurrent duplicates.
2.  Supplier is distinct from Manufacturer; never auto-merge them.
3.  Inactive or unqualified suppliers cannot be used for new commitments
    under policy.
4.  Deactivation preserves purchase history, returns, invoices,
    reconciliation and settlement access.
5.  Every purchase references a supplier in the same tenant and valid
    procurement scope.
6.  Supplier edits do not rewrite purchase snapshots or posted financial
    history.
7.  Credit terms cannot be negative; credit exposure is calculated by
    Purchase/Ledger using an agreed definition.
8.  A credit limit is not a payable balance. Define null/unlimited
    semantics and currency explicitly.
9.  Opening payable is a Ledger opening entry, not a Supplier field.
10. Current payable is a Ledger projection, not a writable Supplier
    column.
11. Bank destination cannot be used before verification.
12. Banking changes require dedicated permission and evidence of who
    requested, verified and activated the change.
13. Qualification is revalidated at purchase approval/commitment time,
    not just supplier creation.
14. No physical deletion when referenced; archive/inactivate and
    preserve historical identity.
15. Ordinary supplier responses never expose bank details, full TIN,
    private evidence URLs or ledger data by default.

## 8. API contract

Paths are relative to `/api/v1`. The seven supplied routes are retained.

  ------------------------------------------------------------------------------------------------
  Method & path                     Rate policy         Permission           Behavior
                                    (verify)                                 
  --------------------------------- ------------------- -------------------- ---------------------
  `POST /suppliers`                 `rate_auth_write`   `suppliers:create`   Create; existing
                                                                             idempotency plus DB
                                                                             uniqueness

  `GET /suppliers`                  `rate_auth_read`    `suppliers:view`     Bounded list: `q`,
                                                                             `city`, `is_active`

  `GET /suppliers/{id}`             `rate_auth_read`    `suppliers:view`     Redacted detail

  `PATCH /suppliers/{id}`           `rate_auth_write`   `suppliers:update`   Allow-listed changes,
                                                                             version check where
                                                                             supported

  `DELETE /suppliers/{id}`          `rate_auth_write`   `suppliers:delete`   Archive/inactivate;
                                                                             never physical delete

  `GET /suppliers/{id}/purchases`   `rate_auth_read`    `purchases:view`     Delegate to Purchase,
                                                                             branch-scope
                                                                             intersection

  `GET /suppliers/{id}/ledger`      `rate_auth_read`    `ledger:view`        Delegate to Ledger;
                                                                             no direct balance
                                                                             query
  ------------------------------------------------------------------------------------------------

Potential future routes, **not enabled until their contract is
implemented**: - `POST /suppliers/{id}/bank-details` --- dedicated
configured rate policy and `suppliers:manage-banking`. -
`GET /suppliers/{id}/bank-details` --- restricted masked fields only. -
`POST /suppliers/{id}/bank-details/{destination_id}/verify` ---
`suppliers:verify-banking`. - Qualification endpoints with approved
verification/evidence rules.

### 8.1 Search and pagination

-   Bound `q` length and parameterize every value.
-   `city` is an exact/normalized filter; never interpolate SQL.
-   `is_active` must distinguish omitted from `false`.
-   Use shared pagination helpers, bounded page size, stable ordering
    and an ID tie-breaker.
-   Allow-list sort fields; select only required columns; avoid N+1.
-   Existing trigram search on `suppliers.name` may be reused after
    checking actual operators/query plans.
-   Follow shared not-found policy for IDs outside the caller's tenant;
    do not leak cross-tenant existence.

### 8.2 Purchase history

Supplier handler calls a narrow Purchase read interface. Purchase owns
document state, snapshot semantics and branch authorization. Enforce
`purchases:view` and the caller's effective branch scope. Supplier
visibility does not grant access to every branch's purchases.

### 8.3 Ledger/payables

Supplier handler calls a narrow Ledger read interface. Ledger owns
payable calculation, currency, opening entries, allocations, aging and
reconciliation. Return an approved statement/projection with currency
and as-of time. If Ledger is not implemented, do not fake balances from
legacy supplier columns; keep the endpoint blocked behind an explicit
interface/contract.

## 9. Authorization and middleware

-   Reuse existing authentication/session and native RBAC
    `Protected`/route conventions.
-   Seed and verify `suppliers:create`, `suppliers:view`,
    `suppliers:update`, `suppliers:delete` in the canonical permission
    catalog.
-   Banking permissions such as `suppliers:manage-banking` and
    `suppliers:verify-banking` are proposed additions; seed role policy
    before enabling those routes.
-   Use existing request ID/recovery, auth, trusted scope, RBAC,
    configured rate limit, idempotency and request audit in the
    established order. Do not create parallel middleware.
-   Verify actual rate-policy keys; `rate_auth_read`/`rate_auth_write`
    in a draft are not proof the limiter registry uses those exact
    names.
-   Idempotency middleware does not replace durable database uniqueness
    or Purchase/Ledger command deduplication.
-   Each delegated endpoint must authorize in its owning module as well
    as at the route.
-   Request audit is not automatically business before/after audit.
    Confirm the implementation before claiming committed business audit.

## 10. Layering and transaction rules

Recommended minimum layout, following current conventions:

``` text
internal/modules/supplier/
  model.go
  dto.go
  mapper.go             # only if useful
  repository.go
  service.go
  handler.go
  routes.go
  supplier_test.go
```

-   **Model:** maps approved SQL columns; no HTTP logic or financial
    calculations.
-   **DTO:** explicit allow-listed requests and redacted response
    projections.
-   **Mapper:** pure conversion only; no DB access or business policy.
-   **Repository:** parameterized SQL, tenant scope, uniqueness and
    stable list queries.
-   **Service:** lifecycle, terms, qualification/eligibility, version
    checks and transaction orchestration.
-   **Handler:** binding, validation and shared response/error mapping
    only.
-   **Purchase/Ledger:** typed read/service interfaces; Supplier never
    writes their tables.

Every tenant-scoped repository method must receive trusted organization
scope (or resolve it using the project's established context pattern).
Reuse `common.BaseModel`, `db.WithTx`, `FromCtx` and `ReadFromCtx` only
after confirming their actual signatures and schema compatibility.

Transaction rules: - Create supplier plus initial contacts atomically if
contacts use a child table. - Use DB uniqueness for duplicate-code races
and map violations to stable conflicts. - PATCH uses optimistic
versioning if supported; otherwise use the established transaction-aware
read/update pattern. - Bank destination submission, verification and
activation are separate audited transitions. - Purchase revalidates
active state, qualifications and branch eligibility in its own
approval/commit transaction, preventing stale eligibility. - Do not lock
supplier rows for every purchase unless a real eligibility/credit
invariant requires it. - Supplier never posts journals. Outbox events
are written transactionally only if the approved shared outbox exists.

## 11. Financial correctness

### Opening balance

Legacy `opening_balance` must be reconciled into approved Ledger opening
payable entries, with currency, effective date, source/reference and
approval. Do not drop or ignore non-zero legacy values until
reconciliation is signed off. Supplier APIs must not independently edit
this balance.

### Current payable

Ledger is the authority. Any read projection must be explicitly derived,
reconcilable and timestamped; it is never write authority. Do not
calculate `opening_balance + purchases - payments` inside Supplier:
returns, credit notes, allocations, write-offs and reversals change the
semantics.

### Credit terms

Define currency, null semantics and which exposure counts (posted
invoices, received-uninvoiced goods, open orders, pending payments,
credits, overdue invoices). Purchase/Ledger owns this calculation and
approval policy. `credit_period_days` is a default term; each purchase
invoice snapshots the terms used to calculate its due date.

## 12. Security and privacy

  -----------------------------------------------------------------------
  Risk                                Control
  ----------------------------------- -----------------------------------
  Cross-tenant IDOR                   Tenant-scoped SQL/FKs and isolation
                                      tests on every route

  Mass assignment                     Explicit DTOs; reject tenant,
                                      balances, actor and verification
                                      fields

  Supplier impersonation/fraud        Controlled onboarding and optional
                                      verified qualifications

  Payment redirection fraud           Separate permission, versioned
                                      destination, verification and
                                      separation of duties

  Bank data leakage                   Approved encryption/tokenization,
                                      masked projections, no plaintext
                                      logs

  TIN/license leakage                 Minimize collection; redact logs
                                      and broad response DTOs

  Purchase history disclosure         Purchase permission plus
                                      branch-scope intersection

  Payables disclosure                 Ledger permission and Ledger-owned
                                      scope checks

  Inactive supplier reuse             Revalidate eligibility at purchase
                                      approval

  Duplicate code races                PostgreSQL unique constraint and
                                      stable conflict mapping

  Unsafe evidence uploads             Private storage, generated keys,
                                      size/type limits, safe access
                                      checks

  SQL injection                       Parameterized SQL and allow-listed
                                      sort/filter

  Resource exhaustion                 Rate limits, bounded
                                      search/pagination/uploads

  Historical corruption               Archive; preserve FKs and
                                      transaction snapshots

  Audit gaps                          Evidence for activation, terms,
                                      qualification and banking
                                      transitions

  Over-logging                        Never log bank data, full TIN, raw
                                      uploads, secrets or signed URLs
  -----------------------------------------------------------------------

This contract does not prescribe homemade encryption. If the shared
platform has no approved key management/tokenization, bank details must
not be stored in plaintext.

## 13. Audit and observability

Use existing logger, metrics, tracing and request audit. Confirm actual
guarantees. Business changes requiring evidence include supplier
creation/archive, activation, terms changes, qualification
verification/revocation, branch eligibility changes, and bank
destination submission/verification/retirement.

Never include plaintext bank data in audit before/after snapshots. Store
destination ID/version and masked metadata only. Request-level audit
does not prove a business transaction committed; if business audit is
not yet implemented, treat it as a dependency rather than inventing a
new subsystem inside Supplier.

Useful low-cardinality metrics: create/update/archive outcomes,
duplicate-code conflicts, purchases blocked for inactive/unqualified
supplier, delegated purchase/ledger latency, banking verification
outcomes (if implemented), DB latency and pool saturation. Do not use
supplier name/code/email/phone/TIN/bank account or raw IDs as metric
labels.

## 14. Performance and indexing

-   PostgreSQL is authoritative; no Redis cache initially.
-   Preserve existing code uniqueness semantics until reviewed.
-   Reuse the existing trigram index on `name` only if the query
    operators match; inspect `EXPLAIN (ANALYZE, BUFFERS)` before adding
    indexes.
-   Add missing indexes for tenant + active + stable ordering and FK
    child lookups only after reviewing actual query plans.
-   Keep list projections narrow; do not join purchases, ledger or
    banking data into normal supplier lists.
-   Bulk-load contacts if needed; avoid N+1.
-   Use owning Purchase pagination for large histories.
-   Do not cache payables, bank details or eligibility without a clear
    invalidation/revalidation policy.

## 15. OpenAPI and errors

-   Follow existing split OpenAPI 3.1 YAML, Redocly,
    `oapi-codegen`/strict Gin generation, `kin-openapi`, Scalar and CI
    drift checks where configured.
-   No Swagger annotations; do not hand-edit generated files.
-   Define DTO schemas, PATCH omission/null semantics, permissions, rate
    policy, pagination and idempotency.
-   Use the shared error package/envelope. Duplicate code maps to the
    existing `ALREADY_EXISTS`/`CONFLICT` contract; missing/out-of-scope
    rows follow shared not-found policy; archive/FK conflicts map to
    stable 409 without SQL details.
-   Do not add banking/qualification paths to OpenAPI until their
    security and implementation contracts are approved.

## 16. Testing strategy

**Unit:** DTO/PATCH validation, credit-term rules, qualification policy,
redacted DTOs, contact normalization.

**Repository/integration:** concurrent duplicate code, tenant isolation,
FK preservation after archive, partial-unique behavior, stale PATCH
conflict, safe forward migration/backfill, stable pagination and query
plans.

**Cross-module:** inactive/unqualified supplier blocks new purchase
commitments; historical returns/invoices/settlements remain accessible;
purchase history intersects branch scope; ledger endpoint delegates to
Ledger; Supplier never treats `current_balance` as authority; opening
balances reconcile through Ledger.

**Banking/qualification if implemented:** unverified destination cannot
be used; ordinary views never reveal sensitive fields; unauthorized
read/change/verify denied; separation-of-duties enforced where required;
logs/audit are masked; expired/revoked qualification blocks new
procurement when required.

Run actual repository gates (`gofmt`, `go test ./...`, `go vet ./...`,
migration checks, OpenAPI lint/generate/drift and integration tests
where configured). Do not claim a gate passed unless it ran.

## 17. Failure behavior

  -----------------------------------------------------------------------
  Failure                             Required behavior
  ----------------------------------- -----------------------------------
  PostgreSQL unavailable              Shared service error; do not
                                      acknowledge a write

  Duplicate code                      Stable conflict; no blind retry

  Ownership cannot be mapped during   Stop rollout and resolve ownership
  migration                           

  Ledger unavailable for payables     Return shared dependency error;
                                      never fall back to supplier balance
                                      columns

  Purchase service unavailable        Fail safely; do not bypass branch
                                      scope with direct SQL

  Supplier deactivated during         Purchase revalidates
  approval                            transactionally and blocks new
                                      commitment

  Qualification expired/revoked       Block new purchasing if required;
                                      retain historical access

  Bank verification fails             Destination remains unusable;
                                      record safe attempt metadata

  Stale PATCH                         Conflict/version error; do not
                                      overwrite newer state

  Evidence storage unavailable        Do not mark qualification verified

  Mandatory audit/outbox unavailable  Follow the approved shared
                                      contract; do not claim evidence
                                      committed when it did not
  -----------------------------------------------------------------------

## 18. Implementation sequence

  ------------------------------------------------------------------------------------
  Phase                     Work                               Exit criteria
  ------------------------- ---------------------------------- -----------------------
  0\. Discovery             Exact supplier DDL, inbound FKs,   Schema/ownership gaps
                            rows, BaseModel, RBAC, middleware, documented
                            limiter registry and module        
                            patterns                           

  1\. Migration decision    Tenant scope/backfill, code        No guessed tenant
                            uniqueness, legacy balances and    assignment; migration
                            contact strategy                   approved

  2\. Model/DTO             Move model ownership to            One owner, exact DB
                            `supplier/model.go`; create        tags, no mass
                            DTOs/mapper if useful              assignment

  3\. Repository            Scoped                             Isolation,
                            create/list/detail/patch/archive   duplicate-race and
                                                               pagination tests

  4\. Service/routes        Eligibility, commercial terms,     Supplied routes use
                            shared middleware and error        native RBAC and real
                            mapping                            rate keys

  5\. Purchase/Ledger       Narrow read interfaces             Correct
  delegation                                                   permissions/scope; no
                                                               direct financial writes

  6\. Financial             Convert legacy opening balances    Reconciled and no
  reconciliation            into Ledger entries                duplicate balance
                                                               authority

  7\.                       Only agreed requirements           Lifecycle/privacy tests
  Qualifications/contacts                                      

  8\. Banking               Only with approved                 No plaintext leakage;
                            encryption/tokenization and        verification/audit
                            approval policy                    complete

  9\. Hardening             OpenAPI, security/integration      Release gates pass
                            tests, query plans                 
  ------------------------------------------------------------------------------------

## 19. Definition of done

-   [ ] `Supplier` has one owner: `internal/modules/supplier`;
    MasterData does not expose Supplier CRUD.
-   [ ] Existing `suppliers` table and inbound FKs remain intact.
-   [ ] Tenant ownership is explicit and safely migrated; no
    cross-tenant access.
-   [ ] No second supplier table or competing balance source.
-   [ ] Opening/current balances are not writable Supplier truth; legacy
    values have a reconciliation plan.
-   [ ] Supplied CRUD endpoints use explicit DTOs, native RBAC,
    configured rate policies and shared response/error conventions.
-   [ ] Inactive suppliers cannot start new commitments; historical
    obligations remain accessible.
-   [ ] Purchase history and payables delegate to owning modules and
    obey branch/ledger permissions.
-   [ ] Ordinary APIs never expose bank details or full sensitive
    identifiers.
-   [ ] No unnecessary Redis, queue, event bus or generic abstraction is
    added.
-   [ ] OpenAPI and tests pass in the actual repository.

## 20. Source alignment and unresolved facts

**From Astra:** organization-scoped supplier identity; optional branch
eligibility; contacts, bank destinations and qualifications as distinct
lifecycles; ledger-owned balances; separate banking permissions;
delegated purchase history/payables; no default Redis; tenant isolation
and payment-redirection fraud controls.

**From MasterData contract:** Supplier table remains but is outside
MasterData API/model ownership; applied migration history must not be
rewritten; supplier is referenced by inventory batches and purchases;
existing code uniqueness/name trigram indexes need reconciliation before
schema changes.

**From supplied API draft:** the seven listed routes and permissions are
retained. Banking/qualification routes are gated future scope, not
silently added to the minimum API.

**Not proven by supplied excerpts:** exact deployed supplier
columns/nullability, whether later migrations add `organization_id`,
whether Ledger and business-audit/outbox are implemented, whether
approved banking encryption/KMS exists, and actual configured
rate-policy names. Verify these before implementation.

### References

-   `answer_in_markdown.md` --- Astra Supplier module and cross-module
    ownership.
-   `MASTERDATA_IMPLEMENTATION_CONTRACT.md` ---
    Supplier/Brand/Manufacturer ownership and migration findings.
-   `migrations/000005_master_data.up.sql` --- existing supplier table.
-   `migrations/000007_inventory_batch.up.sql` --- supplier reference
    from inventory.
-   `migrations/000008_purchase.up.sql` --- purchase supplier FK/delete
    behavior.
-   Current `common.BaseModel`, RBAC, `platform/db`, middleware,
    pagination, Purchase/Ledger modules and split OpenAPI contract ---
    implementation authority.
