# CUSTOMER_CONTRACT.md

> **Project:** Pharmaciano:ERP — Go backend  
> **Module:** `internal/modules/customer`  
> **Status:** Architecture and implementation contract  
> **Primary source:** Astra `answer_in_markdown.md`, Customer module section and cross-module dependency/workflow decisions  
> **Audience:** Backend implementation, API/OpenAPI, database, security, QA, operations  
> **Rule:** This contract governs implementation unless a later approved ADR or frozen OpenAPI contract explicitly supersedes it.

---

## 1. Purpose

The Customer module owns the minimum identity needed to recognize a customer, manage customer contact details, control branch visibility, and record communication consent. It provides safe identity references to Sale and Notification without becoming a second owner of sales, accounting, medical, or loyalty facts.

This is a multi-tenant ERP module. Correctness depends on organization isolation, deliberate branch access, privacy minimization, and respecting the ownership boundaries of the existing modules.

### 1.1 Non-negotiable outcomes

- Every customer read and write is organization-scoped.
- A branch does not gain access to a customer's identity or history merely because the customer exists in the same organization.
- Walk-in sales can use `customer_id = NULL` unless a regulated or explicitly configured workflow requires an identified customer.
- Sale owns purchase/sales history; Ledger owns credit exposure and receivable truth.
- Consent is recorded as append-only evidence and checked again when a communication is sent.
- Sensitive personal data is not returned by default, included in logs, or cached in Redis by default.
- Customer deletion is archival/controlled anonymization, not destructive deletion of posted business references.
- Customer exports are authorized, purpose-bound, asynchronous when large, private, auditable, and tenant-scoped.
- The module reuses the existing auth, RBAC, middleware, errors, audit, observability, pagination, UUID, migration, and OpenAPI infrastructure.
- No duplicate framework or module-specific replacement for shared infrastructure is introduced.

---

## 2. Source-derived decisions and items requiring repository verification

### 2.1 Decisions directly aligned with Astra

| Topic | Contract decision |
|---|---|
| Purpose | Minimal counterparty identity, communication consent, controlled visibility |
| Ownership | Organization-owned customer identity; branch visibility is explicit |
| Medical data | Customer is not a medical-record system |
| Walk-in sale | Customer is optional unless the business/regulatory workflow requires one |
| Credit | Credit limits/exposure belong to Ledger subledger controls |
| Sales history | Sale owns the sales history |
| Receivable statement | Ledger owns the financial statement and balance semantics |
| Contacts | Normalized contact identity; shared family phone numbers are allowed |
| Consent | Append-only history with purpose, channel, decision, timestamp, actor and evidence reference |
| Privacy | Collect minimal PII; redact projections; audit sensitive reads and exports |
| Cache | Avoid PII caching initially |
| Failure handling | Optional communication is suppressed if consent cannot be safely verified; customer service failure must not corrupt a posted sale |
| Performance | Indexed contact lookup, bounded search, no unbounded history joins in customer lists |
| Security | Prevent PII enumeration, cross-branch medical inference, bulk export abuse and consent bypass |

### 2.2 Verify before implementation

The following cannot be safely assumed from the model sketch alone:

1. The actual deployed `customers` table, existing customer rows, constraints, indexes, and inbound foreign keys.
2. Whether customer tables were already created by an applied migration or are still planned.
3. The exact current `BaseModel`, audit columns, response envelope, pagination API, error catalog, auth context, and organization/branch middleware APIs.
4. The exact `rate_export` implementation and whether it supports both request creation and download endpoints.
5. Whether a durable worker, export-job store, private object storage, encryption/key-management service, and transactional outbox are already available.
6. The frozen OpenAPI route/methods. The endpoint list supplied with this request uses `GET /customers/export?format=csv|xlsx`, while Astra describes a purpose-bound `POST /customers/exports`. Resolve this in the OpenAPI contract before implementation; do not ship two competing export APIs by accident.
7. Whether Ledger exposes a stable customer-subledger/credit-control service contract, and whether loyalty points have an existing owner.
8. Whether an existing privacy, retention, consent, or data-export policy has been approved.

**Do not invent infrastructure or claim it exists.** Reuse existing project facilities where confirmed; document gaps and create an ADR when a required capability does not yet exist.

---

## 3. Module ownership and boundaries

### 3.1 Customer owns

- Customer identity and organization association.
- Customer code generation and uniqueness.
- Minimal display identity.
- Contact records and verification metadata.
- Address fields only where justified by an approved workflow.
- Explicit branch-visibility relationships.
- Communication consent evidence by purpose and channel.
- Customer lifecycle state (`active`/archived).
- Privacy-aware read projections and customer-specific export orchestration.
- Customer identity audit evidence and retention/anonymization workflow coordination.

### 3.2 Customer does not own

| Concern | Owner / rule |
|---|---|
| Completed sales, invoices, returns, purchase history | `sale` |
| Accounts receivable, credit exposure, authoritative credit limit, current due | `ledger` |
| Journal entries, financial statements, outstanding balance calculations | `ledger` |
| Medicines, prescriptions, diagnosis, clinical notes, dispensing evidence | Relevant regulated workflow / `sale`; do not place them in generic customer notes |
| Loyalty points and redemption balance | A separately approved loyalty subsystem/ledger; do not treat a mutable customer integer as authoritative |
| Notifications and delivery retries | `notification` |
| User authentication, login credentials, sessions | `auth` / `user` |
| Tenant/branch authorization | Existing organization/branch context and RBAC middleware |
| Physical database backups and restore operations | Operational `backup` module / platform operators |
| General analytics and official reports | `analytics` / `report`, consuming source-owned contracts |

### 3.3 Dependency direction

- `sale` may depend on Customer identity through a stable service/contract.
- `notification` may consume consent decisions through a stable contract and must re-check before delivery.
- Customer must not import Sale or Ledger repository implementations.
- Customer must not create its own journal, sale, invoice, payment, or credit-balance records.
- Customer should not call Notification synchronously in the middle of a customer database transaction.
- Keep module imports directed inward through stable interfaces/contracts; avoid cycles.

---

## 4. Tenancy and visibility model

### 4.1 Organization scope

Customer identity belongs to one organization:

- `organization_id` is mandatory and must be derived from the authenticated, authorized request context.
- Never accept a client-provided `organization_id` as authority.
- Every repository method must require the resolved `orgID`, including reads by UUID.
- UUID unpredictability is not authorization.
- Enforce tenant ownership in SQL predicates and, where practical, relational constraints.
- Do not return `404` versus `403` in a way that leaks cross-tenant record existence; follow the project's established resource-concealment convention consistently.

### 4.2 Branch visibility

A customer may be visible to one or more branches through an explicit relationship. Organization membership alone does not imply that every branch can inspect every customer or infer the customer's transactions.

Recommended logical entity:

`customer_branch_visibility(organization_id, customer_id, branch_id, created_at, created_by)`

Requirements:

- The relationship must prove that customer and branch belong to the same organization.
- Use composite uniqueness/foreign-key patterns or equivalent service-enforced integrity to prevent cross-organization links.
- Scope branch-level list/search/detail operations to the caller's authorized branch set.
- Creating a customer and its initial visibility relationship should be atomic.
- Granting organization-wide visibility must be an explicit policy/permission, not a hidden default.
- A branch visibility grant is not permission to read another branch's sales or ledger history.
- Do not silently broaden visibility when a customer is used in a sale at another branch. The sale workflow must follow the approved policy and record any deliberate grant.
- Avoid exposing branch names, transaction counts, or existence signals that let an unauthorized branch infer activity elsewhere.

### 4.3 Authorization context

Use the existing middleware/context for:

- authenticated actor ID;
- organization ID;
- authorized branch IDs / active branch;
- role and permissions;
- request/correlation ID;
- audit context;
- rate-limit identity.

The service must reject requests where required context is missing or inconsistent. Do not trust headers or body fields as substitutes for authenticated claims unless the existing trusted middleware explicitly establishes them.

---

## 5. Domain model and field decisions

The supplied model is a useful sketch, but it must not be copied unchanged without resolving ownership, data sensitivity, and monetary precision.

### 5.1 Customer identity

Logical fields:

| Field | Required? | Contract |
|---|---:|---|
| `id` | Yes | UUID from the established UUID utility/convention |
| `organization_id` | Yes | Server-derived tenant scope |
| `code` | Policy-dependent | Generated by server; unique within organization; immutable after issuance unless a controlled migration says otherwise |
| `name` / `display_name` | Yes | Bounded string; normalized for search without destroying display form |
| `is_active` | Yes | Explicit lifecycle state |
| `created_at`, `updated_at`, `deleted_at` | Yes per shared model convention | Reuse `common.BaseModel` |
| `created_by`, `updated_by` | Follow shared convention | Reuse `common.AuditColumns` or the actual established audit pattern |
| `version` | Recommended if not already provided by shared model | Optimistic concurrency token for updates |

Use the repository's actual `BaseModel` and audit conventions. Do not add duplicate timestamps, a second ID field, or a competing audit abstraction.

### 5.2 Contact records

Prefer a dedicated contact model/table when multiple phone numbers, shared family contacts, verification, contact purpose, or consent history are required.

Logical fields:

- `id`, `organization_id`, `customer_id`;
- `type` (`phone`, `email`, or an approved finite set);
- normalized value stored securely;
- optional lookup hash for equality lookup;
- `verified_at`, verification method/status where supported;
- contact purpose, if required by an approved product workflow;
- created/updated timestamps and actor metadata.

Rules:

- Do not impose global uniqueness on phone or email. Family members may share a phone.
- Do not use unverified contact data as proof of identity or consent.
- Normalize phone numbers using a single documented library/rule. Do not assume all inputs are Bangladeshi numbers without validating international requirements.
- Normalize email for lookup consistently, while preserving a display form where needed.
- Avoid storing the same contact value redundantly in both the customer row and a contacts table.
- If the existing database contract forces `phone` and `email` onto `customers`, document the compromise and keep normalization, privacy, and uniqueness rules explicit. Do not create two competing sources of truth.

### 5.3 Personal and health-adjacent fields

The proposed model includes `DateOfBirth`, `Gender`, `BloodGroup`, `Address`, and `Notes`. These fields are not automatically justified because a pharmacy system might someday use them.

- **Date of birth:** omit unless an approved business or regulated workflow requires it. If required, collect the minimum precision needed; do not infer age or eligibility from unverified values.
- **Gender:** omit unless a defined workflow requires it. If retained, use an approved vocabulary and allow unknown/not provided; do not infer it from name or appearance.
- **Blood group:** exclude from the generic customer profile by default. It is health-related and generally unnecessary for ordinary pharmacy identity. If a legitimate approved use exists, place it behind a specific data-purpose, permission, retention, and audit decision.
- **Address/city:** collect only for a defined receipt, delivery, billing, or regulatory purpose. Restrict full-address exposure to the roles/workflows that need it.
- **Notes:** do not allow arbitrary clinical notes, diagnoses, prescriptions, symptoms, or sensitive free text. Prefer omitting notes. If retained, enforce a strict length limit, warn users against sensitive content, redact access, and establish retention/audit rules.

Do not add medical history, diagnosis, prescription images, national identity numbers, or unrelated personal attributes to this module without a separate approved domain and privacy review.

### 5.4 Credit and monetary fields

The proposed fields `CreditLimit float64` and `CurrentDue float64` must **not** be treated as authoritative customer financial state.

- `CurrentDue` is a derived/ledger-owned financial fact and must not be independently edited in Customer.
- `CreditLimit` is a credit policy/control owned by Ledger's customer subledger or a specifically approved credit-policy component.
- If compatibility requires a customer API to show these values, use a delegated Ledger read contract and the appropriate Ledger permission. Do not write a duplicate customer-table balance.
- Use exact decimal/numeric or the project's approved money type; never use binary `float64` for financial amounts.
- Currency must be explicit where balances are exposed.
- Credit checks must be enforced by Ledger/Sale at the transaction boundary, not by trusting a cached Customer field.
- A stale balance in a customer profile must never authorize a credit sale.

### 5.5 Loyalty points

`LoyaltyPoints int` is not safe as an untracked mutable field if points can be earned, redeemed, reversed, or audited.

- Do not ship a writable points total until ownership and a ledger/history model are approved.
- If loyalty is in scope, define an append-only points transaction model, idempotent earn/redeem operations, reversal rules, expiry policy, and Sale integration in a separate contract.
- The displayed balance must be derived from that source or be a rebuildable projection.
- If loyalty is not implemented, omit it from schema and DTOs rather than exposing a misleading placeholder.

### 5.6 Code generation

- Code is generated server-side.
- Use a database-backed sequence/counter or another concurrency-safe generator already used by the project.
- Unique constraint on `(organization_id, code)` when code is non-null.
- Handle concurrent creation without a read-then-write race.
- Never use a phone number, email, date of birth, or other PII as the customer code.
- Do not reveal internal sequence details unnecessarily.

---

## 6. Database and migration contract

### 6.1 Pre-migration inspection

Before writing migrations:

1. Inspect the actual `customers` table and all related migrations.
2. Check whether the current model/table already contains customer rows.
3. Inspect inbound FKs from sales, invoices, returns, ledger, or other modules.
4. Identify duplicate customer identities and ambiguous cross-branch ownership.
5. Confirm whether existing rows have a reliable organization association.
6. Inspect current soft-delete, audit-column, UUID, and timestamp conventions.
7. Plan a backfill that never assigns legacy rows to an arbitrary organization.
8. Verify migration status in the target environment before applying changes.

Never rewrite an already-applied migration to retrofit the contract. Add a new forward migration with a reviewed backfill and rollback strategy.

### 6.2 Logical schema

The exact table names and columns must match the existing migration conventions. The following is a logical target, not permission to create duplicates:

| Table | Responsibility | Key constraints/indexes |
|---|---|---|
| `customers` | Minimal organization-owned identity and lifecycle | FK to organization; unique `(organization_id, code)` where code exists; tenant-safe indexes |
| `customer_contacts` | Contact values and verification metadata | FK to customer; organization consistency; bounded type/value; lookup index; no global phone uniqueness |
| `customer_branch_visibility` | Explicit branch grants | Unique `(customer_id, branch_id)`; organization-consistent FKs; index `(organization_id, branch_id, customer_id)` |
| `customer_consents` | Append-only purpose/channel decision evidence | FK to customer; organization scope; index `(organization_id, customer_id, purpose, channel, recorded_at DESC)` |
| `customer_export_jobs` | Only if no shared export-job facility exists | Tenant/scope, requester, purpose, filters, status, expiry, artifact metadata; no raw PII in logs |
| `customer_identity_aliases` | Optional deduplication/merge support | Only if an approved merge process requires it; never add speculatively |

Do not create `customer_export_jobs` if a suitable shared report/export job model already exists. Do not create an independent customer consent table if the repository has an approved shared consent model that meets the same append-only and scoping guarantees.

### 6.3 Constraints and indexes

At minimum, evaluate:

- FK from customer to organization.
- Unique `(organization_id, code)` for non-null generated codes.
- Index `(organization_id, is_active, created_at DESC, id)` or a query-plan-supported equivalent.
- Indexes for organization-scoped name search only if supported by the chosen search implementation.
- Contact lookup index scoped by organization; if a keyed hash is used, index `(organization_id, contact_type, lookup_hash)`.
- Index `(organization_id, branch_id, customer_id)` on visibility links.
- Consent history index `(organization_id, customer_id, purpose, channel, recorded_at DESC)`.
- Indexes that match actual export/status jobs if module-owned jobs are justified.
- Partial indexes for active records only where query plans demonstrate benefit.
- `CHECK` constraints for bounded enum/status values and nonnegative versions where appropriate.

Avoid speculative indexes. Validate with representative query plans and realistic tenant data.

### 6.4 Contact encryption and lookup

Contact data is personal data. Choose a storage strategy based on existing key management:

- Prefer authenticated encryption using a managed key or envelope-encryption design if the platform already provides one.
- Store key version/metadata needed for safe rotation; never store encryption keys beside ciphertext.
- For exact-match search on encrypted values, use a keyed HMAC/hash with a secret held outside the database. A plain unsalted hash of phone numbers/emails is vulnerable to dictionary enumeration.
- Separate encryption and lookup keys.
- Define rotation, re-indexing, backup recovery, and key-loss procedures before enabling this design.
- If proper key management is not available, do not invent an ad hoc cryptosystem. Minimize stored contact fields, restrict access, and document the residual risk and rollout blocker.

Encryption at rest for a database volume does not replace application-level access controls, logging redaction, or backup encryption.

### 6.5 Delete and retention behavior

- Use the existing soft-delete/archive convention.
- Do not cascade-delete posted sales, ledger references, invoices, or audit evidence.
- Foreign keys from immutable business records should normally restrict destructive deletion.
- Anonymization is a separate controlled operation after retention/legal checks; it is not the normal `DELETE` endpoint.
- Anonymization must preserve non-PII financial/business records and referential integrity.
- Do not anonymize while legal hold, active dispute, open credit, or another retention obligation applies.
- Record actor, reason, policy/version, scope, and outcome in audit evidence.
- Any reversible identity merge/anonymization design must be approved explicitly; do not quietly retain a hidden PII copy.

---

## 7. API contract

Base path: `/api/v1`. All routes must use the existing Gin router, API versioning, response envelope, error mapping, OpenAPI 3.1 contract-first process, and strict handler conventions.

### 7.1 Required routes

The following preserves the user-supplied CRUD and nested routes. Resolve the export route discrepancy in §7.7 before implementation.

| Method & path | Rate policy | Permission | Behavior |
|---|---|---|---|
| `POST /api/v1/customers` | `rate_auth_write` | `customers:create` | Create minimal customer identity and initial visibility |
| `GET /api/v1/customers` | `rate_auth_read` | `customers:view` | List only visible customers; bounded search and filters |
| `GET /api/v1/customers/{id}` | `rate_auth_read` | `customers:view` | Scoped, redacted detail |
| `PATCH /api/v1/customers/{id}` | `rate_auth_write` | `customers:update` | Allow-listed partial update with concurrency control |
| `DELETE /api/v1/customers/{id}` | `rate_auth_write` | `customers:delete` | Archive/soft-delete; never hard-delete posted references |
| `GET /api/v1/customers/{id}/sales` | `rate_auth_read` | `sales:view` | Delegate to Sale; preserve Sale's branch authorization |
| `GET /api/v1/customers/{id}/ledger` | `rate_auth_read` | `ledger:view` | Delegate to Ledger; preserve Ledger's scope and permissions |
| Customer export request/download | `rate_export` or existing dedicated policy | `customers:export` plus any required source-data permissions | Asynchronous, private, audited export |

The exact rate keys must be verified against the existing rate-limit registry. Do not silently add a new rate key or assume that a name is implemented.

### 7.2 Recommended consent endpoint

Astra specifies recording consent through:

`POST /api/v1/customers/{id}/consents`

- Permission: `customers:update` plus any separate consent-management permission if approved by the permission catalog.
- Rate: `rate_auth_write`.
- Idempotency: required for a command that creates consent evidence.
- Consent evidence is append-only; correcting an earlier decision creates a new record rather than rewriting the old decision.

Add the route to the frozen OpenAPI contract before implementation. If the current API contract uses another approved route, keep one canonical route.

### 7.3 Query contract

The user-supplied list filter includes `?q=phone|name&is_active=&min_due=`.

Recommended interpretation:

- `q`: bounded search across allowed customer name and contact lookup paths. Do not allow arbitrary SQL, column names, or regular expressions.
- `is_active`: strict boolean parsing; reject malformed values rather than silently treating them as false.
- `min_due`: **not a Customer-owned filter**. If retained for compatibility, implement it only through a Ledger-owned scoped query/read contract, with `ledger:view` authorization and currency semantics. Do not filter against a stale `customers.current_due` column.
- Add pagination using the existing `pkg/pagination` conventions.
- Add a maximum page size and maximum search length.
- Use deterministic ordering with a stable tie-breaker.
- Do not support arbitrary client-selected sort columns or unbounded `limit`.
- Avoid returning total counts if count queries are expensive or leak information; follow the project's existing pagination response contract.

### 7.4 DTO and validation rules

- Use dedicated create, patch, list-query, detail, list-item, consent-command, and export DTOs.
- Never bind request JSON directly into a database model.
- `organization_id`, audit actor IDs, generated code, lifecycle timestamps, due balances, loyalty balances, and other server-owned fields must not be client-writable.
- Patch must distinguish omitted fields from explicit null where the API semantics require clearing a value.
- Reject unknown fields if the established strict-decoding policy supports it.
- Trim and validate display strings; enforce database-compatible length limits.
- Validate contact syntax and normalize before persistence.
- Validate enum values against a finite allow-list.
- Bound notes/address/contact lengths; never accept arbitrarily large strings.
- Reject NaN/Infinity and all floating-point monetary inputs if legacy DTOs still expose numeric amounts.
- Return the established validation error envelope with field-safe messages; do not echo submitted PII into logs or error telemetry.
- Use mappers only when needed to separate storage and API projections. Do not add empty `mapper`/`domain` directories solely for appearance.

### 7.5 Create/update/archive semantics

**Create**
- Derive tenant and actor from context.
- Validate branch access and visibility grant.
- Generate customer code safely.
- Persist customer and initial visibility in one transaction.
- Persist mandatory audit evidence in the same transaction if the audit contract requires atomic evidence.
- Use an idempotency key if the existing middleware/command infrastructure supports it; the database must still protect code uniqueness.

**Patch**
- Apply an allow-list of editable fields.
- Enforce organization scope and current visibility.
- Use optimistic concurrency (version or equivalent) where available.
- Do not let generic profile updates change consent history, financial values, loyalty balances, organization ownership, or branch grants.
- Audit sensitive identity/contact changes with before/after values redacted or minimized according to the audit contract.

**Archive**
- Check tenant scope and lifecycle rules.
- Preserve posted business references.
- Do not archive automatically merely because one branch loses visibility.
- Decide whether active credit/open ledger obligations block archival; at minimum, archival must not erase or bypass them.
- Use a distinct approved reactivation flow if reactivation is permitted.
- Record actor and reason where the established audit policy requires them.

### 7.6 Sales and ledger subresources

`GET /customers/{id}/sales` and `GET /customers/{id}/ledger` are delegated read operations, not customer-owned data queries.

- Authorize `sales:view` or `ledger:view` in addition to any baseline customer permission required by policy.
- The delegated module must enforce its own organization, branch, and record-level scope.
- Customer visibility does not grant access to all sales/ledger rows for that customer.
- Do not join full sales/ledger history into customer list/detail queries.
- Do not import another module's repository directly. Use an approved service interface/read contract.
- Handle a missing/inactive customer according to the shared resource lifecycle policy without leaking cross-tenant existence.
- If the delegated module is unavailable, return a controlled dependency error. Do not return an invented empty statement or fabricate zero due.

### 7.7 Export route and artifact contract

Astra describes a purpose-bound `POST /customers/exports`; the supplied endpoint list describes `GET /customers/export?format=csv|xlsx` and calls it an async job. A GET request should not create a durable job or mutate server state. Resolve this route/method mismatch in OpenAPI before coding.

**Recommended canonical shape:**

1. `POST /api/v1/customers/exports` — creates an export job; `customers:export`; `rate_export`; requires a purpose and bounded authorized filters; idempotent.
2. `GET /api/v1/customers/exports/{job_id}` — checks status; same tenant/authorized requester or explicitly granted operator policy.
3. `GET /api/v1/customers/exports/{job_id}/download` — re-authorizes at download time and returns a short-lived private artifact link or streams the artifact using the established secure pattern.

If compatibility mandates the user-supplied GET endpoint, it should be a read/status operation only, not a job-creation command. Do not support two competing job-creation semantics.

Export requirements:

- Re-check organization and branch scope at job creation, during data extraction, and at download.
- Require an explicit purpose/reason and a bounded filter set.
- Apply field-level redaction; export only approved fields.
- Never export blood group, arbitrary notes, unverified sensitive contacts, or other sensitive fields by default.
- Enforce source permissions where export includes Sale or Ledger data; `customers:export` alone must not imply `sales:view` or `ledger:view`.
- Generate asynchronously for large datasets; cap rows, file size, execution time, and concurrent jobs.
- Store artifacts in private storage; no public bucket or predictable public URL.
- Use short-lived, scoped, single-purpose signed URLs if that is the approved storage pattern. A signed URL is a bearer credential and must not be logged.
- Encrypt artifacts at rest and in transit according to platform standards.
- Set an explicit expiry and delete expired artifacts safely.
- Revoke or deny downloads when the requester loses required access, where the job/artifact design supports reauthorization.
- Prevent spreadsheet formula injection for CSV/XLSX fields beginning with formula-triggering characters (for example `=`, `+`, `-`, `@`, tab, or carriage return); apply format-appropriate safe encoding.
- Record requester, organization, authorized branch scope, purpose, filters or filter hash, field set/version, row count, creation/completion/expiry times, checksum, outcome, and download events.
- Do not place raw PII, full filter payloads, signed URLs, or artifact content in application logs.
- Export failure must not affect POS, sales, ledger, or customer transactions.
- Do not use a physical database backup as a tenant export.

---

## 8. Authentication, authorization, and middleware integration

### 8.1 Reuse existing middleware

Use the project's established middleware and order, typically including:

1. request ID / correlation context;
2. authentication and session/token validation;
3. tenant/organization resolution;
4. active branch and branch-access validation where applicable;
5. RBAC permission enforcement;
6. rate limiting;
7. idempotency for supported commands;
8. audit context / response handling;
9. handler/service execution.

The actual router's established order is authoritative. Do not duplicate authentication, RBAC, tenant resolution, audit, or rate-limiter implementations inside Customer.

### 8.2 Permission matrix

Use the exact permission naming convention already seeded by the repository. The user's endpoint table uses colon syntax (`customers:view`); some older project material uses dot syntax (`customers.view`). **Do not seed both styles as independent permissions.** Confirm the existing RBAC canonical format and map the table accordingly.

| Capability | Permission / additional rule |
|---|---|
| List/detail | `customers:view` |
| Create | `customers:create` |
| Update identity/contact | `customers:update` |
| Archive | `customers:delete` |
| Export | `customers:export` plus source permissions for any non-customer data |
| Sales history | `sales:view` and Sale-owned scope |
| Ledger statement | `ledger:view` and Ledger-owned scope |
| Consent changes | `customers:update` or a separately approved narrower permission |
| Manage branch visibility | A separately defined grant/visibility permission if the RBAC model requires separation of duties |
| View sensitive contact/address fields | Field-level policy or narrower permission if the application supports it |
| Anonymize/retention override | Privileged, separately approved operation; not implied by `customers:delete` |
| Physical backup/restore | Operational backup permissions and operator capability; never implied by any customer permission |

Permission names in this document describe intended capability, not proof that each permission is already seeded. Update the canonical permission catalog, seed data, and OpenAPI security declarations together.

### 8.3 Object- and field-level authorization

Route permission alone is insufficient.

- Scope every lookup by `organization_id` and allowed visibility.
- Re-check access to the specific customer before returning detail or changing state.
- Filter fields according to the caller's need and permission.
- Do not return contact ciphertext, lookup hashes, encryption metadata, internal audit metadata, or raw consent evidence in normal DTOs.
- Avoid separate “does customer exist?” endpoints that enable enumeration.
- Re-authorize delegated Sale/Ledger data independently.
- Do not let export, search, or bulk endpoints bypass detail-level policy.

### 8.4 Rate limiting and abuse controls

- Use `rate_auth_read`, `rate_auth_write`, and `rate_export` only if these are the actual configured keys.
- Apply stricter throttling to contact lookup and bulk export if the existing rate limiter supports route-specific policies.
- Avoid rate-limit keys containing raw phone/email or other PII.
- Rate limiting is a defense-in-depth control, not a replacement for authorization.
- Monitor enumeration-like patterns, repeated denied lookups, and unusually large exports.

---

## 9. Audit and privacy evidence

Use the existing audit port/contract. Do not build a second audit subsystem.

### 9.1 Audit events

At minimum, consider audit evidence for:

- customer creation and archive/reactivation;
- identity/contact changes and verification;
- branch visibility grants/revocations;
- consent grant, withdrawal, and correction;
- sensitive detail/contact/address reads where policy requires it;
- export request, execution outcome, download, expiry/deletion;
- retention review, anonymization, merge, or privileged override;
- denied sensitive operations and suspected enumeration, using the project's security-event policy.

### 9.2 Audit payload minimization

- Never log passwords, tokens, encryption keys, contact plaintext, full addresses, signed URLs, or export contents.
- Use IDs, action, reason code, scope, outcome, timestamps, request ID, and redacted change summaries.
- Before/after evidence should be field-aware: preserve accountability without copying unnecessary PII into the audit store.
- Append-only consent evidence must not be silently edited or deleted.
- If mandatory audit persistence fails, follow the shared audit contract. For operations that require atomic audit, roll back the business transaction rather than commit without evidence.

### 9.3 Privacy and retention

Define a documented retention schedule for identity/contact records, consent evidence, export artifacts, and audit events. This contract does not invent a legal retention period.

- Provide a controlled way to review data subject export/anonymization requests if required by the approved policy.
- Retention deletion must consider open receivables, disputes, legal holds, and statutory records.
- Preserve financial documents and audit evidence as required; anonymize only fields that may lawfully be removed.
- Avoid retaining extra copies of PII in logs, cache, job payloads, analytics events, and exported artifacts.
- Do not include medical details in Customer events or analytics.

---

## 10. Consent and Notification integration

Consent is purpose- and channel-specific. A single boolean such as `marketing_opt_in` is insufficient if the product supports multiple channels or purposes.

### 10.1 Consent evidence

A consent record should capture, where applicable:

- organization and customer IDs;
- purpose (finite allow-list);
- channel (finite allow-list);
- granted/withdrawn decision;
- recorded timestamp;
- actor/source (customer, authorized staff, import, etc.);
- evidence reference or policy/version;
- optional expiry/withdrawal metadata where approved.

Do not store unnecessary full message contents or sensitive documents as consent evidence.

### 10.2 Delivery-time checks

- A consent grant is not a permanent authorization to send every message.
- Notification delivery must re-check the latest applicable consent immediately before sending.
- If consent lookup fails or its state is ambiguous, suppress optional communication and emit a safe operational signal.
- Consent revocation must win over queued-but-not-yet-sent optional messages.
- Required transactional/regulatory communications must follow a separately approved legal/policy classification; do not misclassify marketing as transactional.
- Customer service must not call a notification provider while holding a customer database transaction.
- Delivery failures must not roll back a committed customer update.

### 10.3 Events

A `CustomerConsentChanged` event may be emitted if needed for invalidation or downstream processing. If the event is required for correctness, use the repository's transactional outbox contract; do not assume Redis or an in-memory goroutine guarantees delivery.

Events should contain identifiers, purpose/channel, decision/version, timestamp, and correlation metadata—not contact plaintext or medical data. Consumers must still re-check current consent before delivery.

---

## 11. Transactions, concurrency, and idempotency

### 11.1 Transaction boundaries

- Create customer + initial visibility + mandatory audit/outbox rows: one transaction where the shared contracts require atomicity.
- Patch identity: one short transaction with optimistic concurrency and audit.
- Consent command: append consent evidence and required audit/outbox evidence atomically.
- Export: persist job metadata in a short transaction; perform extraction/rendering/storage outside that transaction.
- Anonymization: dedicated controlled workflow, not a side effect of normal archive.
- Never hold database locks while uploading files, sending messages, or calling external services.

### 11.2 Idempotency

Use the existing idempotency middleware/store and key format where available.

- Create and consent commands should be safe against retries.
- Export job creation must not spawn duplicate jobs when the same valid idempotency key is replayed.
- Persist idempotency outcome durably according to the existing contract; Redis loss must not permit duplicate financial or privacy-sensitive operations.
- Define request fingerprint behavior so the same key with a different payload is rejected.
- Do not add a customer-specific idempotency implementation if the platform already has one.

### 11.3 Optimistic concurrency

- Prefer a version column or the repository's established update-token convention.
- A stale patch must return the project's standard conflict error rather than silently overwrite newer data.
- Concurrent code generation is protected by a database uniqueness constraint and retry policy.
- Consent is append-only, so concurrent updates create separate evidence records; define deterministic latest-applicable decision semantics.

---

## 12. Repository and service design

Follow the current module layout and conventions; do not create layers without a real need.

A reasonable shape, adjusted to existing patterns:

```text
internal/modules/customer/
  model.go
  dto.go
  mapper.go       # only if storage and public DTO representations differ
  repository.go   # or the repository layout already used by peer modules
  service.go
  handler.go
  routes.go       # if this is the established module convention
```

Optional files/directories such as `domain`, `events`, `jobs`, or `privacy` should exist only if they contain real behavior and match the project architecture. Do not create empty files/directories for symmetry.

### 12.1 Repository rules

- Repository methods require explicit `orgID` and any branch visibility scope needed by the operation.
- Queries use parameterized SQL and explicit column lists.
- Do not interpolate user input into SQL identifiers, order clauses, or predicates.
- Avoid global `GetByID(id)` methods that do not encode tenant scope.
- For detail, query by `(organization_id, id)` and then enforce visibility/field policy.
- Keep contact lookup scoped by organization and use the approved encryption/lookup design.
- Use primary/consistent reads for consent decisions and visibility changes; do not use a stale cache for authorization decisions.
- Avoid N+1 queries. Batch contact projection only for visible customer IDs.
- Keep Sale/Ledger repositories out of this module.

### 12.2 Service rules

Service owns orchestration and business invariants:

- derive and validate tenant/branch context;
- validate visibility policy;
- normalize and validate fields;
- generate code;
- enforce lifecycle and concurrency;
- write customer/visibility/consent/audit/outbox atomically as required;
- delegate Sale/Ledger reads through stable contracts;
- create export jobs through shared export infrastructure where available;
- map domain errors to the project's shared error catalog.

Handlers should be thin: decode DTO, validate transport shape, call service, and return the established response envelope.

---

## 13. API response and error behavior

Reuse the shared response/error architecture. Do not invent module-local error envelopes or leak database errors.

Expected error categories include:

- validation failure;
- unauthenticated/invalid session;
- forbidden permission or visibility;
- not found or intentionally concealed cross-tenant record;
- duplicate customer code;
- stale version/conflict;
- archived/ineligible customer;
- idempotency-key conflict;
- rate limit exceeded;
- export too large/unsupported format;
- dependency unavailable for delegated Sale/Ledger read;
- storage/worker failure for export;
- audit failure where mandatory.

Rules:

- Never include SQL, stack traces, ciphertext, contact values, or internal key metadata in client errors.
- Keep error codes stable and document them in OpenAPI.
- Use conflict semantics for stale updates and unique-code collisions according to the shared error contract.
- If Ledger is unavailable, do not return `current_due = 0`.
- If consent cannot be verified, suppress optional communication rather than assume consent.
- Export errors must not expose whether a hidden customer's record exists.

---

## 14. Backup, restore, and data-export access

Customer data contains personal information. Backup permissions must be separate from customer permissions and must follow the project's operational backup contract.

### 14.1 Two different operations

**Tenant/customer export**
- Application-level, purpose-bound, field-redacted, organization/branch-scoped.
- Authorized by `customers:export` and any source permissions needed for included data.
- Does not contain database credentials, unrelated tables, other organizations, or raw infrastructure metadata.

**Physical database backup**
- Operational artifact that may contain many organizations, secrets, and all database tables.
- Must never be downloadable by a tenant administrator or by a user who only has `customers:view` / `customers:export`.
- Requires the existing backup module's explicit operator capability and backup permissions, plus step-up authentication/approval where available.
- Uses the existing private encrypted backup storage, verification, retention, and audit process.
- Customer module must not implement a backup endpoint, shell command, `pg_dump` invocation, or restore capability.

### 14.2 Who may access backups?

Follow the established backup contract and canonical RBAC catalog. At minimum:

- **Tenant user / branch manager:** no access to physical backup artifacts or restore controls. They may request an authorized customer-data export only if permitted.
- **Organization-level administrator:** may request a scoped customer export if granted `customers:export`; this does not grant physical backup access.
- **Authorized operations/backup operator:** may run/view physical backup jobs only through the approved backup module and explicit operational permissions/capability.
- **Restore operator:** must be separately authorized under the backup contract and operational runbook. Do not expose routine production restore as a tenant API.
- **Application service identity:** only the minimum service/storage/database privileges needed for its assigned operation; no general user-download capability.

Do not assume the exact role name or permission strings until the existing backup module and permission seed are inspected. `SUPER_ADMIN` alone must not be treated as a bypass of platform-level operational controls unless the approved backup contract explicitly says so.

### 14.3 Backup security requirements inherited by Customer

- Database credentials, shell commands, filesystem paths, bucket keys, and restore targets are never user-controlled.
- Physical backup metadata and tenant export metadata use separate repository/service boundaries.
- Physical artifacts are encrypted and stored privately; signed links, if used, are short-lived and scoped.
- Audit every backup request, download authorization, retention deletion, verification, and restore operation through the backup module.
- Never log signed URLs, credentials, or artifact contents.
- Backup failures must not interrupt sales or customer CRUD.
- Do not claim a backup is recoverable until the established verification/restore process proves it.
- Restores and restore drills are operational procedures, not customer-module actions.
- Retention/anonymization of customer records must account for backup copies and the approved deletion/expiry policy; do not promise immediate erasure from immutable historical backups unless the platform's backup design supports it.

### 14.4 Export revocation and retention

- Every download must validate job ownership, organization, current permissions, expiry, and artifact status.
- Use a private artifact store and short retention.
- Revoke links where the storage design supports revocation; otherwise keep TTL very short and avoid issuing multiple links.
- Expired files should be deleted by a reliable worker with retry/reconciliation.
- Export-job state must be durable; queue state alone is not authoritative.
- If the existing worker/queue is Redis-backed, job metadata and outcome must survive queue loss in durable storage.

---

## 15. Performance and scalability

### 15.1 Read path

- Customer list queries return a bounded projection, not every customer field.
- Do not join sales, invoices, ledger entries, prescription data, or consent history into every list row.
- Use indexed equality lookups for normalized contact hashes where implemented.
- Use the existing pagination implementation and enforce maximum page size.
- Use query-plan-supported search; avoid leading-wildcard `ILIKE` scans on large tenant tables without a deliberate index/search strategy.
- If fuzzy search is needed later, add it with benchmarks and an explicit privacy/enumeration review.

### 15.2 Write path

- Customer CRUD should be short database transactions.
- Avoid locking the customer row for routine Sale reads unless a specific business invariant requires it.
- Credit enforcement belongs at the Sale/Ledger transaction boundary.
- Consent lookups on message delivery should be efficient but consistent enough to honor revocation; do not rely on stale positive consent cache entries.
- Export work must be bounded and asynchronous for large datasets.

### 15.3 Cache policy

**No PII cache by default.**

If a future measured need justifies caching:

- document the threat model and data fields;
- use tenant-qualified keys;
- use short TTLs and explicit invalidation;
- never cache consent as an authoritative positive send decision;
- never cache a credit balance as authoritative;
- prevent cross-tenant key collisions;
- define behavior during Redis outage;
- avoid putting raw contact values in cache keys;
- obtain security review before enabling.

Redis is optional delivery/performance infrastructure, not a source of truth for customer identity, consent, credit, or export-job state.

---

## 16. Observability and operational readiness

Use the existing logger, metrics, tracing, request IDs, health checks, and alerting conventions. Do not introduce a parallel observability stack.

### 16.1 Metrics to consider

- customer list/detail/create/update/archive latency and error rate;
- contact search latency and no-result/denied rates;
- tenant/visibility authorization denials;
- duplicate code conflict count;
- consent lookup failure and communication suppression count;
- export queue wait, execution duration, row count, artifact size, failures, expirations, and download count;
- anonymization/retention workflow failures;
- delegated Sale/Ledger read latency and dependency errors.

### 16.2 Logging and tracing

- Include request/correlation ID, organization ID where permitted by the logging policy, actor ID, route, action, outcome, and latency.
- Never log raw names, phone numbers, email addresses, full addresses, blood group, free-form notes, consent evidence content, access tokens, signed URLs, or secrets.
- Use IDs or safe keyed identifiers for debugging contact lookup.
- Ensure metric labels have bounded cardinality; do not use customer IDs, phone numbers, emails, or raw search terms as metric labels.
- Redact request bodies at middleware boundaries and in tracing.
- Avoid exposing PII in panic reports or exception telemetry.

### 16.3 Alerts and runbooks

Define alerts for repeated export failures, expired artifacts not being removed, sustained tenant-scope denials suggesting enumeration, consent verification outages, and key/encryption failures. Alerts must contain safe metadata and link to an operational runbook rather than include PII.

---

## 17. Testing contract

Tests must prove both allowed behavior and denied behavior. Unit tests alone are insufficient for tenancy and data-leakage guarantees.

### 17.1 Unit tests

- DTO validation, normalization, length bounds, enum allow-lists.
- Code generation edge cases and conflict handling.
- Patch semantics for omitted/null fields.
- Consent latest-decision logic and append-only behavior.
- Field-level redaction.
- Export format safety and spreadsheet formula-injection mitigation.
- Mapping/storage projection separation.

### 17.2 Repository/integration tests

- Every read and write is tenant-scoped.
- Cross-organization UUID access is denied/concealed.
- Branch visibility matrix works for one, multiple, and no authorized branches.
- Customer creation and initial visibility roll back together on failure.
- Duplicate code race is resolved by a database constraint.
- Shared phone/email values do not create accidental duplicate constraints.
- Contact lookup hash is organization-scoped.
- Soft-delete preserves posted Sale/Ledger references.
- Consent history is append-only and queryable by purpose/channel.
- No SQL injection through `q`, filters, sort, or pagination.
- Export only includes authorized rows and approved fields.
- Download fails after expiry or when authorization is revoked, according to the supported design.
- Cross-tenant export job ID guessing does not disclose status or artifact existence.

### 17.3 Security tests

- User cannot set `organization_id`, `created_by`, code, due, credit limit, loyalty points, audit fields, or lifecycle state through mass assignment.
- Permission alone cannot bypass object/branch visibility.
- `customers:export` cannot expose Sale/Ledger data without required source permissions.
- Sensitive fields are absent from default DTOs, logs, metrics, traces, and errors.
- Physical backup endpoints/artifacts are inaccessible to tenant/customer roles.
- Export signed URLs and storage keys are never returned in logs or audit payloads.
- Contact search throttling and enumeration protections are enforced.
- Consent withdrawal racing with queued communication prevents optional delivery.
- Failure to check consent suppresses optional communication.
- Encryption key rotation and key-unavailable behavior are tested if encryption is implemented.

### 17.4 Concurrency/failure tests

- Concurrent customer creation with generated codes.
- Concurrent updates with stale versions.
- Duplicate idempotency key and mismatched-payload replay.
- Replayed consent command.
- Worker crash during export; job can be reconciled without duplicate uncontrolled artifacts.
- Object storage failure after export generation; no public or orphaned artifact.
- Customer read/service failure during Sale does not corrupt an already-posted sale.
- Ledger dependency outage never returns a fabricated zero balance.
- Audit/outbox failure follows the shared transaction contract.

### 17.5 Contract and migration tests

- OpenAPI validation and generated strict handler/interface checks.
- API/DTO behavior matches the frozen contract.
- Migration up/down behavior follows project policy; production data is not silently discarded.
- Upgrade tests cover existing customer rows and inbound FKs.
- RBAC seeds and permission naming match the existing canonical style.
- Run lint, unit/integration/security tests, and migration checks used by CI.

---

## 18. Implementation sequence

1. **Inspect the repository**: model conventions, customer-related migrations, FK graph, auth/branch context, RBAC seed, rate limiter, audit port, pagination, error envelope, export/backup contracts.
2. **Record unresolved decisions**: schema ownership, permission syntax, sensitive field policy, credit/loyalty ownership, export route/method, encryption/key management, retention.
3. **Freeze OpenAPI**: define canonical routes, DTOs, errors, permission declarations, export-job semantics and security requirements. Use the established OpenAPI 3.1 contract-first pipeline.
4. **Design migrations**: tenant-safe identity, contact model, branch visibility, consent evidence, indexes, constraints, verified backfill.
5. **Implement model and DTOs**: use shared UUID/BaseModel/pagination conventions; never expose storage-only fields.
6. **Implement repository**: mandatory tenant and branch scope; parameterized queries; redacted projections.
7. **Implement service**: lifecycle, visibility, code generation, optimistic concurrency, consent, audit/outbox integration.
8. **Implement handlers/routes**: reuse existing router/middleware and response/error conventions.
9. **Implement delegated reads**: Sale and Ledger service contracts only; no cross-module repository imports.
10. **Implement export only after infrastructure is verified**: durable job, purpose, field allow-list, private artifact storage, formula-injection protection, download reauthorization and expiry.
11. **Integrate Notification consent check**: latest-state lookup before send; fail closed for optional communications.
12. **Add observability and runbooks**: safe logs/metrics, export and consent failure alerts, retention handling.
13. **Run tests and security review**: especially cross-tenant access, branch visibility, PII redaction, export authorization, backup separation and consent races.
14. **Roll out safely**: migrate/backfill verified data, inspect production query plans, monitor denials/errors, and retain a rollback plan.

---

## 19. Definition of done

The Customer module is not production-ready until all applicable items below are satisfied:

- [ ] Existing schema, data, migrations, and inbound FKs were inspected.
- [ ] Customer identity has one canonical owner and one canonical persistence model.
- [ ] Organization scope is enforced in every repository method.
- [ ] Branch visibility is explicit, tested, and cannot leak cross-branch activity.
- [ ] Sensitive and unnecessary fields from the initial model have been removed or justified by an approved decision.
- [ ] No authoritative `CurrentDue` or mutable credit value is duplicated in Customer.
- [ ] Loyalty points are omitted until a proper owner/ledger contract exists.
- [ ] DTOs prevent mass assignment and redact storage/internal fields.
- [ ] RBAC names match the existing canonical permission style and seeds.
- [ ] Existing middleware, rate limiting, idempotency, audit, errors, and pagination are reused.
- [ ] Sale and Ledger reads delegate through stable contracts and preserve their own authorization.
- [ ] Consent is append-only and delivery rechecks current consent.
- [ ] Export semantics are frozen in OpenAPI and enforce purpose, scope, field allow-list, private storage, reauthorization, expiry, and formula-injection safety.
- [ ] Customer export permissions cannot access physical backups.
- [ ] Physical backups and restore remain within the operational backup module and explicit operator controls.
- [ ] No PII is leaked into logs, metrics, traces, errors, cache keys, or public artifacts.
- [ ] Tenant/branch, privacy, security, concurrency, migration, and failure tests pass.
- [ ] OpenAPI validation, generated strict handlers, lint, CI and migration checks pass.
- [ ] Retention/anonymization and export operations have an owner and runbook.
- [ ] Remaining risks and infrastructure gaps are documented rather than silently assumed.

---

## 20. Key architectural decisions

| ID | Decision |
|---|---|
| CUS-001 | Customer identity is organization-owned; branch access is explicit |
| CUS-002 | Customer is not a medical-record system |
| CUS-003 | Walk-in sale may omit customer identity unless the workflow requires it |
| CUS-004 | Sale owns sales history; Ledger owns receivables and credit exposure |
| CUS-005 | No authoritative `CurrentDue` or credit-balance duplication in Customer |
| CUS-006 | No mutable loyalty-points total without an approved ledger/owner |
| CUS-007 | Contacts are not globally unique; family/shared contact use is valid |
| CUS-008 | Consent evidence is append-only and checked again before delivery |
| CUS-009 | No PII cache by default |
| CUS-010 | Export is purpose-bound, field-redacted, private, auditable, and re-authorized at download |
| CUS-011 | Physical backup access is separate from tenant/customer export permissions |
| CUS-012 | Existing shared infrastructure is reused; missing infrastructure is verified and documented, not invented |
| CUS-013 | Export route/method mismatch must be resolved in OpenAPI before implementation |
| CUS-014 | Migration and backfill must preserve tenant ownership and posted business references |

---

## 21. Source and implementation notes

This contract preserves the Customer decisions in Astra's `answer_in_markdown.md`:

- **Domain model:** Customer, branch visibility, consent, contact identity.
- **Database direction:** organization-scoped `customers`, normalized contacts, branch visibility and append-only consent evidence.
- **Business rules:** minimal PII; no required customer for ordinary walk-in sale; Ledger owns credit limits/exposure; Sale owns sales history.
- **API direction:** scoped list/detail, create/update/archive, consent, delegated Sale/Ledger reads, purpose-bound export.
- **Security and operations:** PII enumeration, cross-branch inference, export abuse, consent bypass, audit, indexed lookup, bounded search, no unbounded history joins, safe failure behavior.

The exact production schema, existing migrations, middleware names, canonical permission syntax, rate-limit keys, worker/export implementation, encryption key management, retention periods, and backup operator roles must be confirmed from the live repository and approved contracts before implementation. This document is an implementation contract, not a claim that every optional capability already exists.