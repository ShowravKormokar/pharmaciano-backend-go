# Purchase Implementation Readiness — Database and Migration Safety Audit

**Audit date:** 2026-11-06  
**Scope:** Read-only repository and available database inspection before Purchase implementation.  
**Outcome:** Purchase implementation is blocked. No migration or database operation was executed.

## 1. Executive summary

The migration set is not loadable as currently checked in: version `000023` has two `up` files and two `down` files, and the repository's `golang-migrate` `iofs` loader rejects duplicate version/direction pairs. This happens while constructing the source, before the migrator connects to PostgreSQL. The duplicate therefore blocks the migration command for every database, independently of its current version.

The one database available to this audit is the workspace's local development database. A read-only catalog query observed `schema_migrations.version = 7` and `dirty = true`. The expected version-7 inventory tables are absent, as are later Purchase, Ledger-contract, and normalized Inventory tables. The migration record does not say why the database became dirty, and the absent tables do not establish which SQL statements, if any, ran before it became dirty.

No staging or production database connection/configuration was available. Their migration state and schema are **unknown**, not inferred from the local database or migration version.

Purchase must not be implemented against this database state yet. Resolve the dirty development database and obtain deployment-specific schema evidence first. Separately, the current modules lack the transaction-aware Inventory receipt port, Purchase-specific GRNI account-policy adapter, mandatory error-returning transactional audit append, durable PostgreSQL operation idempotency, and transactional outbox required by the Purchase contract. The job package is also scaffolding rather than a running worker/queue implementation.

### Critical blockers

1. **Migration source cannot initialize:** duplicate version-23 files prevent migration loading.
2. **Development database is dirty and incomplete:** version 7 is marked dirty; version-7 Inventory tables are absent.
3. **Deployment state is unknown:** there is no verified staging/production migration or schema evidence.
4. **Purchase posting prerequisites are incomplete:** Inventory receipt integration, mandatory transactional audit, durable PostgreSQL operation idempotency, and a transactional outbox/operational worker are missing; existing Ledger posting is generic and has no Purchase GRNI policy. The current facilities do not provide the atomic guarantee required for receipts, invoices, payments, and returns.

## 2. Repository migration audit

### 2.1 Loader and execution mechanism

- [migrate.go](../internal/platform/db/migrate.go) constructs the source with `iofs.New(fs, root)` before calling `migrate.NewWithSourceInstance`. The embedded filesystem comes from [embed.go](../migrations/embed.go), which embeds every `*.sql` file.
- `iofs.New` parses filenames using the migration filename convention (`<numeric-version>_<name>.up.sql` / `.down.sql`) and rejects a second file with the same version and direction. The two version-23 files therefore cause source initialization to fail. No database state can resolve this ambiguity.
- [cmd/migrate/main.go](../cmd/migrate/main.go) loads configuration, constructs the migrator, and only then selects `up`, `down`, or `force`. Thus the current collision prevents all three command paths from reaching the database.
- The [PostgreSQL migration driver](https://pkg.go.dev/github.com/golang-migrate/migrate/v4) uses `schema_migrations` with `version` and `dirty`. The installed driver implementation creates exactly those two columns and reads/writes those fields. It does **not** store the applied SQL filename, source revision, or checksum. A migration version is consequently not proof of which same-numbered script ran.
- Migration versions are ordered numerically. Each normal migration has one `.up.sql` and one `.down.sql`; those opposite directions are expected, not duplicates. The only duplicate version/direction pairs found are at `000023`.
- The current migrator's `Down(steps)` changes any `steps <= 0` to one step. However, the CLI describes `-steps 0` as “all” and calls this method for that case. The CLI warning therefore overstates what its implementation does: the zero/default case reaches a one-step down, not a full rollback.
- The CLI exposes `-force`. `golang-migrate` force updates migration metadata; it does not apply or validate schema SQL. Using it to clear a dirty state without a verified schema repair can make the recorded version disagree with the actual schema.

### 2.2 Duplicate version 000023

| Direction | File | Relevant effects | Rollback implications |
|---|---|---|---|
| Up | [000023_backup_module.up.sql](../migrations/000023_backup_module.up.sql) | Creates system backup-run and backup-verification metadata, indexes, update/immutability triggers. | |
| Down | [000023_backup_module.down.sql](../migrations/000023_backup_module.down.sql) | Drops the verification and backup-run tables and related trigger/function. | Drops backup metadata/evidence; must not be treated as harmless if records or artifact references exist. |
| Up | [000023_ledger_contract.up.sql](../migrations/000023_ledger_contract.up.sql) | Renames legacy Ledger tables, converts monetary columns to integer minor units, creates fiscal periods, source-posting identity and subledger tables, and adds constraints, indexes, and posting-protection logic. It also checks legacy data before conversion. | |
| Down | [000023_ledger_contract.down.sql](../migrations/000023_ledger_contract.down.sql) | Explicitly raises an exception and directs operators to roll forward. | Intentionally forward-only; a down migration is not a recovery path. |

These are different schema changes, not two copies of the same migration. The Ledger migration is especially consequential: it transforms accounting schema and financial representations. Selecting one based only on `schema_migrations.version = 23` would be unsafe. Re-running either non-idempotent migration against an environment where its objects already exist can fail; marking the version applied without proving its effects can skip required work.

The complete filename scan found no other duplicate version/direction pairs. Versions `000001`–`000022` and `000024`–`000026` each have the expected up/down pair.

### 2.3 Checksums, dirty state, and deployment implications

The migration metadata has no checksum or migration-name field. The local database inspection independently confirmed that its `schema_migrations` table contains only `version` and `dirty`. Neither that row nor a version-23 value on another environment can distinguish the backup script from the Ledger script.

`dirty=true` means migration execution was recorded as in progress or left unclean; it does not identify the failing statement, whether some DDL persisted, or whether the state was changed manually. The version and catalog must be reconciled before any retry or metadata repair.

The Docker Compose [migrate service](../deployments/docker/docker-compose.yml) gates application and worker startup on successful migration completion. The current duplicate can therefore also block a deployment using that Compose stack.

## 3. Database environment audit

The local connection used the existing development configuration and PostgreSQL session-level `default_transaction_read_only=on`, with bounded statement and connection timeouts. The database identity and connection details are intentionally omitted.

| Environment | Access/evidence | Migration state | Schema findings |
|---|---|---|---|
| Development (workspace-local) | Connected using the workspace's `APP_ENV=dev` configuration. The configured local PostgreSQL endpoint corresponded to the running local PostgreSQL container. Catalog queries confirmed the session was read-only. | `schema_migrations`: `version=7`, `dirty=true`. The table has only `version` and `dirty` columns. | Core organization, supplier, medicine, branch, and warehouse relations exist. The version-7 relations `inventory_batches`, `stock_movements`, `warehouse_transfers`, and `warehouse_transfer_items` are absent. Legacy Purchase relations (`purchases`, `purchase_items`, `purchase_payments`, `purchase_receipts`) and normalized Purchase tables are absent. Ledger contract relations (`ledger_accounts`, `ledger_journals`, `ledger_source_postings`, `ledger_periods`) and normalized Inventory relations (`inventory_batch_identities`, `inventory_balances`, `inventory_movements`, `inventory_cost_layers`) are absent. Since these target relations are absent, their columns, constraints, indexes, and foreign keys cannot be compared in this database. The database is not a clean, fully migrated state at version 7. |
| Staging | Inaccessible. No staging-specific configuration or connection was available in the workspace/session. | **Unknown; not queried.** | **Unknown; not queried.** |
| Production | Inaccessible. No production-specific configuration or connection was available in the workspace/session. | **Unknown; not queried.** | **Unknown; not queried.** |

The repository includes default, development, and Docker configuration, not separate verified staging/production connection profiles. The running local Compose PostgreSQL service is not evidence of staging or production state. No credentials, hostnames, database names, or secret values are included in this report.

The local observations are consistent with an incomplete or interrupted version-7 migration, but they do not prove the reason for the dirty marker or the precise point of failure. Do not retry version 7, run `force`, or infer that the database is disposable without separately establishing its data-retention requirements and inspecting the complete catalog.

## 4. Purchase prerequisite assessment

| Prerequisite | Existing implementation and evidence | Available | Missing before Purchase integration |
|---|---|---|---|
| Shared transactions | [transaction.go](../internal/platform/db/transaction.go) provides `WithTx`, bounded timeout options, rollback, and nested savepoints. [pgx.go](../internal/platform/db/pgx.go) routes repository operations through the transaction attached to context. | A usable shared transaction mechanism for repositories/services wired to the same `db.DB`. | Verify and test the exact shared context/runner across Purchase, Inventory, Ledger, and audit; cross-module writes must not open independent commits. |
| Inventory receipt | [inventory/service.go](../internal/modules/inventory/service.go) exposes batch listing; [inventory/repository.go](../internal/modules/inventory/repository.go) implements scoped batch reads. [000026_inventory_core.up.sql](../migrations/000026_inventory_core.up.sql) defines normalized batch identities, balances, immutable movements, cost layers, and allocations. | Inventory-owned schema contracts and read-side implementation; transaction context support exists at the platform layer. | A transaction-aware receipt command/port that validates tenant, branch, warehouse, product, lot/expiry and condition; writes/locks batch, balance, movement, and cost layer once; returns stable valuation and references; and has rollback/concurrency tests. Purchase must not write Inventory tables directly. |
| Ledger GRNI/posting | [ledger/auto_post.go](../internal/modules/ledger/auto_post.go) has `AutoPost`, balanced journal creation, open-period/account validation, source-posting payload hash, and source-operation uniqueness. It uses `WithTx`, which can nest as a savepoint when passed the same transaction context. [000023_ledger_contract.up.sql](../migrations/000023_ledger_contract.up.sql) defines Ledger source-posting and subledger structures. | Generic Ledger source-originated posting, source deduplication, period checks, and transaction participation. | A Purchase-owned narrow port and Ledger-controlled posting-policy resolver for GRNI, inventory asset, AP, tax, variance, settlement, supplier credit, and reversals. `AutoPost` accepts already-resolved journal lines; it does not supply a Purchase policy or account mapping. Seeded system-account examples do not establish a GRNI account policy. |
| Mandatory transactional audit | [platform/audit/sink.go](../internal/platform/audit/sink.go) writes `audit_logs` through `db.FromCtx`, so a successful insert can join the active transaction. | Durable audit insert and middleware sink integration. | A business-audit append API that returns errors and fails the enclosing transaction when mandatory evidence cannot be written. The current `Record` method has no error return and logs then swallows failures; it also permits a nil database as a no-op. It cannot provide the contract's mandatory-audit guarantee. |
| Durable operation idempotency | Ledger source postings and Inventory movement uniqueness constraints provide domain-level protection for some posting/movement identities when those schemas are installed. | Effect-level uniqueness in specific downstream domains. | A Purchase operation identity in PostgreSQL with canonical payload hash, tenant/resource binding, replay of the committed result, and safe handling of in-progress/rolled-back operations. The relevant tables are absent in the audited development DB. No shared generic PostgreSQL request-operation store was found. |
| Existing request idempotency | [middleware/idempotency.go](../internal/middleware/idempotency.go) uses Redis, fingerprints method/path/raw body, and may replay a cached response. | Best-effort duplicate-request UX while Redis is healthy. | It is not authoritative for money or stock: missing keys are allowed; nil Redis bypasses it; Redis errors, key disappearance, and result-persistence failures proceed/log without durable deduplication; records expire. Do not use it as the sole financial-operation guarantee. |
| Transactional outbox and jobs | No transactional outbox table, append port, or outbox processor was found. Files under `internal/jobs/` and `internal/jobs/handlers/` are package-only placeholders; [cmd/worker/main.go](../cmd/worker/main.go) is an explicit signal-waiting stub. Asynq-related config/constants/comments exist in [types.go](../internal/platform/config/types.go) and [constants.go](../internal/common/constants/constants.go), but [go.mod](../go.mod) has no Asynq runtime dependency. | Redis client and job configuration scaffolding only; no operational task queue/worker is available to deliver outbox events. | PostgreSQL outbox persistence in the business transaction, versioned event envelope, a real dispatcher/worker with bounded retry/dead-letter handling, and consumer deduplication. Queue enqueue alone cannot replace a transactional outbox. |
| Purchase module/API | `purchase/model.go` currently contains domain structs; `dto.go`, `handler.go`, `repository.go`, `routes.go`, `service.go`, `state_machine.go`, and `purchase_test.go` are package-only placeholders. The API composition in [main.go](../cmd/api/main.go) wires Inventory but not Purchase. | A partially prepared Purchase domain model and RBAC module/action constants. | Repository/service/HTTP behavior, routes, validation, permissions/policy tests, integration tests, API composition, OpenAPI/Scalar documentation, and observability. Existing [RBAC seed](../internal/modules/rbac/seed.go) grants only a subset of the contract's Purchase actions; reconcile all permissions and role grants before exposing routes. |

The older [000008_purchase.up.sql](../migrations/000008_purchase.up.sql) creates a legacy Purchase schema with mutable totals, child-row cascades, and batch fields on order items. A future Purchase migration must explicitly handle/backfill existing records in environments where that migration has run; it must not assume a clean schema or overwrite/drop posted history.

## 5. Risks and possible data-loss scenarios

1. **Wrong version-23 interpretation:** advancing from a version that represents the other script may skip financial schema conversion or replay object creation. Applying the Ledger migration to inconsistent legacy data can fail its safety checks; bypassing those checks or forcing a version can leave accounting columns and application code inconsistent.
2. **Dirty version-7 retry:** DDL may be absent, complete, or partly present depending on how the failure occurred and the migration execution path. Blind retry can fail on existing objects or compound partial work. The observed absent version-7 tables do not, on their own, prove that all statements were rolled back.
3. **Rollback/data destruction:** the backup down migration drops backup metadata/evidence. The Ledger down migration is intentionally blocked. General down/force operations are not a safe repair for unknown financial state.
4. **Migration history ambiguity:** `schema_migrations` has no checksum/name history; different environments may have different version-23 schema despite the same recorded version.
5. **Application deployment failure:** the migration loader fails before connecting; Compose service dependencies can prevent API/worker startup when migration does not complete.
6. **Purchase correctness risk:** best-effort audit, Redis-only idempotency, and absent transactional outbox/receipt port would permit committed stock or financial effects without the contract-required durable evidence or replay protection.
7. **Configuration risk:** the Compose file contains fallback values for database and application secrets. Treat these strictly as local defaults, override them with managed secrets in every deployed environment, and rotate any default that may have been used outside development. No value is reproduced here.

## 6. Forward-only migration recovery options

There is not enough deployment evidence to select a version-23 strategy. First collect, separately for every database, a protected schema snapshot/fingerprint and the following evidence:

- `schema_migrations` version and dirty state, plus the migration runner/release logs and deployed artifact/source revision.
- Presence and definitions (columns, constraints, indexes, triggers, and foreign keys) of `system_backup_runs`, `system_backup_verifications`, `ledger_accounts`, `ledger_journals`, `ledger_lines`, `ledger_periods`, `ledger_source_postings`, and Ledger subledger relations.
- Legacy/current Ledger relation names and monetary column types; backup metadata row counts and artifact-reference/retention implications.
- Version-7 and version-26 Inventory relation definitions; existing Purchase/Sale/Inventory rows and their cross-references.
- A database backup/restore point and a rehearsal on a restored clone. Preserve query output and migration-file hashes securely; do not put credentials in the report.

Then choose only the path supported by those facts:

1. **No environment has applied either version-23 migration, and no released artifact depends on the colliding names:** a separately reviewed migration-history correction may establish unique identities/order before deployment. Validate the complete migration chain from an empty database and from each supported prior version.
2. **One version-23 migration is proven applied consistently:** preserve that environment's data and history. Recover the other schema change through a uniquely versioned, forward-only change designed for the actual deployed state; test it against a restored clone. Do not silently substitute different SQL under an already-used version.
3. **Different environments have different version-23 effects, or the state is mixed/ambiguous:** do not run a common migration. Reconcile each environment's schema/data fingerprint first, then develop and rehearse a forward-only compatibility/reconciliation plan for each proven state. Any metadata correction must follow, not precede, schema and data verification.

For the local development database, first diagnose and preserve the dirty version-7 state using a backup/clone and a read-only catalog comparison. Decide whether it contains data to retain before any repair. Do not use `-force` merely to make the runner proceed. No recovery option above was selected or executed during this audit.

## 7. Required implementation sequence

1. Record staging/production migration state and schema fingerprints; diagnose development version 7 on a restorable clone.
2. Approve a migration-history collision resolution that preserves every deployed schema lineage. Validate the migration source initializes and the full chain applies/reverts only where reversibility is explicitly safe.
3. Implement and integration-test Inventory's transaction-aware receipt/return ports and Ledger's transaction-aware GRNI/AP/settlement/return posting-policy ports. Enforce module ownership.
4. Add shared durable operation identity and canonical payload/result replay; add transactional outbox persistence and delivery. Define transactional audit append semantics that propagate failure.
5. Implement Purchase schema/domain and draft/order workflow with tenant-safe constraints, branch scope, exact quantities/money, authorization, maker-checker, and version checks. Reconcile the legacy `000008_purchase` schema and data before any production migration.
6. Implement receipt posting only after Inventory, Ledger, audit, operation identity, and outbox pass shared-transaction rollback, retry, and concurrency tests. Then add invoice matching/posting, payment settlement/reversal, returns, and closure.
7. Wire Purchase in the API composition root and RBAC seeds; add route middleware, OpenAPI/Scalar documentation, metrics/traces/logging, reconciliation jobs, and security/race/failure-injection tests.
8. Rehearse rollout and recovery on sanitized clones of every known schema lineage; verify journal/stock/audit/outbox reconciliation before production approval.

## 8. Exact files/modules for follow-up work

| Area | Files/modules to review or change in a later, approved implementation |
|---|---|
| Migration source and CLI | `backend/internal/platform/db/migrate.go`, `backend/cmd/migrate/main.go`, `backend/migrations/embed.go`, both version-23 up/down pairs, `backend/deployments/docker/docker-compose.yml`; do not alter existing migration history until deployment evidence is complete. |
| Dirty development DB | The deployed migration state and catalog only. Any database repair is a separate, explicitly approved operation after backup/clone rehearsal. |
| Inventory port | `backend/internal/modules/inventory/service.go`, `repository.go`, `model.go`, `routes.go`; `backend/migrations/000026_inventory_core.up.sql` and the established Inventory migration lineage. |
| Ledger GRNI/policy | `backend/internal/modules/ledger/auto_post.go`, `service.go`, `repository.go`, `model.go`, `seed.go`; the proven Ledger migration lineage including `000023_ledger_contract.up.sql`. |
| Transactional audit | `backend/internal/platform/audit/sink.go`, `backend/internal/middleware/audit.go`, `backend/migrations/000011_audit_partitioned.up.sql`. |
| Durable operation identity/idempotency | `backend/internal/middleware/idempotency.go`, shared platform/database infrastructure, and a reviewed additive PostgreSQL schema. Keep Redis as best-effort replay assistance, not the source of truth. |
| Transactional outbox/jobs | New shared PostgreSQL outbox persistence and an operational worker/dispatcher; the current `backend/internal/jobs/` files and `backend/cmd/worker/main.go` are stubs. |
| Purchase module | `backend/internal/modules/purchase/{model.go,dto.go,handler.go,repository.go,routes.go,service.go,state_machine.go,*_test.go}`; current non-model files are placeholders. Add module composition in `backend/cmd/api/main.go`, permissions/role grants in `backend/internal/common/constants/constants.go` and `backend/internal/modules/rbac/seed.go`, and Purchase API definitions in `backend/api/openapi/` and Scalar bundle. |

## 9. Audit actions and change confirmation

- Read migration files, migration loader/CLI, relevant module code, configuration key names, and deployment definitions.
- Queried only the available local development database's catalog using a read-only PostgreSQL session. No business rows were read for this report.
- Did not connect to staging or production.
- Did not run `migrate`, `seed`, DDL, DML, `force`, rollback, or database repair commands.
- Did not edit existing source, SQL, configuration, migration, or other worktree files. This report is the only file created for the audit.
