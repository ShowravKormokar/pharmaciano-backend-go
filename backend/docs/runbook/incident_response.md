
Use this runbook when the API is unavailable, dependencies are unhealthy,
authentication is failing, data integrity is in doubt, or a security event is
suspected.

## Principles

- Protect people, business data, and evidence before optimizing uptime.
- Record timestamps in UTC, deployment version, environment, and request IDs.
- Do not delete containers, volumes, logs, or database rows during triage.
- Treat exposed passwords, JWT secrets, encryption keys, and refresh tokens as
	compromised until proven otherwise.
- Prefer reversible containment and preserve the original state.

## First Five Minutes

Run from `backend/deployments/docker/`:

```powershell
docker compose ps -a
docker compose logs --tail=200 api
docker compose logs --tail=200 worker
docker compose logs --tail=100 migrate
docker compose logs --tail=100 seed
```

Probe services:

```powershell
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/livez).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/readyz).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost/healthz).StatusCode
docker exec pharmaciano-postgres pg_isready -U pharmaciano -d pharmaciano
docker exec pharmaciano-redis redis-cli ping
```

Capture the incident start time, current commit/image tag, container restart
count, first error, and affected users, tenants, routes, or clients.

## API Does Not Start

Check in this order:

1. PostgreSQL and Redis health state.
2. `migrate` exit code and logs.
3. `seed` exit code and logs.
4. API logs for configuration validation, RBAC startup, or bind errors.
5. Host port ownership.

Typical causes include dirty migrations, missing schema, invalid secrets, wrong
Docker hostnames, a port conflict, or a startup query blocked by a database
lock. Do not bypass migration/seed dependency conditions to expose a partially
initialized API.

## API Runs but Requests Fail

- `502` from Nginx: inspect API health and `docker compose logs nginx api`.
- `503` from `/readyz`: inspect the readiness body and test dependencies.
- `401`: verify the Bearer header, token expiry, issuer/audience, and session row.
- `403`: inspect tenant scope and RBAC roles/permissions.
- `429`: read `Retry-After`, `X-RateLimit-Reset`, and the active policy.
- `500`: correlate the request ID with API and database logs.

Protected requests check the PostgreSQL session on every request. A revoked
session can invalidate an otherwise unexpired JWT.

## PostgreSQL or Redis Incident

PostgreSQL diagnostics:

```powershell
docker exec pharmaciano-postgres pg_isready -U pharmaciano -d pharmaciano
docker exec pharmaciano-postgres psql -U pharmaciano -d pharmaciano -c "SELECT pid,state,wait_event_type,wait_event,query_start,left(query,160) FROM pg_stat_activity WHERE datname='pharmaciano';"
```

If data integrity is suspected, stop writes, take a forensic copy, and follow
[db_backup_restore.md](db_backup_restore.md).

Redis supplies rate-limit buckets and ephemeral coordination. The rate limiter
fails open during a Redis outage, so brute-force protection may be reduced. Use
an upstream gateway or emergency local limit for login/reset traffic while
Redis is restored. Do not flush Redis without assessing locks, idempotency,
caches, and event consumers.

## Authentication or Credential Incident

For suspected token theft, refresh-token replay, or account takeover:

1. Preserve API, Nginx, Redis, and PostgreSQL logs.
2. Identify user, session, token family, request IDs, and time range.
3. Revoke the affected session or all user sessions.
4. Force a password reset and rotate affected credentials.
5. If signing secrets may be exposed, stop token issuance, rotate the JWT key,
	 invalidate sessions, and redeploy all verifiers.
6. If encryption keys may be exposed, follow the key-rotation and breach
	 assessment process before changing ciphertext handling.
7. Review audit events and database rows for persistence or privilege changes.

Refresh-token reuse detection revokes the entire family and owning session. A
per-JWT Redis blacklist is not currently active; session revocation is the
access-token invalidation authority.

## Brute-Force and Rate-Limit Incident

Login protection is a Redis token bucket of 20 requests per IP per 15 minutes,
plus account lockout after 5 consecutive failed passwords for 15 minutes.
Password reset is limited to 3 requests per IP per hour.

Preserve rate-limit headers and IP evidence, apply WAF/CDN rules where
available, protect targeted accounts without revealing account existence, and
watch Redis availability because the distributed limiter fails open.

## Migration or Data Incident

1. Stop the API and worker rollout.
2. Read migration logs and inspect `schema_migrations`.
3. Determine whether the migration is dirty and which statements applied.
4. Take a backup before correction.
5. Use `cmd/migrate -force` only after confirming the recovery version.
6. Apply the repaired migration and validate dependent tables.
7. Restart seed, API, and worker in dependency order.

Never edit an already-applied migration in production without a controlled
recovery plan. Add a new forward migration when possible.

## Containment and Recovery

Stop application writers while retaining volumes:

```powershell
docker compose stop api worker
```

Restart after containment or repair:

```powershell
docker compose up -d
docker compose ps -a
```

Do not run `docker compose down -v` during an incident. It deletes named data
volumes and can destroy the only readily available database copy.

## Closure Checklist

- Root cause documented and independently verified.
- Affected sessions, credentials, and keys rotated where required.
- Database and backup integrity validated.
- `/livez`, `/readyz`, and `/healthz` return expected status.
- Login, refresh, logout, and a protected authorization path tested.
- Logs and evidence retained according to policy.
- Monitoring rule or regression test added for the failure mode.
- Timeline, impact, remediation, and follow-up owner recorded.

## Current Automation Gaps

The repository does not currently implement complete incident automation,
automated backup scheduling, WAL archiving, SIEM integration, or key rotation.
Production operators must add and exercise these controls through documented
disaster-recovery and security tests.
