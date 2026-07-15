# ADR 0006 — Audit Log Storage: Native Postgres Partitioning by Month

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Data Engineering
- **Related:** ADR-0002, ADR-0005

---

## Context

Every write in MediCore ERP produces an audit row: who did what, when, from
where, with before/after payloads. Over time this table dominates the working
set:

- Estimated volume: **50 k – 500 k rows/day** at target scale.
- Retention: **12 months hot** in Postgres for online search; older data moved
  to cold storage.
- Queries: filter by user, module, action, entity, and date range.
- Never updated. Rarely deleted (except through the archival job).
- Must be searchable by JSONB payload (`details`, `before_data`, `after_data`)
  for at least the hot window.

A single unpartitioned table quickly becomes a maintenance and query
bottleneck: vacuum takes hours, index size explodes, and range scans over a
year of rows are slow.

Options considered:

1. **Single unpartitioned table** with a large BRIN index on `created_at`.
2. **Native Postgres range partitioning by month** on `created_at`.
3. **Timescale hypertable** — declarative time-series partitioning.
4. **Ship to a separate store (ClickHouse / Loki)**.

## Decision

We use **native PostgreSQL declarative range partitioning by month** on
`audit_logs`, with:

- Automatic monthly partition creation via `cmd/worker` cron job
  (`audit_partition_ensure`) that pre-creates the next N=3 partitions.
- Per-partition indexes on:
  - `(user_id, created_at DESC)`
  - `(organization_id, branch_id, created_at DESC)`
  - `(module, action)`
  - GIN on `details` (JSONB, `jsonb_path_ops`)
- Old partitions (`< now() - 3 months`) get an additional `SET statistics
  extra` and are ANALYZE-only.
- Partitions older than the retention window (default **12 months**) are
  detached and either dropped or exported to Parquet in object storage,
  configurable per organization.

A companion **`audit_logs_archive`** table exists for detached partitions if
the operator prefers to keep them queryable in Postgres.

## Rationale

- **Native partitioning** is stable in Postgres 12+ and matures further in 14
  and 16. It requires no extension and integrates with our replication.
- Monthly granularity gives:
  - Fast retention (drop a partition = instant).
  - Cheap indexes (each partition is small enough to fit).
  - Time-range query pruning without hitting old data.
- BRIN alone is not enough for JSONB search; per-partition GIN is required
  and only feasible when partitions are small.
- We already run Postgres for everything; no new engine.

Timescale was considered and rejected because it is an extension we do not
otherwise need, and it changes the deployment story (needing Timescale
images or extensions on managed Postgres).

Shipping to ClickHouse/Loki was deferred to a later ADR — it becomes
attractive only when we cross ~50 M rows per month.

## Schema Sketch  
```
audit_logs (id, ..., created_at TIMESTAMPTZ)
PARTITION BY RANGE (created_at);
audit_logs_2026_07 PARTITION OF audit_logs
FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
-- Local indexes per partition:
CREATE INDEX ON audit_logs_2026_07 (user_id, created_at DESC);
CREATE INDEX ON audit_logs_2026_07 (organization_id, branch_id, created_at DESC);
CREATE INDEX ON audit_logs_2026_07 (module, action);
CREATE INDEX ON audit_logs_2026_07 USING GIN (details jsonb_path_ops);
```

Detailed DDL lives in `migrations/000011_audit_partitioned.up.sql`.

## Write Path

- Application inserts into the parent `audit_logs`. Postgres routes to the
  correct partition automatically.
- Writes are **asynchronous**: the request handler enqueues an Asynq task
  (`audit_log_persist`) and returns. The worker inserts using pgx's
  `SendBatch` for micro-batching. This keeps p95 request latency untouched
  by audit-log volume.

## Read Path

- All read endpoints are cursor-paginated by `(created_at DESC, id DESC)`.
- The default view is scoped by `organization_id`, `branch_id` and a date
  range so partition pruning kicks in.
- Cross-partition search on `details` JSONB is discouraged; the API rejects
  such queries without a bounded date range.

## Retention

- Configurable per organization; default 12 months hot.
- The nightly `audit_archive` job (see ADR-0005 queue: `low`) detaches
  partitions older than the window and either:
  - **Drops** them if the operator has confirmed archival elsewhere, or
  - **Copies** them into `audit_logs_archive` (append-only), or
  - **Exports** them to Parquet on object storage.

## Consequences

### Positive

- Constant-time deletes for retention (`DETACH PARTITION` / `DROP TABLE`).
- Query planner prunes partitions on date-range filters.
- Indexes stay small and fast; VACUUM/ANALYZE work in bounded time.
- No new datastore.

### Negative

- We must maintain the partition-creation job. If it fails, inserts fail. We
  mitigate by pre-creating 3 months of partitions.
- Global unique constraints across partitions require a composite key that
  includes `created_at`. Our audit-log primary key is `(id, created_at)` for
  this reason.
- Some ORMs and admin tools do not understand partitioned tables well; we
  use plain SQL via pgx (ADR-0002) so this is not a problem for us.

## Guardrails

- `audit_partition_ensure` runs daily at 03:00 and must succeed. A failure
  raises a P1 alert.
- The `audit_logs` table cannot be UPDATEd or DELETEd through application code
  (repository interface has no such methods). A Postgres trigger enforces the
  same rule.

## References

- PostgreSQL Documentation, chapter 5.11 “Table Partitioning”.
- Robert Haas, *Practical Partitioning* (PGCon 2021).