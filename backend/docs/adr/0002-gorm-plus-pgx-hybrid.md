# ADR 0002 — Use pgx as the Primary Driver, with a Thin Repository Layer

- **Status:** Accepted (revised from an earlier GORM-only plan)
- **Date:** 2026-07-15
- **Deciders:** Backend Lead
- **Consulted:** Data Engineering
- **Supersedes:** Internal draft “GORM everywhere”
- **Related:** ADR-0006 (audit_logs partitioning), ADR-0007 (ledger)

---

## Context

We must pick a data-access strategy for a Go/PostgreSQL system that includes:

- A hot POS path (checkout, FEFO batch selection, journal post) with strict
  latency targets and correctness requirements.
- Bulk ingestion (purchase receive, batch import, migrations).
- Complex analytics/report queries that hit materialised views and read
  replicas.
- A double-entry ledger with strong integrity requirements.
- Rich Postgres-native features we want to use: JSONB, partitioning,
  `LISTEN/NOTIFY`, `COPY`, common table expressions, window functions,
  `SELECT ... FOR UPDATE SKIP LOCKED` for job queues, and prepared statements.

Options evaluated:

1. **GORM only** — one high-level ORM everywhere.
2. **pgx only** — the native, high-performance PostgreSQL driver.
3. **Hybrid: GORM for CRUD + pgx for hot paths.**
4. **Pure `database/sql` with sqlx or scany.**

## Decision

We adopt **pgx v5** (`github.com/jackc/pgx/v5`) as the **sole driver**, wrapped
by a **thin, hand-written repository layer** using **scany**
(`github.com/georgegmelo/scany/v2`) for row scanning.

Concretely:

- All modules use pgx pools obtained from `internal/platform/db`.
- Reads scan into typed structs with scany's `pgxscan.Select` / `pgxscan.Get`.
- Writes use pgx's `Exec`, `QueryRow`, and `SendBatch` for bulk operations.
- Transactions are opened through `platform/db.WithTx(ctx, fn)`, which handles
  commit/rollback and supports savepoints.
- Complex reporting queries can use `pgx.Rows` directly for maximum control.
- Migrations are handled by **golang-migrate** with plain SQL files under
  `migrations/`. **The application never runs `AutoMigrate`.**

We deliberately drop GORM even for CRUD, reversing the earlier "hybrid" note
in the planning document.

## Rationale

### Why not GORM everywhere

- Reflection-based query building is measurably slower on the hot POS path.
- GORM's `AutoMigrate` and hook system encourage schema drift that plain SQL
  migrations do not tolerate.
- Advanced Postgres features (partitioning, `LISTEN/NOTIFY`, `COPY`, JSONB
  operators, GIN / trigram indexes) need workarounds in GORM.
- Debugging generated SQL under load is painful.

### Why not a GORM + pgx hybrid

- Two drivers means two connection pools, two configuration surfaces, two
  logging chains, two sets of gotchas. The cognitive cost outweighs the
  velocity we lose by writing our own repositories.
- Every "hybrid" system in practice drifts toward the driver its senior
  engineer likes best; we choose one on purpose.

### Why not `database/sql` + sqlx

- pgx exposes Postgres-specific types (arrays, JSONB, `pgtype.Numeric`,
  `inet`, `daterange`) natively and correctly.
- `database/sql` throws these away behind a generic interface, forcing extra
  parsing.
- pgx is the driver that upstream projects benchmark against.

## Consequences

### Positive

- **Performance**: hot paths shave 40–60 % latency versus a GORM equivalent
  based on internal benchmarks and community reports.
- **Explicit SQL** in `migrations/` and repositories — reviewable, greppable,
  and identical to what runs against production.
- **No ORM leakage** into the domain layer.
- Full access to Postgres features we already plan to rely on (partitioning,
  advisory locks, batch inserts, JSONB indexes).

### Negative

- **More boilerplate**: each repository method is hand-written. We accept this
  as the price of clarity and speed.
- **Scanning correctness** is our responsibility. Mitigated by scany plus
  integration tests that hit a real Postgres.
- **Query building for filtered lists** (dynamic `WHERE` clauses) requires a
  small in-house builder or `squirrel`. We choose `squirrel`
  (`github.com/Masterminds/squirrel`) for the few dynamic list endpoints and
  keep everything else as static SQL.

### Migrations

Managed exclusively by **golang-migrate** with pinned versions:

- `migrate create -ext sql -dir migrations -seq <name>`
- Every up file has a matching down file.
- Migrations are applied by `cmd/migrate` at deploy time, **not** at API
  boot.

## Guardrails

- No package outside `internal/platform/db` imports pgx or scany directly.
- No SQL string is built with `fmt.Sprintf` including user input. Ever.
- `pgx.QueryRow` results must not be discarded — every `Scan` error is
  wrapped and returned.
- Query timeouts default to 30 s at the pool level; hot paths set shorter
  per-call deadlines via `context.WithTimeout`.

## References

- Jackc pgx v5 documentation, https://pkg.go.dev/github.com/jackc/pgx/v5
- scany v2 documentation, https://github.com/georgegmelo/scany
- golang-migrate documentation, https://github.com/golang-migrate/migrate