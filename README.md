<div align="center">

# Pharmaciano ERP — Backend

**A production-grade, multi-branch Pharmacy ERP backend written in Go.**

*Auth, RBAC, inventory with FEFO batch tracking, POS, purchase workflow,
double-entry ledger, AI forecasting, WebSocket notifications, audit trail.*

[![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/postgres-16-336791?logo=postgresql)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/redis-7-DC382D?logo=redis)](https://redis.io/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![CI](https://github.com/YOUR_ORG/medicore-erp-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/YOUR_ORG/medicore-erp-backend/actions/workflows/ci.yml)

</div>

---

## Table of Contents

1. [Overview](#overview)
2. [Features](#features)
3. [Tech Stack](#tech-stack)
4. [Architecture](#architecture)
5. [Project Structure](#project-structure)
6. [Prerequisites](#prerequisites)
7. [Getting Started](#getting-started)
8. [Configuration](#configuration)
9. [Database & Migrations](#database--migrations)
10. [Running with Docker](#running-with-docker)
11. [Testing](#testing)
12. [Code Quality](#code-quality)
13. [API Documentation](#api-documentation)
14. [Background Jobs](#background-jobs)
15. [Observability](#observability)
16. [Security](#security)
17. [Deployment](#deployment)
18. [Roadmap](#roadmap)
19. [Contributing](#contributing)
20. [License](#license)

---

## Overview

Pharmaciano ERP is the backend service that powers a multi-branch pharmacy retail
business. It handles:

- One **organization** with many **branches**, each with one or more **warehouses**.
- A **central catalog** of medicines, brands, and master data (managed by SUPER_ADMIN).
- **Branch-scoped inventory** with strict batch and expiry tracking (FEFO).
- End-to-end **purchase workflow** (request → approval → receive → payment).
- A **POS sales engine** with returns, coupons, offers, and multi-tender payments.
- A **double-entry ledger** that auto-posts from sales, purchases, and payments.
- **AI-driven forecasts** (demand, restock, business summary) via third-party LLMs.
- **Real-time notifications** over WebSocket, backed by Redis pub/sub.
- A comprehensive **audit trail**, **session management**, and **RBAC** via Casbin.

Designed to run on a laptop under Docker (4 vCPU / 4 GB RAM) and scale
horizontally in production.

## Features

**Identity & Access**
- JWT access tokens + rotating refresh tokens with reuse detection.
- Per-email + per-IP + per-device rate limiting on login.
- HTTP-only, Secure, SameSite refresh cookies.
- Dynamic roles via Casbin (RBAC + PBAC hybrid).
- MFA-ready (TOTP).
- Full session ledger with force-logout across all devices.

**Operations**
- Multi-branch, multi-warehouse inventory.
- Immutable inventory batches with mfg/expiry dates.
- FEFO (First-Expiry-First-Out) stock selection.
- Branch-to-branch warehouse transfers.
- Purchase state machine with approval workflow.
- POS checkout with idempotency keys and atomic ledger posting.

**Finance**
- Chart of Accounts with hierarchical structure.
- Balanced journal entries; reversing entries supported.
- Monthly account balance snapshots.
- Revenue / P&L / balance sheet / cash flow reports.
- Targets by organization or branch, monthly / quarterly / yearly.

**Platform**
- OpenAPI 3.1 spec generated from code.
- Structured logging (Zap), Prometheus metrics, OpenTelemetry traces.
- Asynq background jobs (audit, notifications, reports, expiry scans, AI).
- Outbox pattern for reliable event publishing.
- WebSocket hub for real-time client updates.

## Tech Stack

| Layer | Choice | Why |
|---|---|---|
| Language | **Go 1.22+** | Concurrency, static binaries, excellent tooling |
| Framework | **Gin** | Minimal, fast, huge ecosystem |
| DB Driver | **pgx v5 / scany** | Performance-first, Postgres-native features |
| Database | **PostgreSQL 16** | JSONB, partitioning, MVCC, trigram search |
| Cache/Bus | **Redis 7** | Sessions, rate limits, pub/sub, Asynq broker |
| Jobs | **Asynq** | Redis-backed, retries, cron, dead-letter |
| Auth | **JWT (HS256/RS256)** + **Argon2id** | Modern hashing, stateless tokens |
| Authz | **Casbin** with GORM adapter | RBAC + PBAC in one policy engine |
| Migrations | **golang-migrate** | Versioned up/down SQL files |
| Logs | **Zap** | Structured, zero-alloc paths |
| Metrics | **Prometheus** | Battle-tested, pull model |
| Traces | **OpenTelemetry** | Vendor-neutral instrumentation |
| Config | **Viper** | Env + YAML + defaults |
| Validation | **go-playground/validator** | Struct tag rules |
| API Docs | **swaggo/swag** | Generate OpenAPI from Go comments |
| WebSocket | **gorilla/websocket** | Stable, mature |
| Testing | **testify** + **testcontainers-go** | Real Postgres/Redis in CI |
| Lint | **golangci-lint** | Aggregated linters |
| Container | **Docker** + **docker-compose** | Reproducible local dev |
| Reverse proxy | **Nginx** | TLS, rate-limit, gzip |
| Dashboards | **Grafana** + **Loki** | Logs + metrics visualization |

## Architecture

**Modular monolith** organized by domain, deployable as a single binary today
and extractable into services later without rewrites.

Client ──► Nginx ──► Gin (api) ──► Middleware chain ──► Handlers ──► Services ──► Repositories ──► Postgres
│
├─► Redis (cache, sessions, rate limits)
└─► Asynq (audit, notif, reports, AI, backup)  

See [`docs/adr/0001-modular-monolith.md`](docs/adr/0001-modular-monolith.md)
for the full rationale.

## Project Structure  
```
medicore-erp-backend/
├── cmd/                # entrypoints: api, worker, seed, migrate
├── internal/
│   ├── modules/        # domain modules (auth, user, sale, purchase, …)
│   ├── platform/       # cross-cutting infra (db, redis, logger, telemetry)
│   ├── middleware/     # gin middlewares
│   ├── router/         # route registration (v1/v2)
│   ├── jobs/           # Asynq workers + scheduler
│   ├── errors/         # error taxonomy
│   └── common/         # shared context, constants, enums
├── pkg/                # reusable helpers (pagination, response, crypto)
├── migrations/         # golang-migrate SQL files
├── seed/               # YAML seed data
├── deployments/        # Docker, Nginx, Prometheus, Grafana configs
├── docs/               # ADRs, OpenAPI, runbooks, diagrams
├── tests/              # integration + load + fixtures + mocks
├── scripts/            # dev helpers
├── config/             # YAML configs + Casbin model
└── .github/workflows/  # CI / release / security / CodeQL  
```
## Prerequisites

- **Go** 1.22 or later — <https://go.dev/dl/>
- **Docker** 24+ and **Docker Compose** v2
- **Make** (optional but recommended)
- **PostgreSQL client** (`psql`) for ad-hoc queries
- **golang-migrate** CLI (optional; the `migrate` binary in `cmd/` also works)
- **golangci-lint** — `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
- **swag** — `go install github.com/swaggo/swag/cmd/swag@latest`
- **mockery** — `go install github.com/vektra/mockery/v2@latest`

## Getting Started

```bash
# 1. Clone
git clone https://github.com/YOUR_ORG/medicore-erp-backend.git
cd medicore-erp-backend

# 2. Copy env file
cp .env.example .env
# then edit .env — at minimum set JWT_SECRET and DB_PASSWORD

# 3. Boot infra + apps (Postgres, Redis, api, worker, Nginx, Prometheus, Grafana)
./scripts/dev_up.sh

# 4. Run migrations
docker compose exec api /app/migrate up

# 5. Seed default data (SUPER_ADMIN, roles, permissions, master data)
docker compose exec api /app/seed

# 6. Verify
curl http://localhost:8080/healthz
open http://localhost:8080/api/v1/docs   # Swagger UI
```

Default super-admin credentials (only in dev; change immediately):  
email:    superadmin@medicore.local
password: read from .env — SUPER_ADMIN_INITIAL_PASSWORD  

### Common commands

```bash
make run           # go run ./cmd/api
make worker        # go run ./cmd/worker
make migrate-up    # apply migrations
make migrate-down  # roll back last migration
make seed          # seed defaults
make lint          # golangci-lint run
make test          # unit tests
make test-int      # integration tests (testcontainers)
make swag          # regenerate OpenAPI docs
make docker-up     # docker-compose up -d
make docker-down   # docker-compose down
```

## Configuration

Configuration is loaded in this order (later wins):

1. `config/config.yaml` (defaults, committed)
2. `config/config.<env>.yaml` (per environment — `dev`, `docker`, `prod`)
3. Environment variables (see `.env.example`)

Sensitive values (JWT secrets, DB passwords, provider keys) **must** come from
environment variables, never committed files.

Full option reference lives in `config/config.yaml`.

## Database & Migrations

Migrations live in `migrations/` and follow `NNNNNN_description.up.sql` /
`NNNNNN_description.down.sql`. Applied with `golang-migrate`.

```bash
# Apply all pending migrations
./cmd/migrate up

# Roll back the last one
./cmd/migrate down 1

# Force a specific version (recovery only)
./cmd/migrate force <version>

# Create a new migration
migrate create -ext sql -dir migrations -seq add_something
```

Backups: `pg_dump` runs nightly via the `backup` Asynq handler. Point-in-time
recovery via WAL archiving is wired for production only.

## Running with Docker

```bash
docker compose -f deployments/docker/docker-compose.yml up -d
```

Services:

| Service | Port | Purpose |
|---|---|---|
| `api` | `8080` | HTTP API |
| `worker` | — | Asynq worker |
| `postgres` | `5432` | Database |
| `redis` | `6379` | Cache / queue |
| `nginx` | `80/443` | Reverse proxy |
| `prometheus` | `9090` | Metrics scrape |
| `grafana` | `3000` | Dashboards |
| `adminer` | `8081` | DB admin UI (dev only) |
| `asynqmon` | `8082` | Job queue UI (dev only) |

Resource caps (matches the target VPS profile): 4 vCPU, 4 GB RAM across the
stack. Tune in `docker-compose.yml`.

## Testing

```bash
# Unit tests
./scripts/test.sh

# Integration tests (spins up real Postgres + Redis via testcontainers)
go test -tags=integration ./tests/integration/...

# Load tests (k6)
k6 run tests/load/pos_checkout.js
```

Coverage target: **80% overall**, **90%+ on services and repositories**.

## Code Quality

```bash
./scripts/lint.sh           # golangci-lint run --fix
go vet ./...
gofmt -s -w .
./scripts/generate_mocks.sh # regenerate mockery mocks
```

Pre-commit is not enforced but recommended. CI runs the same commands on every PR.

## API Documentation

- OpenAPI 3.1 spec: `docs/api/openapi.yaml` (regenerate with `make swag`)
- Interactive docs (Swagger UI): `http://localhost:8080/api/v1/docs`
- Full endpoint reference: [`API.md`](API.md)

## Background Jobs

Asynq (Redis-backed) runs these queues:

| Queue | Priority | Examples |
|---|---|---|
| `critical` | 6 | audit_log, ledger_post |
| `default` | 3 | notification, expiry_scan, low_stock |
| `low` | 1 | report_generate, ai_forecast, backup |

Cron entries live in `internal/jobs/scheduler.go` (nightly expiry scan,
weekly forecast, daily backup). Dead-letter queue retains failures for 7 days.

## Observability

- **Logs** — JSON, shipped to Loki via Grafana Agent.
- **Metrics** — Prometheus scrapes `/metrics` every 15 s.
- **Traces** — OTLP export to Jaeger/Tempo (env: `OTEL_EXPORTER_OTLP_ENDPOINT`).
- **Dashboards** — pre-built in `deployments/grafana/dashboards/`.
- **Health** — `/healthz` (liveness), `/readyz` (checks DB + Redis + worker).

## Security

- Argon2id password hashing (memory 64 MB, time 3, parallelism 2).
- JWT with short-lived access tokens (15 min) + rotating refresh (7 d).
- Refresh token reuse detection revokes the entire family.
- CORS allow-list, HSTS, CSP, X-Frame-Options, X-Content-Type-Options.
- Body size limit (5 MB default, 25 MB uploads).
- Idempotency keys on every money-moving POST.
- Row-level tenant isolation via `organization_id` scoping.
- PII (NID, bank account, salary) encrypted at rest with AES-256-GCM.
- Secrets never logged; audit-log serializer redacts sensitive fields.
- Dependencies scanned nightly (`govulncheck`, `gosec`, Trivy).

## Deployment

The Docker Compose stack is production-shape (multi-stage builds, non-root
users, read-only rootfs where possible). For real production:

1. Terminate TLS at Nginx or your load balancer.
2. Point `POSTGRES_HOST` at a managed Postgres (RDS, Aiven, or Patroni).
3. Enable WAL archiving and configure PITR.
4. Add a read replica; the app already routes reports to the replica.
5. Front `api` and `worker` with a process supervisor (systemd or k8s).
6. Rotate JWT signing keys via `JWT_KEY_ID` and dual-verifier support.

See `docs/runbook/deployment.md`.

## Roadmap

- [ ] Extract `ai` module into its own service.
- [ ] Split `analytics` reads onto a materialized-view refresher.
- [ ] Add SMS + Email notification channels.
- [ ] Payment gateway integrations (bKash, Nagad, cards).
- [ ] Multi-tenant SaaS mode (organization per tenant on shared DB).
- [ ] gRPC internal APIs between modules.
- [ ] Kubernetes manifests + Helm chart.

## Contributing

1. Fork, branch off `main`.
2. Match existing style — `gofmt -s`, `golangci-lint run`.
3. Add tests. New code paths must include unit and integration coverage.
4. Update `docs/adr/` for any architectural decision.
5. Open a PR with a clear title, linked issue, and testing notes.

## License

MIT — see [`LICENSE`](LICENSE).