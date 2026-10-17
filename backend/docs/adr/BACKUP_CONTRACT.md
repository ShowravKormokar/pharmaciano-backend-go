# Backup Module Contract — Pharmaciano:ERP

> **Status:** Implementation contract; backup capability is intentionally unavailable and not production-ready  
> **Module:** `internal/modules/backup`  
> **API version:** `/api/v1`  
> **Primary concerns:** recoverability, artifact lifecycle, privileged operations, tenant-safe exports  
> **Storage strategy:** provider-neutral abstraction; local development adapter first; S3-compatible adapter later  
> **Database authority:** PostgreSQL  
> **Queue:** existing Asynq/worker infrastructure where configured  
> **Timezone:** `Asia/Dhaka` for human-facing schedule configuration; timestamps persisted in UTC

---

## 1. Purpose

The Backup module exposes safe operational recovery status and controlled backup operations. It owns **backup orchestration metadata and policy**, not the database itself and not a replacement for managed database recovery features.

It must integrate with the existing Pharmaciano foundation rather than create parallel implementations of authentication, authorization, tenant scope, rate limiting, audit, configuration, logging, metrics, job processing, errors, or database access.

The module must be designed so the initial storage implementation can run without S3, while switching to S3/R2/B2 or another supported S3-compatible provider later requires configuration and deployment changes—not changes to handlers, services, repositories, or backup workflows.

### 1.1 Outcomes

1. Schedule and request a full PostgreSQL logical backup where the deployed database supports `pg_dump`.
2. Persist durable metadata and status for every operation.
3. Encrypt backup artifacts before they leave the process; never write plaintext dumps to persistent local disk.
4. Verify artifact integrity and, where configured, prove recoverability with an isolated restore drill.
5. Enforce retention, minimum verified-backup preservation, legal holds, and safe artifact deletion.
6. Provide secure, short-lived download authorization to eligible platform operators.
7. Provide organization-scoped logical exports only as a separate capability, if/when implemented.
8. Detect failed, stale, missing, and unverified backups through metrics, alerts, and a watchdog.
9. Keep backup work away from request handlers and avoid degrading POS, inventory, purchase, sale, or ledger operations.
10. Keep operational evidence in the existing audit and observability systems.

### 1.2 Non-goals

- Replacing Neon/managed-provider PITR, WAL archiving, snapshots, or provider recovery guarantees.
- Claiming minute-level RPO from `pg_dump` alone.
- Exposing routine live production restore through a tenant-facing HTTP endpoint.
- Backing up Redis as a source of truth; Redis is an optimization/coordination layer, not the system of record.
- Treating a successful upload as proof that the database can be restored.
- Storing database credentials, encryption keys, signed URLs, raw database output, or secrets in API responses or logs.
- Adding file/object uploads to the initial scope. Database backup is the first supported artifact class.
- Building a generic file-storage framework unrelated to backup artifacts.
- Reimplementing existing authentication, RBAC, audit, telemetry, config, errors, or job infrastructure.

---

## 2. Architecture decisions

| Concern | Contract decision | Rationale |
|---|---|---|
| Recovery objectives | Initial operational target: nightly logical backup, nominal RPO up to 24 hours; RTO target ≤ 1 hour is an objective to validate, not a guarantee. Provider PITR may improve RPO only if explicitly enabled and verified. | A dump does not provide continuous recovery by itself. |
| Provider recovery | Treat managed PITR/WAL as a separate infrastructure capability. Record its status/last recoverable time when a provider adapter or operational integration exists. | Avoid falsely claiming PITR from a logical dump. |
| Initial backup type | `full` logical backup using `pg_dump -Fc`, subject to provider compatibility. | Portable, compressed custom-format artifact; supports `pg_restore`. |
| Incremental/WAL | Do not implement application-managed WAL streaming in the first phase unless the deployed provider and infrastructure explicitly support it. | Managed services may not expose the required WAL controls. |
| Storage abstraction | `ArtifactStore` interface; initial local adapter for development/testing; future S3-compatible adapter. | Storage provider must not leak into domain/service code. |
| Production storage | Off-host durable object storage or verified provider backup service is required before production sign-off. Ephemeral container disk is not a backup destination. | Local Render/container disk can disappear on redeploy or instance replacement. |
| Encryption | Envelope encryption: per-artifact random data-encryption key (DEK), authenticated streaming encryption, DEK wrapped by a separately managed key-encryption key (KEK)/KeyRing. | Limits key exposure and enables key rotation. |
| Plaintext | Stream `pg_dump` stdout through encryption directly to artifact storage. No plaintext dump file on disk. | Limits exposure of PII and financial data. |
| Execution | Background worker only; no `pg_dump`/`pg_restore` subprocess in the HTTP request path. | Prevents request timeouts and resource exhaustion. |
| Durable state | PostgreSQL metadata is authoritative. Queue state and Redis locks are not the only record of an operation. | A queue loss or Redis restart must not erase operation history. |
| Concurrency | Durable operation lease/unique active-run constraint plus distributed lock if Redis is available. | Avoid duplicate or overlapping full dumps. |
| Restore | API may request an isolated verification/restore drill. Live production recovery uses a separate privileged CLI/runbook and approved target. | Prevents catastrophic HTTP-triggered overwrite. |
| Tenant access | Physical backup operations are system/platform scoped. Physical artifacts are never exposed to tenant administrators. Tenant logical exports are separate, scoped resources. | Prevents cross-tenant data disclosure. |
| Audit | Record trigger, authorization, download authorization, verification, retention deletion, key/config changes where available, and restore actions. | Backup operations are highly privileged. |
| Retention | GFS-style policy configurable; legal holds and minimum verified-backup protection override ordinary cleanup. | Protects recovery points and compliance evidence. |
| Schedule | Explicit `Asia/Dhaka` schedule; persist timestamps in UTC. | Avoids accidental UTC interpretation of local schedules. |
| Redis | Optional coordination/cache only. PostgreSQL remains authoritative for run state. | Recovery metadata survives Redis loss. |
| Storage migration | Switch by config and adapter registration. Existing artifacts retain a stable provider-neutral reference and are readable through the adapter that owns them. | Avoids rewriting business logic when S3 is added. |

### 2.1 Important distinction: logical backup versus PITR

`pg_dump -Fc` creates a consistent logical snapshot of the database. It does **not** create a WAL archive and does not by itself enable point-in-time recovery.

- A logical dump supports recovery to the dump’s snapshot point, subject to successful restore.
- PITR requires a compatible base backup and continuous, complete WAL archive (or a managed provider feature that offers equivalent recovery).
- If the deployed database provider manages PITR, the module must report it as a distinct capability and must not infer its availability from the existence of a logical dump.
- Missing WAL or a broken archive chain means the relevant PITR window is incomplete, even if a logical dump succeeded.

The initial RPO/RTO values are objectives to measure through drills. Do not present them as service guarantees until infrastructure, data volume, provider limits, and measured restore times validate them.

---

## 3. Scope and ownership

The module is **hybrid-scoped**.

### 3.1 System-scoped resources

These resources represent database-wide recovery and can contain data for multiple organizations:

- Physical/logical full backup runs.
- Backup artifact metadata.
- Verification records.
- Restore-drill records.
- Provider recovery/PITR status, if integrated.
- Retention execution and orphan-artifact reconciliation.
- Storage health and backup watchdog state.

Access requires platform-level authorization. `SUPER_ADMIN` is the default allowed principal for physical backup operations.

### 3.2 Organization-scoped resources

If tenant logical exports are implemented, they must be explicitly separate:

- Export request belongs to one organization.
- Requested branch scope must be derived from the authenticated principal and existing branch authorization rules.
- The caller cannot obtain organization-wide access by omitting a branch filter.
- Export repository methods must require `orgID`; branch-limited methods must enforce the established `appctx.BranchScope(ctx)` rules.
- Tenant export artifacts must never reference or expose physical backup artifacts.
- A tenant export is not a substitute for disaster recovery.

### 3.3 Data ownership

The module owns operational metadata only. PostgreSQL application tables remain owned by their respective modules. The backup module must not query or mutate business tables to implement backup history.

---

## 4. Dependencies and integration with the existing foundation

Reuse existing project components. Before implementation, inspect the actual code and adapt to the real interfaces; do not introduce parallel infrastructure.

### 4.0 Repository baseline and mandatory gaps (audited 2026-10-17)

The following facts describe the repository at the time this contract was reviewed. They are implementation prerequisites, not evidence that a backup capability is already available.

| Area | Current state | Required before enabling Backup |
|---|---|---|
| Backup module | Partial metadata/repository/handler scaffolding and migration `000023` exist, but the module is not composed in `cmd/api`, its route registrar is guarded, the worker handler and `cmd/worker` are stubs, and no backup task is enqueued or executed. Backup paths are not in the published OpenAPI contract. | Keep `backup.enabled=false` and reject enablement at config validation until the worker, scheduler, encryption, private artifact store, authorization, audit, and recovery gates are complete. Do not advertise or register unavailable routes. |
| Background jobs | ADR 0005 specifies Asynq, constants/config exist, but `github.com/hibiken/asynq` is not in `go.mod` and `cmd/worker` is a stub. | Bootstrap and test the ADR's producer, worker, scheduler, retries, DLQ, and observability first. Backup state must remain durable in PostgreSQL if queue delivery is lost. |
| Authorization | Casbin/RBAC uses canonical `module:action` permissions; `backup` module plus `run`, `restore`, and generic actions exist, but no backup routes/actions are seeded. | Add and seed explicit `backup:run`, `backup:view`, `backup:download`, `backup:verify`, and `backup:delete` actions. Reserve `backup:restore` for the controlled CLI, not HTTP. Retain the explicit `SUPER_ADMIN` service/route gate. |
| Step-up | MFA is implemented for login/enrolment/recovery only. No middleware currently proves a recent MFA or password verification, despite a telemetry placeholder and older API prose. | Implement a short-lived, session-bound, single-use step-up proof and middleware before allowing download, delete, or verify routes. Until then, keep those routes absent. |
| Storage | `internal/platform/storage.Store` provides a local file store with opaque namespace/key references; S3 is a placeholder. It is designed for user files and does not yet provide backup-specific atomic finalization, `Head`, leases, or safe download authorization. | Extend it compatibly or add a narrowly scoped backup adapter behind the same provider-neutral semantics. Do not use its current local `URL` result as a signed URL or direct artifact exposure. |
| Cryptography | `pkg/crypto` contains field-level AES-GCM and password/TOTP utilities, not a reviewed streaming envelope-encryption container. | Build and review a versioned streaming envelope-encryption format before any dump can be persisted. The field-encryption helper is not a backup encryption implementation. |
| Configuration | Legacy `backup.enabled`, schedule, local path, retention days, and compression fields remain, but they do not configure an executable backup flow. `backup.enabled=true` is rejected. | Replace the legacy settings only as part of a complete runtime implementation; fail closed without durable off-host storage and recoverable encryption key management. |

The existing `docs/runbook/db_backup_restore.md` is a manual operator runbook, not an application backup implementation. Its shell examples must not be exposed through an HTTP handler or copied into worker code.

| Concern | Required integration |
|---|---|
| Authentication/session | Existing auth middleware and session validation |
| Authorization | Existing RBAC/Casbin permission mechanism using `module:action`; add and seed the explicit backup actions before route registration, plus a hard `SUPER_ADMIN` gate for system-wide physical backup routes |
| Step-up authentication | **Missing today.** Add one session-bound recent-MFA/password-verification mechanism for high-risk actions; do not expose high-risk routes until it exists or invent per-route boolean assertions |
| Tenant/branch context | Existing tenant middleware and `appctx` scope helpers for tenant export only |
| Audit | Existing audit middleware/service; add explicit domain audit events for worker and lifecycle actions that HTTP middleware cannot observe |
| Rate limiting | Existing named rate-limit policies for `backup` and `export`; configure or reuse rather than implementing a new limiter |
| Idempotency | Existing idempotency middleware/contract for manual trigger and other mutation requests |
| Queue/jobs | ADR 0005 specifies Asynq, but its runtime is not yet wired; implement that shared infrastructure before Backup and keep task payloads limited to operation IDs and non-sensitive execution metadata |
| Database | Existing PostgreSQL pool/transaction helpers and migration system |
| Redis | Existing Redis client only for optional distributed coordination; never the sole state store |
| Configuration | Existing config loader and validation |
| Errors/responses | Existing `pkg/response`, `errors`/`apperr`, and API error envelope |
| Logging | Existing structured logger; redact secrets and sanitize subprocess errors |
| Observability | Existing Prometheus/OpenTelemetry/metrics wiring, request IDs, trace IDs, and job metrics |
| Storage | Extend the existing `internal/platform/storage.Store` contract compatibly, or place a backup adapter beside it; do not create a second general-purpose file-storage framework |
| Cryptography | Add a reviewed reusable streaming crypto implementation under `pkg/crypto` only after defining its versioned format; the existing field-level AES helper is insufficient |
| API contract | Existing OpenAPI 3.1 contract-first workflow; update source YAML and generated strict handlers/types according to the project’s process |

### 4.1 Middleware order

Use the existing protected-route helper and route registration conventions. The expected effective order is:

1. Request ID / tracing / recovery / security headers as already configured globally.
2. Authentication and session validation.
3. Idle-session checks where the existing `Protected` helper includes them.
4. Tenant context where applicable.
5. RBAC permission enforcement.
6. Explicit `SUPER_ADMIN` guard for system-wide physical operations.
7. Step-up authentication for high-risk operations.
8. Rate limiting and idempotency according to existing middleware conventions.
9. Handler → service → repository/operational adapter.

Do not bypass existing middleware by registering backup routes directly on the root router. Confirm actual middleware ordering in the current code before wiring routes.

### 4.2 Worker authorization

A worker task is not an HTTP request and cannot depend on request-context middleware being present. It must:

- Accept only a validated operation ID and non-sensitive execution metadata.
- Load the persisted operation from PostgreSQL.
- Validate operation type, current state, attempt/lease ownership, and configured target.
- Use service-level authorization decisions already recorded for the requested operation.
- Never accept user-provided executable paths, shell fragments, database URLs, bucket names, local paths, encryption keys, or arbitrary restore targets from task payloads.
- Record system actor/task identity and operation ID in audit/logging.
- Be safe under retry and duplicate delivery.

---

## 5. Domain model

Keep the module file-oriented and small. Add separate files only where responsibilities genuinely differ.

### 5.1 `BackupRun`

Represents one backup attempt and its resulting artifact.

Required conceptual fields:

- `id`: UUID/operation ID.
- `type`: initially `full`; future values must be explicitly introduced.
- `status`: `queued`, `in_progress`, `succeeded`, `failed`, `cancelled`, `expired` (only states that are actually implemented should be accepted).
- `trigger`: `scheduled` or `manual`.
- `triggered_by`: nullable actor UUID; null for scheduler/system actor.
- `started_at`, `finished_at`.
- `size_bytes`.
- `sha256`: checksum of the **stored encrypted artifact bytes** or clearly specified canonical artifact stream. The contract must choose one and use it consistently; recommended: checksum encrypted artifact bytes for storage-integrity validation.
- `storage_provider`: provider identifier such as `local` or `s3`; never a credential.
- `storage_ref`: opaque, provider-neutral object reference; never a public URL or signed URL.
- `encryption_algorithm`, `encryption_key_id`, `wrapped_dek`, `encryption_format_version`.
- `pg_version`, `pg_dump_version`.
- `schema_version` / current migration version captured at backup time.
- `manifest_ref` or manifest metadata.
- `verify_status`, `verified_at`, optional latest verification reference.
- `expires_at`, `legal_hold`.
- Sanitized `error_code` and sanitized `error_message`; never raw subprocess stderr.
- `attempt_count`, `lease_owner`/lease generation and lease expiry where required for reconciliation.
- Standard timestamps using the project’s existing base model conventions where compatible.

Do not store a database URL, database password, raw KEK, plaintext DEK, local absolute path, signed download URL, or dump contents in this row.

### 5.2 `BackupVerification`

Verification evidence should be append-only rather than overwriting the historical result of each drill.

Conceptual fields:

- `id`, `backup_id`.
- `verification_type`: `integrity` or `restore_drill`.
- `status`: `queued`, `in_progress`, `succeeded`, `failed`.
- `started_at`, `finished_at`.
- `checksum_ok`, `decrypt_ok`, `restore_ok` as applicable.
- `schema_version_expected`, `schema_version_restored`.
- `sanity_check_summary` with bounded, non-sensitive fields.
- `measured_recovery_point` / `measured_rpo` where meaningful and supported.
- `measured_restore_duration` / `measured_rto`.
- Sanitized failure code/message.
- `worker_version` or verification tool version.

A checksum-only verification is not a restore drill. Expose the distinction in the API and UI.

### 5.3 `RestoreDrill`

Can be a dedicated table or represented by `BackupVerification` with a clear `restore_drill` type if that better matches the existing migration and repository patterns. Avoid duplicating state unnecessarily.

Required semantics:

- Target is an explicitly approved isolated scratch database/environment.
- Target identity is selected from server-side configuration/allow-list, not supplied as an arbitrary connection string by the user.
- No production target may be selected by a public API request.
- Record target class/identifier without storing credentials.
- Record cleanup outcome; a failed drill must not silently leave an exposed scratch database behind.

### 5.4 `TenantExportJob` — separate future capability

Do not combine tenant exports with physical `BackupRun` in a way that weakens authorization.

Conceptual fields: `id`, `organization_id`, requested branch scope, requested-by actor, status, artifact reference, expiry, checksum, format/version, sanitized error, created/finished timestamps. Repository methods must require organization scope.

### 5.5 Manifest

Each completed artifact has a versioned manifest stored alongside the encrypted artifact or in a protected manifest reference. Minimum fields:

- Manifest format version.
- Backup ID and type.
- Creation timestamp (UTC).
- Database engine/version and dump-tool version.
- Application build/version where available.
- Migration/schema version.
- Artifact format (`pg_dump custom`).
- Stored artifact size.
- Stored-artifact SHA-256.
- Encryption algorithm and encryption format version.
- Key identifier (not the key).
- Wrapped DEK or protected reference required to recover the artifact.
- Chunk format parameters needed for decryption.
- Verification state is recorded separately or referenced; do not mutate the original manifest to claim a later restore drill.
- Optional non-sensitive recovery metadata.

The manifest must not contain credentials, raw keys, customer data, or a public download URL. Sign the manifest or authenticate it through an established mechanism if tamper-evidence requirements justify it; a checksum stored beside an artifact does not protect against an attacker who can replace both.

---

## 6. Database design and migrations

Inspect current migrations and existing backup-related tables before writing DDL. **Never edit an already-applied migration.** Add a new forward migration to extend or repair existing schema.

Recommended conceptual tables:

| Table | Purpose |
|---|---|
| `system_backup_runs` | System-scoped backup operation and artifact metadata |
| `system_backup_verifications` | Append-only integrity and restore-drill results |
| `system_restore_drills` | Optional separate drill metadata if not represented as a verification subtype |
| `tenant_export_jobs` | Organization-scoped logical export jobs, only when tenant exports are implemented |

### 6.1 Constraints and indexes

- Primary keys use the project’s established UUID conventions.
- Check constraints validate enumerated states/types and nonnegative sizes/attempt counts.
- `finished_at` must not precede `started_at`.
- `succeeded` requires a completed artifact reference, size, checksum, and encryption metadata.
- A queued/in-progress run must not be represented as a completed artifact.
- Prevent overlapping active full-backup runs using a durable database invariant or lease design. Do not rely only on a Redis lock.
- Index system runs by `(started_at DESC, id DESC)` and status/time for watchdog/reaper queries.
- Index verification records by `(backup_id, started_at DESC)`.
- For tenant export jobs, index `(organization_id, created_at DESC, id DESC)` and status/expiry for cleanup.
- Foreign-key deletion behavior must preserve evidence. Do not cascade-delete verification/audit evidence merely because an artifact expires.
- Legal-hold state and deletion decisions must be persisted and auditable.
- Avoid a single giant JSON field for core queryable state; use JSONB only for bounded, non-sensitive extensible metadata.

### 6.2 Retention and database metadata

Artifact retention does not automatically imply deletion of the run record. Keep metadata/evidence long enough to explain what happened, subject to the project’s approved operational and compliance policy. If metadata itself is eventually purged, that must be a separate, explicit policy and must not erase audit evidence.

---

## 7. Storage abstraction: local now, S3 later

### 7.1 Required port

Define a small provider-neutral interface by compatibly extending the existing `internal/platform/storage.Store` (`Put`, `Get`, `Delete`, `URL`) or wrapping it with a backup-specific adapter. The current store does not yet meet all requirements below. Its `URL` method returns an application path for local storage; it is not a presigned URL or authorization decision.

```go
type ArtifactStore interface {
    Put(ctx context.Context, key string, src io.Reader, opts PutOptions) (StoredObject, error)
    Open(ctx context.Context, ref ObjectRef) (io.ReadCloser, error)
    Head(ctx context.Context, ref ObjectRef) (ObjectInfo, error)
    Delete(ctx context.Context, ref ObjectRef) error
    CreateDownloadAuthorization(ctx context.Context, ref ObjectRef, ttl time.Duration) (DownloadAuthorization, error)
}
```

This is conceptual contract code, not a mandate to copy it verbatim. Keep provider-specific types out of domain/service APIs. If a provider cannot safely issue a URL, the adapter can return an application-mediated download authorization instead.

`ObjectRef` should be opaque and serializable, for example provider + namespace + object key. It must not include credentials or signed query parameters. Do not use a user-controlled path as the object key.

### 7.2 Initial local adapter

A local adapter is for local development, tests, and explicitly approved single-host environments only.

Requirements:

- Use a configured root directory; never accept an arbitrary path from an API request.
- Resolve and validate paths so `..`, absolute-path injection, symlinks, and path traversal cannot escape the configured root.
- Use restrictive directory/file permissions and non-public serving.
- Write to a temporary object name, flush/close, then atomically finalize where the filesystem supports it.
- Never expose the storage directory through a static-file server.
- Never create plaintext dump files. Only encrypted artifacts and non-sensitive manifests may be written.
- Do not claim off-site durability or production readiness from a local adapter on ephemeral container storage.
- If local storage is used across multiple worker replicas, ensure they share the same durable mount; otherwise the API may not be able to resolve an artifact created by a different instance.
- `CreateDownloadAuthorization` should not create a publicly reachable URL for local files. Prefer an authenticated application-mediated streaming download, or return an unsupported-capability error until the route is implemented.

### 7.3 Future S3-compatible adapter

The S3 adapter must implement the same port and be configured through the existing config system. It should support:

- Private bucket/container and namespace/prefix.
- TLS and server-side encryption where available, in addition to application-level encryption.
- Upload via multipart streaming where supported; abort incomplete multipart uploads on failure.
- Provider checksum/size validation where available.
- Private-object reads.
- Presigned download URL with a short TTL (default 5 minutes), generated only after fresh authorization.
- Lifecycle/versioning/object-lock settings managed by infrastructure/IaC where supported.
- Separate IAM credentials with least privilege; no bucket-admin credential in the API.
- Provider timeouts, retries with bounded backoff, cancellation, and error normalization.
- Orphan multipart/object reconciliation.
- Stable object references independent of hostnames or presigned URLs.

Do not make an S3 SDK a mandatory dependency of the core service if the initial local adapter does not need it. The S3 adapter can be added as an infrastructure implementation without changing domain semantics.

### 7.4 Storage configuration

Configuration names should match the existing project configuration style. Conceptual shape:

```yaml
backup:
  enabled: true
  timezone: Asia/Dhaka
  schedule: "0 2 * * *"
  timeout: 30m
  active_run_limit: 1
  storage:
    provider: local              # local initially; s3-compatible later
    namespace: backups
    local:
      root: ./var/private/backups # development only; never ephemeral production
    s3:
      endpoint: ""                # optional for R2/B2-compatible providers
      region: ""
      bucket: ""
      prefix: backups
      force_path_style: false
      # Credentials come from secret/env injection, not committed YAML.
  encryption:
    enabled: true
    key_provider: env             # replace with a real KMS/Vault provider when available
    active_key_id: ""
    # Key material is injected through secret management, never committed.
  retention:
    daily: 7
    weekly: 4
    monthly: 12
    keep_min_verified: 3
  verify_schedule: "0 4 * * 0"
  alert_after: 26h
```

This is an illustrative schema, not an instruction to add duplicate configuration keys blindly. First inspect the current config structs, YAML, validation, environment override conventions, and deployment secrets. Keep backward compatibility where config already exists.

**Production gate:** if encryption/key management is not configured, backup creation must fail closed. Do not silently create unencrypted backups as a fallback.

---

## 8. Encryption and integrity

Backup artifacts contain sensitive operational, personal, and financial data. Application-level encryption is mandatory.

### 8.1 Envelope encryption

For each artifact:

1. Generate a cryptographically secure random DEK using the approved Go crypto APIs.
2. Use an authenticated encryption format, recommended AES-256-GCM, with a documented streaming/chunk format. The existing `pkg/crypto/aes.go` field-encryption helper must not be reused as this format.
3. Wrap the DEK using a separately managed KEK/KeyRing.
4. Persist only the wrapped DEK, key ID, algorithm, and format version in protected metadata/manifest.
5. Stream encrypted bytes to `ArtifactStore`.
6. Compute SHA-256 over the exact stored artifact byte stream and persist it with the completed metadata.
7. Zero sensitive byte buffers on a best-effort basis when practical; never log keys.

A static key hard-coded in source code or committed `.env` is forbidden. The active key must be injected via secret management. The ability to recover old artifacts requires retaining the KEK/key version that wrapped their DEKs. Key rotation must not retire a key while retained artifacts still depend on it unless those artifacts have been safely rewrapped/re-encrypted and verified.

### 8.2 Chunked authenticated encryption

If the stream can exceed memory limits, use a documented chunked AEAD container. Requirements:

- Each chunk is independently authenticated.
- A unique nonce is guaranteed for every encryption operation/chunk under the same key. A counter-based nonce scheme is acceptable only if its uniqueness, encoding, bounds, and crash/retry behavior are formally enforced; do not invent an ad hoc nonce format.
- AAD includes the immutable backup ID, format version, and chunk index.
- The header declares format/version, nonce strategy, and chunk sizing without revealing secrets.
- The format detects reordering, truncation, duplicate chunks, missing finalization, and tampering.
- Include an authenticated finalization/trailer or equivalent mechanism so a valid prefix cannot be mistaken for a complete artifact.
- Test nonce uniqueness, corrupted tags, reordered chunks, truncation, and incorrect backup IDs.
- Never decrypt into an untrusted path. Restore drill may stream plaintext into a controlled process or isolated scratch target without persisting a plaintext dump file.

Use a vetted, reviewed implementation and documented format. Do not claim that simply using AES-GCM makes a custom stream format safe.

### 8.3 Checksum semantics

Specify exactly what SHA-256 covers. Recommended initial contract:

- `sha256` is calculated over the exact encrypted artifact bytes stored by the storage adapter.
- The manifest records that same value and stored size.
- Verification first checks size/checksum, then decrypts/authenticates, then optionally runs `pg_restore` and application sanity checks.
- A checksum stored beside an artifact detects accidental corruption but is not tamper-proof if an attacker can rewrite both the artifact and metadata. Stronger tamper evidence requires separate trust domains, signatures/MACs, immutable storage, or external audit.

### 8.4 Secret and error hygiene

Never persist or log:

- `DATABASE_URL`, host credentials, passwords, access tokens, KEK/DEK plaintext.
- Raw `pg_dump` stderr/stdout.
- Signed URLs.
- SQL content or database rows.
- Environment dumps or full subprocess command environment.

Subprocess errors must be normalized to a stable error code and sanitized message. Do not rely on regex-only sanitization as the primary control; avoid passing secret-bearing arguments and do not expose raw stderr. Detailed diagnostic output, if required, must be tightly restricted and scrubbed before storage.

---

## 9. Backup execution pipeline

The HTTP API creates a durable request and queues work. The worker performs the heavy operation.

```text
Scheduler / POST /api/v1/backups
          |
          v
Authenticate + authorize + step-up where required
          |
          v
Service validates policy and creates durable BackupRun
          |
          v
Enqueue task with operation ID (no credentials/secrets)
          |
          v
Worker claims run using durable lease / active-run guard
          |
          v
Record in_progress; acquire optional distributed lock
          |
          v
Run pg_dump -Fc with fixed executable and validated args
          |
          v
stdout -> streaming encryptor -> SHA-256 -> ArtifactStore
          |
          v
Finalize artifact + manifest; verify size/checksum/metadata
          |
          v
Persist succeeded state in a short DB transaction
          |
          v
Emit audit event + BackupCompleted event + metrics/notification
```

### 9.1 API process responsibilities

- Validate the request and permission.
- Apply rate limiting, step-up checks, and idempotency.
- Create the durable run row in a short transaction.
- Enqueue the task using the existing job infrastructure.
- Return `202 Accepted` with an operation resource location.
- If enqueue fails after metadata creation, leave a recoverable state such as `queued`/`enqueue_pending` and let a reconciler enqueue it; do not falsely mark it successful.
- Never run `pg_dump` inline in a handler.

### 9.2 Worker responsibilities

- Claim an operation with a lease/generation token to prevent stale workers from finalizing a newer attempt.
- Enforce one active physical backup at a time by default.
- Check configured timeout, cancellation, disk/network/provider health, and storage availability.
- Launch the configured, fixed `pg_dump` executable with an argument array; never use a shell.
- Ensure the client major version is compatible with the database server. Validate at startup/deployment and record the tool version.
- Use a direct/non-pooled connection if required by the provider/tool; do not assume a transaction-pooler endpoint is suitable for dump operations.
- Avoid exposing database passwords in process arguments. Use the supported protected environment or credential mechanism; be aware environment variables may still be visible to privileged processes.
- Stream stdout; bound stderr capture and sanitize it.
- Close/kill the subprocess on cancellation/timeout; reap the process.
- Finalize artifact and manifest only after the dump, encryption, upload, checksum, and metadata steps all succeed.
- If any step fails, mark the run failed and clean up incomplete temporary objects where safe.
- Use bounded retries only for retryable failures. A retry must not create duplicate successful artifacts or bypass active-run protection.
- Do not starve POS: cap concurrency, CPU/I/O/network usage where possible, and schedule off-peak.

### 9.3 Idempotency and duplicate delivery

- Manual trigger requires the project’s existing idempotency contract.
- Task payload includes the durable run ID and an attempt/lease generation, not secrets.
- A duplicate task for an already successful run must become a no-op.
- A stale worker must not overwrite a newer attempt’s result.
- Unique task IDs help, but are not a substitute for durable database state and idempotent workers.
- A Redis lock can be added as coordination; correctness must still survive Redis loss.
- Use a durable active-run guard so two application instances cannot start overlapping full dumps.

### 9.4 Stale-run recovery

A periodic reconciler detects stale `queued`/`in_progress` operations:

- Re-enqueue valid queued work that was never delivered.
- Mark abandoned attempts failed or recoverable according to a clear policy.
- Check for partial/orphaned objects and clean them only after proving they are not referenced by an active attempt.
- Never infer success from an artifact’s existence alone.
- Preserve attempt history or a clear attempt counter and failure record.

---

## 10. PostgreSQL command execution

### 10.1 `pg_dump`

- Use `pg_dump -Fc` for the initial logical full backup, where supported by the provider.
- Execute a fixed binary path from trusted configuration using `exec.CommandContext` or equivalent; do not invoke `sh -c`, concatenate shell commands, or accept executable/flags from API input.
- Use an argument array with explicit connection/database parameters and validated options.
- Keep secrets out of command-line arguments and logs.
- Set a strict timeout (initial target: 30 minutes, configurable after load testing).
- Capture only bounded stderr and sanitize it.
- Verify the actual server/client versions at startup or before execution.
- Record `pg_dump --version`, server version, and application schema/migration version.
- A successful process exit is necessary but not sufficient; artifact finalization and integrity checks must also succeed.

### 10.2 `pg_restore` for verification

- Use only an isolated, allow-listed scratch target.
- Use a fixed binary and argument array; never accept arbitrary target URLs, file paths, database names, or flags from an API request.
- Keep credentials out of command-line arguments and diagnostics.
- Do not connect a restore drill to the production database.
- Apply timeouts, resource limits, and cleanup.
- Verify restored schema/migration compatibility and run non-sensitive application sanity checks.
- If a scratch environment cannot be created or safely isolated, report verification as unavailable/failed; do not downgrade it to a checksum-only pass.

### 10.3 Live restore

Live recovery is an operational procedure, not a normal HTTP mutation:

- `cmd/restore` CLI plus a reviewed runbook.
- Explicit operator authorization, fresh step-up authentication where supported, approved change/incident reference, maintenance mode, and a verified target.
- Pre-restore backup/rollback plan where feasible.
- Restore into a new/isolated database first whenever the provider supports it.
- Validate schema, core data, ledger invariants, and application health before cutover.
- Record the operation and outcome in audit evidence.
- Never allow a tenant-supplied database URL or arbitrary target to control restore execution.

---

## 11. API contract

All endpoints must be included in the project’s OpenAPI 3.1 source contract and generated/validated through the existing contract-first workflow. Paths below are relative to `/api/v1`. Keep established response envelopes, pagination, error codes, and naming conventions.

### 11.1 Physical backup APIs

| Method | Endpoint | Permission | Purpose |
|---|---|---|---|
| `GET` | `/backups` | `backup:view` + `SUPER_ADMIN` gate | List system backup runs; cursor pagination and bounded filters |
| `GET` | `/backups/{id}` | `backup:view` + `SUPER_ADMIN` gate | Run metadata, current state, latest verification summary |
| `POST` | `/backups` | `backup:run` + `SUPER_ADMIN` gate | Request a manual backup; return `202 Accepted` and `Location` |
| `GET` | `/backups/{id}/download` | `backup:download` + `SUPER_ADMIN` + fresh step-up | Authorize a short-lived download; never expose a public object |
| `POST` | `/backups/{id}/verify` | `backup:verify` + `SUPER_ADMIN` + fresh step-up | Queue integrity/isolated restore verification; return `202 Accepted` |
| `DELETE` | `/backups/{id}` | `backup:delete` + `SUPER_ADMIN` + fresh step-up | Request artifact deletion if retention/hold rules permit |

`backup:download` and `backup:verify` are intentionally new explicit RBAC actions and must be added to the constants/catalogue, seeded, covered by tests, and included in OpenAPI before handlers are generated. `backup:restore` remains reserved for the controlled live-recovery CLI.

**Do not add `POST /backups/{id}/restore` to the routine API.**

### 11.2 Future tenant-export APIs

Only add these routes if tenant logical export is an approved product requirement:

| Method | Endpoint | Permission | Purpose |
|---|---|---|---|
| `POST` | `/backups/tenant-exports` | `backup:export` | Create an export scoped to the authenticated organization/authorized branches |
| `GET` | `/backups/tenant-exports` | `backup:view` or dedicated export-view permission | List only the current organization’s export jobs |
| `GET` | `/backups/tenant-exports/{id}` | Same scoped read permission | Read own organization’s export status |
| `GET` | `/backups/tenant-exports/{id}/download` | `backup:download` or dedicated export-download permission | Short-lived authorized download |

Do not expose physical backup metadata through tenant-export routes.

### 11.3 Request/response rules

- `POST /backups` accepts only an approved backup type/reason if applicable. It does not accept storage path, executable, database connection, encryption key, provider credentials, or target database.
- Return `202 Accepted`, operation ID, status URL/`Location`, and a stable response envelope.
- List endpoints use bounded page size and cursor/keyset pagination; do not permit unbounded table scans.
- Detail endpoints expose sanitized metadata only.
- Download endpoint first re-authorizes the caller and then issues a short-lived authorization. Signed URLs are bearer secrets: never log them, store them in the database, include them in audit payloads, or return them to unauthorized users.
- A download authorization should expire after 5 minutes by default. If local storage cannot safely issue a URL, use authenticated streaming download.
- Delete endpoint is idempotent where appropriate and must return a clear conflict if the run is in progress, under legal hold, or protected as one of the minimum verified backups.
- Verification endpoint queues a job; it does not perform the restore synchronously.
- All mutations return the standard error envelope and correlation/request ID according to project conventions.

### 11.4 Filters

For `GET /backups`, support only operationally useful bounded filters, such as `status`, `type`, `trigger`, `from`, `to`, and cursor/page size. Validate `from <= to`; cap the date range where appropriate. Avoid arbitrary JSON filters.

---

## 12. Authorization and step-up security

### 12.1 Default policy

Physical backup data is effectively a copy of the entire database. The default policy is **platform operator only**, with `SUPER_ADMIN` required even when a named permission is present.

- `backup:view`: view physical backup metadata.
- `backup:run`: request a manual physical backup.
- `backup:download`: authorize download of an encrypted physical artifact.
- `backup:delete`: delete an eligible artifact.
- `backup:verify` or the project-approved canonical verification permission: request verification/restore drill.
- `backup:export`: tenant logical export only, if implemented.
- `backup:restore`: reserved for tightly controlled operational restore capability; not a normal tenant API permission.

Permission strings must match the existing RBAC/seed conventions exactly. Confirm whether this project uses colon-separated (`backup:view`) or dot-separated (`backup.view`) permission keys before implementation. Do not mix formats.

### 12.2 Step-up requirements

Require a recent MFA/password verification proof for:

- Physical backup download authorization.
- Artifact deletion.
- Restore drill if the action has material infrastructure cost or can expose restored data.
- Any live restore CLI workflow, using its operational identity/control process.

Manual backup creation should require platform authorization and the configured backup rate limit; add step-up if the existing privileged-action policy requires it.

A boolean request field such as `mfa_verified: true` is never sufficient. The trusted auth/session middleware must establish recent verification. **The repository does not implement this proof or middleware today:** high-risk routes must remain unregistered until it is implemented, session-bound, short-lived, single-use where appropriate, and covered by expiry/replay tests.

### 12.3 Tenant and branch isolation

- Physical backup endpoints are platform-scoped and must not become accessible to an organization admin through a broad tenant permission.
- Tenant export repository methods require `orgID`.
- Branch-limited export methods require the effective branch scope from the authenticated context.
- Never trust `organization_id`, `branch_id`, or scope arrays sent by the client as authorization proof.
- Do not expose physical object references or IDs through tenant APIs.
- Test IDOR using valid IDs from another organization and physical-run IDs from a tenant session.

### 12.4 Rate limits

Reuse the existing named limiter configuration. The proposed policy is:

- `backup`: maximum 10 manual trigger attempts per day per authorized operator or the project’s established scope.
- `export`: maximum 5 requests per hour for export creation/download initiation, aligned with existing configuration.

Confirm current rate-limit definitions and whether limits are keyed by user, organization, IP, or route. Avoid counting a scheduled system run as a user’s manual request. Apply download rate limits as well as trigger rate limits. Limits must not be the only protection against expensive operations; the durable active-run guard is required.

### 12.5 Least privilege

- Database credentials used for dumps should have only the privileges required by the selected backup method.
- Storage credentials should only access the configured bucket/prefix and required operations.
- Worker identity should not have bucket-admin or production restore privileges unless operationally necessary.
- Local storage directory must not be web-served.
- Encryption key access should be isolated from ordinary API handlers as far as practical.
- Only the backup worker/approved operator path should be able to decrypt artifacts.
- Use separate production and development keys and storage namespaces.

---

## 13. Audit requirements

Reuse the existing audit module and generic audit middleware. Do not create a parallel audit log.

Record at least:

- Manual backup request accepted/denied.
- Scheduled run started.
- Backup succeeded/failed/cancelled.
- Download authorization issued/denied and, if observable, artifact download completed.
- Verification/restore drill requested, started, succeeded/failed.
- Retention deletion planned, blocked, and completed.
- Legal hold added/released if supported.
- Key configuration/rotation events where the application can observe them.
- Restore CLI invocation, target class, operator, change/incident reference, result.
- Orphan cleanup and reconciliation actions where they materially alter artifacts.

Audit events should contain actor/system identity, operation ID, action, outcome, timestamp, request/correlation ID, and sanitized reason. They must not contain artifact bytes, database rows, secrets, wrapped-key material unless explicitly required and protected, or signed URLs.

A worker failure must still create a durable failure state and operational event even though no HTTP request is active. Audit delivery failure must follow the existing audit module’s required/optional event policy; do not silently invent success events after a failed operation.

---

## 14. Retention, deletion, and legal holds

### 14.1 Proposed GFS policy

- Keep 7 daily recovery points.
- Keep 4 weekly recovery points.
- Keep 12 monthly recovery points.
- Never delete the latest 3 **verified and recoverable** backups.
- Never delete an artifact under legal hold.
- Never delete the only remaining backup in a required recovery window.
- Retention must preserve the complete recovery chain for any physical/PITR strategy; a base backup cannot be deleted if retained recovery points depend on it.

These are configurable defaults. The retention algorithm must define how a backup qualifies as daily/weekly/monthly, using UTC storage timestamps and an explicit timezone policy for schedule buckets.

### 14.2 Safe deletion algorithm

1. Acquire a retention lease so two cleanup workers do not race.
2. Load the artifact and current retention/legal-hold policy.
3. Exclude active/in-progress operations.
4. Exclude legal-hold artifacts.
5. Compute retained daily/weekly/monthly points and required recovery-chain dependencies.
6. Preserve at least `keep_min_verified` successful, verified, recoverable backups.
7. Re-check policy and reference state immediately before deletion.
8. Delete the object through `ArtifactStore`.
9. Persist deletion outcome and audit it.
10. Keep the metadata/evidence record unless an approved metadata-retention policy says otherwise.

If object deletion succeeds but metadata update fails, reconciliation must detect the missing object and converge safely. If metadata is marked deleted before object deletion succeeds, a retryable cleanup state must remain; do not falsely claim that the bytes are gone.

### 14.3 Object lock/versioning

For production S3-compatible storage, enable versioning and object lock/retention where the provider supports them and the operational policy requires them. Manage these controls at infrastructure level. The application must not assume all S3-compatible providers implement identical object-lock semantics.

---

## 15. Verification and restore drills

A backup is not proven recoverable until a restore test succeeds.

### 15.1 Verification levels

1. **Artifact metadata check:** object exists and metadata/size are plausible.
2. **Integrity check:** encrypted bytes match SHA-256.
3. **Cryptographic check:** artifact decrypts/authenticates completely using the expected key version.
4. **Database restore drill:** decrypted stream is restored into a clean, isolated scratch database.
5. **Application sanity checks:** migration/schema version, required core tables and representative row counts, foreign-key/constraint validity where practical, and domain-specific invariants.

The API and dashboard must not label level 1 or 2 as a successful restore drill.

### 15.2 Sanity checks

At minimum, a restore drill should check:

- Expected migration/schema version can be read.
- Required core tables exist.
- Core reference/transaction tables are not unexpectedly empty for a non-empty production dataset (use environment-appropriate thresholds rather than hard-coded assumptions for empty test DBs).
- Database constraints/indexes are available after restore.
- Ledger invariant: posted journal entries balance (total debit equals total credit) using the ledger’s actual schema and currency/rounding rules.
- Application can connect to the restored database using a restricted verification identity.
- No outbound jobs, notification senders, or external integrations run from the scratch environment.
- Scratch database cleanup succeeds.

A restore drill must not write to production or trigger business side effects.

### 15.3 Scheduling

- Run integrity checks frequently enough to catch corruption early.
- Run full isolated restore drills on a defined schedule (weekly is the proposed default, subject to infrastructure cost).
- Record measured restore duration and the age of the recovery point.
- Alert on verification age exceeding policy.
- If the provider limits scratch branches/databases, use a controlled local/CI PostgreSQL environment as a supplemental test, but do not misrepresent a local test as verification of a provider-managed PITR chain.

---

## 16. Scheduler, watchdog, and background jobs

All jobs must be registered through the shared scheduler and worker architecture. ADR 0005 names Asynq as that architecture, but the runtime is not currently implemented; complete and verify it before registering any Backup job. Do not create a second scheduler or use request-owned goroutines as a substitute.

### 16.1 Required jobs

| Job | Responsibility |
|---|---|
| `backup_task` | Run one approved backup operation |
| `backup_verify_task` | Integrity/decryption checks and optional isolated restore drill |
| `backup_retention_task` | Apply retention safely |
| `backup_watchdog_task` | Detect missing/stale successful backups and recovery-health regressions |
| `backup_reconcile_task` | Recover queue-loss/stale metadata and orphan objects; may be combined with watchdog if responsibilities remain clear |
| Provider recovery monitor | Optional check of provider PITR/WAL status if supported |

### 16.2 Queue policy

- Use the existing low-priority/background queue (e.g. `low`) for expensive backup work if that queue exists.
- Concurrency defaults to one physical full backup per environment.
- Set a bounded timeout (initially 30 minutes) and tune using measured database size and provider limits.
- Use retry backoff and maximum attempts; classify permanent configuration/key/permission errors separately from transient network failures.
- Do not let a failing backup retry forever.
- Alert on exhausted retries and preserve sanitized failure evidence.

### 16.3 Schedule and watchdog

- Schedule nightly at 02:00 `Asia/Dhaka` as the initial default, after validating the cron library’s timezone support.
- Persist all event timestamps in UTC.
- The watchdog must run independently from the backup task so it can detect when the task/scheduler did not run.
- Alert if no successful backup exists within 26 hours (configurable) or if the latest verified recovery point exceeds policy.
- Also monitor PITR/WAL archive lag where a provider integration exists.
- The alert must not depend solely on the same queue whose failure it is intended to detect; use the existing independent alerting/monitoring path where available.

---

## 17. Observability and alerts

Use the existing metrics and structured logging stack. Do not introduce high-cardinality labels such as user IDs, operation IDs, organization IDs, object keys, or raw error messages into Prometheus labels.

### 17.1 Metrics

Required or recommended metrics:

- `backup_runs_total{status,trigger,type}` counts state transitions; it is not a current-state gauge.
- `backup_run_duration_seconds`
- `backup_size_bytes`
- `backup_last_success_timestamp_seconds`
- `backup_last_verified_timestamp_seconds`
- `backup_verification_total{status,type}`
- `backup_restore_duration_seconds`
- `backup_storage_operation_total{operation,status,provider}`
- `backup_storage_operation_duration_seconds{operation,provider}`
- `backup_active_runs`
- `backup_stale_runs`
- `backup_retention_deleted_total{result}`
- `backup_watchdog_alerts_total{reason}`
- `backup_pitr_last_recoverable_timestamp_seconds` when provider integration exists
- `backup_wal_archive_lag_seconds` when the provider exposes this signal

Avoid labels with unbounded values. `provider`, `operation`, `status`, and `trigger` must come from bounded enumerations.
Run/verification histograms measure the complete worker operation, not the HTTP request, SQL update, or individual pipeline-step latency. Derive current active/stale run counts from durable PostgreSQL state; never infer them from transition counters.

### 17.2 Structured logs

Include request/trace/operation IDs, task name, attempt, provider, operation, outcome, duration, and sanitized error code. Include tenant/branch identifiers only where relevant and permitted. Never log secrets, signed URLs, full connection strings, raw command environments, dump contents, or unrestricted PII.

### 17.3 Alert rules

- No successful backup within `backup.alert_after` (initially 26 hours).
- Latest verified backup older than the allowed verification window.
- Backup or verification failure after retry exhaustion.
- Artifact checksum/decryption/restore failure.
- Storage unavailable, capacity/quota errors, or repeated upload failures.
- Encryption key unavailable or a retained artifact’s key version approaching retirement.
- Stale `in_progress` operations.
- PITR/WAL archive lag or missing WAL segment where provider telemetry is available.
- Retention blocked for an extended period or deletion reconciliation stuck.
- Measured restore duration exceeding the RTO objective.

Alerts must include operation ID and sanitized context, not credentials or signed artifact URLs.

---

## 18. Performance and availability

Backup jobs must not compromise critical pharmacy operations.

- Keep API requests short and return `202`.
- Use a single full-backup concurrency slot initially.
- Stream data end-to-end; do not buffer the entire dump in RAM.
- Bound chunk size, channel buffers, stderr buffers, upload concurrency, and retries.
- Apply timeouts to database subprocesses, storage operations, and verification.
- Schedule during low traffic, but do not assume night-time traffic is zero.
- Monitor database load, I/O, CPU, network egress, worker memory, and storage latency.
- Use provider-supported snapshots/PITR when a logical dump is too expensive or cannot meet the recovery objective.
- Do not hold a database transaction open for the duration of a backup.
- Do not use Redis as the backup metadata database.
- Do not perform retention object deletion while a verification/download operation holds a valid lease/reference.
- Set upload and download limits appropriate to available worker resources.
- Test with realistic database size and transaction activity.

---

## 19. Configuration and secrets

Before adding keys, inspect the existing `config.yml`, config structs, env overrides, secret injection, and validation. Preserve existing names where possible; avoid parallel configuration trees.

Recommended settings include:

- `backup.enabled`
- `backup.timezone`
- `backup.schedule`
- `backup.timeout`
- `backup.active_run_limit`
- `backup.storage.provider`
- `backup.storage.namespace`
- `backup.storage.local.root`
- `backup.storage.s3.endpoint`, `region`, `bucket`, `prefix`, path-style option
- `backup.encryption.enabled`
- `backup.encryption.key_provider`
- `backup.encryption.active_key_id`
- `backup.retention.daily`, `weekly`, `monthly`, `keep_min_verified`
- `backup.verify_schedule`
- `backup.alert_after`
- retry policy and scratch restore target identifier/allow-list

Credentials and key material must be provided by environment/secret management, not committed to source control or sample config. The example config should contain placeholders only. Config validation should fail at startup or disable the feature explicitly if required settings are missing; do not silently fall back to plaintext or an unsafe directory.

---

## 20. Failure semantics

| Failure | Required behavior |
|---|---|
| Scheduler did not run | Independent watchdog alerts; run remains absent, not falsely successful |
| Queue enqueue failed | Persist recoverable pending state; reconciler retries; API response reflects accepted/pending semantics honestly |
| Duplicate task delivery | No-op if already completed; lease/attempt token protects finalization |
| `pg_dump` exits nonzero | Fail run; sanitize error; delete incomplete artifact where safe; alert after retry policy |
| Process timeout/cancellation | Kill and reap child; close streams; mark failed/cancelled; preserve evidence |
| Storage upload fails | Do not mark success; abort multipart/temp object; bounded retry |
| Checksum mismatch | Mark verification failed; artifact not downloadable as trusted; alert |
| Encryption key unavailable | Fail closed; no plaintext fallback; alert and preserve artifact |
| Decryption/tag failure | Verification fails; do not attempt to restore unauthenticated data |
| Restore drill fails | Keep backup record, mark drill failed, alert; never modify production |
| Scratch cleanup fails | Record cleanup-required state and alert; do not claim drill fully completed |
| Redis unavailable | Continue using durable DB state if safe; if coordination is required for safety, fail closed rather than start a duplicate run |
| Database metadata update fails after upload | Artifact remains an orphan candidate; reconciler resolves it; do not report success to caller until durable state is committed |
| Object deletion succeeds but DB update fails | Reconcile missing object; preserve operation/audit evidence |
| Key is scheduled for retirement but still referenced | Block retirement or require verified rewrap/re-encryption first |
| Retention sees legal hold/minimum verified set | Skip deletion and record reason |
| PITR/WAL chain incomplete | Mark provider recovery window degraded; do not report PITR healthy |
| Alert delivery fails | Preserve metrics/logging evidence and retry through the existing notification/alerting path |

---

## 21. File and package layout

Follow the existing module conventions and avoid unnecessary abstractions.

```text
internal/
  modules/
    backup/
      model.go
      dto.go
      repository.go
      service.go
      handler.go
      routes.go
      runner.go       # safe pg_dump/pg_restore process boundary
      pipeline.go     # stream -> encrypt -> checksum -> ArtifactStore
      verify.go       # integrity and isolated restore-drill orchestration
      retention.go    # retention policy evaluation and safe cleanup
      backup_test.go
  jobs/
    handlers/
      backup_task.go
      backup_verify_task.go
      backup_retention_task.go
      backup_watchdog_task.go
      backup_reconcile_task.go
  platform/
    storage/
      storage.go      # provider-neutral ArtifactStore contract
      local.go        # local development/test adapter
      s3.go           # add when S3-compatible storage is enabled
  cmd/
    restore/
      main.go          # controlled live-restore CLI; not a public API
pkg/
  crypto/
    stream.go          # authenticated chunked encryption format if shared here
docs/
  runbook/
    db_backup_restore.md
migrations/
  <new additive migration for backup metadata>
api/
  api-docs/
    <OpenAPI source and generated artifacts following existing workflow>
```

Only add files/subpackages when they correspond to real behavior. If storage or crypto already exists in the foundation, extend that implementation rather than duplicating it.

---

## 22. Implementation order

Implement in small, testable phases. Do not generate all module files before verifying the current foundation and migration state.

### Phase 01 — Architecture and integration freeze

- Inspect the existing Astra architecture document, config, scheduler, worker/Asynq wiring, auth/RBAC permissions, rate limiter, idempotency, audit, DB pool, migration conventions, logger, metrics, and storage placeholders.
- Document existing interfaces and exact middleware order.
- Decide canonical permission naming and verification permission.
- Confirm database provider capabilities: `pg_dump`, client/server versions, direct connection, PITR/WAL, scratch database/branch availability.
- Define artifact-store and key-management interfaces.
- Define production readiness gates and recovery ownership.

### Phase 02 — Storage port and local adapter

- Implement `ArtifactStore` interface.
- Implement safe local adapter for development/test only.
- Add traversal, symlink, permissions, atomic finalize, missing object, and concurrent access tests.
- Confirm local adapter cannot expose plaintext or publicly serve artifacts.
- Keep provider-neutral object references.

### Phase 03 — Crypto stream

- Implement envelope encryption with random DEK and wrapped DEK metadata.
- Define and document the authenticated chunk format.
- Add tamper, reorder, truncation, key-missing, wrong-key, nonce uniqueness, and large-stream tests.
- Verify bounded memory use and streaming behavior.
- Do not continue until the format is reviewed and tests pass.

### Phase 04 — Additive schema and models

- Inspect existing migration history and backup tables.
- Add a new migration; do not rewrite applied migrations.
- Add run, verification, and required metadata models/constraints/indexes.
- Add migration/schema version capture.
- Test clean install and upgrade from a database with existing data.

### Phase 05 — Runner and pipeline

- Implement safe fixed-command `pg_dump` execution.
- Stream stdout to encryption/checksum/storage.
- Add timeout/cancellation and bounded sanitized stderr.
- Finalize manifest and run metadata atomically as far as separate DB/object-store boundaries permit.
- Add orphan reconciliation for the unavoidable DB/object-store dual-write failure window.

### Phase 06 — Service, repository, and jobs

- Durable run creation, state transitions, lease/active-run guard.
- Shared Asynq runtime prerequisite, then an idempotent Backup task and handler.
- Manual trigger request and scheduler registration.
- Retry/backoff policy and stale-run reaper.
- Verify that queue loss does not lose operation metadata.

### Phase 07 — API, RBAC, audit

- List/detail/manual trigger/download authorization/delete/verify routes.
- OpenAPI 3.1 source contract and generated handler/types per existing workflow.
- Existing protected middleware, explicit `SUPER_ADMIN` guard, named rate limiter, idempotency, and the newly implemented step-up guard for high-risk routes.
- Audit all high-risk actions.
- Cross-tenant/IDOR tests and response redaction tests.

### Phase 08 — Retention and watchdog

- GFS selection logic and minimum verified-backup guarantee.
- Legal hold handling.
- Safe object deletion and reconciliation.
- Independent watchdog and alert integration.
- Metrics and dashboards.

### Phase 09 — Restore verification

- Integrity verification.
- Decryption/authentication verification.
- Isolated scratch restore.
- Schema/core-table/ledger sanity checks.
- Cleanup and failed-cleanup alert.
- Weekly drill scheduling and measured RPO/RTO evidence.

### Phase 10 — Production storage adapter

- Add S3-compatible adapter using the same `ArtifactStore`.
- Configure private bucket, IAM least privilege, TLS, versioning/object lock where available, lifecycle policy, and short-lived signed downloads.
- Add multipart abort/reconciliation and provider failure tests.
- Switch `backup.storage.provider` and secrets/config only after integration tests and migration of any required existing artifacts.
- Do not assume switching the provider automatically moves old artifacts; plan a verified copy/migration or keep the old adapter available for existing references.

### Phase 11 — Live recovery CLI and runbook

- Implement controlled `cmd/restore` only after isolated restore works.
- Write and rehearse `docs/runbook/db_backup_restore.md`.
- Define incident/change approval, operator identity, target verification, pre-restore safeguards, cutover, post-restore reconciliation, and rollback.
- Measure actual restore time and update RPO/RTO objectives.

---

## 23. Testing requirements

### Unit tests

- State transitions and invalid transitions.
- Retention selection with daily/weekly/monthly buckets.
- Minimum verified backup preservation.
- Legal-hold protection.
- Retry classification and stale lease rejection.
- Manifest serialization/versioning.
- Error sanitization.
- Configuration validation and provider selection.

### Crypto and storage tests

- Empty, small, and multi-gigabyte synthetic streams with bounded memory.
- Correct decrypt round-trip.
- Wrong key/key ID.
- Corrupted ciphertext/tag.
- Reordered, duplicated, missing, and truncated chunks.
- Nonce uniqueness across chunks and retries.
- Checksum mismatch.
- Local path traversal and symlink attack.
- Object upload interruption and cleanup.
- Storage provider timeout, throttling, and missing object.
- Signed URL expiry and no URL logging.

### Integration tests

- Real PostgreSQL `pg_dump` and `pg_restore` using the supported test version.
- Fresh schema and populated database restore.
- Queue duplicate delivery and worker crash.
- Redis outage while durable run metadata remains correct.
- Object upload success + metadata DB failure.
- Object delete success + metadata DB failure.
- Stale `in_progress` reaper.
- Scratch DB cleanup failure.
- Backup job under concurrent application traffic.
- PITR/WAL status integration where supported.

### Security tests

- Non-SUPER_ADMIN cannot view/download/create/delete/verify physical backups.
- Tenant administrator cannot access physical backup records by guessing IDs.
- Missing/expired step-up state denies high-risk actions.
- Invalid or foreign organization/branch scope is denied for tenant exports.
- User cannot choose a local path, executable, flags, connection string, bucket, key, or restore target.
- Secrets do not appear in API errors, logs, manifests, metadata, or audit payloads.
- Signed URL is short-lived and never persisted.
- Retention cannot delete legal-hold or minimum verified backups.
- Worker task payload cannot cause arbitrary command execution.
- Download authorization is rechecked on every request.

### Recovery tests

- Restore latest backup into an isolated database.
- Validate migration/schema version and core tables.
- Validate posted ledger balance using actual ledger rules.
- Measure end-to-end recovery time.
- Confirm no outbound notifications/jobs execute from the scratch environment.
- Confirm application can start against the restored copy.
- Record the result as an immutable verification/drill record.

---

## 24. Definition of done

The module is not production-ready until all applicable items pass:

- [ ] Existing foundation interfaces reused; no duplicate auth/RBAC/audit/config/logger/metrics/job systems.
- [ ] Physical backup operations are system-scoped and `SUPER_ADMIN`-gated.
- [ ] Tenant exports, if implemented, are separately scoped and cannot expose physical artifacts.
- [ ] Manual and scheduled backup requests are durable and idempotent.
- [ ] Worker concurrency and stale-run recovery are safe across multiple instances.
- [ ] `pg_dump` runs without shell interpolation and without secrets in command-line arguments.
- [ ] Plaintext dump is never written to persistent disk.
- [ ] Encryption keys are external to the database/artifact storage and can recover retained backups.
- [ ] Integrity checksum and manifest semantics are documented and tested.
- [ ] Local storage is clearly restricted to development/testing or an explicitly durable approved mount.
- [ ] Storage provider can be changed through config/adapter wiring without domain/API changes.
- [ ] S3-compatible storage is private and least-privileged before production use.
- [ ] Download authorization is short-lived and audited.
- [ ] Delete respects legal holds, minimum verified backups, and recovery dependencies.
- [ ] Restore drills are isolated and cannot target production.
- [ ] A watchdog detects missing/stale backups independently.
- [ ] Metrics/alerts expose backup age, verification age, failures, restore duration, and provider recovery health where available.
- [ ] OpenAPI contract, permissions, migrations, tests, and runbook are updated.
- [ ] At least one clean isolated restore drill succeeds and measured RTO/RPO are recorded.

---

## 25. Decisions to freeze before implementation

1. **Permission key convention:** use the project's canonical colon-separated convention. Seed `backup:run`, `backup:view`, `backup:download`, `backup:verify`, and `backup:delete`; reserve `backup:restore` for the live-recovery CLI.
2. **Step-up prerequisite:** implement and test one session-bound recent-verification mechanism before registering download, delete, or verification routes. MFA-at-login does not satisfy this requirement.
3. **Initial storage provider:** `local` only for development/tests; production must use durable off-host storage or explicitly verified provider-managed backups.
4. **Encryption key provider:** determine the real secret/KMS/Vault mechanism. Do not claim production-grade key management with a hard-coded or committed static key.
5. **Backup method:** `pg_dump -Fc` for the first logical backup; provider PITR/WAL status is separate.
6. **RPO/RTO:** nightly logical backup implies up to approximately 24-hour RPO absent a verified continuous recovery mechanism. The ≤1-hour RTO is an objective until measured.
7. **Schedule:** nightly 02:00 `Asia/Dhaka`; confirm scheduler timezone support.
8. **Concurrency:** one active full backup per environment initially.
9. **Retention:** 7 daily / 4 weekly / 12 monthly, keep at least 3 verified backups, never delete legal-hold artifacts.
10. **Restore boundary:** no public live-restore endpoint; isolated restore drills in the worker, live recovery through CLI/runbook.
11. **Tenant exports:** defer unless product requirements explicitly call for them; keep the model/API boundary separate.
12. **PITR support:** document provider capability and owner. Do not implement or advertise application-managed WAL archiving until provider access and operational support are confirmed.
13. **Job-runtime prerequisite:** deliver the shared Asynq producer/worker/scheduler/DLQ runtime described by ADR 0005 before Backup. The present config/constants/stub worker are not sufficient.

---

## 26. Final implementation guidance for Astra/Kilo Code

Treat this file as a contract, not permission to redesign the existing foundation.

Before coding, inspect the actual repository and report:
- Existing backup-related code, tables, config, and placeholders.
- Existing middleware/permission/idempotency/audit/rate-limit interfaces.
- Existing worker queue, scheduler timezone behavior, and retry conventions.
- Existing PostgreSQL connection configuration and provider capabilities.
- Existing storage and crypto packages.
- Any contract conflicts or missing prerequisites.

Then implement one phase at a time. Do not silently invent APIs or infrastructure that the repository does not contain. Where the contract leaves an implementation choice open, explain the options and choose the smallest design that preserves security, recoverability, and compatibility with the current project.

**Production principle:** a backup is successful only when its encrypted artifact and metadata are durable and integrity-checked; recovery is proven only when a controlled restore test succeeds.
