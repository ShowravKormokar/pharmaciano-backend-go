# Deployment Runbook

This runbook describes local and Docker Compose deployment for the Go backend.
Production deployments should add secret management, immutable images, backup
monitoring, and an orchestrator-specific rollout policy.

## Prerequisites

- Docker Desktop with Compose v2.
- Go compatible with `backend/go.mod` for local execution.
- Production secrets supplied externally. Do not use Compose fallback values for
	database, JWT, encryption, cursor-signing, or super-admin credentials.

## Docker Compose Startup

Run from `backend/deployments/docker/`:

```powershell
docker compose config --quiet
docker compose up -d --build
docker compose ps -a
```

Startup order is:

```text
PostgreSQL/Redis healthy -> migrate successful -> seed successful -> API/worker
API healthy -> Nginx
```

The `migrate` service runs `cmd/migrate`; `seed` runs `cmd/seed`. API and worker
do not start when either one-shot service fails.

## Verification

```powershell
docker compose ps
docker compose logs --tail=100 migrate
docker compose logs --tail=100 seed
docker compose logs --tail=100 api
```

Expected API logs include `postgres pools ready`, `redis ready`, `http server
listening`, and `rbac enforcer snapshot loaded`.

```powershell
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/livez).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/readyz).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost/healthz).StatusCode
```

All should return `200`.

## Local Go Execution

From `backend/`, start PostgreSQL and Redis, then run:

```powershell
go run ./cmd/migrate
go run ./cmd/seed
go run ./cmd/api
```

The API checks dependencies and warms RBAC at startup but intentionally does
not run migrations. Schema changes remain explicit and are not raced by API
replicas.

## Configuration

`internal/platform/config` loads base `config/config.yaml`, the environment
overlay such as `config/config.docker.yaml`, and environment variables. Compose
variable substitution uses the shell and the Compose-directory `.env` file.

Validate the rendered configuration without printing secrets:

```powershell
docker compose config --quiet
```

## Update and Rollback

```powershell
docker compose build api worker migrate seed
docker compose up -d
```

Prefer rolling back the image while retaining a backward-compatible schema.
Do not automatically run down migrations during an application rollback.

For a dirty failed migration, inspect logs and database state, take a backup,
then force only the confirmed last good version:

```powershell
docker compose logs migrate
docker compose run --rm migrate ./migrate -force <last-known-good-version>
docker compose run --rm migrate ./migrate -direction up
```

## Shutdown and Cleanup

Stop services while retaining volumes:

```powershell
docker compose down
```

`docker compose down -v` deletes named PostgreSQL, Redis, and application data;
use it only for disposable development data after confirming a backup is not
needed.

## Production Gaps

Before production use, add image signing, secret-manager integration, TLS
automation, resource limits, centralized logs, backup monitoring, restore
drills, and alerts for failed migrations, unhealthy dependencies, and readiness
failures.
