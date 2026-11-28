# MasterData Module Runbook

This runbook describes the operational, monitoring, troubleshooting, and release
procedures for the Pharmaciano ERP MasterData module (`internal/modules/masterdata`).

------------------------------------------------------------------------

## 1. Module Overview

### 1.1 Purpose

MasterData owns the central reference catalogs used across Pharmaciano ERP:
product categories, routes, dosage forms, package types, unit types, storage
conditions, tax rates, drug groups, drug classes, therapeutic classes, and
generic medicines. It provides CRUD endpoints for each resource under
`/api/v1/master/{resource}`.

### 1.2 Ownership

- MasterData owns the eleven central master-data resources.
- It does NOT own manufacturers, brands, or suppliers (those are separate
  modules).
- Currency definitions and minor-unit metadata are referenced by other modules
  (e.g. Ledger) but owned by MasterData/config.
- MasterData is a core reference-data module; most other modules depend on it
  for code/name lookups.

### 1.3 Module Status

Implemented and mounted in the API router. Provides full CRUD for 11 resources
with standard 5-verb endpoints each (create, get, list, update, delete).

------------------------------------------------------------------------

## 2. Scope and Boundaries

### 2.1 In scope

- Product categories (hierarchical via parent_id).
- Routes, dosage forms, package types, unit types.
- Storage conditions (temperature ranges).
- Tax rates (percent, default flag).
- Drug groups, drug classes, therapeutic classes (hierarchical).
- Generic medicines (with controlled flag).

### 2.2 Out of scope

| Concern | Owning module |
|---|---|
| Manufacturers, brands, suppliers | Separate modules |
| Medicine identity/variants | Medicine |
| COA, currency definitions | Ledger / MasterData config |
| Authentication, RBAC | Auth / RBAC |

### 2.3 Dependency direction

```
MasterData -> shared platform, common, errors, PostgreSQL repositories
Other modules -> MasterData (read-only reference lookups)
```

------------------------------------------------------------------------

## 3. Endpoint and Permission Inventory

### 3.1 Routes (registered in `routes.go`, mounted under `/api/v1/master`)

Each of the 11 resources exposes 5 endpoints:

| Method | Path | Handler pattern | Rate policy | Permission |
|---|---|---|---|---|
| POST | `/master/{resource}` | `Create*` | `auth_write` + idempotency + audit | `masterdata:create` |
| GET | `/master/{resource}` | `List*` | `auth_read` | `masterdata:view` |
| GET | `/master/{resource}/:id` | `Get*` | `auth_read` | `masterdata:view` |
| PATCH | `/master/{resource}/:id` | `Update*` | `auth_write` + idempotency + audit | `masterdata:update` |
| DELETE | `/master/{resource}/:id` | `Delete*` | `auth_write` + idempotency + audit | `masterdata:delete` |

Resources: `product-categories`, `routes`, `dosage-forms`, `package-types`,
`unit-types`, `storage-conditions`, `tax-rates`, `drug-groups`, `drug-classes`,
`therapeutic-classes`, `generic-medicines`.

### 3.2 RBAC permissions

- `masterdata:view` --- read master data.
- `masterdata:create` --- create master data records.
- `masterdata:update` --- update master data records.
- `masterdata:delete` --- delete master data records.

### 3.3 Rate policies

- `auth_read` --- read endpoints.
- `auth_write` --- mutation endpoints (with idempotency + audit middleware).

------------------------------------------------------------------------

## 4. Configuration

### 4.1 Feature gating

MasterData is not behind a feature flag; it is mounted unconditionally. It is a
core platform module.

### 4.2 Relevant config keys

| Key | Default | Purpose |
|---|---|---|
| `pagination.default_limit` | 20 | Default page size |
| `pagination.max_limit` | 100 | Maximum page size |

------------------------------------------------------------------------

## 5. Data Model Summary

### 5.1 Core entities (11 resources)

All entities share `common.BaseModel` (id, created_at, updated_at, deleted_at).
Each has `code`, `name`, `is_active` (except where noted). Hierarchical
resources: `ProductCategory` (parent_id), `DrugClass` (drug_group_id),
`TherapeuticClass` (drug_class_id), `DosageForm` (route_id), `UnitType`
(base_unit_id, conversion_factor).

### 5.2 Seed data

`seed.go` provides initial seed data for reference catalogs. Run on first
deployment or via migration.

------------------------------------------------------------------------

## 6. Operational Procedures

### 6.1 Health checks

- `GET /health` --- general liveness.
- `GET /ready` --- readiness (DB connectivity).

### 6.2 Monitoring

- Request rate/latency per resource endpoint.
- Error rate by endpoint.
- Seed data consistency checks.

------------------------------------------------------------------------

## 7. Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| 404 on resource | Route not mounted or wrong path | Verify `RegisterRoutes` wiring in `main.go` |
| 409 conflict | Duplicate code or FK violation | Check unique constraints and parent references |
| Empty list | No records or is_active filter | Check seed data and filters |
| Delete fails | Referenced by other records | Use soft-delete; check FK constraints |

------------------------------------------------------------------------

## 8. Reconciliation

- Orphaned references: verify drug_class.drug_group_id and
  therapeutic_class.drug_class_id point to existing records.
- Tax rate default flag: ensure exactly one default tax rate per organization
  (if applicable).

------------------------------------------------------------------------

## 9. Release Procedure

1. Apply all MasterData migrations.
2. Run seed data if first deployment.
3. Run `go build ./...` and `go test ./internal/modules/masterdata/...`.
4. Smoke test CRUD for at least one resource.

------------------------------------------------------------------------

## 10. Incident Response

- If reference data is corrupted, restore from backup or re-run seed.
- Do not hard-delete records referenced by other modules; use soft-delete
  (is_active=false).

------------------------------------------------------------------------

## 11. Related Documents

- `docs/runbook/LEDGER.md` --- Ledger operational runbook.
- `docs/runbook/PURCHASE.md` --- Purchase operational runbook.
- `docs/runbook/INVENTORY.md` --- Inventory operational runbook.
- `docs/runbook/SALE.md` --- Sale operational runbook.
- `docs/runbook/deployment.md` --- deployment procedures.
- `docs/runbook/incident_response.md` --- incident response procedures.
