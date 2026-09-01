# Pharmaciano ERP Documentation

This directory contains the operational, architectural, API, and development
documentation for the Pharmaciano ERP backend.

## Documentation Map

### Start Here

- [Build sequence](BUILD-SEQUENCE.md): implementation order and current status.
- [API reference](api/API.md): HTTP routes, middleware, permissions, and errors.

### Runbooks

- [Authentication](runbook/AUTH_DOCS.md): JWTs, sessions, refresh rotation,
	Redis, rate limits, RBAC, and security considerations.
- [Deployment](runbook/deployment.md): local, Docker Compose, migration, seed,
	health-check, and rollback procedures.
- [Database backup and restore](runbook/db_backup_restore.md): PostgreSQL
	backup, restore, validation, and disaster recovery.
- [Incident response](runbook/incident_response.md): triage, containment,
	recovery, and evidence collection.

### Architecture Decision Records

ADRs record decisions that should remain stable while the modular monolith
evolves:

1. [Modular monolith](adr/0001-modular-monolith.md)
2. [GORM and pgx boundary](adr/0002-gorm-plus-pgx-hybrid.md)
3. [Argon2id passwords](adr/0003-argon2id-over-bcrypt.md)
4. [Casbin RBAC](adr/0004-casbin-rbac.md)
5. [Asynq background jobs](adr/0005-asynq-background-jobs.md)
6. [Audit-log partitioning](adr/0006-audit-log-partitioning.md)
7. [Double-entry ledger](adr/0007-double-entry-ledger.md)
8. [JWT rotation and reuse detection](adr/0008-jwt-rotation-reuse-detect.md)
9. [Folder structure](adr/0009-Folder-Structure.md)

## Source of Truth

Code and configuration are authoritative for runtime behavior. Documentation
must distinguish implemented behavior from planned features.

- Schema is versioned in `migrations/` and applied by `cmd/migrate`.
- Bootstrap data is stored in `seed/` and applied by `cmd/seed`.
- Docker startup ordering is defined in `deployments/docker/docker-compose.yml`.
- Runtime configuration is defined by `config/config.yaml`, environment
	overlays, and environment variables.

## Local Checks

From `backend/`:

```powershell
go test ./...
go vet ./...
```

Run Docker Compose commands from `backend/deployments/docker/` unless a command
explicitly supplies another Compose file.
