# MediCore ERP — REST API Specification

**Base URL:** `https://api.medicore.example.com`  
**Current Version:** `v1`  
**All endpoints:** `/api/v1/...`  
**Content-Type:** `application/json`  
**Auth:** Bearer JWT (Access Token in `Authorization` header) + HTTP-only Refresh Cookie  
**Standard:** OpenAPI 3.1  

---

## Table of Contents

1. [Global Conventions](#1-global-conventions)
2. [Middleware Stack](#2-middleware-stack)
3. [Rate-Limit Policies](#3-rate-limit-policies)
4. [Standard Query Parameters](#4-standard-query-parameters)
5. [Standard Response Envelope](#5-standard-response-envelope)
6. [Error Codes](#6-error-codes)
7. **Modules**
   - 7.1 [Auth](#71-auth)
   - 7.2 [Users](#72-users)
   - 7.3 [Roles & Permissions](#73-roles--permissions)
   - 7.4 [Organizations](#74-organizations)
   - 7.5 [Branches](#75-branches)
   - 7.6 [Warehouses](#76-warehouses)
   - 7.7 [Master Data](#77-master-data)
   - 7.8 [Brands](#78-brands)
   - 7.9 [Manufacturers](#79-manufacturers)
   - 7.10 [Suppliers](#710-suppliers)
   - 7.11 [Medicines](#711-medicines)
   - 7.12 [Inventory](#712-inventory)
   - 7.13 [Purchases](#713-purchases)
   - 7.14 [Purchase Payments](#714-purchase-payments)
   - 7.15 [Sales (POS)](#715-sales-pos)
   - 7.16 [Sales Returns](#716-sales-returns)
   - 7.17 [Customers](#717-customers)
   - 7.18 [Coupons & Offers](#718-coupons--offers)
   - 7.19 [Ledger & Finance](#719-ledger--finance)
   - 7.20 [Targets](#720-targets)
   - 7.21 [Reports](#721-reports)
   - 7.22 [Analytics](#722-analytics)
   - 7.23 [AI Forecasting](#723-ai-forecasting)
   - 7.24 [Notifications](#724-notifications)
   - 7.25 [Audit Logs](#725-audit-logs)
   - 7.26 [Sessions](#726-sessions)
   - 7.27 [Settings](#727-settings)
   - 7.28 [Feature Flags](#728-feature-flags)
   - 7.29 [Backup](#729-backup)
   - 7.30 [WebSocket & Health](#730-websocket--health)

---

## 1. Global Conventions

| Rule | Detail |
|------|--------|
| **Resource style** | Plural nouns (`/sales`, `/users`, `/medicines`). |
| **Sub-resources** | Owned collections: `/sales/{id}/returns`, `/purchases/{id}/payments`. |
| **HTTP verbs** | `GET` (read), `POST` (create/action), `PUT` (full replace, idempotent), `PATCH` (partial update), `DELETE` (soft-delete). |
| **Actions** | Non-CRUD verbs as sub-routes: `/purchases/{id}/approve`, `/sales/{id}/void`. |
| **Versioning** | URL: `/api/v1`. `v2` only for breaking changes. Additive stays in `v1`. `Deprecation` and `Sunset` headers served ≥ 6 months before removal. |
| **Identifiers** | UUID v4 everywhere. Never expose sequential integers. |
| **Idempotency** | `Idempotency-Key` header supported on all `POST` that create money-moving resources (sales, purchases, payments, returns, transfers). |
| **Pagination** | Cursor (`?cursor=`) preferred on hot endpoints; offset (`?page=&limit=`) elsewhere. |
| **Sorting** | `?sort=field,-other_field` — leading `-` means descending. |
| **Filtering** | Explicit query params (`?status=`, `?branch_id=`), date ranges (`?from=&to=`). |
| **Search** | `?q=` for trigram text search where supported. |
| **Timestamps** | RFC 3339 UTC (`2026-07-15T10:22:33Z`). |
| **Content negotiation** | `Accept: application/json` (default). Exports return `text/csv` or `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`. |
| **CORS** | Allow-list only. Credentials allowed. |
| **Compression** | gzip / brotli at Nginx. |
| **Correlation** | Every response carries `X-Request-ID`. Propagate to logs/traces. |

### Verb semantics

| Verb | Purpose | Idempotent | Body |
|------|---------|-----------|------|
| `GET` | Retrieve | Yes | No |
| `POST` | Create / action | No (unless `Idempotency-Key`) | Yes |
| `PUT` | Full replace | Yes | Yes |
| `PATCH` | Partial update | Yes | Yes |
| `DELETE` | Soft-delete | Yes | No |

---

## 2. Middleware Stack

Applied in order at the Gin router level. Each route below declares the **middlewares** it uses via shorthand chips like `[req_id, sec_headers, cors, rate_public, audit]`.

| Chip | Middleware | Responsibility |
|------|------------|----------------|
| `req_id` | Request ID | Attach `X-Request-ID`, propagate to logs. |
| `sec_headers` | Security Headers | HSTS, CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy. |
| `cors` | CORS | Origin allow-list, credentials, methods, headers. |
| `recovery` | Recovery | Panic → 500 + stack trace to log, never to client. |
| `logger` | Access Log | Structured Zap access line per request. |
| `metrics` | Prometheus | Latency histogram, request counter, error counter. |
| `tracing` | OpenTelemetry | Span per request, propagate `traceparent`. |
| `body_limit` | Body Size Limit | Reject > 5 MB (25 MB for uploads). |
| `timeout` | Request Timeout | Default 30 s; heavier endpoints declare their own. |
| `rate_public` | Public Rate Limit | Anonymous, IP-based. Nginx + Redis. |
| `rate_auth` | Authenticated Rate Limit | Per-user + per-endpoint bucket. |
| `rate_sensitive` | Strict Rate Limit | For `/auth/*`, password reset, MFA. |
| `auth` | JWT Auth | Verify HS256/RS256, check session is not revoked, load user into context. |
| `tenant` | Tenant Scope | Inject `organization_id` + `branch_id` from JWT claims. |
| `rbac` | RBAC Enforce | Casbin check `{sub, obj, act}` against user permissions. |
| `super_admin` | Super Admin Only | Reject if role ≠ `SUPER_ADMIN`. |
| `mfa_required` | MFA Guard | For sensitive actions (backup restore, super-admin ops). |
| `idempotency` | Idempotency Store | Deduplicate on `Idempotency-Key`, TTL 24 h. |
| `audit` | Audit Log Writer | Async write to Asynq queue. |
| `validate` | DTO Validation | go-playground/validator + custom rules. |

### Standard chain

**Every route** starts with: `req_id → sec_headers → cors → recovery → logger → metrics → tracing → body_limit → timeout → <rate limit>`.

**Protected routes** append: `→ auth → tenant → rbac → audit`.

**Write routes** append: `→ idempotency → validate`.

---

## 3. Rate-Limit Policies

Two layers:

### 3.1 Nginx layer (crude, per-IP)

| Zone | Path | Limit |
|------|------|-------|
| `global` | `/*` | 100 req/s per IP, burst 200 |
| `auth` | `/api/v1/auth/*` | 20 req/min per IP |
| `pos` | `/api/v1/sales/pos/*` | 60 req/s per IP |

### 3.2 App layer (fine-grained, Redis token bucket)

| Policy Name | Applied To | Limit | Window | Key |
|-------------|-----------|-------|--------|-----|
| `rate_public` | Unauthenticated | **60 req/min** | 60 s | `ip` |
| `rate_auth_read` | Authenticated GET | **300 req/min** | 60 s | `user_id` |
| `rate_auth_write` | Authenticated POST/PUT/PATCH/DELETE | **120 req/min** | 60 s | `user_id + endpoint` |
| `rate_login` | `POST /auth/login` | **5 attempts / 15 min** per email; **20 attempts / 15 min** per IP | 15 min | `email`, `ip` |
| `rate_refresh` | `POST /auth/refresh` | **60 req/min** | 60 s | `user_id + device_fp` |
| `rate_reset` | `POST /auth/password/reset*` | **3 req/hour** | 3600 s | `email + ip` |
| `rate_pos_checkout` | `POST /sales/pos/checkout` | **30 req/min** | 60 s | `cashier_id` |
| `rate_search` | `GET /medicines/search`, `GET /*` with `?q=` | **60 req/min** | 60 s | `user_id` |
| `rate_export` | Any `/export` route | **5 req/hour** | 3600 s | `user_id` |
| `rate_ai` | `POST /ai/*` | **20 req/hour** | 3600 s | `organization_id` |
| `rate_broadcast` | `POST /notifications/broadcast` | **10 req/hour** | 3600 s | `organization_id` |
| `rate_backup` | `POST /backups/*` | **10 req/day` | 86400 s | `organization_id` |
| `rate_ws_connect` | `GET /ws` (upgrade) | **10 connect/min** | 60 s | `user_id` |

Every `429` carries: `Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`.

---

## 4. Standard Query Parameters

Available on every `GET` list endpoint unless a resource-specific note overrides.

| Param | Type | Description |
|-------|------|-------------|
| `page` | int, ≥ 1, default 1 | Offset pagination. |
| `limit` | int, 1–100, default 20 | Page size (max 100, capped at 500 on `/audit-logs/export`). |
| `cursor` | string | Cursor pagination (opaque, base64). Preferred on `/audit-logs`, `/sales`, `/sale-items`, `/stock-movements`. |
| `sort` | csv | e.g. `sort=-created_at,name`. Whitelisted per endpoint. |
| `q` | string | Full-text/trigram search. |
| `from` | RFC 3339 | Date range start (inclusive). |
| `to` | RFC 3339 | Date range end (exclusive). |
| `status` | string | Resource-specific status filter. |
| `branch_id` | UUID | Scope to a branch. |
| `include_deleted` | bool, default false | SUPER_ADMIN only. |
| `fields` | csv | Sparse fieldsets: `fields=id,name,status`. |

Response headers on list endpoints:
```
X-Total-Count: 12483  
X-Page: 3  
X-Limit: 20  
Link: <...&page=4>; rel="next", <...&page=1>; rel="prev"  
X-Next-Cursor: eyJpZCI6...   (when cursor pagination is used)  
```

---

## 5. Standard Response Envelope

**Success**

```json
{
  "success": true,
  "data": { },
  "meta": { 
    "request_id": "01H...",
    "timestamp": "2026-07-15T10:22:33Z"
  }
}
```

**List success**

```json
{
  "success": true,
  "data": [ ],
  "pagination": {
    "page": 1, 
    "limit": 20, 
    "total": 348, 
    "total_pages": 18,
    "next_cursor": "eyJpZCI6..."
  },
  "meta": { 
    "request_id": "01H...", 
    "timestamp": "..." 
  }
}
```

**Error**

```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "email is required",
    "details": [
      { "field": "email", "rule": "required" }
    ]
  },
  "meta": { 
    "request_id": "01H...", 
    "timestamp": "..." 
  }
}
```

---

## 6. Error Codes

| HTTP | Code | Meaning |
|------|------|---------|
| 400 | `VALIDATION_ERROR` | Body/query validation failed. |
| 401 | `UNAUTHENTICATED` | Missing/invalid token. |
| 401 | `TOKEN_EXPIRED` | Access token expired — refresh. |
| 401 | `TOKEN_REUSE_DETECTED` | Refresh token reuse. Whole family revoked. |
| 403 | `FORBIDDEN` | RBAC denies. |
| 403 | `ACCOUNT_LOCKED` | Too many failed logins. |
| 403 | `ACCOUNT_INACTIVE` | Status ≠ active. |
| 404 | `NOT_FOUND` | Resource missing or soft-deleted. |
| 409 | `CONFLICT` | Duplicate, or state-machine violation. |
| 409 | `IDEMPOTENCY_KEY_CONFLICT` | Same key, different payload. |
| 422 | `BUSINESS_RULE_VIOLATION` | e.g. selling below cost, expired batch. |
| 423 | `RESOURCE_LOCKED` | Concurrent edit conflict. |
| 429 | `RATE_LIMITED` | Retry-After present. |
| 500 | `INTERNAL_ERROR` | Unhandled. |
| 502 | `UPSTREAM_ERROR` | AI provider / third party. |
| 503 | `SERVICE_UNAVAILABLE` | Maintenance / circuit open. |

---

# 7. Modules

Legend for each route:
`METHOD path` **·** *Middlewares* **·** *Rate policy* **·** *Permission* **·** description.

---

## 7.1 Auth

Public endpoints. No `auth` middleware.

| Method & Path | Middlewares | Rate | Permission | Description |
|---|---|---|---|---|
| `POST /api/v1/auth/login` | `req_id, sec_headers, cors, recovery, logger, metrics, tracing, body_limit, timeout, rate_login, validate, audit` | `rate_login` | Public | Authenticate; returns access token in body, refresh token as `HttpOnly; Secure; SameSite=Strict` cookie. |
| `POST /api/v1/auth/refresh` | `..., rate_refresh, validate, audit` | `rate_refresh` | Public (with valid refresh cookie) | Rotate refresh token; reuse-detection; sets new cookie. |
| `POST /api/v1/auth/logout` | `..., auth, audit` | `rate_auth_write` | Authenticated | Revoke current session only. |
| `POST /api/v1/auth/logout-all` | `..., auth, audit` | `rate_auth_write` | Authenticated | Revoke every session for the user. |
| `POST /api/v1/auth/password/change` | `..., auth, rate_sensitive, validate, audit` | `rate_sensitive` | Authenticated | Old + new password. Revokes other sessions. |
| `POST /api/v1/auth/password/forgot` | `..., rate_reset, validate, audit` | `rate_reset` | Public | Issue reset token (email channel disabled for now — logs to system). |
| `POST /api/v1/auth/password/reset` | `..., rate_reset, validate, audit` | `rate_reset` | Public (with token) | Set new password via reset token. |
| `POST /api/v1/auth/mfa/setup` | `..., auth, rate_sensitive, audit` | `rate_sensitive` | Authenticated | Generate TOTP secret + QR. |
| `POST /api/v1/auth/mfa/verify` | `..., auth, rate_sensitive, audit` | `rate_sensitive` | Authenticated | Confirm TOTP and enable MFA. |
| `POST /api/v1/auth/mfa/disable` | `..., auth, mfa_required, rate_sensitive, audit` | `rate_sensitive` | Authenticated | Disable MFA. |
| `GET  /api/v1/auth/me` | `..., auth` | `rate_auth_read` | Authenticated | Return current user + permissions + branch scope. |

---

## 7.2 Users

All routes: `..., auth, tenant, rbac, audit`.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/users` | `rate_auth_write` | `users:create` **(SUPER_ADMIN only for user creation policy)** | Create user. |
| `GET    /api/v1/users` | `rate_auth_read` | `users:view` | List users. Filters: `?status=&stage=&role_id=&branch_id=&q=&joining_date_from=&joining_date_to=`. |
| `GET    /api/v1/users/{id}` | `rate_auth_read` | `users:view` | Get user profile. |
| `PATCH  /api/v1/users/{id}` | `rate_auth_write` | `users:update` | Partial update. |
| `PUT    /api/v1/users/{id}` | `rate_auth_write` | `users:update` | Full replace. |
| `DELETE /api/v1/users/{id}` | `rate_auth_write` | `users:delete` | Soft-delete. |
| `PATCH  /api/v1/users/{id}/status` | `rate_auth_write` | `users:update` | `active/deactivated/inactive/suspended/resigned/terminated`. Deactivate/suspend triggers force-logout. |
| `PATCH  /api/v1/users/{id}/stage` | `rate_auth_write` | `users:update` | `unverified/pending/verified`. |
| `POST   /api/v1/users/{id}/roles` | `rate_auth_write` | `users:assign` | Assign roles. |
| `DELETE /api/v1/users/{id}/roles/{role_id}` | `rate_auth_write` | `users:revoke` | Revoke role. |
| `GET    /api/v1/users/{id}/permissions` | `rate_auth_read` | `users:view` | Effective permissions. |
| `GET    /api/v1/users/{id}/sessions` | `rate_auth_read` | `users:view` + `sessions:view` | List sessions for a user. |
| `DELETE /api/v1/users/{id}/sessions/{session_id}` | `rate_auth_write` | `sessions:revoke` | Revoke one session. |
| `DELETE /api/v1/users/{id}/sessions` | `rate_auth_write` | `sessions:revoke` | Revoke all sessions. |
| **User self-profile** ||||
| `GET    /api/v1/users/me/profile` | `rate_auth_read` | Authenticated | Full profile (nested). |
| `PATCH  /api/v1/users/me/profile` | `rate_auth_write` | Authenticated | Update own profile. |
| `GET    /api/v1/users/me/sessions` | `rate_auth_read` | Authenticated | Own active devices. |
| `DELETE /api/v1/users/me/sessions/{session_id}` | `rate_auth_write` | Authenticated | Logout that device. |
| **Nested collections** — same pattern for each: `GET/POST/PATCH/DELETE` ||||
| `.../users/{id}/addresses` | `users:update` | | 4 verbs. |
| `.../users/{id}/contacts` | `users:update` | | Emergency contacts. |
| `.../users/{id}/educations` | `users:update` | | |
| `.../users/{id}/experiences` | `users:update` | | |
| `.../users/{id}/bank-accounts` | `users:update` | | |
| `.../users/{id}/documents` | `users:update` | | Multipart upload for document files. |
| **Export** ||||
| `GET /api/v1/users/export?format=csv\|xlsx` | `rate_export` | `users:export` | Async job → returns `job_id`. |

---

## 7.3 Roles & Permissions

All routes: `..., auth, tenant, rbac, audit`.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/roles` | `rate_auth_write` | `roles:create` | Create dynamic role (e.g. `JHON_SALESMAN`). |
| `GET    /api/v1/roles` | `rate_auth_read` | `roles:view` | List roles. `?is_system=&is_active=`. |
| `GET    /api/v1/roles/{id}` | `rate_auth_read` | `roles:view` | Detail + permission list. |
| `PATCH  /api/v1/roles/{id}` | `rate_auth_write` | `roles:update` | Rename, description, active flag. |
| `DELETE /api/v1/roles/{id}` | `rate_auth_write` | `roles:delete` | Cannot delete if `is_system=true`. |
| `PUT    /api/v1/roles/{id}/permissions` | `rate_auth_write` | `roles:assign` | Full replace of permission set. |
| `POST   /api/v1/roles/{id}/permissions` | `rate_auth_write` | `roles:assign` | Add subset. |
| `DELETE /api/v1/roles/{id}/permissions/{perm_id}` | `rate_auth_write` | `roles:assign` | Remove one. |
| `GET    /api/v1/permissions` | `rate_auth_read` | `permissions:view` | Full permission catalog, grouped by module. |
| `GET    /api/v1/permissions/modules` | `rate_auth_read` | `permissions:view` | Distinct module list. |

---

## 7.4 Organizations

All routes: `..., auth, tenant, rbac, audit`. SUPER_ADMIN only where noted.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET   /api/v1/organizations/current` | `rate_auth_read` | `organizations:view` | Own org (scoped by token). |
| `GET   /api/v1/organizations/{id}` | `rate_auth_read` | `organizations:view` | SUPER_ADMIN. |
| `PATCH /api/v1/organizations/{id}` | `rate_auth_write` | `organizations:update` | Update details. |
| `GET   /api/v1/organizations/{id}/summary` | `rate_auth_read` | `organizations:view` | Branch count, user count, active plan. |

---

## 7.5 Branches

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/branches` | `rate_auth_write` | `branches:create` | Create branch. |
| `GET    /api/v1/branches` | `rate_auth_read` | `branches:view` | List. `?is_active=&city=&q=`. |
| `GET    /api/v1/branches/{id}` | `rate_auth_read` | `branches:view` | Detail. |
| `PATCH  /api/v1/branches/{id}` | `rate_auth_write` | `branches:update` | Partial. |
| `PUT    /api/v1/branches/{id}` | `rate_auth_write` | `branches:update` | Full replace. |
| `DELETE /api/v1/branches/{id}` | `rate_auth_write` | `branches:delete` | Soft-delete. |
| `GET    /api/v1/branches/{id}/users` | `rate_auth_read` | `users:view` | Users at this branch. |
| `GET    /api/v1/branches/{id}/summary` | `rate_auth_read` | `analytics:view` | Today's sales, low stock, expiring. |

---

## 7.6 Warehouses

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/warehouses` | `rate_auth_write` | `warehouses:create` | Create. |
| `GET    /api/v1/warehouses` | `rate_auth_read` | `warehouses:view` | List. `?branch_id=&is_active=&is_main=`. |
| `GET    /api/v1/warehouses/{id}` | `rate_auth_read` | `warehouses:view` | Detail. |
| `PATCH  /api/v1/warehouses/{id}` | `rate_auth_write` | `warehouses:update` | Partial. |
| `DELETE /api/v1/warehouses/{id}` | `rate_auth_write` | `warehouses:delete` | Soft-delete. |
| `GET    /api/v1/branches/{branch_id}/warehouses` | `rate_auth_read` | `warehouses:view` | Nested list. |

---

## 7.7 Master Data

All under `/api/v1/master`. Same CRUD pattern per sub-resource. **Central (not branch-scoped).** Writes restricted to `SUPER_ADMIN`.

| Sub-resource | Methods | Permission |
|---|---|---|
| `/master/product-categories` | `POST, GET, GET/{id}, PATCH/{id}, DELETE/{id}` | `masterdata:create/view/update/delete` |
| `/master/dosage-forms` | same | same |
| `/master/routes` | same | same |
| `/master/package-types` | same | same |
| `/master/unit-types` | same | same |
| `/master/storage-conditions` | same | same |
| `/master/tax-rates` | same | same |
| `/master/drug-groups` | same | same |
| `/master/drug-classes` | same | same |
| `/master/therapeutic-classes` | same | same |
| `/master/generic-medicines` | same | same |

Rates: reads `rate_auth_read`, writes `rate_auth_write`.  
Middlewares: `..., auth, tenant, rbac, audit`.  
Filters: `?is_active=&q=&parent_id=`.

---

## 7.8 Brands

| Method & Path | Rate | Permission |
|---|---|---|
| `POST   /api/v1/brands` | `rate_auth_write` | `brands:create` |
| `GET    /api/v1/brands` | `rate_auth_read` | `brands:view` — `?manufacturer_id=&q=&is_active=` |
| `GET    /api/v1/brands/{id}` | `rate_auth_read` | `brands:view` |
| `PATCH  /api/v1/brands/{id}` | `rate_auth_write` | `brands:update` |
| `DELETE /api/v1/brands/{id}` | `rate_auth_write` | `brands:delete` |

---

## 7.9 Manufacturers

| Method & Path | Rate | Permission |
|---|---|---|
| `POST   /api/v1/manufacturers` | `rate_auth_write` | `manufacturers:create` |
| `GET    /api/v1/manufacturers` | `rate_auth_read` | `manufacturers:view` — `?country=&q=&is_active=` |
| `GET    /api/v1/manufacturers/{id}` | `rate_auth_read` | `manufacturers:view` |
| `PATCH  /api/v1/manufacturers/{id}` | `rate_auth_write` | `manufacturers:update` |
| `DELETE /api/v1/manufacturers/{id}` | `rate_auth_write` | `manufacturers:delete` |

---

## 7.10 Suppliers

| Method & Path | Rate | Permission |
|---|---|---|
| `POST   /api/v1/suppliers` | `rate_auth_write` | `suppliers:create` |
| `GET    /api/v1/suppliers` | `rate_auth_read` | `suppliers:view` — `?q=&city=&is_active=` |
| `GET    /api/v1/suppliers/{id}` | `rate_auth_read` | `suppliers:view` |
| `PATCH  /api/v1/suppliers/{id}` | `rate_auth_write` | `suppliers:update` |
| `DELETE /api/v1/suppliers/{id}` | `rate_auth_write` | `suppliers:delete` |
| `GET    /api/v1/suppliers/{id}/purchases` | `rate_auth_read` | `purchases:view` — nested purchase history |
| `GET    /api/v1/suppliers/{id}/ledger` | `rate_auth_read` | `ledger:view` — payables |

---

## 7.11 Medicines

Catalog is **central**. Writes SUPER_ADMIN only. All branches read.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/medicines` | `rate_auth_write` | `medicines:create` | Create. |
| `GET    /api/v1/medicines` | `rate_auth_read` | `medicines:view` | List. `?category_id=&brand_id=&generic_id=&is_active=&requires_prescription=&is_controlled=&q=`. |
| `GET    /api/v1/medicines/{id}` | `rate_auth_read` | `medicines:view` | Detail with joined master data. |
| `PATCH  /api/v1/medicines/{id}` | `rate_auth_write` | `medicines:update` | Partial. |
| `PUT    /api/v1/medicines/{id}` | `rate_auth_write` | `medicines:update` | Full replace. |
| `DELETE /api/v1/medicines/{id}` | `rate_auth_write` | `medicines:delete` | Soft-delete. |
| `GET    /api/v1/medicines/search?q=` | `rate_search` | `medicines:view` | Trigram search over `name`, `generic_name`, `barcode`, `sku`. |
| `POST   /api/v1/medicines/lookup` | `rate_auth_read` | `medicines:view` | Body: `{barcode\|sku}` for POS lookup. |
| `GET    /api/v1/medicines/{id}/variants` | `rate_auth_read` | `medicines:view` | List variants. |
| `POST   /api/v1/medicines/{id}/variants` | `rate_auth_write` | `medicines:update` | Add variant. |
| `PATCH  /api/v1/medicines/{id}/variants/{variant_id}` | `rate_auth_write` | `medicines:update` | Update variant. |
| `DELETE /api/v1/medicines/{id}/variants/{variant_id}` | `rate_auth_write` | `medicines:update` | Remove variant. |
| `GET    /api/v1/medicines/{id}/barcodes` | `rate_auth_read` | `medicines:view` | |
| `POST   /api/v1/medicines/{id}/barcodes` | `rate_auth_write` | `medicines:update` | |
| `DELETE /api/v1/medicines/{id}/barcodes/{bc_id}` | `rate_auth_write` | `medicines:update` | |
| `GET    /api/v1/medicines/{id}/price-history` | `rate_auth_read` | `medicines:view` | |
| `POST   /api/v1/medicines/import` | `rate_export` | `medicines:import` | Upload CSV/XLSX; async job. |
| `GET    /api/v1/medicines/export?format=csv\|xlsx` | `rate_export` | `medicines:export` | Async job. |

---

## 7.12 Inventory

Branch-scoped by `tenant` middleware. `X-Branch-ID` header can override for SUPER_ADMIN.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET    /api/v1/inventory/batches` | `rate_auth_read` | `inventory:view` | List batches. `?branch_id=&medicine_id=&status=&expiring_before=&low_stock=true&q=`. Cursor pagination. |
| `GET    /api/v1/inventory/batches/{id}` | `rate_auth_read` | `inventory:view` | Detail with movements. |
| `PATCH  /api/v1/inventory/batches/{id}` | `rate_auth_write` | `inventory:adjust` | Update selling price, tax, status. |
| `POST   /api/v1/inventory/batches/{id}/deactivate` | `rate_auth_write` | `inventory:deactivate` | Stop sales for this batch. |
| `POST   /api/v1/inventory/batches/{id}/activate` | `rate_auth_write` | `inventory:adjust` | Re-enable. |
| `POST   /api/v1/inventory/adjust` | `rate_auth_write` | `inventory:adjust` | Manual adjustment (damage, count discrepancy). Requires `Idempotency-Key`. |
| `GET    /api/v1/inventory/stock-movements` | `rate_auth_read` | `inventory:view` | Cursor list. `?batch_id=&medicine_id=&movement_type=&from=&to=`. |
| `GET    /api/v1/inventory/stock-summary` | `rate_auth_read` | `inventory:view` | Per-medicine aggregate for a branch. |
| `GET    /api/v1/inventory/low-stock` | `rate_auth_read` | `inventory:view` | Medicines under `min_stock_threshold`. |
| `GET    /api/v1/inventory/expiring` | `rate_auth_read` | `inventory:view` | `?days=30` default. |
| `POST   /api/v1/inventory/transfers` | `rate_auth_write` | `inventory:transfer` | Create branch-to-branch transfer (draft). |
| `GET    /api/v1/inventory/transfers` | `rate_auth_read` | `inventory:view` | List. `?status=&from_branch_id=&to_branch_id=`. |
| `GET    /api/v1/inventory/transfers/{id}` | `rate_auth_read` | `inventory:view` | Detail. |
| `POST   /api/v1/inventory/transfers/{id}/dispatch` | `rate_auth_write` | `inventory:transfer` | Mark dispatched (decrements source). |
| `POST   /api/v1/inventory/transfers/{id}/receive` | `rate_auth_write` | `inventory:transfer` | Mark received (increments destination). |
| `POST   /api/v1/inventory/transfers/{id}/cancel` | `rate_auth_write` | `inventory:transfer` | Cancel (only if not dispatched). |
| `GET    /api/v1/inventory/export?format=csv\|xlsx` | `rate_export` | `inventory:export` | Async job. |

---

## 7.13 Purchases

State machine: `draft → pending_approval → approved → dispatched → partially_received → received → completed`. Cancellable from any state before `received`.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/purchases` | `rate_auth_write` | `purchases:create` | Create draft. Body includes items[]. Requires `Idempotency-Key`. |
| `GET    /api/v1/purchases` | `rate_auth_read` | `purchases:view` | List. `?status=&payment_status=&supplier_id=&branch_id=&from=&to=&q=`. |
| `GET    /api/v1/purchases/{id}` | `rate_auth_read` | `purchases:view` | Detail with items, payments, receipts. |
| `PATCH  /api/v1/purchases/{id}` | `rate_auth_write` | `purchases:update` | Only when `status=draft`. |
| `DELETE /api/v1/purchases/{id}` | `rate_auth_write` | `purchases:delete` | Only when `status=draft`. |
| `POST   /api/v1/purchases/{id}/submit` | `rate_auth_write` | `purchases:submit` | `draft → pending_approval`. |
| `POST   /api/v1/purchases/{id}/approve` | `rate_auth_write` | `purchases:approve` | SUPER_ADMIN/ADMIN/OWNER. |
| `POST   /api/v1/purchases/{id}/reject` | `rate_auth_write` | `purchases:reject` | Requires `reason`. |
| `POST   /api/v1/purchases/{id}/receive` | `rate_auth_write` | `purchases:receive` | Create InventoryBatches. Full or partial. |
| `POST   /api/v1/purchases/{id}/cancel` | `rate_auth_write` | `purchases:cancel` | Compensating action. |
| `GET    /api/v1/purchases/{id}/items` | `rate_auth_read` | `purchases:view` | Line items. |
| `GET    /api/v1/purchases/{id}/receipts` | `rate_auth_read` | `purchases:view` | Delivery receipts. |
| `GET    /api/v1/purchases/{id}/audit-trail` | `rate_auth_read` | `audit:view` | State transitions. |
| `GET    /api/v1/purchases/export?format=csv\|xlsx` | `rate_export` | `purchases:export` | Async job. |

---

## 7.14 Purchase Payments

Nested under purchases.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/purchases/{id}/payments` | `rate_auth_write` | `purchase_payments:create` | Add payment. Requires `Idempotency-Key`. |
| `GET    /api/v1/purchases/{id}/payments` | `rate_auth_read` | `purchase_payments:view` | List. |
| `GET    /api/v1/purchase-payments/{payment_id}` | `rate_auth_read` | `purchase_payments:view` | Direct access. |
| `PATCH  /api/v1/purchase-payments/{payment_id}` | `rate_auth_write` | `purchase_payments:update` | Same-day only. |
| `DELETE /api/v1/purchase-payments/{payment_id}` | `rate_auth_write` | `purchase_payments:delete` | Reverses ledger entry. |

---

## 7.15 Sales (POS)

Hot path. Cursor pagination on lists. Tight rate limits on checkout.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/sales/pos/checkout` | `rate_pos_checkout` | `sales:create` | Atomic sale. Decrements stock (FEFO), posts ledger, generates invoice. **Requires `Idempotency-Key`**. |
| `POST   /api/v1/sales/pos/lookup` | `rate_auth_read` | `medicines:view` | Body: `{barcode\|sku\|name}`. Returns medicine + active batches sorted FEFO. |
| `POST   /api/v1/sales/pos/apply-coupon` | `rate_auth_read` | `sales:create` | Preview discount for cart. |
| `POST   /api/v1/sales/pos/calculate` | `rate_auth_read` | `sales:create` | Preview totals (tax, discount, coupon) without persisting. |
| `POST   /api/v1/sales` | `rate_auth_write` | `sales:create` | Non-POS sale (wholesale). |
| `GET    /api/v1/sales` | `rate_auth_read` | `sales:view` | Cursor list. `?branch_id=&cashier_id=&customer_id=&status=&payment_status=&from=&to=&q=invoice`. |
| `GET    /api/v1/sales/{id}` | `rate_auth_read` | `sales:view` | Detail + items + payments. |
| `PATCH  /api/v1/sales/{id}` | `rate_auth_write` | `sales:update` | Same-day, cashier-only, no stock change. |
| `POST   /api/v1/sales/{id}/void` | `rate_auth_write` | `sales:void` | Same-day cancel. Reverses stock + ledger. |
| `POST   /api/v1/sales/{id}/refund` | `rate_auth_write` | `sales:refund` | Money-back without restocking. |
| `GET    /api/v1/sales/{id}/invoice` | `rate_auth_read` | `sales:view` | Formatted invoice payload. |
| `GET    /api/v1/sales/{id}/invoice/pdf` | `rate_auth_read` | `sales:view` | PDF stream. |
| `POST   /api/v1/sales/{id}/payments` | `rate_auth_write` | `sales:update` | Add payment (partial/credit sale). |
| `GET    /api/v1/sales/{id}/payments` | `rate_auth_read` | `sales:view` | |
| `GET    /api/v1/sales/export?format=csv\|xlsx` | `rate_export` | `sales:export` | Async job. |

---

## 7.16 Sales Returns

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/sales/{sale_id}/returns` | `rate_auth_write` | `sales_returns:create` | Create return (pending). Requires `Idempotency-Key`. |
| `GET    /api/v1/sales/{sale_id}/returns` | `rate_auth_read` | `sales_returns:view` | Returns for a sale. |
| `GET    /api/v1/sales-returns` | `rate_auth_read` | `sales_returns:view` | Global list. `?status=&from=&to=&branch_id=`. |
| `GET    /api/v1/sales-returns/{id}` | `rate_auth_read` | `sales_returns:view` | Detail. |
| `POST   /api/v1/sales-returns/{id}/approve` | `rate_auth_write` | `sales_returns:approve` | Restocks if `restockable=true`. |
| `POST   /api/v1/sales-returns/{id}/reject` | `rate_auth_write` | `sales_returns:reject` | Requires reason. |

---

## 7.17 Customers

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/customers` | `rate_auth_write` | `customers:create` | Create. |
| `GET    /api/v1/customers` | `rate_auth_read` | `customers:view` | List. `?q=phone\|name&is_active=&min_due=`. |
| `GET    /api/v1/customers/{id}` | `rate_auth_read` | `customers:view` | Detail. |
| `PATCH  /api/v1/customers/{id}` | `rate_auth_write` | `customers:update` | Partial. |
| `DELETE /api/v1/customers/{id}` | `rate_auth_write` | `customers:delete` | Soft-delete. |
| `GET    /api/v1/customers/{id}/sales` | `rate_auth_read` | `sales:view` | Purchase history. |
| `GET    /api/v1/customers/{id}/ledger` | `rate_auth_read` | `ledger:view` | Credit account. |
| `GET    /api/v1/customers/export?format=csv\|xlsx` | `rate_export` | `customers:export` | Async job. |

---

## 7.18 Coupons & Offers

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/coupons` | `rate_auth_write` | `coupons:create` | Create. |
| `GET    /api/v1/coupons` | `rate_auth_read` | `coupons:view` | List. `?is_active=&valid_now=true&q=`. |
| `GET    /api/v1/coupons/{id}` | `rate_auth_read` | `coupons:view` | Detail. |
| `PATCH  /api/v1/coupons/{id}` | `rate_auth_write` | `coupons:update` | Update. |
| `DELETE /api/v1/coupons/{id}` | `rate_auth_write` | `coupons:delete` | Soft-delete. |
| `POST   /api/v1/coupons/validate` | `rate_auth_read` | `sales:create` | Body: `{code, subtotal, customer_id?}`. Preview only. |
| `GET    /api/v1/coupons/{id}/redemptions` | `rate_auth_read` | `coupons:view` | Redemption history. |
| `POST   /api/v1/offers` | `rate_auth_write` | `coupons:create` | Create offer. |
| `GET    /api/v1/offers` | `rate_auth_read` | `coupons:view` | List. |
| `PATCH  /api/v1/offers/{id}` | `rate_auth_write` | `coupons:update` | |
| `DELETE /api/v1/offers/{id}` | `rate_auth_write` | `coupons:delete` | |

---

## 7.19 Ledger & Finance

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET    /api/v1/ledger/accounts` | `rate_auth_read` | `ledger:view` | Chart of Accounts tree. |
| `POST   /api/v1/ledger/accounts` | `rate_auth_write` | `ledger:post` | Add account (non-system). |
| `PATCH  /api/v1/ledger/accounts/{id}` | `rate_auth_write` | `ledger:post` | Update. |
| `GET    /api/v1/ledger/accounts/{id}/balance` | `rate_auth_read` | `ledger:view` | `?period_year=&period_month=&branch_id=`. |
| `GET    /api/v1/ledger/journals` | `rate_auth_read` | `ledger:view` | Cursor list. `?from=&to=&source_module=&source_id=&status=`. |
| `POST   /api/v1/ledger/journals` | `rate_auth_write` | `ledger:post` | Manual journal entry. Balanced DR/CR. |
| `GET    /api/v1/ledger/journals/{id}` | `rate_auth_read` | `ledger:view` | With lines. |
| `POST   /api/v1/ledger/journals/{id}/reverse` | `rate_auth_write` | `ledger:post` | Create reversing entry. |
| `GET    /api/v1/ledger/reports/trial-balance` | `rate_auth_read` | `ledger:view` | `?as_of=&branch_id=`. |
| `GET    /api/v1/ledger/reports/profit-loss` | `rate_auth_read` | `ledger:view` | `?from=&to=&branch_id=`. |
| `GET    /api/v1/ledger/reports/balance-sheet` | `rate_auth_read` | `ledger:view` | `?as_of=`. |
| `GET    /api/v1/ledger/reports/cash-flow` | `rate_auth_read` | `ledger:view` | `?from=&to=`. |
| `GET    /api/v1/ledger/reports/general-ledger` | `rate_auth_read` | `ledger:view` | `?account_id=&from=&to=`. |
| `GET    /api/v1/ledger/export?report=<name>&format=csv\|xlsx\|pdf` | `rate_export` | `ledger:export` | Async job. |

---

## 7.20 Targets

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/targets` | `rate_auth_write` | `targets:create` | Create target (org or branch scope). |
| `GET    /api/v1/targets` | `rate_auth_read` | `targets:view` | List. `?scope=&branch_id=&period_year=&period_month=`. |
| `GET    /api/v1/targets/{id}` | `rate_auth_read` | `targets:view` | Detail + progress. |
| `PATCH  /api/v1/targets/{id}` | `rate_auth_write` | `targets:update` | Update. |
| `DELETE /api/v1/targets/{id}` | `rate_auth_write` | `targets:delete` | Soft-delete. |
| `GET    /api/v1/targets/{id}/progress` | `rate_auth_read` | `targets:view` | Progress time series. |

---

## 7.21 Reports

Async pattern. Client polls or receives WebSocket event when ready.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST   /api/v1/reports/generate` | `rate_export` | `reports:export` | Body: `{type, filters, format}`. Returns `{job_id}`. |
| `GET    /api/v1/reports/jobs/{job_id}` | `rate_auth_read` | `reports:view` | Job status: `queued/running/success/failed`. |
| `GET    /api/v1/reports/jobs/{job_id}/download` | `rate_auth_read` | `reports:view` | Download artifact when ready. |
| `POST   /api/v1/reports/schedule` | `rate_auth_write` | `reports:schedule` | Recurring report definition. |
| `GET    /api/v1/reports/scheduled` | `rate_auth_read` | `reports:view` | List. |
| `DELETE /api/v1/reports/scheduled/{id}` | `rate_auth_write` | `reports:schedule` | Cancel. |
| **Report types** | | | `sales`, `inventory`, `purchase`, `sales_returns`, `low_stock`, `expiring`, `audit`, `ledger_pl`, `ledger_balance_sheet`, `customer`, `supplier`, `top_products`. |

---

## 7.22 Analytics

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET /api/v1/analytics/dashboard` | `rate_auth_read` | `analytics:view` | Today snapshot. `?branch_id=`. |
| `GET /api/v1/analytics/sales/overview` | `rate_auth_read` | `analytics:view` | `?from=&to=&group_by=day\|week\|month&branch_id=`. |
| `GET /api/v1/analytics/sales/by-branch` | `rate_auth_read` | `analytics:view` | Comparison across branches. SUPER_ADMIN. |
| `GET /api/v1/analytics/sales/by-category` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/sales/by-medicine` | `rate_auth_read` | `analytics:view` | `?limit=20` top-N. |
| `GET /api/v1/analytics/sales/by-cashier` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/sales/by-hour` | `rate_auth_read` | `analytics:view` | Peak hours. |
| `GET /api/v1/analytics/inventory/turnover` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/inventory/valuation` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/inventory/aging` | `rate_auth_read` | `analytics:view` | Batch age buckets. |
| `GET /api/v1/analytics/customers/top` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/products/top` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/products/slow-moving` | `rate_auth_read` | `analytics:view` | |
| `GET /api/v1/analytics/profitability` | `rate_auth_read` | `analytics:view` | Margin by product/branch. |
| `GET /api/v1/analytics/export?report=<name>&format=csv\|xlsx` | `rate_export` | `analytics:export` | Async job. |

---

## 7.23 AI Forecasting

**SUPER_ADMIN only** (`super_admin` middleware). Cached in DB; second call within TTL returns cached.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST /api/v1/ai/forecast/demand` | `rate_ai` | `ai:forecast` | Body: `{from, to, horizon_days, medicine_id?, branch_id?}`. Min window: 30 days history. |
| `POST /api/v1/ai/forecast/restock` | `rate_ai` | `ai:forecast` | Restock suggestion per branch. |
| `POST /api/v1/ai/forecast/business` | `rate_ai` | `ai:forecast` | Overall business summary + recommendation. |
| `POST /api/v1/ai/forecast/product-mix` | `rate_ai` | `ai:forecast` | Which categories to push. |
| `GET  /api/v1/ai/forecasts` | `rate_auth_read` | `ai:view` | Historical forecasts. `?type=&from=&to=&medicine_id=&branch_id=`. |
| `GET  /api/v1/ai/forecasts/{id}` | `rate_auth_read` | `ai:view` | Detail with actual-vs-predicted. |

---

## 7.24 Notifications

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET    /api/v1/notifications` | `rate_auth_read` | `notifications:view` | Own inbox. `?is_read=&type=&priority=&from=&to=`. |
| `GET    /api/v1/notifications/unread-count` | `rate_auth_read` | `notifications:view` | Badge. |
| `POST   /api/v1/notifications/{id}/read` | `rate_auth_write` | `notifications:read` | Mark read. |
| `POST   /api/v1/notifications/read-all` | `rate_auth_write` | `notifications:read` | Mark all read. |
| `DELETE /api/v1/notifications/{id}` | `rate_auth_write` | `notifications:dismiss` | Dismiss. |
| `POST   /api/v1/notifications/broadcast` | `rate_broadcast` | `notifications:broadcast` | Body: `{scope: org\|branch\|role\|user, target_id, title, message, priority, action_url}`. |
| `GET    /api/v1/notifications/broadcasts` | `rate_auth_read` | `notifications:view` | Sent broadcasts (admin). |

---

## 7.25 Audit Logs

SUPER_ADMIN and users with `audit:view`. Cursor pagination.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET /api/v1/audit-logs` | `rate_auth_read` | `audit:view` | Filters: `?user_id=&module=&action=&entity_type=&entity_id=&outcome=&from=&to=&ip=&q=`. |
| `GET /api/v1/audit-logs/{id}` | `rate_auth_read` | `audit:view` | Detail with `before_data/after_data`. |
| `GET /api/v1/audit-logs/modules` | `rate_auth_read` | `audit:view` | Distinct module list for dropdowns. |
| `GET /api/v1/audit-logs/actions` | `rate_auth_read` | `audit:view` | Distinct actions. |
| `GET /api/v1/audit-logs/export?format=csv` | `rate_export` | `audit:export` | Async job. |

---

## 7.26 Sessions

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET    /api/v1/sessions/active` | `rate_auth_read` | `sessions:view` | System-wide currently active. SUPER_ADMIN. |
| `GET    /api/v1/sessions` | `rate_auth_read` | `sessions:view` | List. `?user_id=&is_active=&device_type=&country=`. |
| `GET    /api/v1/sessions/{id}` | `rate_auth_read` | `sessions:view` | Detail. |
| `DELETE /api/v1/sessions/{id}` | `rate_auth_write` | `sessions:revoke` | Revoke — immediate logout on that device. |
| `POST   /api/v1/sessions/revoke-user/{user_id}` | `rate_auth_write` | `sessions:revoke` | Revoke all for a user. |

---

## 7.27 Settings

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET    /api/v1/settings` | `rate_auth_read` | `settings:view` | List settings (non-sensitive by default). `?include_sensitive=true` requires SUPER_ADMIN. |
| `GET    /api/v1/settings/{key}` | `rate_auth_read` | `settings:view` | Single. |
| `PUT    /api/v1/settings/{key}` | `rate_auth_write` | `settings:update` | Update value. |
| `POST   /api/v1/settings/bulk` | `rate_auth_write` | `settings:update` | Body: `[{key,value}]`. |

---

## 7.28 Feature Flags

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `GET   /api/v1/feature-flags` | `rate_auth_read` | `feature_flags:view` | List. |
| `GET   /api/v1/feature-flags/{key}` | `rate_auth_read` | `feature_flags:view` | Detail. |
| `PATCH /api/v1/feature-flags/{key}` | `rate_auth_write` | `feature_flags:toggle` | `{enabled, target_scope}`. |

---

## 7.29 Backup

`super_admin` + `mfa_required` on all write routes.

| Method & Path | Rate | Permission | Description |
|---|---|---|---|
| `POST /api/v1/backups/run` | `rate_backup` | `backup:run` | Trigger backup. Body: `{type: full\|incremental}`. |
| `GET  /api/v1/backups` | `rate_auth_read` | `backup:view` | List history. `?status=&type=&from=&to=`. |
| `GET  /api/v1/backups/{id}` | `rate_auth_read` | `backup:view` | Detail. |
| `POST /api/v1/backups/{id}/restore` | `rate_backup` | `backup:restore` | Trigger restore. High-risk; extra confirmation token required. |
| `GET  /api/v1/backups/{id}/download` | `rate_backup` | `backup:view` | Signed URL. |

---

## 7.30 WebSocket & Health

| Method & Path | Middlewares | Rate | Permission | Description |
|---|---|---|---|---|
| `GET /api/v1/ws` (Upgrade) | `req_id, sec_headers, recovery, logger, metrics, tracing, rate_ws_connect, auth, tenant` | `rate_ws_connect` | Authenticated | WebSocket upgrade. Events: `notification.new`, `notification.read`, `session.revoked`, `low_stock`, `purchase.approved`, `sale.completed`, `report.ready`. |
| `GET /healthz` | `req_id, recovery, logger` | none | Public | Liveness. |
| `GET /readyz` | `req_id, recovery, logger` | none | Public | Readiness — checks DB, Redis, worker. |
| `GET /metrics` | `req_id, recovery` (bound to internal listener) | none | Internal only (Prometheus scrape, network-restricted) | Prometheus metrics. |
| `GET /api/v1/openapi.json` | standard chain | `rate_public` | Public | OpenAPI spec. |
| `GET /api/v1/docs` | standard chain | `rate_public` | Public | Swagger UI. |

---

## Appendix A — Idempotency

Any `POST` that creates: sales, purchases, sales returns, purchase payments, sale payments, inventory adjustments, transfers, notifications broadcast — **must** carry:
Idempotency-Key: <uuid v4>
Server stores `(user_id, endpoint, request_hash, response)` for **24 h**. Same key + same body → returns cached response. Same key + different body → `409 IDEMPOTENCY_KEY_CONFLICT`.

## Appendix B — Deprecation policy

Deprecated endpoints return:  
Deprecation: true  
Sunset: Wed, 01 Jul 2026 00:00:00 GMT  
Link: https://docs.medicore.example.com/migrations/v2; rel="deprecation"  
Minimum notice: **180 days** before removal.  

## Appendix C — HTTP status matrix

| Operation | Success | Not Found | Validation | Auth | Forbidden | Conflict | Rate |
|-----------|---------|-----------|-----------|------|-----------|----------|------|
| GET one   | 200 | 404 | 400 | 401 | 403 | — | 429 |
| GET list  | 200 | — | 400 | 401 | 403 | — | 429 |
| POST create | 201 | — | 400/422 | 401 | 403 | 409 | 429 |
| PUT replace | 200 | 404 | 400/422 | 401 | 403 | 409 | 429 |
| PATCH partial | 200 | 404 | 400/422 | 401 | 403 | 409 | 429 |
| DELETE | 204 | 404 | — | 401 | 403 | 409 | 429 |
| Action POST | 200/202 | 404 | 400/422 | 401 | 403 | 409 | 429 |
| Async job POST | 202 | — | 400/422 | 401 | 403 | — | 429 |

## Appendix D — Async response

For long-running actions (imports, exports, backups, AI):  
HTTP/1.1 202 Accepted  
```json
Location: /api/v1/reports/jobs/01H9XZ...  
{  
  "success": true,  
  "data": { "job_id": "01H9XZ...", "status": "queued" }
}
```
Poll `GET /reports/jobs/{id}` or subscribe over WebSocket to `report.ready`.