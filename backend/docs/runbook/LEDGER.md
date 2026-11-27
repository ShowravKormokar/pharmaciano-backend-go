# Ledger Module Runbook

This runbook describes the operational, monitoring, troubleshooting, reconciliation,
and release procedures for the Pharmaciano ERP Ledger module
(`internal/modules/ledger`).

It is the operational companion to `docs/adr/LEDGER_CONTRACT.md` and must be read
together with `docs/runbook/deployment.md` and `docs/runbook/incident_response.md`.

------------------------------------------------------------------------

## 1. Module Overview

### 1.1 Purpose

The Ledger module is the accounting system of record for Pharmaciano ERP. It owns
the chart of accounts, fiscal periods and posting controls, double-entry
journals and immutable journal lines, controlled manual journals and reversal
workflows, durable source-operation deduplication, customer/supplier subledger
exposure where accounting decisions require it, effective-dated posting-rule
account mappings, trial-balance and other authoritative ledger read models,
period close/reopen controls, reconciliation evidence, financial auditability,
and scoped exports.

### 1.2 Ownership

- Ledger owns double-entry accounting, fiscal control, immutable financial
  postings, subledger exposure, and authoritative financial read models.
- Ledger does NOT own customer/supplier identity, purchase/sale documents, stock,
  medicine identity, currency definitions, business configuration, authentication,
  audit infrastructure, notifications, analytics, or report presentation.
- Ledger may persist a stable party reference (`party_type`, `party_id`) for
  subledger attribution, but must not duplicate customer/supplier profile fields.
- Purchase, Sale, and Inventory call a Ledger application port with typed source
  facts. Ledger does not import their concrete packages.

### 1.3 Module Status

Implemented and mounted in the API router (`RegisterRoutes` wired in
`cmd/api/main.go`). Provides accounts, periods, journals, postings, reversals,
and trial balance. The module is the financial authority; all other modules
depend on it for posted financial facts.

------------------------------------------------------------------------

## 2. Scope and Boundaries

### 2.1 In scope

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

### 2.2 Out of scope (owned by other modules)

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

### 2.3 Dependency direction

Purchase, Sale, and Inventory call a Ledger application port with typed source
facts. Ledger does not import their concrete packages.

```text
Purchase -> Ledger application port / posting service
Sale     -> Ledger application port / posting service
Inventory -> Ledger application port / posting service

Ledger -> shared platform, common, errors, MasterData currency port,
         Settings policy port, Audit port, PostgreSQL repositories
```

Do not call Ledger through its own HTTP endpoint from another module inside the
same process. Internal calls should use the application service and the caller's
transaction context.

------------------------------------------------------------------------

## 3. Endpoint and Permission Inventory

### 3.1 Routes (registered in `routes.go`, mounted under `/api/v1/ledger`)

| Method | Path | Handler | Rate policy | Permission |
|---|---|---|---|---|
| GET | `/ledger/accounts` | `ListAccounts` | `auth_read` | `ledger:view` |
| POST | `/ledger/accounts` | `CreateAccount` | `auth_write` + idempotency + audit | `ledger:manage-accounts` |
| GET | `/ledger/accounts/:id` | `GetAccount` | `auth_read` | `ledger:view` |
| PATCH | `/ledger/accounts/:id` | `UpdateAccount` | `auth_write` + idempotency + audit | `ledger:manage-accounts` |
| POST | `/ledger/accounts/:id/deactivate` | `DeactivateAccount` | `auth_write` + idempotency + audit | `ledger:manage-accounts` |
| GET | `/ledger/accounts/:id/balance` | `GetAccountBalance` | `auth_read` | `ledger:view` |
| GET | `/ledger/periods` | `ListPeriods` | `auth_read` | `ledger:view` |
| POST | `/ledger/periods` | `CreatePeriod` | `auth_write` + idempotency + audit | `ledger:manage-periods` |
| POST | `/ledger/periods/:id/close` | `ClosePeriod` | `auth_write` + idempotency + audit | `ledger:close-period` |
| POST | `/ledger/periods/:id/reopen` | `ReopenPeriod` | `auth_write` + idempotency + audit | `ledger:reopen-period` |
| POST | `/ledger/journals` | `CreateJournal` | `auth_write` + idempotency + audit | `ledger:create` |
| GET | `/ledger/journals` | `ListJournals` | `auth_read` | `ledger:view` |
| GET | `/ledger/journals/:id` | `GetJournal` | `auth_read` | `ledger:view` |
| PATCH | `/ledger/journals/:id` | `UpdateJournal` | `auth_write` + idempotency + audit | `ledger:create` |
| POST | `/ledger/journals/:id/post` | `PostJournal` | `auth_write` + idempotency + audit | `ledger:post` |
| POST | `/ledger/journals/:id/reverse` | `ReverseJournal` | `auth_write` + idempotency + audit | `ledger:reverse` |
| GET | `/ledger/reports/trial-balance` | `TrialBalance` | `auth_read` | `ledger:view` |

### 3.2 RBAC permissions

- `ledger:view` --- read accounts, periods, journals, balances, trial balance.
- `ledger:manage-accounts` --- create/update/deactivate accounts.
- `ledger:manage-periods` --- create periods.
- `ledger:close-period` --- close a fiscal period.
- `ledger:reopen-period` --- reopen a closed fiscal period (exceptional).
- `ledger:create` --- create/update draft journals.
- `ledger:post` --- post a draft journal.
- `ledger:reverse` --- reverse a posted journal.

### 3.3 Rate policies

- `auth_read` --- read endpoints.
- `auth_write` --- mutation endpoints (with idempotency + audit middleware).

### 3.4 Authorization invariants

- `organization_id` comes only from trusted Principal/context, never request body/query.
- Branch scope is enforced separately from organization scope.
- All repository methods are tenant-scoped (`organization_id` predicate in SQL).
- Row-level locks (`FOR UPDATE`) used for account/period/journal consistency.
- No cross-tenant or cross-org access under any circumstance.

------------------------------------------------------------------------

## 4. Configuration

### 4.1 Feature gating

Ledger is not behind a feature flag; it is mounted unconditionally in
`cmd/api/main.go` (`ledger.New(pg, val, log)`). It is a core platform module.

### 4.2 Relevant config keys

| Key | Default | Env override | Purpose |
|---|---|---|---|
| `pagination.default_limit` | 20 | --- | Default page size |
| `pagination.max_limit` | 100 | --- | Max page size |
| `idempotency.ttl` | 24h | `IDEMPOTENCY_TTL` | HTTP idempotency cache TTL |
| `idempotency.key_max_length` | 128 | `IDEMPOTENCY_KEY_MAX_LENGTH` | Max idempotency key length |
| `rate_limit.enabled` | true | `RATE_LIMIT_ENABLED` | Rate limiting enablement |
| `audit.enabled` | true | --- | Durable audit sink |

### 4.3 Environment variables

See `internal/platform/config` `bindEnvVars` for the full mapping. Ledger relies
on the existing `RATE_LIMIT_*`, `IDEMPOTENCY_*`, and `AUDIT_*` conventions.

------------------------------------------------------------------------

## 5. Domain Model and Invariants

### 5.1 Core entities

- **ChartOfAccount** --- organization-owned account with code, name, type, normal
  side, parent, postable/active flags, version.
- **LedgerPeriod** --- organization-owned fiscal period with start/end dates,
  status (open/closing/closed), close/reopen metadata, version.
- **Journal** --- accounting document with header, lines, status (draft/posted),
  source module/ID, currency, totals, posting/reversal linkage, version.
- **JournalLine** --- debit/credit fact referencing account, optional branch,
  amount (minor units), description, line order.
- **AccountBalance** --- read projection for period balances (opening/debit/credit/
  closing).

### 5.2 Accounting invariants

- Every posted journal is balanced: total debits == total credits.
- At least two lines per journal.
- Every line references an account in the same organization.
- Each line has exactly one positive side (debit or credit); zero-value lines
  rejected.
- Journal currency must match organization functional currency (BDT initial).
- Every branch dimension must belong to the journal organization.
- Posting only to active, postable accounts; control accounts require explicit
  posting-source/subledger rules.
- Closed periods reject normal posting and reversal.
- Posted journals are immutable; corrections via reversal (new journal).

### 5.3 Money representation

- PostgreSQL `NUMERIC` (not float). Go uses integer minor units (int64).
- Currency minor-unit precision from MasterData currency metadata.
- Initial release: one functional currency per organization (BDT). Reject
  unsupported foreign-currency postings.
- Rounding mode defined in accounting policy; applied consistently.

### 5.4 Fiscal period rules

- Periods are organization-owned and non-overlapping.
- `start_date <= end_date`; posting date must fall within exactly one open period.
- Period close and journal post serialize via same period row lock protocol.
- Closed periods reject normal posting and reversal.
- Backdated posting requires explicit permission and open period.
- Reopening requires exceptional permission, reason, fresh authorization,
  immutable evidence.
- Close must verify all required reconciliations and unresolved exceptions.

### 5.5 Reversal rules

- Reversal creates a new journal with exact opposite debit/credit amounts.
- Original journal remains unchanged.
- Reversal references original journal with reason, actor, date, source/idempotency.
- Only posted journals can be reversed.
- A journal may be reversed only once (unless approved partial-correction workflow).
- Reversal date must be in an open period.

### 5.6 Source identity and posting rules

Durable source identity: `(organization_id, source_type, source_operation_id,
posting_kind)`. Examples: `sale`/sale UUID/`recognition`, `sale`/sale UUID/`cogs`,
`sale_return`/return UUID/`reversal`, `purchase_receipt`/receipt UUID/
`inventory_accrual`, `supplier_invoice`/invoice UUID/`payable_recognition`,
`supplier_payment`/payment UUID/`settlement`, `inventory_adjustment`/adjustment
UUID/`write_off`.

- `source_type` and `posting_kind` are allow-listed domain identifiers.
- `source_operation_id` must be a stable UUID/identifier generated by owning domain.
- Duplicate source identity with same canonical payload returns original result.
- Duplicate identity with different payload is a conflict; never overwrite.

------------------------------------------------------------------------

## 6. Deployment and Release Gates

### 6.1 Prerequisites

1. Confirm actual transaction helper and repository transaction propagation.
2. Confirm current middleware idempotency storage, key scope, response replay
   behavior, and retention.
3. Confirm audit API and whether it writes inside the caller transaction.
4. Confirm outbox table/worker exists before designing event publishing.
5. Confirm organization functional currency storage and currency precision.
6. Inspect existing ledger/account/journal migrations before creating new schema.
7. Confirm branch-scope middleware semantics.
8. Confirm UUID, pagination, error, DTO, and migration naming conventions.

### 6.2 Verification checklist

```powershell
# From backend/
go build ./...
go test ./internal/modules/ledger/... -count=1
go vet ./internal/modules/ledger/...
```

- Confirm `schema_migrations` is clean (no dirty migrations).
- Confirm RBAC seeder includes `ledger:*` permissions.
- Confirm rate-limit policies `auth_read` and `auth_write` are registered.
- Confirm idempotency middleware is active on write endpoints.

### 6.3 Rollback

- Preserve all posted journals, accounts, periods (immutable evidence).
- Do not run down migrations automatically.
- If a bug is found, fix forward with a new migration or corrected code.
- Disable specific endpoints only if absolutely necessary via RBAC policy changes.

------------------------------------------------------------------------

## 7. Monitoring and Observability

### 7.1 Suggested metrics (follow existing naming)

- `ledger_operations_total{operation,outcome}` --- account/period/journal/post/
  reverse outcomes.
- `ledger_operation_duration_seconds{operation}` --- latency per operation.
- `ledger_journals_posted_total{branch_id}` --- posted journals (bounded label).
- `ledger_period_close_total{outcome}` --- period close attempts.
- `ledger_reconciliation_mismatches_total{source,dimension,severity}` --- if
  reconciliation is implemented.

### 7.2 Structured logs

Include request/trace/correlation IDs, org/scope fingerprint, operation, duration,
rows/bytes, and sanitized error class. Redact PII, tokens, payment data, and
signed URLs. No user/customer/sale/org IDs in Prometheus labels.

### 7.3 Alerts

- Journal posting failure rate spike.
- Double-entry balance violations (should never happen; indicates bug).
- Period close failures or stuck periods.
- Reconciliation mismatches.
- Database lock contention on accounts/periods/journals.
- Idempotency cache miss rate (Redis outage).

------------------------------------------------------------------------

## 8. Troubleshooting

### 8.1 Account management

| Symptom | Likely cause | Action |
|---|---|---|
| `409 CONFLICT` on create | Duplicate account code in org | Verify code uniqueness; check soft-deleted accounts. |
| `422` parent account invalid | Parent inactive or postable | Ensure parent is active and non-postable (header account). |
| `404` on get/update | Account not found or deleted | Check org scope and `deleted_at`. |
| `409` on update | Version mismatch | Reload account, retry with current version. |

### 8.2 Period management

| Symptom | Likely cause | Action |
|---|---|---|
| `409` on create | Period overlaps existing | Check date ranges; adjust or use existing period. |
| `422` on close | Period not open or reconciliation pending | Verify period status and close checklist. |
| `403` on reopen | Insufficient permission | Requires `ledger:reopen-period` and exceptional justification. |
| Posting rejected | Period closed or no open period for date | Check period status and date range. |

### 8.3 Journal operations

| Symptom | Likely cause | Action |
|---|---|---|
| `422` validation | Unbalanced lines, invalid account, zero lines | Verify debit==credit, at least 2 lines, accounts active/postable. |
| `409` on post | Version mismatch or already posted | Reload journal; check status. |
| `404` on get | Journal not found or wrong org | Verify org scope and ID. |
| Reversal rejected | Journal not posted or already reversed | Check journal status and reversal linkage. |
| `403` | Insufficient permission for post/reverse | Verify role has `ledger:post` / `ledger:reverse`. |

### 8.4 Trial balance

| Symptom | Likely cause | Action |
|---|---|---|
| Empty result | No posted journals in period | Verify data and as-of date. |
| Unbalanced totals | Bug or data corruption | Investigate immediately; check journal line integrity. |
| `400` invalid date | Missing or malformed `as_of` | Provide valid date (YYYY-MM-DD or RFC3339). |

### 8.5 Common operational checks

```powershell
# Build and test Ledger package
cd D:\Codding\pharmaciano-backend-go\backend
go build ./internal/modules/ledger/...
go test ./internal/modules/ledger/... -count=1
go vet ./internal/modules/ledger/...

# Check routes are mounted
Select-String -Path "D:\Codding\pharmaciano-backend-go\backend\cmd\api\main.go" -Pattern "ledger" -SimpleMatch
```

------------------------------------------------------------------------

## 9. Data Integrity and Evidence Rules

- Posted journals, lines, accounts, and periods are immutable evidence.
- Corrections use reversal (new journal), never edits or deletes.
- Reversal references original journal with reason, actor, date, source/idempotency.
- A journal may be reversed only once unless approved partial-correction workflow.
- Account code/name changes after use should be disallowed or handled as
  controlled alias/reclassification.
- Deactivation blocks future postings but does not hide historical records.
- Account already referenced by posted journal lines cannot be hard-deleted.
- Period close must verify all required reconciliations and unresolved exceptions.
- Close must not be represented as successful while required checks are pending
  or failed.
- Reconciliation detects differences and raises evidence/exceptions. It never
  silently rewrites accounting history.

------------------------------------------------------------------------

## 10. Security and Privacy

- Organization and branch scope derived from trusted Principal/context, never
  request body/query.
- Row-level tenant predicates in SQL as defense-in-depth.
- Composite tenant foreign keys where practical.
- No client-selected accounting policy; server resolves approved posting-rule
  versions and account mappings.
- No cache authority for balances, periods, or credit limits.
- Fail closed for financial writes: if authorization, period status, source
  identity, mapping, currency, or transaction state cannot be verified, do not
  post.
- Never log signed URLs, auth headers, or export rows.
- No user/customer/sale/org IDs in Prometheus labels.
- Audit evidence is complementary; Ledger records financial domain evidence.

------------------------------------------------------------------------

## 11. Release and Rollback Summary

| Action | Procedure |
|---|---|
| Enable | Module is already mounted unconditionally (core platform). |
| Disable | Remove from `cmd/api/main.go` modules list and rebuild (emergency only). |
| Rollback schema | Do not run down migrations automatically; preserve source facts; fix forward. |
| Emergency | Disable routes via RBAC policy changes, preserve logs/DB state, follow `incident_response.md`. |

------------------------------------------------------------------------

## 12. Related Documents

- `docs/adr/LEDGER_CONTRACT.md` --- full architecture and implementation contract.
- `docs/runbook/deployment.md` --- deployment and migration procedures.
- `docs/runbook/incident_response.md` --- incident triage and response.
- `docs/runbook/db_backup_restore.md` --- backup/restore procedures.
- `docs/api/API.md` --- API surface documentation.
- `docs/api/OPENAPI.md` --- OpenAPI-first contract workflow.

------------------------------------------------------------------------

## 13. Document Control

- **Module:** `internal/modules/ledger`
- **Status:** Implemented and mounted.
- **Owner:** Ledger domain team.
- **Last updated:** 2026-11-27
- **Review cadence:** Update when endpoints, permissions, invariants, or operational
  procedures change.
