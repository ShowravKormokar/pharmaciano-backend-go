# ADR 0005 — Background Jobs on Asynq (Redis-backed)

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead
- **Related:** ADR-0001, ADR-0006

---

## Context

Several MediCore ERP features must run outside the request/response cycle:

- Async audit-log persistence.
- Notification fan-out (WebSocket, DB row, future email/SMS).
- Nightly and hourly cron work (expiry scan, low-stock scan, materialised-view
  refresh, backup, session cleanup, target progress snapshot).
- AI-forecast requests to a third-party LLM (30 s timeout, retries, cost cap).
- Report generation (CSV/XLSX/PDF).
- Purchase receive → inventory batch materialisation events.

Requirements:

- Retries with exponential backoff.
- Dead-letter queue for repeated failures.
- Cron scheduling.
- Priority queues (POS ledger post > report export).
- Observability (queue depth, latency, failure counts).
- Small operational footprint (no new datastore).

Options considered:

1. **Goroutines + channels** — in-process only.
2. **Asynq** (`github.com/hibiken/asynq`) — Redis-backed.
3. **River** — Postgres-backed jobs.
4. **NATS JetStream / Kafka** — event streaming.
5. **RabbitMQ** — traditional broker.

## Decision

We use **Asynq** with Redis as the broker.

- One `cmd/worker` binary consumes tasks in three priority queues:
  `critical` (6), `default` (3), `low` (1).
- Cron tasks are registered in `internal/jobs/scheduler.go`.
- Every task type has a typed payload struct and handler under
  `internal/jobs/handlers/`.
- Retries: exponential backoff, `max_retries=25`, terminal failures land in
  Asynq's dead-letter queue with `retention=168h`.
- Web UI (`asynqmon`) is available in dev on port 8082 and protected by
  admin auth in production.

## Rationale

- Redis is already in the stack for cache, sessions and rate limiting. No
  new dependency.
- Asynq is idiomatic Go, actively maintained, and battle-tested.
- Cron scheduling is built-in (no `cron` sidecar).
- Priority queues match our workload shape naturally.
- The web UI aids incident response.

Rejected alternatives:

- **In-process goroutines**: no durability across restarts, no visibility, no
  retries. Fine for tiny fire-and-forget notifications; unacceptable for
  audit or AI forecasts.
- **River (Postgres-backed)**: attractive because it removes Redis as a
  dependency for jobs, but we already need Redis; adding another queue on
  Postgres adds contention on the main OLTP database.
- **NATS JetStream / Kafka**: streaming platforms are overkill for our scale
  and add substantial operational cost.
- **RabbitMQ**: capable but requires another daemon to run.

## Queues and Priorities

| Queue | Weight | Typical jobs |
|---|---|---|
| `critical` | 6 | audit_log_persist, ledger_post, notification.fanout |
| `default` | 3 | expiry_scan, low_stock_scan, report_generate (small), outbox_publisher |
| `low` | 1 | ai_forecast, backup, report_generate (large), materialised-view refresh |

With `StrictPriority=true`, the worker fully drains `critical` before serving
`default`, and `default` before `low`.

## Idempotency and Retries

- Every task carries a unique task ID; Asynq enforces at-least-once delivery.
- Handlers are written to be **idempotent**: upserts by natural key, no
  double posts on the ledger, no double notifications.
- Backoff schedule uses Asynq's default `ExponentialBackoff` with jitter.
- Failed tasks flow to the dead-letter queue after retries are exhausted;
  Grafana alerts fire when DLQ depth > 10.

## Consequences

### Positive

- Single Redis to manage.
- Priority queues + backoff + DLQ out of the box.
- Real cron scheduler with pause/resume via the web UI.
- Observability via Prometheus scrape endpoint on the worker.

### Negative

- Redis becomes the durability boundary for background work. If Redis is lost
  and AOF/RDB are not enabled, in-flight tasks are lost. Mitigation: enable
  AOF with `appendfsync everysec` and back up the Redis volume nightly.
- Redis memory sizing must account for queued jobs. Baseline: allow 512 MiB
  for the queue.

## Observability

- Every task handler logs with Zap using the task ID as the correlation key.
- Prometheus metrics: `asynq_tasks_enqueued_total`,
  `asynq_tasks_processed_total`, `asynq_tasks_failed_total`,
  `asynq_queue_size`.
- Grafana dashboard: `deployments/grafana/dashboards/jobs.json`.
- Alerts:
  - DLQ depth > 10 for 15 minutes.
  - Any queue depth > 5 000.
  - Failure rate > 5 % over 5 minutes.

## References

- Asynq documentation, https://github.com/hibiken/asynq
- Reliable background jobs patterns, https://brandur.org/queue