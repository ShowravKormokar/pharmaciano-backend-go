# LEDGER_CONTRACT.md

**Project:** Pharmaciano ERP — Go Backend  
**Module:** `ledger`  
**Status:** Implementation Contract — Proposed for Review and Freeze  
**API Version:** `/api/v1`  
**Architecture:** Modular Monolith · Three-Tier · Contract-First OpenAPI 3.1  
**Primary Storage:** PostgreSQL  
**Optional Infrastructure:** Existing transactional outbox and reconciliation workers, if already implemented  
**Prerequisites:** Foundation, authentication, authorization/RBAC, organization/branch, middleware, audit, settings, MasterData currency, and transaction infrastructure.

---

## 1. Purpose

The Ledger module owns the accounting system of record for Pharmaciano.

Its responsibilities are:

- Organization-owned chart of accounts.
- Fiscal periods and posting controls.
- Double-entry journals and immutable journal lines.
- Controlled manual journals and reversal workflows.
- Durable source-operation deduplication.
- Customer/supplier subledger exposure where accounting decisions require it.
- Effective-dated posting-rule/account mappings.
- Trial-balance and other authoritative ledger read models.
- Period close/reopen controls and reconciliation evidence.
- Financial auditability and scoped exports.

**Core invariant: every posted journal is balanced, belongs to exactly one organization and functional currency, references valid accounts and dimensions, and is immutable after posting.**

Ledger is the financial authority. It must not become a generic place for other modules to store operational state.

## 2. Source basis and design decisions

This contract follows the Ledger section in Astra's `answer_in_markdown.md`.

### 2.1 Decisions taken directly from Astra

1. Ledger owns double-entry accounting, fiscal control, immutable financial postings, subledger exposure, and authoritative financial read models.
2. Chart of accounts and fiscal periods are organization-owned.
3. Journals belong to an organization; applicable journal lines carry branch dimensions.
4. Ledger depends on MasterData currency, Settings, the audit port, PostgreSQL, and organization/branch identity.
5. Inventory, Purchase, Sale, Report, and Analytics consume Ledger capabilities.
6. Ledger must not import Purchase, Sale, or Inventory implementations.
7. Draft journals may be edited; posted journals and their lines are immutable.
8. A source-posting record provides durable deduplication.
9. Subledger balances are rebuildable projections but must be strongly maintained when used for financial decisions such as credit exposure.
10. PostgreSQL is authoritative. Redis must never be authoritative for balances, periods, or credit limits.
11. Posting and period close require explicit concurrency control.
12. Reversal creates a new journal; it does not mutate the original.
13. Outbox events are emitted after the journal transaction commits. Events update downstream projections; they do not asynchronously create the original financial journal.
14. Reconciliation detects differences and raises evidence/exceptions. It never silently rewrites accounting history.

### 2.2 Implementation clarifications and conservative choices

The following make Astra's requirements implementable and safe:

- Use the existing transaction manager/context and repository transaction conventions. Do not introduce a second transaction framework.
- Use exact PostgreSQL `NUMERIC` arithmetic and currency-aware rounding. Never use Go `float32` or `float64` for monetary values.
- Use explicit row locks and unique constraints for invariants. Do not rely on application checks alone.
- Treat HTTP idempotency and source-operation deduplication as two related but distinct protections.
- Use an organization functional currency in the initial release. Multi-currency accounting requires a separately approved design.
- Do not maintain a mutable organization-wide account balance on every posting merely to make reports fast. Prefer indexed journal-line reads and versioned snapshots; strongly maintain only projections needed for transactional decisions.
- Keep account mappings typed and versioned. A caller may provide business facts and source identity, but must not choose arbitrary ledger account IDs.
- Do not assume that an outbox, job queue, distributed lock, or cache exists. Reuse the repository's actual implementation if present; otherwise record the gap and decide it explicitly before adding infrastructure.

Where the existing repository or migrations disagree with this document, inspect the real schema and existing contracts first. Do not silently create duplicate tables or replace an established shared abstraction.

## 3. Architectural principles

1. **PostgreSQL is the source of truth.** A committed posted journal is the accounting fact.
2. **Double-entry always.** Total debits must equal total credits for each posted journal.
3. **Posted evidence is immutable.** Correct with reversal and replacement, never update/delete.
4. **Atomic business posting.** When a business operation requires a journal, its operational mutation and journal commit together in the same PostgreSQL transaction wherever both are in this modular monolith.
5. **Exactly-once effect, not exactly-once delivery.** Requests and events may be delivered more than once; database uniqueness and idempotent handlers prevent duplicate financial effects.
6. **Tenant isolation is mandatory.** Every repository operation is organization-scoped, including internal service calls.
7. **Branch scope is a separate authorization dimension.** Organization-wide access does not automatically grant every user branch-level detail.
8. **No domain implementation imports.** Ledger exposes typed application/service ports; it does not import Purchase, Sale, or Inventory packages.
9. **No client-selected accounting policy.** The server resolves approved posting-rule versions and account mappings.
10. **No cache authority.** Redis, in-memory caches, snapshots, and analytics projections cannot authorize a posting or credit decision.
11. **Fail closed for financial writes.** If authorization, period status, source identity, mapping, currency, or transaction state cannot be verified, do not post.
12. **Audit evidence is complementary.** Middleware audit records request activity; Ledger records financial domain evidence.
13. **No speculative infrastructure.** Reuse existing transaction, idempotency, audit, outbox, queue, tracing, and error packages.
14. **Historical reproducibility.** A journal retains the mapping/version and source facts needed to explain why it was posted.

## 4. Scope and ownership boundaries

### 4.1 Ledger owns

- Chart of accounts and account hierarchy.
- Fiscal period definitions and state transitions.
- Journal headers, journal lines, posting validation, posting state.
- Source-posting identity and deduplication.
- Reversal relationships.
- Posting-rule versions and their effective dates.
- Financial subledger identity/exposure projections where required.
- Trial balance and ledger-derived balances.
- Ledger close/reopen evidence.
- Ledger reconciliation runs and exceptions.
- Ledger exports and report data exposed through authorized APIs.

### 4.2 Ledger does not own

| Concern | Owning module |
|---|---|
| Customer and supplier identity/contact data | Customer / Supplier |
| Purchase orders, goods receipts, supplier invoices, purchase returns | Purchase |
| Sale documents, POS checkout, sale returns, payment collection workflow | Sale |
| Physical stock, batches, quantity balances, cost layers, stock movements | Inventory |
| Medicine and product identity | Medicine |
| Currency definitions and minor-unit metadata | MasterData |
| Business configuration and policy resolution | Settings |
| Authentication, sessions, roles, permissions | Auth / RBAC |
| Generic request audit infrastructure | Existing Audit infrastructure |
| Delivery of email/SMS/push notifications | Notification |
| Analytics dashboards and predictive models | Analytics / AI |
| Cross-domain report presentation | Report |

Ledger may persist a stable party reference (`party_type`, `party_id`) for subledger attribution, but it must not duplicate customer/supplier profile fields.

### 4.3 Integration rule

Purchase, Sale, and Inventory call a Ledger application port with typed source facts. Ledger does not import their concrete packages.

Recommended dependency direction:

```text
Purchase ─┐
Sale ─────┼──> Ledger application port / posting service
Inventory ┘

Ledger ───> shared platform, common, errors, MasterData currency port,
            Settings policy port, Audit port, PostgreSQL repositories
```

Do not call Ledger through its own HTTP endpoint from another module inside the same process. Internal calls should use the application service and the caller's transaction context.

## 5. Dependencies and readiness gates

| Dependency | Required capability | Rule |
|---|---|---|
| Organization | Valid organization identity | Derived from authenticated/validated context; never trusted from request body |
| Branch | Branch ownership and scope validation | Every non-null line branch must belong to the journal organization |
| Auth/RBAC | Permission evaluation | Existing authorization middleware/policy |
| Middleware | Protected routes, idempotency, rate limits, request IDs | Reuse existing implementation |
| PostgreSQL | ACID transactions, constraints, row locks | Authoritative |
| MasterData | Currency code and minor-unit precision | Initial release uses one functional currency per organization |
| Settings | Approved accounting policy/configuration | Resolve effective version; do not allow arbitrary runtime overrides |
| Audit | Actor/action/reason evidence | Existing audit port plus ledger domain evidence |
| Transaction manager | Shared transaction context | Required for cross-module atomic posting |
| Outbox | Durable post-commit event publication | Use only if already present/approved |
| Observability | Structured logs, metrics, traces | Existing instrumentation |

### Readiness gates before coding

- Confirm actual transaction helper and repository transaction propagation.
- Confirm current middleware idempotency storage, key scope, response replay behavior, and retention.
- Confirm audit API and whether it writes inside the caller transaction.
- Confirm outbox table/worker exists before designing event publishing.
- Confirm organization functional currency storage and currency precision.
- Inspect existing ledger/account/journal migrations before creating new schema.
- Confirm branch-scope middleware semantics and how organization accountants receive consolidated access.
- Confirm the repository's UUID, pagination, error, DTO, and migration naming conventions.

If a required capability does not exist, document it as an ADR or infrastructure prerequisite. Do not invent package names or assume a tool is available.

## 6. Domain model

| Entity | Purpose | Scope | Lifecycle |
|---|---|---|---|
| Account | Chart of accounts | Organization | Create, update descriptive fields, deactivate after use |
| Fiscal period | Posting window | Organization | Open → closing → closed; controlled reopen |
| Journal | Accounting document | Organization | Draft mutable; posted immutable |
| Journal line | Debit/credit fact | Organization + optional branch | Mutable only while draft |
| Source posting | Durable source-operation deduplication | Organization | Immutable |
| Subledger account | Customer/supplier accounting identity | Organization | Controlled lifecycle |
| Subledger balance | Transactional exposure projection | Organization | Strongly maintained; rebuildable from postings |
| Balance snapshot | Accelerated reporting projection | Organization/account/branch/period | Derived, versioned, rebuildable |
| Posting-rule version | Business fact → account mapping | Organization | Effective-dated/versioned |
| Reconciliation run | Consistency evidence | Organization | Immutable run/result evidence |
| Reconciliation exception | Difference requiring investigation | Organization | State-controlled; resolution evidence retained |

### 6.1 Journal lifecycle

```text
Draft
  ├── edit header/lines
  ├── validate
  ├── post ───────────────> Posted (immutable)
  └── discard/archive      (only if policy permits and never posted)

Posted
  └── reverse ────────────> Original remains Posted
                             New reversal journal is Posted
```

A posted journal must never transition back to Draft.

A draft may be deleted only if the existing audit/retention policy explicitly permits it. Prefer a `voided`/`discarded` state with actor, timestamp, and reason when preserving workflow evidence is required.

## 7. Accounting invariants and business rules

### 7.1 Double-entry invariant

For each posted journal:

\[
\sum_{i=1}^{n} debit_i = \sum_{i=1}^{n} credit_i
\]

Additional requirements:

- At least two lines.
- Every line references an account in the same organization.
- Each line has exactly one positive side: debit or credit.
- Debit and credit cannot both be positive.
- Amounts must be greater than or equal to zero; zero-value lines are rejected.
- No NaN, Infinity, exponent overflow, or floating-point input conversion.
- Journal currency must match the organization's functional currency in the initial release.
- Every branch dimension must belong to the journal organization and be allowed for that posting type.
- Total debit and credit are compared after currency precision/rounding policy is applied.
- Posting cannot occur if any required account is inactive, a control-account rule is violated, or the period is not open.

A normal row `CHECK` constraint cannot aggregate sibling lines. Enforce balance through a controlled posting transition and a database-level guard/constraint trigger that checks the final journal aggregate at transaction completion. The service must validate first for a useful error; the database guard remains the final protection against bypasses and programming errors.

### 7.2 Money representation

- Use PostgreSQL `NUMERIC`, not `REAL`, `DOUBLE PRECISION`, or floating-point columns.
- Use an exact decimal representation in Go. Reuse an existing decimal/money package if one exists; do not add another dependency without checking `/pkg` and `go.mod`.
- Never map a money amount to `float64` in DTOs, models, mappers, or calculations.
- Currency minor-unit precision comes from MasterData currency metadata.
- Define and freeze the rounding mode in the accounting policy. Apply it consistently at the business calculation boundary and persist the final posted amount.
- Do not round each journal line independently if that changes the intended total. The originating domain must provide final, validated accounting amounts and Ledger must verify currency precision and balancing.
- Persist currency code/reference on the journal or immutable accounting context so historical interpretation does not depend on a mutable display setting.
- Initial release: one functional currency per organization. Reject unsupported foreign-currency postings rather than silently treating them as functional currency.

### 7.3 Account rules

- Account code is unique within an organization.
- Account type/class and normal side are controlled enums.
- Parent account must belong to the same organization.
- Prevent hierarchy cycles in the service and validate concurrent parent changes.
- Posting is allowed only to active posting accounts, not headers/summary accounts.
- An account already referenced by posted journal lines cannot be hard-deleted.
- Deactivation blocks future postings but does not hide historical records.
- Control accounts (e.g., Accounts Receivable, Accounts Payable, Inventory) require explicit posting-source/subledger rules. Manual posting to a control account should be restricted or require a specific controlled workflow.
- Do not let a user change account type, normal side, or control-account semantics after the account has been used in a posted journal.
- Account code/name changes must preserve audit history; code changes after use should be disallowed or handled as a controlled alias/reclassification policy.

### 7.4 Fiscal period rules

- Periods are organization-owned and non-overlapping.
- `start_date <= end_date`.
- Posting date must fall within exactly one open period.
- Period dates use the organization's accounting calendar/timezone policy; accounting dates are dates, not timestamps.
- A period close and a journal post must serialize against each other using the same period row lock protocol.
- Closed periods reject normal posting and reversal.
- Backdated posting requires explicit permission and an open period; never bypass a closed period.
- Reopening requires exceptional permission, reason, fresh authorization, and immutable evidence.
- Do not reopen periods through a generic PATCH endpoint.
- A close operation must verify all required reconciliations and unresolved exceptions according to the approved close checklist.
- Close must not be represented as successful while required checks are pending or failed.

### 7.5 Reversal rules

- Reversal creates a new journal with the exact opposite debit/credit amounts and appropriate original dimensions.
- Original journal remains unchanged.
- Reversal references original journal and records reason, actor, reversal date, and source/idempotency identity.
- Only a posted journal can be reversed.
- A journal may be reversed only once unless a separately approved correction workflow explicitly supports multiple partial corrections.
- Reversal date must be in an open period.
- Reversal of a reversal is not a normal workflow; use a new correcting journal with explicit reason and linkage.
- If the original source operation is also being reversed operationally, the originating module must coordinate its state transition and Ledger reversal atomically.

### 7.6 Source identity and posting rules

A source posting is not merely an optional text field. It is a durable identity for the accounting effect.

Recommended natural key:

`(organization_id, source_type, source_operation_id, posting_kind)`

Examples:

- `sale`, sale UUID, `recognition`
- `sale`, sale UUID, `cogs`
- `sale_return`, return UUID, `reversal`
- `purchase_receipt`, receipt UUID, `inventory_accrual`
- `supplier_invoice`, invoice UUID, `payable_recognition`
- `supplier_payment`, payment UUID, `settlement`
- `inventory_adjustment`, adjustment UUID, `write_off`

Rules:

- `source_type` and `posting_kind` are allow-listed domain identifiers, not arbitrary unbounded client strings.
- `source_operation_id` must be a stable UUID/identifier generated by the owning domain.
- A duplicate source identity with the same canonical payload returns the original result.
- A duplicate identity with a different payload is a conflict and must never overwrite the first posting.
- Source records are inserted in the same transaction as the journal.
- Do not use a client-generated HTTP idempotency key as the only source identity. HTTP keys expire; accounting source identity must remain durable.
- Do not allow a caller to reuse one source identity for unrelated posting kinds unless the posting-kind contract explicitly allows it.
- Keep a canonical payload hash or immutable source reference/version sufficient to detect a conflicting replay.

## 8. Database design

**Important:** This is a logical schema contract, not permission to duplicate existing tables. Inspect current migrations and adapt names/types to established conventions before writing migrations.

Legend: `T` = tenant-owned (`organization_id` required); `M` = shared audit/model columns where applicable; `B` = branch dimension.

### 8.1 Core tables

| Table | Key columns | Constraints and indexes |
|---|---|---|
| `ledger_accounts` | T+M; `code`, `name`, `account_type`, `normal_side`, `parent_id?`, `is_active`, `is_control_account`, `is_posting_allowed`, `version` | Unique `(organization_id, code)`; same-org parent FK; account type/side checks; indexed parent/tree lookup |
| `ledger_periods` | T+M; `start_date`, `end_date`, `status`, `closed_at?`, `closed_by?`, `version` | `start_date <= end_date`; no overlapping periods per organization; index by org/date/status |
| `ledger_journals` | T+M; `period_id`, `posting_date`, `currency_id`, `currency_code_snapshot`, `status`, `description`, `source_reference?`, `created_by`, `posted_by?`, `posted_at?`, `reversal_of_id?`, `version` | Same-org period FK; same-org reversal FK; index `(org, posting_date, id)`; state consistency checks |
| `ledger_lines` | T; `journal_id`, `line_no`, `account_id`, `branch_id?`, `debit`, `credit`, `counterparty_type?`, `counterparty_id?`, `memo?` | Unique `(org, journal, line_no)`; same-org journal/account/branch references; positive-side checks; account/date indexes via journal join |
| `ledger_source_postings` | T; `source_type`, `source_operation_id`, `posting_kind`, `journal_id`, `payload_hash`, `created_at` | Unique `(org, source_type, source_operation_id, posting_kind)`; journal FK; immutable |
| `ledger_subledger_accounts` | T+M; `party_type`, `party_id`, `currency_id`, `status`, `credit_limit?`, `version` | Unique `(org, party_type, party_id, currency_id)`; party identity validated by owning workflow |
| `ledger_subledger_balances` | T; `subledger_account_id`, `outstanding`, `reserved_credit`, `version`, `updated_at` | PK/unique `(org, subledger_account_id)`; exact numeric; nonnegative/approved sign policy |
| `ledger_posting_rule_versions` | T+M; `rule_code`, `version`, `effective_from`, `effective_to?`, `mapping`, `status`, `approved_by?` | Unique `(org, rule_code, version)`; effective interval validation; referenced accounts same-org and eligible |
| `ledger_balance_snapshots` | T; `account_id`, `branch_id?`, `period_id`, `debit_total`, `credit_total`, `as_of`, `generation`, `generated_at` | Unique dimensional key with explicit NULL handling; indexed by org/period/account |
| `ledger_reconciliation_runs` | T+M; `kind`, `window_start`, `window_end`, `status`, `mismatch_count`, `started_at`, `completed_at?`, `evidence_ref?` | Index `(org, created_at, id)`; immutable completed result |
| `ledger_reconciliation_exceptions` | T; `run_id`, `exception_code`, `source_ref?`, `expected_value?`, `actual_value?`, `status`, `resolution_reason?`, `resolved_by?`, `resolved_at?` | Run FK; bounded detail; state transition audit |

### 8.2 Tenant-safe foreign keys

Where supported by the repository's migration conventions, use composite tenant-safe keys:

- Unique `(organization_id, id)` on tenant-owned parent rows.
- Child foreign keys `(organization_id, parent_id)` to `(organization_id, id)`.
- Branch references validated by `(organization_id, branch_id)`.
- Journal-line account references validated within the same organization.

This prevents accidental cross-tenant references even if a service query is incorrectly scoped.

Do not add composite keys blindly if the existing schema uses another established safe pattern; document the equivalent guarantee.

### 8.3 Journal state constraints

At minimum, enforce database checks so that:

- Draft journal has no `posted_at` and no `posted_by`.
- Posted journal has both `posted_at` and `posted_by`.
- Reversal reference is not self-referential.
- Journal period and organization are consistent.
- Journal line amounts are exact and valid.
- Posted journal lines cannot be inserted, updated, or deleted except through the controlled posting transaction before the state transition is finalized.

Use database triggers/permissions where appropriate to protect immutability from accidental alternate write paths. Keep trigger logic small, deterministic, tested, and documented. The application service remains the primary workflow boundary.

### 8.4 Indexing strategy

Create indexes for actual query patterns, not every column.

Minimum expected access paths:

- Accounts: `(organization_id, code)` unique; `(organization_id, parent_id, code)`.
- Periods: `(organization_id, start_date, end_date)` plus status lookup.
- Journals: `(organization_id, posting_date DESC, id DESC)`.
- Journals by period: `(organization_id, period_id, posting_date, id)`.
- Journal lines by journal: `(organization_id, journal_id, line_no)`.
- Journal lines by account: `(organization_id, account_id, journal_id)`.
- Source deduplication: unique natural key.
- Subledger: unique party/currency key and `(organization_id, subledger_account_id)`.
- Snapshots: account/period/branch dimensional lookup.
- Reconciliation: `(organization_id, created_at DESC, id DESC)`.

Use partial indexes for active/open states only when the query planner benefits and the predicate matches the repository queries.

### 8.5 Delete behavior

- Posted journals, lines, source postings, and completed reconciliation evidence are never hard-deleted.
- Referenced accounts and periods cannot be hard-deleted.
- Draft records may be discarded according to retention policy.
- Use deactivation/archival for master records.
- Avoid cascading deletes from organization/account/period to financial evidence. Organization lifecycle must follow the platform's retention/legal-hold policy.

## 9. Posting engine and transaction contract

This is the most critical part of the module.

### 9.1 One transaction boundary

For any synchronous operation that changes operational state and requires a journal, both changes must commit or roll back together.

Examples:

- Sale checkout + sale records + payment allocation + COGS/stock consumption + journal.
- Purchase goods receipt + inventory receipt/cost layer + receipt accrual journal.
- Supplier invoice recognition + payable/tax journal.
- Payment settlement + payment allocation + payable/receivable journal.
- Inventory write-off + stock movement + loss journal.

The orchestrating application service owns the top-level transaction. It passes the same transaction context to Inventory, Purchase/Sale, and Ledger repositories/services.

```text
BEGIN
  validate authenticated actor and tenant/branch scope
  claim durable source identity / verify duplicate
  lock required business rows in canonical order
  validate domain state and accounting policy
  mutate operational records
  create journal header and lines
  validate balance, accounts, currency, period and dimensions
  insert source-posting identity
  perform required subledger exposure update
  append domain audit evidence
  append outbox event (if existing outbox is enabled)
COMMIT
```

If any step fails, roll back all database writes in the operation.

Do not commit the operational document and then call Ledger in a second transaction. That creates a window where stock/sales/purchases exist without their required financial entry.

### 9.2 Transaction context propagation

- Use the project's existing transaction manager and context key/abstraction.
- Repositories must use the transaction-bound query executor when a transaction is present.
- Do not open nested independent transactions inside Ledger.
- Do not call an HTTP API or external service while holding database locks.
- Avoid long-running work inside the transaction: no report generation, external payment provider calls, email/SMS, or large exports.
- Keep lock duration short and perform pure validation before acquiring locks where safe; revalidate mutable facts after locking.
- A transaction retry must rerun the entire operation with the same source identity and idempotency context.

### 9.3 Isolation and lock strategy

Use PostgreSQL `READ COMMITTED` with explicit row locks for the invariants defined here. Use `SERIALIZABLE` only where a predicate invariant cannot be protected with a deterministic lock/constraint strategy.

Recommended posting lock order:

1. Organization/accounting policy rows required for the operation, if mutable and transactionally versioned.
2. Fiscal period row.
3. Source operation/business document row.
4. Account rows in stable sorted UUID order, if account status/configuration needs locking.
5. Subledger exposure rows in stable sorted key order.
6. Journal header/lines and source-posting row.

The exact global order must be reconciled with Inventory/Purchase/Sale lock order before integration. Define one cross-module lock order and follow it consistently to reduce deadlocks.

### 9.4 Period post/close race

Posting and closing must coordinate on the same `ledger_periods` row.

- Posting acquires a shared lock compatible with other postings, then verifies status is `open`.
- Closing acquires an exclusive lock, waits for in-flight posters, changes state to a closing/closed state only after all close checks pass.
- After the close transaction obtains its lock, it rechecks unresolved prerequisites and the period state.
- A posting that starts after close has acquired/committed the exclusive transition must fail.
- Do not rely on a prior unlocked `SELECT status` check.
- Do not use a Redis lock as a substitute for the PostgreSQL period lock.

If the current PostgreSQL driver/query layer cannot express the desired lock mode, use the closest safe row-lock protocol supported by the existing repository and document its concurrency behavior. A conservative exclusive lock is safer than an unproven lock mode, though it may reduce parallel posting throughput.

### 9.5 Source deduplication race

Use the unique source key as the final arbiter.

- Attempt to claim/insert the source identity within the same transaction.
- On conflict, load the existing source posting using the organization-scoped key.
- Compare canonical payload hash and source identity.
- Same payload: return the original journal/result.
- Different payload: return a conflict; do not mutate or create another journal.
- Never implement `SELECT then INSERT` without a unique constraint; two concurrent requests can both pass the read.

### 9.6 Credit exposure race

When a credit sale or other credit operation depends on outstanding exposure:

- Lock the relevant subledger balance row with `FOR UPDATE`.
- If the row does not exist, create it with a unique-key-safe insert/upsert, then lock/read it.
- Recalculate exposure from the authoritative transactional projection.
- Apply the approved credit policy and any reservation amount.
- Update the projection and post the journal in the same transaction.
- Concurrent operations for the same customer must serialize on the same balance row.
- Do not check credit limit from Redis or an eventually consistent report snapshot.
- Do not allow two concurrent checkouts to both pass based on the same stale balance.

Credit-limit ownership and policy must be explicitly confirmed against the Customer/Settings contract. Customer identity remains in Customer; Ledger may own the accounting exposure projection. A customer profile field must not become a second authoritative due balance.

### 9.7 Deadlocks and retry policy

- Lock rows in deterministic order.
- Keep transactions short.
- Retry only known transient PostgreSQL errors (deadlock, serialization failure, selected lock timeout) using bounded attempts and jitter, following existing platform retry conventions.
- Retry the complete transaction, never only the failed statement in a partially executed business workflow.
- Use the same durable source identity and HTTP idempotency key across retries.
- Do not retry validation, authorization, closed-period, stale-version, or business conflict errors.
- If retry budget is exhausted, return a retryable service error and log the trace/request/source identifiers without leaking financial PII.

### 9.8 Commit ambiguity

If the client times out after the database commits but before receiving the response:

- The client retries with the same HTTP idempotency key.
- Middleware replays the stored response if supported.
- Independently, the source-posting natural key resolves whether the financial effect already exists.
- If the response store is unavailable or expired, source identity still prevents a duplicate journal.
- Never infer rollback solely from a network timeout.

## 10. Idempotency contract

Two layers are mandatory for financial write paths.

### 10.1 HTTP request idempotency

Use the existing idempotency middleware for mutating public endpoints.

Expected behavior (adapt to the existing implementation):

- Require an `Idempotency-Key` for journal create/post/reverse, period close/reopen, and export creation.
- Key must be bounded, non-empty, and high-entropy; UUID is an acceptable client convention.
- Scope key by authenticated principal, organization, HTTP method, normalized route, and key value (or the exact existing middleware contract).
- Store a canonical request fingerprint.
- Same key + same request: replay the original status/body/result.
- Same key + different request: return conflict.
- Concurrent same-key requests: only one executes; others wait/replay or receive the existing in-progress response according to middleware semantics.
- Do not expose another user's stored response.
- Retain idempotency records for the existing platform policy; financial source identity remains permanent independently.
- Never bypass idempotency for internal retries; internal calls use the source identity and transaction context.

If current middleware does not provide these semantics, record the exact gap. Do not build a Ledger-only middleware implementation.

### 10.2 Domain source idempotency

`ledger_source_postings` protects the accounting effect across different HTTP routes, workers, internal calls, and retry windows.

It must be checked/inserted in the same transaction as the journal. This protection is permanent for the source operation's retention lifetime.

### 10.3 Idempotency response behavior

| Situation | Expected result |
|---|---|
| Same key, same request, completed | Replay original result |
| Same key, different request | Conflict |
| Same source identity, same canonical facts | Return original journal |
| Same source identity, different facts | Conflict + security/consistency log |
| First attempt rolled back | Retry may execute |
| Commit succeeded but response lost | Resolve original journal; do not repost |
| Concurrent duplicate source calls | Unique constraint allows one posting |

## 11. Posting rules and domain integration

Ledger accepts typed facts, not arbitrary account IDs from business handlers.

### 11.1 Posting-rule version

Each rule version should identify:

- Stable rule code.
- Version number.
- Effective-from and optional effective-to date.
- Approved account mapping.
- Required dimensions (branch, party/subledger, tax treatment, etc.).
- Rounding/aggregation policy reference where relevant.
- Status/approval metadata.
- Immutable reference from the resulting journal.

Rules must be validated when activated:

- Every account belongs to the organization.
- Every account is active and posting-eligible.
- Required control-account semantics are correct.
- Mapping covers all required debit/credit components.
- Effective periods do not overlap ambiguously.
- Rule cannot be edited after use; create a new version.
- Historical journal retains the exact mapping version used.

Do not accept account IDs directly from untrusted clients for system-generated source postings. Manual journal users may select accounts only after permission and account eligibility validation.

### 11.2 Example accounting patterns

These are accounting patterns to guide integration, not permission to hardcode account IDs or tax treatment. Final mappings depend on approved organization policy and effective tax rules.

| Business fact | Debit | Credit |
|---|---|---|
| Cash sale recognition | Cash/Bank/Clearing | Sales revenue and applicable tax payable |
| Credit sale recognition | Accounts receivable | Sales revenue and applicable tax payable |
| Sale cost recognition | Cost of goods sold | Inventory |
| Customer payment against receivable | Cash/Bank/Clearing | Accounts receivable |
| Purchase receipt before supplier invoice | Inventory | Goods received not invoiced / receipt accrual |
| Supplier invoice recognition | Receipt accrual and applicable input tax | Accounts payable |
| Supplier payment | Accounts payable | Cash/Bank |
| Purchase return | Accounts payable/receipt clearing as policy dictates | Inventory/tax adjustment as policy dictates |
| Expiry/write-off | Expired stock loss | Inventory |
| Approved stock shortage | Inventory loss/variance | Inventory |
| Customer refund | Sales return/refund liability or cash clearing | Cash/Bank/receivable according to settlement |

**Important:** Purchase, Sale, and Inventory own their operational workflow and source facts. Ledger owns the mapping and journal. Do not infer that every purchase order creates a journal: an approved purchase order alone is not a financial posting.

### 11.3 Typed posting command

A posting command should carry only the minimum trusted facts, conceptually:

- Organization ID from validated context.
- Actor/system actor identity.
- Stable source type, source operation ID, posting kind.
- Posting date.
- Functional currency reference.
- Typed debit/credit components or approved business fact structure.
- Branch/party dimensions where required.
- Description/reason and source reference.
- Expected posting-rule version or server-resolved rule identity.
- Canonical payload fingerprint.

Do not expose a generic public endpoint where callers can submit arbitrary account IDs and arbitrary lines unless the caller is explicitly performing an authorized manual journal workflow.

## 12. Subledger and balance projections

### 12.1 Subledger ownership

Ledger owns accounting exposure, not customer/supplier profile data.

- Customer and Supplier modules remain the identity authorities.
- Ledger stores a party type and stable party ID reference.
- The originating workflow validates that the party exists in the same organization.
- Receivable/payable journal lines must carry the required party reference.
- Control accounts must reconcile to their corresponding subledger totals.
- Party IDs are not accepted as authorization evidence.

### 12.2 Subledger balance

The subledger balance is a projection used for transactional decisions such as credit exposure. It is not an independent accounting source.

- Update it in the same transaction as the journal lines that change exposure.
- Protect concurrent updates with row locks and version checks.
- Rebuild it from immutable posted journal lines.
- Reconciliation compares it to the underlying journal facts.
- If a projection is missing or inconsistent, block sensitive credit decisions or use a controlled authoritative recomputation; never trust a stale cache.
- Do not update it from outbox consumers asynchronously if it gates a synchronous credit decision.

### 12.3 General ledger account balances

Do not update every account's organization-wide balance on every sale merely to optimize dashboards.

Initial strategy:

1. Journal lines remain the authoritative source.
2. Trial balance queries aggregate posted lines with proper tenant/date/branch filters.
3. Add versioned snapshots for known expensive reporting windows.
4. Snapshot generation is rebuildable and must identify its `as_of` point and generation/version.
5. A snapshot is never used to validate posting, period close correctness, or credit exposure.
6. If later introducing incrementally maintained account balances, first define atomic update/rebuild/reconciliation semantics and benchmark the actual workload.

### 12.4 Branch dimensions

- Organization-wide journals may have `branch_id = NULL` only when policy explicitly defines the entry as organization-level.
- Branch-level postings must carry the correct branch dimension on each applicable line.
- Do not infer branch from an arbitrary request body.
- Consolidated reports include organization-level lines exactly once.
- Branch reports must not silently include organization-level entries as if they belonged to every branch.
- Cross-branch journals require an explicit supported workflow and branch authorization for each affected branch.
- Warehouse is an Inventory dimension; do not add `warehouse_id` to every ledger line unless a specific accounting report and approved policy require it.

## 13. API contract

All routes are under `/api/v1`. Exact route registration must follow the existing router conventions and OpenAPI source-of-truth.

| Operation | Endpoint | Permission | Idempotency |
|---|---|---|---|
| List accounts | `GET /ledger/accounts` | `ledger.view` | No |
| Read account | `GET /ledger/accounts/{id}` | `ledger.view` | No |
| Create account | `POST /ledger/accounts` | `ledger.manage-accounts` | Required |
| Update account metadata | `PATCH /ledger/accounts/{id}` | `ledger.manage-accounts` | Natural/versioned |
| Deactivate account | `POST /ledger/accounts/{id}/deactivate` | `ledger.manage-accounts` | Required |
| List periods | `GET /ledger/periods` | `ledger.view` | No |
| Create period | `POST /ledger/periods` | `ledger.manage-periods` | Required |
| Create draft journal | `POST /ledger/journals` | `ledger.create` | Required |
| Read journal list/detail | `GET /ledger/journals`, `GET /ledger/journals/{id}` | `ledger.view` | No |
| Update draft | `PATCH /ledger/journals/{id}` | `ledger.create` or existing update permission | Versioned |
| Post journal | `POST /ledger/journals/{id}/post` | `ledger.post` | Required |
| Reverse journal | `POST /ledger/journals/{id}/reverse` | `ledger.reverse` | Required |
| Close period | `POST /ledger/periods/{id}/close` | `ledger.close-period` | Required |
| Reopen period | `POST /ledger/periods/{id}/reopen` | `ledger.reopen-period` | Required |
| Trial balance | `GET /ledger/reports/trial-balance` | `ledger.view` | No |
| General ledger | `GET /ledger/reports/general-ledger` | `ledger.view` | No |
| Account activity | `GET /ledger/accounts/{id}/activity` | `ledger.view` | No |
| Export | `POST /ledger/exports` | `ledger.export` | Required |
| Read export job | `GET /ledger/exports/{id}` | `ledger.export` | No |

Astra specifies the allowed financial report endpoint pattern as `GET /ledger/reports/{allowed-report}`. Prefer explicit allow-listed report routes or an enum-backed report name; never accept arbitrary table/query names.

### 13.1 API design requirements

- Do not expose `organization_id` as a trusted write field.
- Branch filters must be intersected with authorized branch scope.
- Cursor pagination for journal lists and activity.
- Bounded date ranges and bounded page sizes.
- Explicit sort allow-list; stable tie-breaker by UUID.
- Validate all UUIDs, dates, enum values, money precision, line counts, and text lengths.
- Use `If-Match`/expected version or the project's established optimistic-lock DTO convention for draft updates and period state changes.
- Do not return internal payload hashes, raw audit metadata, or hidden cross-tenant existence details.
- Use consistent platform error envelopes and error codes.
- Export is asynchronous if it may be large; job creation is idempotent and reauthorizes download.

### 13.2 Manual journal safeguards

Manual journal creation/posting is privileged:

- Require reason/description.
- Require an approved permission.
- Enforce account eligibility and open period.
- Require branch scope on branch-sensitive lines.
- Require explicit approval/separation-of-duties policy where the project supports it.
- Consider maker-checker for high-value or sensitive journals, but do not hardcode role names in the service.
- Record who created, approved, and posted.
- Do not allow the same actor to approve their own journal when maker-checker policy forbids it.
- Thresholds, if introduced, come from governed Settings/accounting policy, not request input.

## 14. Authorization and access matrix

Authentication establishes identity. RBAC/authorization establishes the allowed action. Tenant and branch scope constrain the data. All three are required.

Permission names below follow Astra's Ledger section and may be mapped to the existing RBAC permission catalog. Do not create a second authorization system.

| Actor capability | Read | Create/manage | Post/close/reverse | Export |
|---|---|---|---|---|
| Organization accountant with `ledger.view` | Organization-wide permitted ledger views | Only with explicit manage/create permission | Only with explicit action permission | Only with `ledger.export` |
| Branch accountant | Authorized branch data | Only authorized branch-scoped drafts | Only if assigned the corresponding posting/close permission and branch scope | Authorized branch export only |
| Cashier/sales operator | No general ledger access by default | No manual journal access | No direct ledger posting permission; Sale invokes internal typed posting port | No |
| Purchase operator | No general ledger access by default | No manual journal access | Purchase invokes internal typed posting port | No |
| Inventory operator | No general ledger access by default | No manual journal access | Inventory invokes internal typed posting port | No |
| Super Admin | Only as granted by RBAC policy | Only explicit permissions | No implicit bypass of financial controls | Only explicit permission |
| Background worker/system actor | No interactive user session | Narrow service capability only | Only approved source posting/reconciliation actions | No general export access |

**Important authorization rules:**

- Never hardcode `SUPER_ADMIN`, `ADMIN`, or role names inside Ledger business logic as a substitute for permissions.
- A role may receive permissions through the existing RBAC configuration, but service code checks permission/capability.
- A super-admin account is not automatically exempt from period locks, immutability, source deduplication, or audit.
- Internal module calls must pass a trusted service identity/capability and organization context; they must not bypass domain validation.
- Do not expose a public endpoint that allows Purchase/Sale/Inventory callers to submit arbitrary journals.
- Read permission does not imply export permission.
- Account management does not imply journal posting.
- Posting does not imply period reopen.
- Reopen permission must be exceptional and separately granted.
- Branch access must be checked on every read and write, not only when loading the UI.

## 15. Middleware and platform integration

Use the existing project middleware stack. Do not reimplement it inside Ledger.

Expected protected mutation flow:

```text
Request ID / Correlation ID
  → Authentication
  → Organization / tenant resolution
  → Branch-scope resolution
  → RBAC permission check
  → Rate limit
  → Idempotency (for protected financial mutations)
  → Request validation
  → Handler
  → Application service + shared transaction
  → Repository
  → Existing audit / outbox ports
  → Standard response/error mapper
```

The exact order must match the repository's established router/middleware design. If Audit middleware wraps handlers, preserve its existing behavior.

### 15.1 Required reuse

- Existing auth principal/context.
- Existing tenant/organization middleware.
- Existing branch-scope middleware/helper.
- Existing Casbin/RBAC policy service and permission catalog.
- Existing `rate_auth_read`, `rate_auth_write`, and stricter policies where already defined.
- Existing idempotency middleware.
- Existing audit middleware and domain audit port.
- Existing request ID/correlation ID and structured logger.
- Existing transaction manager.
- Existing pagination, UUID, validation, and error packages.
- Existing metrics/tracing/health instrumentation.
- Existing OpenAPI/strict handler generation pattern.

### 15.2 Internal service calls

- Internal calls do not traverse HTTP middleware.
- They must still enforce tenant ownership, source identity, posting rules, period status, account mapping, and branch constraints.
- Caller authorization is enforced by the originating module's user permission and the approved service capability contract.
- Never trust a user-supplied `organization_id`, `actor_id`, or `branch_id` from an internal DTO without validating the context that created it.

## 16. Auditability and financial evidence

Middleware audit and Ledger domain evidence serve different purposes.

### 16.1 Record at least

- Account creation, metadata changes, activation/deactivation.
- Period creation, close, reopen, and failed exceptional attempts where audit policy requires.
- Draft journal creation/update/discard.
- Journal approval and posting.
- Reversal and correction links.
- Posting-rule version creation/activation/deactivation.
- Manual journal reason and actor.
- Source operation type/ID and posting kind.
- Export creation, execution, download authorization, and completion.
- Reconciliation runs and exception resolution.
- Rejected cross-tenant/branch access attempts according to security logging policy.

### 16.2 Evidence requirements

For posted journals retain:

- Journal ID and organization ID.
- Posting date and functional currency snapshot.
- Actor/system actor and timestamp.
- Source type, source operation ID, posting kind.
- Posting-rule code/version.
- Journal lines and dimensions.
- Reason/description.
- Reversal relationship if any.
- Relevant request/correlation/trace ID where available.

Do not store secrets, credentials, full payment instrument data, or unnecessary customer PII in journal descriptions or audit payloads.

### 16.3 Immutability

- No update/delete endpoint for posted journals or posted lines.
- Repository methods must make illegal state transitions difficult to express.
- Database guard/trigger or restrictive DB role permissions should protect against accidental direct mutation.
- Corrections are compensating entries.
- Reconciliation resolution records do not alter source journals.

## 17. Events and outbox

Only use the existing transactional outbox if it is already part of the platform or is explicitly approved as a prerequisite.

Expected domain events:

- `JournalPosted.v1`
- `JournalReversed.v1`
- `PeriodClosed.v1`
- `PeriodReopened.v1` (if consumers need it)
- `PostingRuleActivated.v1` (if consumers need it)

Event rules:

- Insert outbox event in the same PostgreSQL transaction as the journal/period state change.
- Publish only after commit.
- Event payload should contain stable IDs, organization ID, event version, occurrence time, source identity, and minimal necessary facts.
- Do not include full customer/supplier PII or complete journal payload unless a consumer has a justified need.
- Consumers must be idempotent and tolerate duplicate/out-of-order delivery.
- Events update Analytics/Report projections and trigger downstream work. They must not be the only record that a sale/purchase was financially posted.
- Event publication failure must not roll back a committed journal; the durable outbox worker retries.
- If outbox is not implemented, do not publish directly to Redis and pretend it is reliable. Record an ADR and choose a durable strategy.

## 18. Redis and caching

Redis is not required for Ledger correctness.

Never use Redis as the authority for:

- Journal existence or posted state.
- Account balances.
- Fiscal period status.
- Source deduplication.
- Credit exposure/limit decisions.
- Authorization to post.

Optional cache candidates, only after profiling:

- Account tree/read-only metadata.
- Non-sensitive report results/snapshots.
- Effective posting-rule read model, with strict version invalidation.

Cache rules:

- Cache keys include organization and all relevant dimensions.
- Cache invalidation occurs after committed change, preferably via existing outbox.
- A cache miss/failure falls back to PostgreSQL.
- Critical posting always re-reads/validates from PostgreSQL inside the transaction.
- Never cache a response in a way that bypasses branch authorization.
- Do not add Redis dependency solely for account balances or period locks.

## 19. Background jobs and reconciliation

Jobs are operational helpers, not accounting authorities.

Possible jobs, subject to existing worker infrastructure:

1. Trial-balance integrity scan.
2. Subledger-to-control-account reconciliation.
3. Inventory valuation-to-GL reconciliation.
4. Accounts payable/receivable reconciliation.
5. Balance snapshot generation.
6. Long-running export generation.
7. Period-close checklist preparation.

### 19.1 Job safety

- Every job has organization scope and a bounded date range.
- Worker identity is narrow and auditable.
- Jobs are safe to retry.
- Use durable job identity/unique constraints where duplicate execution could create duplicate artifacts.
- Read authoritative PostgreSQL data.
- Snapshot jobs are replaceable/rebuildable by generation.
- Reconciliation jobs never auto-correct posted journals.
- A failed job records status/error classification and can be retried safely.
- Large jobs must not hold a transaction open for the entire report duration.

### 19.2 Reconciliation behavior

A mismatch creates an exception with:

- Reconciliation run ID.
- Organization and date/window.
- Source subsystem and stable source reference.
- Expected and observed values.
- Difference amount/quantity where applicable.
- Detection timestamp.
- Status and resolution evidence.

A mismatch must not be silently patched. Resolve it through the owning domain's correction workflow and a compensating journal where required.

## 20. Security requirements

### 20.1 Tenant isolation

- Every ledger repository method requires organization scope.
- No lookup by bare journal/account/period UUID without organization predicate.
- Validate organization ownership of all referenced accounts, periods, branches, parties, and source records.
- Use tenant-safe composite foreign keys where consistent with existing schema.
- Do not reveal whether a foreign-tenant journal/account UUID exists; return the standard not-found/forbidden behavior prescribed by platform policy.
- Background jobs and exports are tenant-scoped.
- Cross-tenant tests are mandatory.

### 20.2 Branch isolation

- Apply authorized branch filters to list, detail, reports, and exports.
- A branch user cannot widen scope by sending another `branch_id`.
- Organization-level journal lines are treated as organization-level, not duplicated into each branch.
- Cross-branch transactions require explicit policy and source workflow.
- Branch access is revalidated at execution time for long-running export jobs.

### 20.3 Financial privilege escalation

- No client-selected account mapping for system postings.
- No direct posting through a generic unauthenticated/internal route.
- Separate account management, journal creation, posting, reversal, close, reopen, and export permissions.
- Enforce maker-checker where configured.
- No privileged bypass for super-admin or service accounts.
- Service credentials must be narrow, rotated, and never logged.

### 20.4 Input and query safety

- Use parameterized SQL only.
- Allow-list sort fields, report names, filters, and enum values.
- Bound line count, page size, date range, text length, and export size.
- Reject malformed decimal values, excessive precision, negative/zero line amounts, and overflow.
- Prevent mass assignment: DTOs expose only intended writable fields.
- Do not accept database column names, SQL expressions, or arbitrary JSON account maps from clients.
- Protect export output from spreadsheet formula injection for CSV/XLSX.
- Escape/encode user-controlled descriptions in any rendered report.

### 20.5 Sensitive data and exports

- Journal descriptions should avoid unnecessary PII.
- Export requires `ledger.export`, scoped filters, and reauthorization at download.
- Store export files privately; use short-lived signed links only if the existing file-storage contract supports them.
- Never make ledger exports public or guessable.
- Record requester, filters, scope, row count, and download authorization.
- Avoid logging full journal line payloads or customer/supplier identifiers in high-cardinality metrics.

### 20.6 Abuse and denial of service

- Apply existing read/write/export rate policies.
- Bound trial balance/general ledger date ranges.
- Require asynchronous export for large result sets.
- Limit concurrent expensive exports per organization/user using existing job controls.
- Add query timeouts and cancellation propagation where supported.
- Do not let report requests hold row locks or block posting.

## 21. Observability

Use existing structured logging, metrics, tracing, and health infrastructure. Do not add a parallel telemetry stack.

### 21.1 Structured log fields

Where available:

- `request_id`, `correlation_id`, `trace_id`
- `organization_id`
- `actor_id` or system actor
- `journal_id`
- `source_type`, `source_operation_id`, `posting_kind`
- `period_id`
- `operation`
- `idempotency_outcome`
- `error_code`
- `duration_ms`

Never log secrets, authorization tokens, complete sensitive payloads, or unredacted PII.

### 21.2 Metrics

Recommended low-cardinality metrics:

- Posting success/failure count.
- Posting latency histogram.
- Duplicate source-posting count.
- Idempotency replay/conflict count.
- Unbalanced journal rejection count.
- Closed-period rejection count.
- Period lock wait duration.
- Deadlock/retry count.
- Reconciliation mismatch count by bounded kind.
- Snapshot generation duration/failure.
- Export job duration/failure.

Do not use organization ID, journal ID, user ID, customer ID, or source UUID as Prometheus labels.

### 21.3 Alerts

Alert on:

- Reconciliation mismatch above the agreed severity threshold.
- Persistent trial-balance imbalance (should be impossible for valid posted journals; indicates corruption/bug).
- Repeated source payload conflicts.
- Abnormal posting failure/deadlock spikes.
- Period close stuck in closing state.
- Outbox backlog/oldest event age if outbox exists.
- Repeated unauthorized export or cross-tenant access attempts.
- Snapshot lag beyond the reporting freshness objective.

## 22. Performance and scalability

- Keep journal writes append-oriented.
- Avoid broad organization-level locks.
- Lock only the period and subledger/account rows required for the invariant.
- Batch insert journal lines where the existing database layer supports it safely.
- Use stable cursor pagination.
- Aggregate trial balance with appropriate indexes and date/branch predicates.
- Generate snapshots asynchronously for expensive historical reporting.
- Do not join customer/supplier profile tables into every ledger report.
- Avoid N+1 account/branch lookups; batch-load references.
- Use `EXPLAIN (ANALYZE, BUFFERS)` on representative trial balance, general ledger, and subledger queries before adding indexes.
- Partitioning is not a default requirement. Consider it only after real volume/retention evidence and an explicit migration/foreign-key strategy.
- Read replicas may serve non-critical reports only if the existing architecture supports them; posting, close, idempotency resolution, and credit decisions use the primary database.

## 23. Error handling

Use the existing `errors` package and standard API error envelope.

Expected domain error categories (map to existing error codes):

| Condition | Expected classification |
|---|---|
| Invalid journal lines / unbalanced | Validation or domain rule error |
| Account inactive/not posting-enabled | Conflict/domain rule |
| Period not found in organization | Not found |
| Period closed | Conflict |
| Duplicate source with different payload | Conflict |
| Stale expected version | Conflict |
| Unauthorized permission | Forbidden |
| Cross-tenant/branch reference | Not found/forbidden per platform policy |
| Unsupported currency | Validation/domain rule |
| Already reversed | Conflict |
| Idempotency key reused with different request | Conflict |
| Deadlock/retry budget exhausted | Retryable service error |
| Database unavailable | Service unavailable |
| Reconciliation mismatch | Recorded exception; not a mutation success |

Never return SQL, stack traces, internal table names, or sensitive identifiers in public error messages.

## 24. Testing strategy

Tests must prove invariants under concurrency and failure, not only happy-path CRUD.

### 24.1 Unit tests

- Debit/credit validation.
- Exact decimal arithmetic and currency precision.
- Rounding policy.
- Account type/normal-side validation.
- Posting-rule resolution by effective date/version.
- Period date boundaries.
- Reversal line generation.
- Canonical source payload hashing.
- Permission-to-operation mapping.
- Branch dimension validation.

### 24.2 PostgreSQL integration tests

- Balanced journal posts successfully.
- Unbalanced journal cannot post.
- Journal with fewer than two lines cannot post.
- Zero, negative, over-precision, and overflow amounts are rejected.
- Cross-organization account/period/branch references fail.
- Posted journal/lines cannot be updated/deleted.
- Source unique key prevents duplicate journals.
- Same source key + different payload conflicts.
- Closed period rejects posting.
- Period close waits/serializes correctly with posting.
- Account deactivation prevents new posting but preserves history.
- Reversal creates opposite journal and preserves original.
- Reversal cannot be duplicated.
- Subledger projection updates atomically with journal.
- Rollback leaves neither operational mutation nor journal/source posting.

### 24.3 Concurrency tests

Run against real PostgreSQL, not only mocks:

1. Two requests post the same source operation simultaneously — exactly one journal.
2. Same idempotency key submitted concurrently — one execution and deterministic replay/in-progress result.
3. Two credit sales consume the same customer's remaining credit — policy is enforced without oversubscription.
4. Period close races with posting — no journal posts into a closed period.
5. Two reversals race — one reversal only.
6. Two account hierarchy edits attempt a cycle — invalid tree is rejected.
7. Missing subledger balance row is concurrently initialized — one logical row, no lost update.
8. Multiple branch postings proceed without organization-wide serialization.
9. Deadlock retry reruns the full operation without duplicate financial effect.

### 24.4 Property-based tests

For generated valid line sets:

- Sum debit equals sum credit for accepted journals.
- Reversal of a journal has equal total debit/credit and exact opposite account/amount dimensions.
- Reversal plus original nets to zero for the reversed dimensions.
- Decimal arithmetic never depends on binary floating-point behavior.
- Reordering lines does not change the journal's accounting totals.

### 24.5 Cross-module integration tests

- Sale checkout failure after Ledger validation rolls back sale, stock, payment allocation, journal, source posting, and outbox row.
- Purchase receipt failure during journal insert rolls back receipt, stock/cost layer, and journal.
- Inventory adjustment and loss journal commit atomically.
- Customer credit exposure changes in the same transaction as receivable posting.
- Duplicate source event does not create another journal.
- Outbox consumer duplicate delivery does not duplicate analytics/report projection.
- Customer/Supplier profile deletion/deactivation does not destroy financial history.
- Branch users cannot read/export another branch's journals.

### 24.6 Security tests

- Missing/invalid JWT.
- Missing permission.
- Permission mismatch (e.g., `ledger.view` cannot post).
- Branch scope escalation.
- Cross-tenant UUID probing.
- Export download after permission revocation.
- Manual journal maker-checker bypass.
- Mass assignment of `organization_id`, `posted_at`, `status`, or internal actor fields.
- SQL injection in search/sort/filter.
- CSV formula injection.
- Idempotency replay across different users/organizations.
- Service identity cannot invoke unrelated posting kinds.

### 24.7 Performance tests

- Journal posting latency under realistic line counts.
- Concurrent branch sales and postings.
- Trial balance query at realistic historical volume.
- Period close under concurrent posting.
- Subledger credit check under concurrent requests.
- Export workload does not block posting.

## 25. Failure scenarios and recovery

| Failure | Required behavior |
|---|---|
| DB failure before commit | Entire transaction rolls back |
| Ledger validation failure | Originating operation does not commit |
| Journal insert failure | Operational document/stock/payment changes roll back |
| Outbox insert failure | If outbox is part of required atomic contract, transaction rolls back; otherwise follow explicitly documented outbox policy |
| Event publish failure after commit | Journal remains committed; durable outbox retries |
| Client timeout after commit | Retry resolves same journal by idempotency/source identity |
| Duplicate request | Return original result or conflict on changed payload |
| Redis outage | Posting continues from PostgreSQL; no correctness change |
| Worker crash | Durable job/outbox state allows safe retry |
| Snapshot generation failure | Reports use valid previous snapshot or authoritative query; show freshness |
| Reconciliation mismatch | Create exception and alert; never auto-edit journals |
| Period close interrupted | Transaction rolls back or state remains explicitly recoverable; no ambiguous closed state |
| Account mapping missing | Reject posting; do not guess a fallback account |
| Unsupported currency | Reject; do not silently convert |

Recovery must preserve financial evidence. Never "fix" a discrepancy by directly editing posted journal lines or balances.

## 26. Implementation structure

Follow existing module conventions. Do not create a new architecture solely for Ledger.

A possible logical structure, to be adapted to the current repository:

```text
internal/modules/ledger/
  model.go
  dto.go
  mapper.go
  repository.go
  repository_postgres.go
  service.go
  handler.go
  routes.go
  validation.go
  posting/
    command.go
    rules.go
    engine.go
  errors.go
  ports.go
```

If the project uses a different established layout, keep that layout. Avoid splitting files into many abstractions before the module's real complexity requires it.

### 26.1 Layer responsibilities

- **model:** persistence/domain structures; shared BaseModel/audit conventions.
- **dto:** request/response DTOs only; never expose persistence models directly.
- **mapper:** explicit conversions; no hidden business logic.
- **repository:** organization-scoped persistence and transaction-aware locking.
- **service:** domain invariants, posting orchestration, authorization-independent business rules.
- **handler:** HTTP parsing, context extraction, DTO validation, response mapping.
- **routes:** existing protected middleware and permission wiring.
- **posting engine:** typed posting commands, rule resolution, balance checks.
- **ports:** narrow interfaces for audit, settings, currency, outbox, and caller integration where needed.

### 26.2 Repository method contract

Methods should make tenant scope and transaction usage explicit. Expected capabilities include:

- `LockPeriodForPosting`
- `LockPeriodForClose`
- `InsertJournal`
- `InsertJournalLines`
- `FindJournalByID`
- `FindSourcePosting`
- `InsertSourcePosting`
- `LockSubledgerBalance`
- `UpsertAndLockSubledgerBalance`
- `ReadTrialBalance`
- `ReadGeneralLedger`
- `CreateBalanceSnapshot`
- `InsertReconciliationRun`

These are conceptual responsibilities, not mandatory exact Go signatures. Match existing repository interfaces and query conventions.

Every method that reads/writes tenant-owned data must receive organization scope explicitly or obtain it through the established trusted transaction context. Do not rely on a developer remembering to add a tenant predicate in every ad hoc query.

## 27. OpenAPI and API documentation

- OpenAPI 3.1 YAML is the source of truth.
- Follow existing Redocly lint/bundle and `oapi-codegen` strict Gin generation.
- Do not add Swagger annotations.
- Do not hand-edit generated code.
- Define request/response schemas, enums, constraints, security requirements, and idempotency headers.
- Document which operations require `Idempotency-Key`.
- Document expected version/optimistic concurrency fields.
- Keep route registration and OpenAPI paths synchronized.
- Include examples for balanced manual journal, post, reversal, period close, and trial balance.
- Do not expose internal source-posting APIs as public endpoints unless explicitly approved.

## 28. Migration and rollout strategy

1. Inspect current migrations and any existing ledger tables before writing new migrations.
2. Confirm organization, branch, currency, BaseModel, audit, and transaction conventions.
3. Add schema with tenant-safe foreign keys, checks, unique keys, and indexes.
4. Seed a minimal approved chart of accounts only through the project's seed conventions; make seeding idempotent and organization-aware.
5. Do not silently seed jurisdiction-specific tax or accounting policy without approved source data.
6. Add posting-rule versions only after account mappings are reviewed.
7. Add database immutability/balance guards and integration tests.
8. Deploy schema before enabling posting routes.
9. Verify trial balance and source-deduplication behavior in a non-production environment.
10. Enable source-module posting one workflow at a time.
11. Reconcile opening balances and legacy financial data before production cutover.
12. Keep a rollback plan for schema/application deployment; never roll back by deleting committed financial evidence.

### 28.1 Opening balances

Opening balances are accounting events, not arbitrary initial values.

- Require an approved opening-balance journal.
- Require an explicit as-of date in an open period.
- Require balanced lines and supporting evidence.
- Require separate branch dimensions where the opening balance is branch-attributed.
- Record source identity such as `opening_balance` plus a stable migration/import operation ID.
- Re-import must be idempotent.
- Reconcile the resulting trial balance before enabling live operations.

## 29. Decisions requiring explicit confirmation before implementation

The source contract does not specify every accounting policy. Do not guess these silently.

| Decision | Required confirmation |
|---|---|
| Functional currency | Exact organization setting/reference and whether it is immutable after first posting |
| Currency precision | MasterData field and supported minor units |
| Rounding | Accounting rounding mode and where tax/discount rounding occurs |
| Fiscal calendar | Calendar year vs configurable fiscal year; timezone/date boundary |
| Account seed | Approved chart of accounts and jurisdiction-specific accounts |
| Manual journal approval | Maker-checker policy and thresholds, if any |
| Credit policy | Customer credit limit ownership, exposure formula, reservation rules |
| Branch accounting | Which postings are branch-level vs organization-level |
| Period close checklist | Required reconciliations and who can certify them |
| Reopen policy | Approval/separation-of-duties and audit requirements |
| Multi-currency | Explicitly out of initial scope unless approved |
| Outbox | Confirm existing durable outbox contract and event naming |
| Balance snapshots | Snapshot cadence, freshness objective, and rebuild procedure |
| Legacy opening balances | Source data, as-of date, approval, and reconciliation process |

Until confirmed, implement safe defaults: one functional currency, no unsupported foreign-currency posting, no closed-period bypass, no arbitrary manual posting to control accounts, and no silent account-mapping fallback.

## 30. Implementation checklist and order

| Order | Work item | Completion evidence |
|---:|---|---|
| 1 | Repository and migration audit | Existing tables, transaction, middleware, audit, outbox, and money conventions documented |
| 2 | Freeze accounting policy | Currency, rounding, fiscal calendar, account seed, branch rules approved |
| 3 | Account and period schema | Tenant-safe constraints, indexes, hierarchy and overlap tests |
| 4 | Journal schema and guards | Exact money, line constraints, balanced-posting guard, immutability tests |
| 5 | Source deduplication | Unique source key, payload conflict, concurrent duplicate tests |
| 6 | Posting engine | Typed commands, mapping versions, validation, transaction propagation |
| 7 | Manual journal workflow | Draft, post, reversal, permissions, audit, maker-checker if required |
| 8 | Period lifecycle | Close/reopen locks, checklist, concurrency tests |
| 9 | Subledger exposure | Atomic update, row locking, rebuild and reconciliation |
| 10 | Trial balance/general ledger | Tenant/branch filters, stable pagination, performance plans |
| 11 | Outbox/reconciliation | Only existing/approved infrastructure; idempotent consumers |
| 12 | Purchase integration | Receipt accrual, invoice recognition, supplier settlement patterns |
| 13 | Inventory integration | Stock valuation, adjustment/write-off reconciliation |
| 14 | Sale integration | Revenue/tax, COGS, receivable/payment posting |
| 15 | Security/performance hardening | Cross-tenant, branch, concurrency, load and export tests |
| 16 | Production readiness | Opening balance, monitoring, runbook, reconciliation sign-off |

## 31. Definition of done

Ledger is not production-ready until all of the following are true:

- [ ] Every posted journal is balanced and has at least two valid lines.
- [ ] Posted journals and lines cannot be edited or deleted.
- [ ] Reversal creates a new immutable journal.
- [ ] Source posting uniqueness is enforced by PostgreSQL.
- [ ] Same source retry cannot create a duplicate financial effect.
- [ ] Changed-payload replay is rejected.
- [ ] Posting and period close are concurrency-safe.
- [ ] Credit exposure cannot be oversubscribed by concurrent requests.
- [ ] Every repository query is organization-scoped.
- [ ] Branch visibility is enforced for reads, writes, and exports.
- [ ] System postings use approved versioned account mappings.
- [ ] No monetary calculation uses floating-point types.
- [ ] Operational mutation and required journal commit atomically.
- [ ] Existing middleware, RBAC, audit, idempotency, transaction, errors, and observability are reused.
- [ ] Redis is not authoritative for accounting decisions.
- [ ] Reconciliation reports differences without mutating financial history.
- [ ] OpenAPI contract and generated handlers are synchronized.
- [ ] Integration, concurrency, property, security, and failure tests pass.
- [ ] Opening balances and cutover have been reconciled and approved.
- [ ] Operational runbook covers failed posting, duplicate source, period close, reversal, and reconciliation exception handling.

---

**Final engineering rule:** Ledger must guarantee that a committed business operation cannot leave its required financial posting behind, and a retry cannot create the same financial effect twice. PostgreSQL constraints, a shared transaction boundary, immutable journals, durable source identity, explicit locks, and strict authorization provide that guarantee—not Redis, UI validation, or best-effort event delivery.
