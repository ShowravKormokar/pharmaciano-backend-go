# AUDIT_CONTRACT

**Project:** Pharmaciano ERP — Go Backend  
**Module:** `internal/modules/audit/`  
**Status:** Draft — repository facts reviewed; phased policy decisions remain open  
**API Version:** `/api/v1`  
**Architecture:** Modular Monolith · Three-Tier · Contract-First OpenAPI 3.1  
**Primary Storage:** PostgreSQL  
**Existing Infrastructure:** Gin, PostgreSQL, Redis, Asynq, existing middleware, structured logging, and configuration  
**Prerequisites:** Foundation, authentication, RBAC, middleware, router, and PostgreSQL migration infrastructure. Settings exists, but Audit must not depend on mutable settings for its security, retention, or durability policy.

---

## 1. Purpose

The Audit module provides trustworthy, authorized, searchable, and retention-managed evidence of security activity and business operations across Pharmaciano.

It must answer:

- Who performed an operation?
- What operation was attempted or completed?
- Which organization, branch, resource, and record were affected?
- When did it happen?
- Did it succeed, fail, or get rejected?
- What relevant state changed?
- Which request, business operation, or distributed workflow did it belong to?
- Can the evidence be retrieved and verified later?
- How is evidence retained, archived, exported, and protected against unauthorized modification?

**Core principle:** Audit evidence is append-only application evidence. It is not ordinary application logging, a replacement for database transactions, or a substitute for domain-owned financial records.

The module must reuse the existing technical/security audit pipeline and add approved business-audit capabilities without duplicating existing infrastructure. This contract describes a target state, not evidence that all listed capabilities or guarantees exist today. Implementation must be phased; unresolved legal, retention, archive, and durability decisions in §22 are release gates, not implicit permission to invent policy.

## 2. Design principles

1. **Preserve the existing audit pipeline.** Inspect and extend the current sink, middleware, worker, model, and migration design before introducing new tables or components.
2. **Separate technical and business audit.** Request/security evidence and committed business-change evidence have different consistency and retention requirements.
3. **Keep evidence append-only.** Normal application roles must not edit or delete audit events.
4. **Make business evidence transactionally correct.** Mandatory audit evidence for a business mutation must commit atomically with the operation it describes.
5. **Use PostgreSQL as the durable authority.** Redis, Loki, and worker queues are not authoritative audit databases.
6. **Partition for lifecycle management.** Use time-based partitioning when supported by the existing schema and PostgreSQL migration strategy.
7. **Bound storage growth.** Limit payload sizes, retain records according to policy, archive eligible evidence, and monitor storage.
8. **Never silently lose mandatory evidence.** A queue or sink failure must follow the explicit failure policy for the event class.
9. **Protect tenant boundaries.** Business audit reads must respect organization and branch authorization; platform security events may have no tenant.
10. **Redact before persistence.** Sensitive credentials and data must not enter audit records merely because they appeared in a request.
11. **Use cursor pagination.** Avoid unbounded queries and deep offset pagination on large audit datasets.
12. **Export asynchronously.** Large exports must not hold API requests open or consume unbounded memory.
13. **Use existing infrastructure.** Reuse current errors, context, middleware, UUID conventions, transactions, rate limiting, telemetry, and configuration.
14. **Keep the design operable.** Partition creation, retention, archival, and export cleanup must be automated, observable, and safe to retry.

### 2.1 Repository state at contract review

The following is the observed baseline, not a guarantee or a completed feature list:

| Area | Current repository state | Contract implication |
|---|---|---|
| Technical audit storage | Migration `000011` creates `audit_logs`, a range-partitioned table and a `DEFAULT` partition, plus a separate `audit_log_archive` table. | The archive table is only a schema destination; it does not implement an archive workflow. |
| Technical request capture | `middleware.Audit` captures mutating HTTP requests and calls the configured sink. The entry includes fields not persisted by the PostgreSQL sink, including status code and masked request body. | Document exactly which fields are durable; do not equate middleware capture with stored evidence. |
| PostgreSQL sink | `PostgresAuditSink` inserts a narrow projection into PostgreSQL, but the `AuditSink` port returns no error and the sink logs and swallows insert errors. | It is best-effort technical telemetry, not transactionally reliable business evidence. |
| Loki | Optional fanout sink; delivery is best-effort and process-memory buffered. | Loki is neither durable storage nor proof of successful PostgreSQL persistence. |
| Audit module | The current implementation provides validated DTOs, scoped read repository/service, protected read routes, access auditing, telemetry, and focused tests. The six read operations are registered in the API and documented in OpenAPI. | Business-event capture, asynchronous export, automated partition lifecycle, and audit retention/archive workflows remain incomplete; the read API must not be represented as the full target contract. |
| Partition lifecycle | The migration has a default partition, but no monthly partition-creation worker was found. | `audit.partition_by: month` is configuration, not automated monthly partition maintenance. |
| Retention and archive | The existing retention worker sweeps auth-related tables and explicitly excludes audit tables. No archive manifest, verification, legal-hold, or audit deletion workflow was found. | Configured retention/archive ages are not enforced for audit data. Do not start destructive cleanup until policy and verification exist. |
| Business events and exports | No separate business-event, export-request, or export-artifact schema/API implementation was found. | These remain future work and require approved transaction, privacy, storage, and authorization decisions. |

Treat the gaps above as implementation prerequisites. Do not describe the Audit module as production-complete until each required capability is implemented, tested against PostgreSQL, and operated with explicit durability and retention policies.

## 3. Scope and ownership

### 3.1 Included

- Existing technical/security audit pipeline integration.
- HTTP request and security-event evidence.
- Business audit events for committed domain operations.
- Before/after snapshots for approved, redacted fields.
- Organization and branch-aware audit access.
- Cursor-paginated search and detail APIs.
- Distinct module/action metadata endpoints.
- Asynchronous CSV export.
- Partition lifecycle management.
- Retention and archival policies.
- Legal holds where required by approved business/compliance policy.
- Storage monitoring and audit-pipeline health.
- Evidence integrity controls and regression tests.

### 3.2 Excluded

The Audit module must not own or replace:

- Authentication/session/refresh-token persistence.
- RBAC policy and permission assignment.
- Application debug, access, and performance logs.
- Domain transaction tables.
- Financial journals, inventory movements, invoices, or immutable transaction records.
- Backup execution and database recovery.
- Notification delivery.
- A second independent logging framework.
- A general-purpose data lake or SIEM implementation.

Domain modules remain authoritative for business state. Audit records provide evidence of actions and changes; they do not replace the underlying transaction records.

## 4. Dependencies and integration boundaries

| Dependency | Responsibility |
|---|---|
| Existing authentication | Authenticated actor and session context |
| Existing tenant middleware | Organization context |
| Existing RBAC | Permissions and resource visibility |
| Existing branch authorization | Branch-level access restrictions |
| Existing audit middleware | HTTP/security event capture |
| Existing audit sink | Durable technical audit persistence |
| Existing PostgreSQL platform | Transactions, migrations, partitioned tables and queries |
| Existing transaction helpers | Atomic business-event persistence |
| Existing error package | Consistent domain/API errors |
| Existing rate limiter | Read and export protection |
| Existing structured logger | Operational diagnostics only |
| Existing Redis | Optional coordination/cache; not evidence storage |
| Existing Asynq | Export jobs and asynchronous processing when configured |
| Existing outbox, if available | Reliable post-commit event publication |
| Existing object/archive storage, if available | Long-term archive artifacts |

Do not introduce a second PostgreSQL connection manager, transaction manager, worker framework, audit middleware, or error taxonomy.

## 5. Audit event classes

The implementation must distinguish at least the following event classes.

### 5.1 Technical and security audit

Examples:

- Login success/failure.
- Authentication and authorization rejection.
- Session revocation.
- Privileged administrative access.
- Sensitive configuration changes.
- Protected API operations and their outcomes.
- Rate-limit/security events where the existing policy requires audit evidence.

These events originate from the existing middleware and security pipeline.

The current configuration includes:

- `audit.enabled: true`
- `audit.async: false`
- Monthly partitioning.
- `retention_days: 365`
- `archive_after_days: 90`
- Optional Loki delivery settings.

The synchronous PostgreSQL audit sink must remain the operational default while asynchronous durability and worker behavior are not fully established.

Do not change `audit.async` to `true` merely to improve request latency. First prove that the configured worker, durable enqueue path, retry behavior, backpressure, and failure handling are production-ready.

### 5.2 Business audit

Examples:

- Organization or branch administration.
- User, role, or permission changes.
- Settings and feature-flag changes.
- Medicine catalog changes.
- Inventory adjustments and transfers.
- Purchase creation, approval, receipt, and cancellation.
- Sale, return, refund, and void operations.
- Financial posting and reversal.
- Export and backup administration.

Business events describe the domain operation and its committed outcome. They must be written by the owning service or transaction orchestration layer, not inferred solely from HTTP status codes.

### 5.3 Data-change audit

Where before/after data is necessary, record a minimal, redacted change representation.

Prefer changed fields and relevant identifiers over copying entire database rows.

Do not capture full request or response bodies as a general audit strategy.

### 5.4 Financial and inventory evidence

For financial postings, payments, inventory movements, and similar critical operations:

- Keep the authoritative record in the owning domain.
- Record a business audit event in the same transaction where mandatory.
- Include stable references to the authoritative operation.
- Preserve reversal/correction relationships.
- Never treat an audit event as a substitute for a journal, stock movement, or transaction receipt.

## 6. Domain model

The existing `common.BaseModel`, `common.AuditColumns`, `common.JSONMap`, UUID utilities, and database conventions must be inspected before designing any new model.

`BaseModel` contains standard identity and lifecycle fields. However, audit evidence must not inherit ordinary mutable-record semantics blindly.

**Audit events are append-only.** Do not expose `UpdatedAt` or `DeletedAt` as ordinary lifecycle operations for audit records. If the existing technical model already uses `BaseModel`, preserve compatibility while preventing application-level updates and deletes.

### 6.1 Existing technical event

Reuse the existing `audit_logs` table and `PostgresAuditSink`. The current persistence model is defined by migration `000011_audit_partitioned.up.sql`; `internal/modules/audit/model.go` must map that schema exactly and must not invent columns that the migration does not contain.

Current table columns are: `id`, `created_at`, `organization_id`, `branch_id`, `user_id`, `role_name`, `session_id`, `request_id`, `module`, `action`, `entity_type`, `entity_id`, `ip`, `browser`, `os`, `device`, `location`, `user_agent`, `details`, `before_data`, `after_data`, `outcome`, `error_code`, and `duration_ms`.

`created_at` is the occurrence/partition timestamp. The primary key is `(id, created_at)`, not `id` alone; a detail lookup that accepts only an ID cannot rely on a database-enforced global uniqueness constraint and must use an explicit, measured lookup policy. The legacy `audit_log_archive` has the same columns but a separate primary key on `id`; it is not the same partitioned table.

Detail lookups must provide both the event ID and exact `occurred_at` value returned by a list response. This is the partition key, enables partition pruning, and avoids assuming that the partitioned primary key makes IDs globally unique. Do not add an ID-only fallback query across every partition.

`common.AuditColumns` is not an appropriate embedded model for audit evidence. It embeds `BaseModel` (`updated_at` and `deleted_at`) and adds `created_by`/`updated_by`, none of which exist in `audit_logs`. Audit records must not expose ordinary update/delete lifecycle fields. Map `created_at` explicitly as the event occurrence time; do not silently change `common.AuditColumns` or its users to fit this module.

The migration constrains `outcome` to `success` or `failure`; the current schema has no `status_code` or `actor_type` column. A rejected request is currently represented, if captured, as a failure, and the HTTP status is not persisted by the current sink. Do not claim status-code or actor-type query support until a reviewed migration and sink mapping add it. Likewise, the schema's nullable actor/tenant columns do not by themselves distinguish anonymous, system, or platform actors.

The sink writes method, route, path, module, and action into `details`; it does not currently persist `AuditEntry.StatusCode` or `AuditEntry.RequestBody` in PostgreSQL. The request body may be present in the in-memory entry sent to Loki. Do not treat these in-memory fields as durable evidence until the persistence mapping is deliberately changed and tested.

The existing request audit middleware records mutating requests only. Audit-log reads and export creation are not currently recorded as separate access events. For environments that require evidence of who viewed or exported sensitive audit data, add an explicit, non-recursive access-event policy and durable sink mapping before claiming that audit access itself is audited.

The logical event fields should cover, where available:

- Event ID.
- Organization ID, nullable for platform-level events.
- Branch ID, nullable where not applicable.
- User/actor ID, nullable for anonymous or system actors.
- Actor type and role metadata where safe.
- Session ID, where appropriate.
- Module.
- Action.
- HTTP method and normalized route template.
- Outcome.
- HTTP status.
- Safe error code.
- Request/correlation ID.
- Client IP according to trusted-proxy policy.
- User-agent metadata with bounded length.
- Occurrence timestamp in UTC.
- Duration.
- Redacted, bounded details.

Do not assume every field must be added if the existing schema already carries it or the source cannot reliably provide it.

The project-specific `audit_logs` projection and the existing `middleware.AuditEntry` are not currently one-to-one. Before expanding the model or promising API fields, define the migration and sink mapping for every new durable field. Keep IP as the project's established nullable string representation with explicit `::inet` writes and `host(ip)` reads; normalize invalid/empty values to SQL NULL rather than sending an empty string to PostgreSQL `inet`. Normalize zero UUIDs and empty optional strings to NULL where their columns are nullable. Enforce the migration's actual bounds: `role_name` and `request_id` 80 characters; `module` 60; `action` 40; `entity_type` 60; `browser` and `os` 80; `device` 40; `location` 100; `user_agent` 500; and `error_code` 40. Bound JSON details by an explicit policy, and reject or safely handle durations outside the `INTEGER` range before insertion.

The current sink interface returns no error, and `PostgresAuditSink.Record` logs and swallows database failures. The current middleware invokes it after a mutating HTTP request; this is a best-effort technical/request record, not a fail-closed mandatory business-audit mechanism. Never use this interface to claim that Class A business evidence is durable or transactionally committed. Mandatory business events require a separate transaction-aware write path that returns errors to the owning domain transaction.

The current `LokiAuditSink` is optional and best-effort: pending entries are held in process memory, failed pushes are dropped, and there is no durable queue. `BatchSize` triggers a flush but does not cap pending memory or the amount sent in one request. Its current `Stop` behavior is not safe to call repeatedly or concurrently with new `Record` calls. These properties must be fixed and tested before claiming bounded buffering, lossless delivery, or robust graceful shutdown. Loki remains non-authoritative regardless.

The current PostgreSQL insert also passes `ClientIP` directly to an `inet` column, passes `LatencyMS` (`int64`) to an `INTEGER` column, and does not normalize zero UUIDs or bound variable-length values against the migration's limits. The model describes the database shape; it does not perform those conversions or validate inserts. Before calling the sink production-safe, normalize optional identifiers and empty values, validate IP addresses, cast IP parameters to `inet`, reject or safely handle out-of-range durations, and bound all strings/JSON to a deliberate policy. Insert errors must be surfaced to an observable health/alert path even when technical-audit failure is configured not to fail the request.

### 6.2 BusinessAuditEvent

Logical fields:

- Event ID.
- Organization ID.
- Actor ID, nullable for system actors.
- Actor type.
- Action.
- Entity type.
- Entity ID.
- Outcome.
- Occurrence timestamp.
- Operation ID or business transaction reference.
- Correlation/request ID.
- Optional reason.
- Redacted before-data.
- Redacted after-data.
- Relevant metadata.
- Schema/event version.

Business events are append-only and associated with the owning organization's scope.

Recommended uniqueness semantics must be defined around the actual business operation and event identity. Do not use a global uniqueness constraint that is incompatible with partitioned PostgreSQL tables.

### 6.3 Multi-branch evidence

Some business operations affect multiple branches, such as stock transfers.

Where one event must be visible to multiple authorized branch scopes, represent the branch relationship explicitly, for example through a branch-link table containing organization ID, event ID, and branch ID.

Enforce tenant consistency and composite foreign keys where practical.

Do not duplicate the same logical event merely to make it visible in multiple branches unless the event contract explicitly requires separate records.

### 6.4 ExportRequest

Track export requests with:

- Export ID.
- Requesting actor.
- Authorized organization/scope.
- Validated filters.
- Format.
- Status.
- Created/started/completed timestamps.
- Artifact reference, when ready.
- Artifact checksum.
- Row count and size metadata where available.
- Expiry timestamp.
- Safe failure code.
- Correlation ID.

Do not store the generated CSV content directly in the database.

### 6.5 Retention policy

Retention policy metadata may identify:

- Record class.
- Retention duration.
- Archive eligibility.
- Whether a legal hold is required or applicable.
- Policy version.
- Administrative change metadata.

Retention configuration is security-sensitive. Only authorized administrators may change it, and changes must themselves be audited.

A policy must not reduce retention below the approved minimum or override a legal hold.

### 6.6 Legal hold

If legal holds are required, represent activation and release as auditable, append-only actions.

A hold must identify its scope, reason, actor, activation time, and release metadata.

Do not implement a generic legal-hold engine without a defined operational or compliance requirement. If included, its effect on retention and archive deletion must be tested.

## 7. Database and partition strategy

### 7.1 Partitioning

The existing configuration specifies monthly partitioning, and migration `000011` uses range partitioning on `created_at`. However, that migration creates only `audit_logs_default`; it does not create monthly partitions. Preserve month-based partitions as the target only after adding an idempotent maintenance mechanism and safe handling for data accumulated in the default partition.

Use PostgreSQL declarative range partitioning on the audit occurrence timestamp where feasible.

For example, partitions represent UTC calendar months:

- `audit_logs_2026_10`
- `audit_logs_2026_11`

Actual partition names and creation methods must follow the migration conventions.

Monthly partitioning supports:

- Smaller indexes per partition.
- Time-bounded query pruning.
- Predictable retention cleanup.
- Fast removal of expired data by detaching or dropping eligible partitions.
- Easier archive lifecycle management.

Partitioning is not a complete storage strategy by itself. Without retention and archive cleanup, the database will continue to grow.

### 7.2 Partition correctness

PostgreSQL partitioned tables impose uniqueness constraints that must include the partition key in applicable primary/unique constraints.

Therefore:

- Inspect PostgreSQL version and the existing audit schema.
- Design primary and unique keys that are compatible with the partition key.
- Do not assume a globally unique constraint on event ID alone can be enforced across all partitions.
- If global event-ID lookup is required, use an appropriate UUID strategy and verify the resulting query behavior.
- Avoid adding an unbounded secondary lookup table without a demonstrated need.
- Ensure detail lookup uses the partition key where possible.
- If an ID-only lookup must search across partitions, measure and document the approach.

### 7.3 Partition creation

A scheduled maintenance job must ensure the next required partitions exist before they are needed. No such audit partition creator was present at review time; do not count the configuration setting or the default partition as partition maintenance.

The process must:

1. Determine the required future partition horizon.
2. Create missing partitions idempotently.
3. Verify expected partition boundaries.
4. Alert on missing partitions or creation failures.
5. Avoid creating duplicate or overlapping partitions.
6. Preserve the default-partition strategy, if one exists.

If the current PostgreSQL design uses a default partition, verify how rows are moved into new partitions safely. Do not introduce a default partition blindly; it can complicate partition maintenance.

**A missing partition must be detected before it causes audit writes to fail.**

### 7.4 Indexes

Design indexes based on actual query patterns, not every possible field.

Likely access patterns include:

- Organization plus occurrence time.
- Organization plus branch and occurrence time.
- Actor/user plus occurrence time.
- Module/action plus occurrence time.
- Entity type and ID.
- Outcome plus occurrence time.
- Request/correlation ID.
- Export job lookup by requester and creation time.

Use partition-local indexes through the partitioned parent where appropriate.

Avoid indexing large JSON payloads without a proven query requirement.

Migration `000022` adds tenant, branch, user, and platform timeline indexes for the repository's scoped time-order predicates, plus `pg_trgm` indexes for its minimum-three-character scalar metadata search. These indexes intentionally do not cover JSONB payloads. They increase write and storage cost; production rollout must account for index build locks and validate representative plans, partition sizes, and ingestion throughput. Do not claim high-volume performance without `EXPLAIN (ANALYZE, BUFFERS)` and workload measurements on realistic data.

## 8. Storage growth and lifecycle management

This section is mandatory. Audit data can grow rapidly and cause disk exhaustion if every request body is stored indefinitely.

### 8.1 Separate the storage destinations

| Data | Destination | Lifecycle |
|---|---|---|
| Durable technical/security audit | PostgreSQL partitioned audit table | Retention-controlled |
| Business audit evidence | PostgreSQL business audit tables | Retention/hold-controlled |
| Operational application logs | Structured stdout/log platform such as Loki | Log-platform retention |
| Export CSV artifacts | Private object/archive storage or approved artifact storage | Short-lived and expiry-controlled |
| Backups | Existing backup infrastructure | Independent backup policy |

Loki is an operational log destination, not the authoritative audit store.

Do not copy every audit record into Loki and treat that as a substitute for durable database evidence. If both destinations receive events, failures and retention must be handled independently.

### 8.2 Bounded event payloads

Audit payload size must be bounded before database insertion or queueing.

Requirements:

- Use an explicit maximum payload size in configuration or a documented constant.
- Bound request-body snippets, user-agent, IP metadata, error messages, and free-text reasons.
- Never capture multipart files or binary uploads.
- Avoid storing complete request bodies by default.
- Prefer allowlisted event metadata over arbitrary JSON.
- Redact sensitive fields recursively before persistence.
- Reject or truncate oversized optional details according to a documented policy.
- Preserve mandatory identifying metadata when optional details are truncated.
- Record that redaction/truncation occurred without exposing the original sensitive content.

Redaction must handle nested objects and arrays, case variations, and known credential/secret patterns.

At minimum, never store:

- Passwords or password hashes.
- Access and refresh tokens.
- Authorization headers and cookies.
- OTPs, recovery codes, or MFA secrets.
- Private keys and API credentials.
- Database connection secrets.
- Unnecessary national identity or passport data.
- Payment credentials or other prohibited sensitive data.

Do not rely on a top-level-only redaction helper for nested request structures.

### 8.3 Retention

Current configured defaults:

- `retention_days: 365`
- `archive_after_days: 90`

Treat these as configurable policy values, not universal legal requirements.

Define the exact lifecycle:

1. Events remain queryable in the hot PostgreSQL store.
2. Events become eligible for archival after the configured archive age.
3. Archived data is verified before any source partition is removed.
4. Data is retained according to the approved retention policy.
5. Expired, unheld data is deleted or its complete partition is dropped only after eligibility is established.
6. Legal holds, if implemented, prevent deletion of covered evidence.
7. All lifecycle operations produce operational logs and metrics; administrative policy changes are audited.

Do not confuse `archive_after_days` with `retention_days`. Archival does not mean deletion, and a record must not be removed merely because it was archived.

### 8.4 Archive strategy

Prefer a low-cost archive destination suited to large historical datasets, such as private object storage with appropriate lifecycle controls.

The archive format should be:

- Versioned.
- Compressed.
- Encrypted in transit and at rest.
- Organized by event class and time range.
- Accompanied by a manifest with schema version, event count, time boundaries, checksum, and creation metadata.
- Verifiable before source deletion.
- Accessible only through controlled administrative or investigation workflows.

Possible formats include compressed newline-delimited JSON or Parquet. Choose one format based on actual retrieval, tooling, and operational requirements; do not implement multiple formats without need.

Archive files must not be publicly accessible.

### 8.5 Safe archive/delete workflow

The archive job must be idempotent and recoverable.

Recommended workflow:

1. Select eligible partitions or bounded time ranges.
2. Exclude records covered by legal holds.
3. Export the selected data into a temporary archive artifact.
4. Calculate and record a cryptographic checksum.
5. Validate record count, time boundaries, and schema.
6. Persist the archive manifest and archive reference.
7. Verify that the archive artifact can be read.
8. Mark the range as archived.
9. Delete source data only when the retention policy permits deletion and the archive has been verified.
10. Record the operation result and emit metrics.

A failed archive must never trigger source deletion.

Where PostgreSQL partition detachment or dropping is used, verify that every record in the partition is eligible. If records in a partition have different retention or legal-hold requirements, partition-level deletion may be unsafe. Use a policy-compatible strategy or retain the partition until all records are eligible.

Do not implement destructive retention jobs before archive integrity and legal-hold behavior are tested.

### 8.6 Capacity planning and alerts

Monitor:

- Database size and growth rate.
- Audit-table and partition size.
- Index size.
- Oldest unarchived eligible event.
- Archive backlog.
- Partition creation horizon.
- Export artifact storage.
- Failed archive/delete operations.
- Audit write failures.
- Queue depth and oldest queued event, if asynchronous processing is enabled.

Define warning and critical thresholds in deployment configuration based on available storage and observed event volume.

Alert before the database filesystem approaches exhaustion.

## 9. Audit write pipeline and durability

### 9.1 Existing synchronous pipeline

With `audit.async: false`, preserve the existing synchronous `PostgresAuditSink` behavior.

Verify:

- Middleware can construct a safe audit event.
- The sink persists it through the established PostgreSQL path.
- Errors are observable.
- Context cancellation does not accidentally discard a mandatory business event.
- Failure behavior follows the event's durability classification.

Do not add a second sink or duplicate middleware.

### 9.2 Optional asynchronous pipeline

Asynchronous technical auditing may be enabled only after the worker and durable delivery design are ready.

An in-memory channel or fire-and-forget goroutine is not sufficient for mandatory evidence.

If Asynq is used, verify:

- Durable enqueue semantics.
- Retry/backoff policy.
- Maximum attempts.
- Queue capacity and backlog monitoring.
- Idempotent writes or deduplication.
- Poison-event handling and dead-letter/reconciliation procedures.
- Graceful shutdown.
- Behavior when Redis or the queue is unavailable.
- Behavior when the database is unavailable.
- Whether event loss is acceptable for that event class.

The queue is not the final audit store.

### 9.3 Event durability classes

Define at least two classes:

**Class A — Mandatory business evidence**

Examples: committed sale, refund, purchase receipt, stock adjustment, journal posting, role/permission mutation.

- The event must be persisted in the same database transaction as the corresponding business mutation, or through a proven transactional-outbox design.
- If mandatory evidence cannot be committed, the business operation must not be reported as successfully committed.
- Do not rely on a later best-effort middleware callback to record this evidence.

**Class B — Technical/request evidence**

Examples: request metadata, rejected request, operational security telemetry.

- Follow the existing technical audit policy.
- Synchronous PostgreSQL persistence remains the current default.
- Any future asynchronous mode requires an explicit durability and failure policy.

Not every request failure must block an API response. However, the system must never silently treat mandatory business evidence as successfully recorded when it has been lost.

## 10. Transaction and concurrency contract

### 10.1 Business events

The owning domain service controls the business transaction.

For a mandatory business audit event:

`Begin transaction → Mutate domain state → Insert business audit event → Insert outbox event if required → Commit`

If any mandatory operation fails, roll back the entire transaction.

Do not let the Audit service open and independently commit a separate transaction for an event that must be atomic with the business mutation.

Provide transaction-aware repository methods or the existing transaction abstraction.

### 10.2 Technical events

Technical request auditing follows the existing middleware/sink contract. Do not force all HTTP request logs into the same transaction as business data; these represent different evidence classes.

### 10.3 Idempotency

Retrying a request or worker job must not create duplicate business evidence for the same logical operation.

Use stable operation/event identity where available.

Do not assume that request ID alone is a sufficient deduplication key: a client retry may create a new request ID for the same logical operation.

Partition-compatible uniqueness constraints and concurrency behavior must be verified against the chosen schema.

## 11. Security, isolation, and immutability

### 11.1 Permissions

The required read/export permissions are:

- `audit:view`
- `audit:export`

The original API specification permits `SUPER_ADMIN` and users with `audit:view` to inspect logs. Preserve this policy while applying tenant and branch visibility constraints.

Global platform-security events without an organization require a separate system-level access path restricted to authorized platform administrators.

Do not expose all organizations' audit records merely because a user has `audit:view`.

### 11.2 Route protection

Use the existing middleware conventions for:

- Authentication.
- Session validation.
- Tenant context.
- RBAC.
- Permission checks.
- Rate limiting.
- Audit of sensitive administrative actions.

Do not implement authorization inside SQL filters alone. Enforce the permission boundary before service access and enforce organization/branch scope in the service/repository query path as appropriate.

### 11.3 Append-only access

Normal application database roles should have only the insert/select privileges required by their responsibilities.

Where feasible:

- Separate audit writers from audit readers.
- Deny ordinary `UPDATE` and `DELETE` privileges on event tables.
- Use separate, tightly controlled database roles for partition maintenance and retention.
- Prevent API handlers from exposing mutation or deletion operations for audit events.
- Restrict administrative database access and record privileged maintenance activity.

Application-level append-only behavior is necessary but not sufficient if all application components share a database role with unrestricted write privileges.

### 11.4 Integrity verification

For high-assurance environments, consider signed archive manifests or chained event hashes.

Do not add a hash chain by default without assessing partitioning, concurrent writers, ordering semantics, and throughput. A naïve global hash chain can serialize writes and become a bottleneck.

At minimum, archive checksums and manifests must make corruption or incomplete export detectable.

## 12. Query API contract

Base path: `/api/v1`

### 12.1 Audit endpoints

The Audit module currently mounts the read endpoints below. Each route is rate-limited, authenticated, tenant-scoped, and RBAC-protected; authorized GET access is recorded through the audit sink without recording query values or response bodies. Audit responses disable intermediary caching.

| Method and path | Rate policy | Permission | Description |
|---|---|---|---|
| `GET /audit-logs` | `audit_read` | `audit:view` | Search/list audit records using cursor pagination |
| `GET /audit-logs/{id}?occurred_at={RFC3339}` | `audit_read` | `audit:view` | Retrieve authorized event details using its occurrence/partition timestamp, including redacted before/after data where available |
| `GET /audit-logs/modules` | `audit_read` | `audit:view` | List distinct authorized module values |
| `GET /audit-logs/actions` | `audit_read` | `audit:view` | List distinct authorized action values |
| `GET /audit-logs/platform` | `audit_read` | `audit:view` plus SUPER_ADMIN | List only platform events with no organization owner |
| `GET /audit-logs/platform/{id}?occurred_at={RFC3339}` | `audit_read` | `audit:view` plus SUPER_ADMIN | Retrieve one platform event using its partition timestamp |

`GET /audit-logs/export?format=csv` remains a target endpoint and is intentionally not mounted: the export job, authorization lifecycle, artifact storage, expiration, and download policy are not implemented. Do not provide a synchronous or success-shaped placeholder in place of that workflow.

Handlers reject unknown, repeated, malformed, and oversized query parameters. Read endpoints apply a fail-closed `audit_read_ip` policy (300 requests per IP per minute) before authentication and a fail-closed `audit_read` policy (60 requests per authenticated user per minute) after authentication. Both policies return service unavailable when Redis or policy configuration is unavailable. Audit record and export creation access must be durably recorded before any future export endpoint is enabled.

Audit reads are included in the shared HTTP request/latency metrics and distributed tracing. The telemetry registry also exposes `audit_read_requests_total{operation,outcome}`, `audit_sink_writes_total{sink,outcome}`, and `security_rate_limited_total{policy}`. Labels are bounded categories only; never add tenant IDs, actor IDs, event IDs, query filters, or payload values. Audit sink failures remain non-blocking by policy but increment the sink error counter and emit a structured warning.

Preserve the existing route naming and response-envelope conventions.

Do not expose a general endpoint to mutate or delete audit records.

### 12.2 Search filters

Support the defined filters where applicable:

- `user_id`
- `module`
- `action`
- `entity_type`
- `entity_id`
- `outcome`
- `from`
- `to`
- `ip`
- `q`
- Cursor and page-size parameters

Validate every filter.

Requirements:

- Require a valid time range where policy requires it.
- Reject malformed timestamps.
- Require both `from` and `to` for list and export queries; normalize accepted RFC3339 values to UTC.
- Enforce a maximum 90-day query range in the DTO. A later policy change may lower this bound, but must not make the query unbounded by default.
- Reject `from > to`.
- Normalize and bound free-text search.
- Use parameterized SQL.
- Apply organization and branch restrictions to every query.
- Never allow the `ip` filter to become an injection vector.
- Use an explicit, deterministic ordering.

### 12.3 Cursor pagination

Use cursor pagination instead of deep `OFFSET` pagination.

The cursor must encode or reference a stable ordering, usually occurrence timestamp plus a unique event identifier compatible with the partition strategy.

Requirements:

- Stable ordering across pages.
- Bounded page size.
- Opaque or integrity-protected cursor representation.
- Cursor validation.
- Verify cursor integrity and bind it to the caller's authorized scope and normalized filters in the service/repository layer; DTO syntax validation alone is not an authorization check.
- Consistent organization/scope binding.
- No ability to change the authorized tenant by editing cursor contents.
- Correct handling of multiple events with the same timestamp.

Avoid running an expensive `COUNT(*)` over the complete audit history for every page. Return `has_more` or a next cursor when appropriate.

### 12.4 Detail endpoint

The detail response must:

- Return only authorized evidence.
- Require `occurred_at` from the selected list item and use it with `id` in the lookup predicate; reject a missing or malformed timestamp rather than scanning all partitions.
- Include redacted before/after data when available and permitted.
- Include actor, module, action, entity, outcome, timestamp, and correlation information where available.
- Avoid exposing raw credentials, tokens, internal secrets, or unredacted payloads.
- Distinguish a missing event from an event outside the caller's visibility according to the established API policy.

### 12.5 Modules/actions endpoints

Distinct values must be derived from authorized data or a controlled registry.

Do not scan the entire historical dataset on every request if a maintained registry or efficient indexed query is available.

These endpoints must not reveal other tenants' activity.

## 13. Asynchronous export

Large CSV exports must be executed as background jobs.

### 13.1 Request flow

`Authenticated request → Validate permission and filters → Create ExportRequest → Enqueue job → Worker queries bounded data → Generate CSV → Verify artifact → Mark completed → Authorized download`

No Audit export route, job-status endpoint, download mechanism, or OpenAPI operation is implemented. Define and review those operations and response contracts in OpenAPI before implementation; do not describe the export flow as already available.

### 13.2 Worker requirements

- Use the existing Asynq infrastructure where configured.
- Validate filters and scope before queueing.
- Persist the export request before acknowledging acceptance.
- Make job execution idempotent.
- Stream rows instead of loading the full result set into memory.
- Use bounded database batches.
- Escape CSV fields correctly.
- Prevent spreadsheet formula injection for values beginning with formula-significant characters.
- Avoid including secrets or unredacted sensitive values.
- Enforce maximum time range, row count, artifact size, and execution time.
- Store artifacts privately.
- Verify artifact checksum and metadata.
- Expire artifacts according to a separate short-lived artifact policy.
- Retain export metadata according to the audit policy.
- Log safe failure codes without exposing query internals or secrets.

### 13.3 Download security

If artifact download is exposed through an API:

- Require authentication.
- Recheck permission and scope.
- Verify that the requester is authorized to access the export.
- Use short-lived, controlled download access.
- Never make export objects public.
- Prevent path traversal and arbitrary artifact-reference access.
- Audit export creation and download where required.

Do not return a permanent public URL.

## 14. Retention, archival, and legal holds

### 14.1 Policy enforcement

Retention must be implemented as a controlled background maintenance process, not as an unbounded API request.

Use the configured values:

- `retention_days: 365`
- `archive_after_days: 90`
- `partition_by: month`

Verify whether the current configuration supports policy differences by event class. Do not invent per-tenant retention values without an approved policy.

### 14.2 Partition-level deletion

A partition may be dropped only if every record it contains is eligible for removal.

Because a monthly partition contains records from different days, the retention job must calculate eligibility against the partition's complete time range and the applicable retention rules.

If records are not uniformly eligible, retain the partition or use a more granular cleanup strategy.

Never bypass legal holds to simplify partition deletion.

### 14.3 Retention job safety

The job must:

- Be idempotent.
- Have bounded work per execution.
- Avoid overlapping destructive executions.
- Use existing distributed locking or job-concurrency controls where available.
- Verify archive completion before source deletion.
- Emit success/failure metrics.
- Alert on prolonged backlog.
- Preserve an administrative maintenance record.
- Recover safely after process or database failure.

Do not run retention as an unbounded startup task.

## 15. Configuration contract

Use the existing configuration package and validation conventions.

Existing configuration:

```yaml
audit:
  enabled: true
  async: false
  partition_by: month
  retention_days: 365
  archive_after_days: 90
  loki:
    enabled: false
    url: http://loki:3100/loki/api/v1/push
    batch_size: 256
    flush_interval: 500ms
    timeout: 3s
```

### 15.1 Existing settings

- `enabled`: enables/disables the existing audit pipeline according to an explicit policy. Disabling mandatory audit must require a controlled operational decision; it must not silently disable mandatory business evidence.
- `async`: remains `false` until the durable asynchronous path and worker are operational.
- `partition_by`: monthly partitioning is the current design.
- `retention_days`: configured retention period.
- `archive_after_days`: age at which records become eligible for archival.
- `loki.enabled`: optional operational log integration.
- `loki.url`: destination URL, validated and protected from unsafe configuration.
- `loki.batch_size`: bounded batch size.
- `loki.flush_interval`: flush interval.
- `loki.timeout`: bounded delivery timeout.

### 15.2 Additional configuration

Introduce additional configuration only when required by the implementation, such as:

- Maximum audit payload bytes.
- Partition creation horizon.
- Retention job batch size.
- Archive destination and encryption configuration.
- Archive verification settings.
- Export maximum range, row count, and artifact size.
- Export artifact TTL.
- Worker retry/backoff settings.
- Alert thresholds.

Defaults must be safe, documented, validated, and compatible with the existing configuration loader.

Do not put credentials or encryption keys directly in the committed YAML file.

## 16. Failure handling

| Failure | Required behavior |
|---|---|
| PostgreSQL audit sink unavailable | Follow explicit event-class durability policy; emit operational alerts |
| Mandatory business audit insert fails | Roll back the owning business transaction |
| Asynq/Redis unavailable | Do not silently acknowledge a mandatory event as durably queued |
| Audit worker crashes | Retry safely; avoid duplicate business evidence |
| Missing future partition | Alert and repair before sustained write failure |
| Archive upload fails | Retain source data and retry |
| Archive checksum/count mismatch | Reject archive; do not delete source |
| Retention job fails | Preserve data; retry safely |
| Export worker fails | Mark job appropriately and allow safe retry |
| Export storage unavailable | Do not report export as complete |
| Loki unavailable | Apply bounded retry/drop policy for operational logs; never delete PostgreSQL audit evidence because Loki failed |
| Redaction failure | Reject or safely minimize the payload; never persist an unredacted sensitive body |
| Invalid or excessive query | Return a structured client error |
| Caller lacks permission | Deny access and follow the security audit policy |

Operational logs should report audit pipeline failures without recursively creating unbounded audit-on-audit events.

## 17. Observability and alerting

Reuse existing structured logging, request IDs, metrics, tracing, and health checks.

Where the infrastructure supports it, measure:

- Audit events written by event class and outcome.
- Audit sink latency and failure count.
- Mandatory audit transaction failures.
- Asynchronous queue depth and oldest queued event.
- Worker retries and dead-letter count.
- Missing partition horizon.
- Audit storage size and growth rate.
- Largest partitions and index growth.
- Archive backlog and oldest eligible unarchived record.
- Archive checksum/verification failures.
- Retention job duration and failures.
- Export queue depth, duration, failures, and artifact size.
- Query latency by endpoint and bounded operation type.
- Rate-limit rejections.
- Redaction/truncation counts.

Do not use user IDs, raw IPs, entity IDs, or arbitrary action strings as unbounded metric labels.

Alert before storage exhaustion, not only after audit writes fail.

## 18. Testing requirements

### 18.1 Model and repository tests

- Append-only persistence behavior.
- Correct UUID and timestamp handling.
- Partition-aware inserts and reads.
- Scope-aware queries.
- Unique/event identity behavior.
- Cursor pagination across partition boundaries.
- Stable ordering for equal timestamps.
- Query filters and date boundaries.
- Transaction-aware business-event insertion.
- Tenant and branch isolation.
- Redaction and payload limits.

### 18.2 Security tests

- Anonymous access denied.
- `audit:view` enforcement.
- `audit:export` enforcement.
- SUPER_ADMIN platform-level access.
- Cross-organization queries denied.
- Branch-restricted users cannot read unauthorized branch evidence.
- No API mutation/deletion of audit events.
- Sensitive payloads are redacted.
- Export access is reauthorized.
- Cursors cannot be modified to cross scope boundaries.
- Rate limiting applies to all audit endpoints.

### 18.3 Transaction and worker tests

- Mandatory audit failure rolls back the business transaction.
- Duplicate worker execution does not duplicate logical evidence.
- Worker crash and retry are safe.
- Queue failure is handled according to durability class.
- Archive failure never deletes source data.
- Archive verification mismatch blocks deletion.
- Retention respects legal holds.
- Export failures do not produce completed jobs.
- CSV output is correctly escaped and formula-safe.

### 18.4 PostgreSQL partition tests

Using a real PostgreSQL test environment where available:

- Current and next-month partition inserts.
- Month-boundary timestamp behavior.
- Missing partition detection.
- Partition-aware query pruning where verifiable.
- Indexes and constraints.
- Partition creation idempotency.
- Retention eligibility across month boundaries.
- Safe partition drop/detach behavior.
- Cursor pagination across partitions.

Mocks alone are insufficient to prove PostgreSQL partition correctness.

### 18.5 Load and resilience tests

Simulate sustained event writes and large historical queries.

Measure:

- Insert latency.
- Query latency.
- Memory use.
- Queue growth if asynchronous mode is enabled.
- Archive throughput.
- Database and index growth.
- Export worker memory under large datasets.

Use the observed workload to tune payload limits, batching, indexes, and retention jobs. Do not claim production throughput without measurement.

## 19. Recommended file structure

Follow the repository's actual module conventions and avoid duplicating existing infrastructure.

```text
internal/modules/audit/
├── model.go
├── dto.go
├── repository.go
├── service.go
├── handler.go
├── routes.go
├── audit_test.go
├── business_event.go       # if separate business-event model is needed
├── retention.go            # retention/partition orchestration if needed
├── archive.go              # archive workflow if required
└── export.go               # export job orchestration if required
```

These are candidate files, not a requirement to create empty files.

Use the existing migration directory, configuration package, worker registration, seed conventions, middleware, and application bootstrap.

Inspect the current audit middleware and sink before changing their ownership. The Audit module must reuse the existing pipeline rather than register a second copy.

## 20. Implementation sequence

### Phase 01 — Audit architecture and contract freeze

- Inspect existing `audit_logs` schema and `PostgresAuditSink`.
- Inspect middleware, worker/Asynq configuration, and existing migrations.
- Confirm the current event fields and redaction implementation.
- Distinguish technical audit from business audit.
- Freeze durability classes, scope rules, retention behavior, and export API.

### Phase 02 — Schema and partition design

- Reuse existing audit schema where appropriate.
- Define business event and export metadata tables only where missing.
- Verify partition-compatible keys and constraints.
- Add required indexes and partition maintenance strategy.
- Avoid destructive migrations.

### Phase 03 — Model and DTO contracts

- Define typed event models and query DTOs.
- Bound payloads and validate filters.
- Define cursor encoding and response contracts.
- Define export request/status DTOs.

### Phase 04 — Repository layer

- Implement append-only writes.
- Implement transaction-aware business-event insertion.
- Implement partition-aware reads.
- Implement safe cursor pagination and metadata queries.
- Implement export scanning and retention queries as needed.

### Phase 05 — Service layer

- Implement scope-aware query authorization.
- Implement redaction and payload policy.
- Implement business audit transaction participation.
- Implement export orchestration.
- Implement retention/archival orchestration only after policy is frozen.

### Phase 06 — Handler and routes

- Implement the specified audit read/detail/metadata/export endpoints.
- Reuse established response envelopes and errors.
- Enforce permissions and rate limits through existing middleware.
- Verify registered routes against OpenAPI.

### Phase 07 — Worker and storage lifecycle

- Integrate the existing Asynq setup if appropriate.
- Implement bounded exports.
- Implement partition creation monitoring.
- Implement archive verification and safe retention.
- Keep async mode disabled until all durability requirements pass.

### Phase 08 — Security and hardening

- Test isolation, immutability, redaction, and failure behavior.
- Validate storage-growth controls.
- Run partition integration tests.
- Add metrics and alerts.
- Run full tests, build, lint, and OpenAPI validation.

Do not implement destructive archival/retention before the schema, retention policy, legal-hold semantics, and recovery procedure are agreed.

## 21. Definition of done

The Audit module is complete only when:

- Existing technical/security audit behavior is preserved.
- Business audit is distinct and transactionally correct.
- Audit events are append-only through normal application interfaces.
- Tenant and branch visibility is enforced.
- Read/export permissions and rate policies are applied.
- Cursor pagination works across partitions.
- Sensitive values are redacted before persistence.
- Event payload size is bounded.
- Monthly partitions are created and monitored safely.
- Retention and archival are distinct and operational.
- Source data is never deleted before archive verification and retention eligibility.
- Legal holds are respected where implemented.
- Export jobs are bounded, private, asynchronous, and retry-safe.
- Audit failures are observable.
- Mandatory business evidence cannot be silently lost.
- PostgreSQL remains the authoritative evidence store.
- Existing middleware and error conventions are reused.
- OpenAPI matches the actual routes.
- Integration tests validate PostgreSQL partition behavior.
- Storage growth, archive backlog, and partition health are monitored.
- No unrelated modules are rewritten.

## 22. Contract decisions to freeze before implementation

Confirm these decisions against the existing repository and Astra architecture:

1. Exact existing technical audit schema and sink contract.
2. Which technical events are mandatory and which may be best-effort.
3. Business-event schema and the owning transaction interface.
4. Whether a business audit table already exists.
5. Partition key and partition-compatible identity/uniqueness constraints.
6. Exact policy for events with no organization context.
7. Retention and archive rules for each event class.
8. Archive storage destination, encryption, and verification method.
9. Legal-hold requirements and authorization.
10. Export job-status and artifact-download endpoints.
11. Asynq worker durability, retries, and failure handling.
12. Whether Loki is used for operational logs only or receives a controlled audit projection.
13. Maximum payload size and export limits.
14. Whether archive integrity needs signed manifests or another tamper-evidence mechanism.

Do not silently assume legal requirements, archive infrastructure, or asynchronous durability that the current project has not implemented.

## 23. Final rule

**Build the Audit module as a durable evidence system with controlled growth—not as a table that accumulates every request forever.**

Reuse the existing technical/security audit pipeline, atomically record mandatory business evidence, partition and index for real query patterns, bound event payloads, archive verified records, enforce retention safely, and monitor storage before capacity becomes a production incident.

No record may be silently lost, no sensitive payload may be persisted without redaction, and no historical evidence may be deleted before the approved retention policy permits it.