# Database Backup and Restore Runbook

This runbook covers the PostgreSQL database used by the Compose `postgres`
service. The repository does not currently provide an automated backup command;
these procedures use standard PostgreSQL tools.

## Backup Policy

Production should have daily full backups, more frequent recovery data or WAL
archiving when required by the RPO, encrypted off-host copies, a retention
policy, and regular restore drills. Backups contain personal data, password
hashes, sessions, refresh-token hashes, and audit records. Treat them as
sensitive production secrets.

## Logical Backup from Docker

Run from `backend/deployments/docker/` and keep dumps outside the repository:

```powershell
New-Item -ItemType Directory -Force .\backups | Out-Null
docker exec pharmaciano-postgres pg_dump -U pharmaciano -d pharmaciano -Fc > .\backups\pharmaciano-$(Get-Date -Format yyyyMMdd-HHmmss).dump
```

For a reviewable SQL export:

```powershell
docker exec pharmaciano-postgres pg_dump -U pharmaciano -d pharmaciano --no-owner --no-privileges > .\backups\pharmaciano.sql
```

Confirm that the archive exists and is non-empty:

```powershell
Get-Item .\backups\*.dump | Select-Object Name,Length,LastWriteTime
docker exec pharmaciano-postgres pg_dump --version
```

## Consistency and Security

`pg_dump` creates a consistent logical snapshot while normal transactions run.
Encrypt the dump before off-host transfer, restrict access to backup operators,
and do not commit it or expose database passwords in shell history.

## Restore into a Disposable Database

Always test a restore into a new database or isolated PostgreSQL instance first:

```powershell
docker exec pharmaciano-postgres createdb -U pharmaciano pharmaciano_restore
Get-Content .\backups\pharmaciano.sql | docker exec -i pharmaciano-postgres psql -U pharmaciano -d pharmaciano_restore
```

For a custom-format archive, use a mounted file or native `pg_restore` for large
files. A small PowerShell stream is:

```powershell
Get-Content .\backups\pharmaciano-latest.dump -AsByteStream | docker exec -i pharmaciano-postgres pg_restore -U pharmaciano -d pharmaciano_restore --clean --if-exists --no-owner
```

## Full Restore Procedure

1. Declare an incident and stop writes or enable maintenance mode.
2. Preserve the damaged instance and take a forensic copy.
3. Provision a clean PostgreSQL instance with the required version.
4. Restore the selected dump.
5. Validate schema version, row counts, constraints, and application queries.
6. Rotate credentials if compromise is suspected.
7. Start API and worker only after validation.
8. Reopen traffic gradually and monitor errors.

Never restore over the only copy of a damaged production database.

## Validation Queries

```sql
SELECT version, dirty FROM schema_migrations;
SELECT count(*) FROM users WHERE deleted_at IS NULL;
SELECT count(*) FROM organizations WHERE deleted_at IS NULL;
SELECT count(*) FROM roles WHERE deleted_at IS NULL;
SELECT count(*) FROM permissions WHERE deleted_at IS NULL;
SELECT count(*) FROM audit_logs;
```

Then check readiness:

```powershell
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/readyz).StatusCode
```

## Point-in-Time Recovery Gap

The repository does not configure PostgreSQL WAL archiving or point-in-time
recovery. Logical dumps cannot recover changes after the dump time. Enterprise
deployments should add managed PostgreSQL backups or encrypted WAL archiving,
monitor archive lag, and test recovery to a precise timestamp.
