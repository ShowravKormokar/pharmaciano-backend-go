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

1. Is this system ready to build other modules like medicines, category, brand?

Yes, absolutely. The auth + RBAC + tenant + branch-scope foundation is complete and production-hardened. Here's why it's ready:

What's already in place that new modules inherit:

Org-scoped queries by default — every repository method receives orgID as a mandatory parameter; cross-tenant reads return ErrNoRows
Branch-scoped List queries — the ListFilter.BranchScope pattern (from appctx.BranchScope(ctx)) is wired; you just add WHERE branch_id = ANY($n) when len(BranchScope) > 0
Route protection — mw.Protected(module, action) gives you Auth → IdleSession → Tenant → RBAC chain automatically
Idempotency + Audit — mutations get mw.Idempotency() + mw.Audit() for free
Rate limiting — auth_read / auth_write policies already configured
Branch-scope enforcement — enforceBranchScope(ctx, branchID) helper pattern established (see warehouse module)

What a new module needs to implement:

Domain models (DTOs + entities)
Repository with org-scoped + branch-scoped queries
Service with business logic + enforceBranchScope on Get/Update/Delete
Handler with validation
Routes using mw.Protected("medicines", "view/create/update/delete")
Seed permissions in the RBAC catalogue (via migration)

The pattern is established and consistent across organization, branch, warehouse, and user modules. No architectural gaps remain.

2. Completed modules: organization, branch, warehouse, auth, rbac, user — what module next? Give me the sequence.

Recommended sequence based on dependency order and business value:

Priority	Module	Reason
1	medicines (products)	Core domain entity. Everything else (categories, brands, inventory, POS, prescriptions) references medicines. Defines the medicines permission set.
2	categories	Hierarchical taxonomy for medicines. Simple CRUD + tree operations. Referenced by medicines.
3	brands	Flat list, simple CRUD. Referenced by medicines.
4	units	UOM (tablet, ml, strip, box). Referenced by medicines + purchase/sale.
5	suppliers	Vendor master. Needed for purchase orders. Branch-scoped (each branch may have different suppliers).
6	purchases	Purchase order → goods receipt → stock entry. First transactional module. Complex workflow (draft → ordered → received → invoiced).
7	inventory (stock)	Real-time stock per branch+warehouse+medicine+batch+expiry. Consumed by POS and replenishment.
8	pos (point of sale)	Sales transaction. High-throughput, idempotent, integrates inventory deduction + payment + receipt.
9	customers	Patient/customer master. Referenced by POS + prescriptions. Branch-scoped.
10	prescriptions	Doctor prescriptions → dispensing workflow. Links customer + medicines + prescriber.
11	payments	Payment methods, collections, reconciliation. Standalone but used by POS + purchases.
12	expenses	Operational expenses per branch. Simple but needed for P&L.
13	reports/analytics	Sales, stock, expiry, profitability dashboards. Read-heavy, uses read replicas.

Why this order:

Foundation first (medicines, categories, brands, units) — no dependencies on transactional modules
Supply chain before sales (purchases → inventory → POS) — stock must exist before you can sell
Branch-scoped master data (suppliers, customers) before the modules that use them
POS last among transactional — it's the most complex (idempotency, payments, inventory deduction, receipt printing, offline-capable design)

Modules that can be built in parallel:

Categories, brands, units (independent)
Suppliers, customers (independent)
Reports/analytics (after core data exists)

Estimated effort: Medicines module ~3-4 days (including repository patterns, service, handler, routes, permissions seed, tests). Each subsequent module gets faster as patterns are reused.