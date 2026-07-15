# ADR 0001 — Adopt a Modular Monolith Architecture

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Tech Lead
- **Consulted:** Platform, DevOps
- **Informed:** All engineering
- **Supersedes:** —
- **Superseded by:** —

---

## Context

MediCore ERP must serve a multi-branch pharmacy retail business: identity,
inventory (batch/expiry), POS sales, purchase workflow, double-entry ledger,
AI forecasting, notifications, audit, and analytics. The system will be
deployed on modest hardware (4 vCPU / 4 GB RAM per node in dev, VPS-class in
early production) and must survive to production with a small team.

We evaluated three architectural styles:

1. **Full microservices from day one** — one service per domain (auth, user,
   inventory, sale, purchase, ledger, ai, notification), inter-service auth,
   service mesh, distributed tracing, saga orchestrator.
2. **Pure layered monolith** — single Go binary with folders for handlers,
   services, and repositories, no per-domain packaging.
3. **Modular monolith** — single Go binary, but code is organised by domain
   modules with explicit contracts, an eventing seam (outbox), and a clear
   extraction path.

Constraints and forces that shaped the decision:

- **Team size**: 1–3 engineers for the foreseeable future.
- **Latency budget**: POS checkout p95 ≤ 500 ms.
- **Consistency**: sale, stock decrement, and ledger post must be atomic.
- **Deployment**: `docker compose up` on a laptop must produce a working system
  in under 5 minutes.
- **Change velocity**: expect significant schema and business-rule churn in
  the first year.
- **Future scale**: we should be able to extract hot modules (sale, analytics,
  ai) into separate services later without a rewrite.

## Decision

We will build MediCore ERP as a **modular monolith** in Go.

Concretely:

1. Each domain is a self-contained package under `internal/modules/<name>/`,
   with its own model, DTO, repository, service, handler, and routes.
2. Modules **do not import each other's repositories or models directly**.
   Cross-module communication happens through:
   - Public service interfaces defined by the callee (dependency inversion).
   - Domain events posted via the **outbox** table and delivered by Asynq to
     interested subscribers (audit, notifications, analytics refresh).
3. Cross-cutting concerns live in `internal/platform/` (db, redis, logger,
   telemetry, validator) and `internal/middleware/`.
4. There is exactly **one deployable API binary** (`cmd/api`) and **one worker
   binary** (`cmd/worker`), sharing the codebase.
5. A future extraction to a microservice replaces the in-process service call
   with a gRPC/HTTP client behind the same interface. **The domain code does
   not change.**

## Alternatives Considered

### Full microservices from day one

- **Pros**: Independent scaling, technology per service, small blast radius.
- **Cons**: Requires service discovery, distributed tracing, saga orchestration,
  per-service CI/CD, and 5× the operational surface. A 1–3 person team cannot
  maintain this without slowing feature work to a crawl. Also, most of our
  traffic goes through a single hot path (POS + inventory + ledger) where
  the network hop adds latency and failure modes for zero benefit at MVP scale.
- **Verdict**: Rejected as premature.

### Pure layered monolith (no domain packaging)

- **Pros**: Fastest to start.
- **Cons**: Every new feature ends up touching a single `services/` or
  `handlers/` package. Cross-cutting concerns leak into business code. There
  is no clean seam to extract later — the whole thing becomes a big ball of
  mud.
- **Verdict**: Rejected as short-sighted.

### Event-driven from day one (Kafka/NATS)

- **Pros**: Loose coupling, replayable event log.
- **Cons**: Adds a broker to run, monitor and back up; consumer groups; DLQ
  management; and schema evolution tooling. Postgres + outbox + Asynq gives
  us 80 % of the value with 10 % of the operational cost.
- **Verdict**: Deferred to a later ADR when volume warrants it.

## Consequences

### Positive

- One repository, one CI pipeline, one deploy. **Fast iteration.**
- Local transactions across sale ↔ stock ↔ ledger — no distributed transactions.
- Refactors can span modules cheaply because everything compiles together.
- Extraction paths are explicit and reviewable — the interface is the seam.

### Negative

- Discipline is required to keep modules honest. It is easy to import a
  neighbour's repository "just this once" and destroy the seam.
- Deploys are all-or-nothing: a bug in the notification module can crash the
  whole API. Mitigated by the `worker` binary being separate and by strict
  panic recovery middleware.
- Scaling a hot module means scaling the whole binary, until we extract it.

### Neutral

- Team size and workload will govern when (if ever) we extract services.

## Enforcement

- Package boundary check in CI via `go-arch-lint` or an internal script.
- Code review checklist item: “Does this PR import across `internal/modules/`
  without going through a public service interface?”
- ADR review whenever a new module is created.

## References

- Sam Newman, *Monolith to Microservices*, Chapter 1.
- Simon Brown, *The Modular Monolith* (2018).
- Shopify Engineering, *Deconstructing the Monolith* (2019).