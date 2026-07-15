```
medicore-erp-backend/
│
├── cmd/
│   ├── api/
│   │   └── main.go                          # HTTP server entrypoint (Gin)
│   ├── worker/
│   │   └── main.go                          # Asynq worker entrypoint
│   ├── seed/
│   │   └── main.go                          # DB seeder (SUPER_ADMIN, permissions, master data)
│   └── migrate/
│       └── main.go                          # golang-migrate runner (up/down/force/version)
│
├── internal/
│   │
│   ├── modules/                             # Domain-driven, each module is self-contained
│   │   │
│   │   ├── auth/
│   │   │   ├── model.go                     # Session, RefreshToken, LoginAttempt structs
│   │   │   ├── dto.go                       # LoginRequest, TokenResponse, RefreshRequest, LogoutRequest
│   │   │   ├── repository.go                # session store, refresh-token store, attempt counter (Postgres + Redis)
│   │   │   ├── service.go                   # login, refresh (rotation + reuse-detect), logout, logout-all
│   │   │   ├── handler.go                   # HTTP handlers, cookie set/clear
│   │   │   ├── routes.go                    # /auth/login, /auth/refresh, /auth/logout, /auth/sessions
│   │   │   ├── jwt.go                       # generate/parse (HS256 or RS256), claims struct
│   │   │   ├── password.go                  # Argon2id hash/verify wrapper
│   │   │   ├── limiter.go                   # per-email + per-IP + per-device Redis counters
│   │   │   └── auth_test.go
│   │   │
│   │   ├── user/
│   │   │   ├── model.go                     # User, UserProfile, UserEmployment, UserAddress, EmergencyContact,
│   │   │   │                                # Education, Experience, UserDocument, UserBankAccount
│   │   │   ├── dto.go                       # CreateUserRequest, UpdateUserRequest, UserResponse, ListUsersQuery,
│   │   │   │                                # ChangePasswordRequest, StatusChangeRequest
│   │   │   ├── repository.go                # CRUD, list with filters, status transitions
│   │   │   ├── service.go                   # business rules: only SUPER_ADMIN creates, status change side-effects
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /users, /users/:id, /users/:id/status, /users/me
│   │   │   └── user_test.go
│   │   │
│   │   ├── rbac/
│   │   │   ├── model.go                     # Role, Permission, RolePermission
│   │   │   ├── dto.go                       # CreateRoleRequest, AssignPermissionsRequest, RoleResponse
│   │   │   ├── repository.go                # role/permission CRUD, role-permission map
│   │   │   ├── service.go                   # dynamic role creation, permission assignment, casbin policy sync
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /roles, /permissions, /roles/:id/permissions
│   │   │   ├── casbin.go                    # enforcer init, gorm-adapter wiring, policy loader
│   │   │   ├── seed.go                      # module-local seed helper (default permissions catalog)
│   │   │   └── rbac_test.go
│   │   │
│   │   ├── organization/
│   │   │   ├── model.go                     # Organization
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /organizations, /organizations/:id
│   │   │   └── organization_test.go
│   │   │
│   │   ├── branch/
│   │   │   ├── model.go                     # Branch
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /branches, /branches/:id
│   │   │   └── branch_test.go
│   │   │
│   │   ├── warehouse/
│   │   │   ├── model.go                     # Warehouse
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /warehouses, /branches/:id/warehouses
│   │   │   └── warehouse_test.go
│   │   │
│   │   ├── masterdata/
│   │   │   ├── model.go                     # Category, DosageForm, Route, PackageType, UnitType,
│   │   │   │                                # Manufacturer, TherapeuticClass, DrugClass, StorageCondition,
│   │   │   │                                # Generic, TaxRate
│   │   │   ├── dto.go
│   │   │   ├── repository.go                # generic + typed queries per master table
│   │   │   ├── service.go                   # central-only writes, cached reads
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /master/categories, /master/dosage-forms, /master/routes, ...
│   │   │   ├── seed.go                      # seed defaults for each master table
│   │   │   └── masterdata_test.go
│   │   │
│   │   ├── brand/
│   │   │   ├── model.go                     # Brand
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /brands, /brands/:id
│   │   │   └── brand_test.go
│   │   │
│   │   ├── supplier/
│   │   │   ├── model.go                     # Supplier
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /suppliers, /suppliers/:id
│   │   │   └── supplier_test.go
│   │   │
│   │   ├── medicine/
│   │   │   ├── model.go                     # Medicine, Variant, MedicineImportDetail, Barcode
│   │   │   ├── dto.go                       # CreateMedicineRequest, VariantRequest, ImportDetailRequest,
│   │   │   │                                # SearchMedicineQuery
│   │   │   ├── repository.go                # full-text/trigram search, filters, joins to master tables
│   │   │   ├── service.go                   # central-managed lifecycle (create/update/deactivate)
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /medicines, /medicines/:id, /medicines/search, /medicines/:id/variants
│   │   │   └── medicine_test.go
│   │   │
│   │   ├── customer/
│   │   │   ├── model.go                     # Customer
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /customers, /customers/:id
│   │   │   └── customer_test.go
│   │   │
│   │   ├── inventory/
│   │   │   ├── model.go                     # InventoryBatch, StockMovement, StockTransfer, TransferItem
│   │   │   ├── dto.go                       # BatchQuery, TransferRequest, StockAdjustmentRequest
│   │   │   ├── repository.go                # FEFO queries, movement ledger writes, transfer records
│   │   │   ├── service.go                   # reserve stock, commit/release, transfer between branches, adjust
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /inventory/batches, /inventory/transfers, /inventory/adjust
│   │   │   ├── fefo.go                      # First-Expiry-First-Out selector logic
│   │   │   └── inventory_test.go
│   │   │
│   │   ├── purchase/
│   │   │   ├── model.go                     # Purchase, PurchaseItem, PurchaseApproval, PurchasePayment,
│   │   │   │                                # PurchaseReceipt
│   │   │   ├── dto.go                       # CreatePurchaseRequest, ApprovalRequest, ReceiveRequest,
│   │   │   │                                # PaymentRequest
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # state machine: DRAFT → PENDING_APPROVAL → APPROVED → RECEIVED →
│   │   │   │                                # PAID / PARTIALLY_PAID / COMPLETED / CANCELLED
│   │   │   ├── state_machine.go             # explicit transition table + guards
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /purchases, /purchases/:id/approve, /purchases/:id/receive,
│   │   │   │                                # /purchases/:id/pay, /purchases/:id/cancel
│   │   │   └── purchase_test.go
│   │   │
│   │   ├── sale/
│   │   │   ├── model.go                     # Sale, SaleItem, SalesReturn, ReturnItem, Coupon, CouponRedemption
│   │   │   ├── dto.go                       # POSCheckoutRequest, LineItemDTO, ReturnRequest, CouponApplyRequest
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # POS transaction, stock decrement (FEFO), pricing, tax, coupon
│   │   │   ├── pricing.go                   # tax/vat/demand-charge %-or-flat calculator, coupon rules
│   │   │   ├── invoice.go                   # invoice number generator (monotonic per branch)
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /sales, /sales/:id, /sales/:id/return, /sales/pos/checkout,
│   │   │   │                                # /sales/pos/lookup
│   │   │   └── sale_test.go
│   │   │
│   │   ├── ledger/
│   │   │   ├── model.go                     # Account, JournalEntry, JournalLine, FiscalPeriod, Target
│   │   │   ├── dto.go                       # PostEntryRequest, LedgerQuery, TargetRequest, TrialBalanceQuery
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # double-entry post, target set/track, close period, P&L, balance sheet
│   │   │   ├── auto_post.go                 # event-to-journal mapping (sale → DR cash / CR revenue, etc.)
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /ledger/accounts, /ledger/journal, /ledger/reports/pl,
│   │   │   │                                # /ledger/reports/balance-sheet, /ledger/targets
│   │   │   ├── seed.go                      # default Chart of Accounts seeder
│   │   │   └── ledger_test.go
│   │   │
│   │   ├── notification/
│   │   │   ├── model.go                     # Notification, NotificationTemplate, DeliveryLog
│   │   │   ├── dto.go                       # SendNotificationRequest, ListQuery
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # create + fan-out (in-app + WebSocket + future channels)
│   │   │   ├── publisher.go                 # Redis pub/sub publisher
│   │   │   ├── ws_hub.go                    # WebSocket hub: register/unregister, broadcast, per-user rooms
│   │   │   ├── ws_client.go                 # per-connection client (read/write pumps)
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /notifications, /notifications/:id/read, /ws
│   │   │   └── notification_test.go
│   │   │
│   │   ├── audit/
│   │   │   ├── model.go                     # AuditLog (partitioned monthly)
│   │   │   ├── dto.go                       # AuditQuery (filters: user, module, action, ip, date range)
│   │   │   ├── repository.go                # append-only writes, partition-aware reads
│   │   │   ├── service.go                   # async enqueue via Asynq
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /audit-logs (SUPER_ADMIN only), /audit-logs/export
│   │   │   └── audit_test.go
│   │   │
│   │   ├── analytics/
│   │   │   ├── model.go                     # MaterializedView struct wrappers, DailySalesSummary,
│   │   │   │                                # BranchKPI, ProductRanking
│   │   │   ├── dto.go                       # DashboardQuery, SalesFilterQuery, BranchAnalyticsQuery
│   │   │   ├── repository.go                # heavy aggregate queries, cached reads
│   │   │   ├── service.go                   # dashboard, branch-wise, product-wise, top-N, trend
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /analytics/dashboard, /analytics/sales, /analytics/branches,
│   │   │   │                                # /analytics/products, /analytics/inventory
│   │   │   └── analytics_test.go
│   │   │
│   │   ├── ai/
│   │   │   ├── model.go                     # Forecast, ForecastRun, RestockSuggestion
│   │   │   ├── dto.go                       # ForecastRequest (date range, medicine, branch), ForecastResponse
│   │   │   ├── repository.go                # cached forecasts, run history
│   │   │   ├── service.go                   # demand forecast, restock suggestion, business summary
│   │   │   ├── client.go                    # third-party API client (OpenAI/Anthropic/local)
│   │   │   ├── feature_builder.go           # aggregate sales windows into feature payload
│   │   │   ├── circuit_breaker.go           # provider failure isolation
│   │   │   ├── prompt_templates.go          # versioned prompts (no code = plain text .tmpl if preferred)
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /ai/forecast/demand, /ai/forecast/restock, /ai/summary
│   │   │   └── ai_test.go
│   │   │
│   │   ├── settings/
│   │   │   ├── model.go                     # SystemSetting, FeatureFlag
│   │   │   ├── dto.go
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # typed getters, cache invalidation on write
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /settings, /settings/:key, /feature-flags
│   │   │   └── settings_test.go
│   │   │
│   │   ├── report/
│   │   │   ├── model.go                     # ReportJob, ReportArtifact
│   │   │   ├── dto.go                       # GenerateReportRequest (type, filters, format)
│   │   │   ├── repository.go
│   │   │   ├── service.go                   # enqueue report generation, fetch artifact URL
│   │   │   ├── handler.go
│   │   │   ├── routes.go                    # /reports/sales, /reports/inventory, /reports/purchase,
│   │   │   │                                # /reports/audit, /reports/ledger, /reports/:id/download
│   │   │   └── report_test.go
│   │   │
│   │   └── backup/
│   │       ├── model.go                     # BackupLog
│   │       ├── dto.go
│   │       ├── repository.go
│   │       ├── service.go                   # trigger pg_dump job, list history, restore metadata
│   │       ├── handler.go
│   │       ├── routes.go                    # /backups, /backups/:id
│   │       └── backup_test.go
│   │
│   ├── platform/                            # Cross-cutting infrastructure (no domain logic)
│   │   ├── config/
│   │   │   ├── config.go                    # Viper loader (env + YAML)
│   │   │   ├── types.go                     # typed Config struct
│   │   │   └── validate.go                  # required-field checks on boot
│   │   ├── db/
│   │   │   ├── postgres.go                  # GORM open, pool tuning, health check
│   │   │   ├── pgx.go                       # native PGX pool for hot paths (POS, analytics)
│   │   │   ├── transaction.go               # WithTx helper, saved-point / nested tx
│   │   │   ├── hooks.go                     # slow-query, tenant-scope, soft-delete guards
│   │   │   └── migrate.go                   # embed & apply migrations at startup (optional)
│   │   ├── redis/
│   │   │   ├── client.go                    # go-redis client, health check
│   │   │   ├── keys.go                      # centralized key constants
│   │   │   ├── locker.go                    # distributed lock (redsync)
│   │   │   └── pubsub.go                    # publisher/subscriber wrappers
│   │   ├── logger/
│   │   │   └── zap.go                       # Zap production/dev init, request-scoped logger
│   │   ├── telemetry/
│   │   │   ├── metrics.go                   # Prometheus registry, common metrics
│   │   │   ├── tracing.go                   # OpenTelemetry init, exporter wiring
│   │   │   └── health.go                    # /health, /ready endpoints
│   │   ├── mailer/
│   │   │   └── mailer.go                    # future-channel placeholder (interface only)
│   │   ├── storage/
│   │   │   └── file_store.go                # local disk now, S3-compatible interface for later
│   │   ├── validator/
│   │   │   ├── validator.go                 # go-playground/validator init
│   │   │   └── custom_rules.go              # phone, NID, batch-no, strength format
│   │   └── uuid/
│   │       └── uuid.go                      # generation helpers, parse guards
│   │
│   ├── middleware/
│   │   ├── auth.go                          # JWT + session validation, sets user in context
│   │   ├── rbac.go                          # Casbin enforce per route
│   │   ├── tenant.go                        # inject org_id + branch_id scope into context
│   │   ├── audit.go                         # capture request/response for audit log
│   │   ├── rate_limit.go                    # Redis token-bucket per route family
│   │   ├── recovery.go                      # panic recovery to 500 + stack log
│   │   ├── request_id.go                    # generate/propagate X-Request-ID
│   │   ├── security_headers.go              # HSTS, X-Frame-Options, CSP, etc.
│   │   ├── cors.go                          # CORS allowlist
│   │   ├── idempotency.go                   # Idempotency-Key handling for POST
│   │   ├── logger.go                        # structured access log
│   │   └── timeout.go                       # per-route context timeout
│   │
│   ├── router/
│   │   ├── router.go                        # top-level RegisterRoutes, version groups
│   │   ├── v1.go                            # /api/v1 group wiring
│   │   ├── v2.go                            # reserved for future
│   │   └── health.go                        # /health, /metrics, /ready
│   │
│   ├── jobs/                                # Background worker layer (Asynq)
│   │   ├── server.go                        # Asynq server bootstrap
│   │   ├── client.go                        # enqueue helpers
│   │   ├── scheduler.go                     # cron entries (expiry alerts, backups, forecasts)
│   │   ├── task_types.go                    # task-type constants + payload structs
│   │   ├── handlers/
│   │   │   ├── audit_task.go                # persist audit log entries
│   │   │   ├── notification_task.go         # fan-out notifications
│   │   │   ├── expiry_scan_task.go          # daily expiry / low-stock scanner
│   │   │   ├── report_task.go               # generate PDF/XLSX reports
│   │   │   ├── ai_forecast_task.go          # nightly forecast refresh
│   │   │   ├── ledger_post_task.go          # async double-entry post
│   │   │   ├── backup_task.go               # pg_dump trigger
│   │   │   └── cleanup_task.go              # session cleanup, expired tokens
│   │   └── middleware.go                    # retry policy, dead-letter, logging
│   │
│   ├── errors/
│   │   ├── errors.go                        # AppError type, code taxonomy
│   │   ├── codes.go                         # ErrCodeNotFound, ErrCodeConflict, ErrCodeForbidden, ...
│   │   └── mapper.go                        # AppError → HTTP status mapping
│   │
│   └── common/
│       ├── context/
│       │   └── context.go                   # typed context keys (user_id, org_id, branch_id, request_id)
│       ├── constants/
│       │   └── constants.go                 # user statuses, purchase states, sale statuses, roles
│       └── enums/
│           └── enums.go                     # PaymentType, PurchaseState, SaleState, UserStatus, UserStage
│
├── pkg/                                     # Reusable, safe to extract to public repo later
│   ├── pagination/
│   │   ├── pagination.go                    # offset + cursor pagination helpers
│   │   └── filter.go                        # query string → filter struct
│   ├── response/
│   │   ├── response.go                      # standard envelope: data, meta, error
│   │   └── error_response.go
│   ├── crypto/
│   │   ├── aes.go                           # field-level encryption (NID, bank account)
│   │   └── hash.go                          # HMAC helpers
│   ├── times/
│   │   └── times.go                         # timezone, date range, ISO parsing
│   ├── strings/
│   │   └── strings.go                       # slug, mask, normalize
│   └── httpclient/
│       └── client.go                        # retry + timeout wrapper for outbound calls
│
├── migrations/                              # golang-migrate SQL files (versioned)
│   ├── 000001_init_extensions.up.sql
│   ├── 000001_init_extensions.down.sql
│   ├── 000002_organization_branch_warehouse.up.sql
│   ├── 000002_organization_branch_warehouse.down.sql
│   ├── 000003_rbac_tables.up.sql
│   ├── 000003_rbac_tables.down.sql
│   ├── 000004_user_tables.up.sql
│   ├── 000004_user_tables.down.sql
│   ├── 000005_master_data.up.sql
│   ├── 000005_master_data.down.sql
│   ├── 000006_medicine_brand_supplier.up.sql
│   ├── 000006_medicine_brand_supplier.down.sql
│   ├── 000007_inventory_batch.up.sql
│   ├── 000007_inventory_batch.down.sql
│   ├── 000008_purchase.up.sql
│   ├── 000008_purchase.down.sql
│   ├── 000009_sale_customer.up.sql
│   ├── 000009_sale_customer.down.sql
│   ├── 000010_ledger.up.sql
│   ├── 000010_ledger.down.sql
│   ├── 000011_audit_partitioned.up.sql
│   ├── 000011_audit_partitioned.down.sql
│   ├── 000012_notification_settings.up.sql
│   ├── 000012_notification_settings.down.sql
│   ├── 000013_ai_forecast.up.sql
│   ├── 000013_ai_forecast.down.sql
│   ├── 000014_indexes_and_views.up.sql
│   └── 000014_indexes_and_views.down.sql
│
├── seed/                                    # Declarative seed data (loaded by cmd/seed)
│   ├── permissions.yaml                     # module + action catalog
│   ├── roles.yaml                           # SUPER_ADMIN, ADMIN, MANAGER, PHARMACIST, CASHIER, ACCOUNTANT
│   ├── super_admin.yaml                     # default SUPER_ADMIN user (password from env on first boot)
│   ├── categories.yaml
│   ├── dosage_forms.yaml
│   ├── routes.yaml
│   ├── package_types.yaml
│   ├── unit_types.yaml
│   ├── storage_conditions.yaml
│   ├── chart_of_accounts.yaml
│   └── system_settings.yaml
│
├── deployments/
│   ├── docker/
│   │   ├── api.Dockerfile                   # multi-stage build for API
│   │   ├── worker.Dockerfile                # multi-stage build for worker
│   │   ├── docker-compose.yml               # api + worker + postgres + redis + nginx + prometheus + grafana
│   │   ├── docker-compose.override.yml      # local dev overrides (volumes, hot-reload)
│   │   └── .dockerignore
│   ├── nginx/
│   │   ├── nginx.conf                       # reverse proxy, gzip, rate limit, TLS termination stub
│   │   └── conf.d/
│   │       └── medicore.conf
│   ├── prometheus/
│   │   ├── prometheus.yml
│   │   └── alerts.yml
│   └── grafana/
│       ├── dashboards/
│       │   ├── api.json
│       │   ├── db.json
│       │   └── business.json
│       └── provisioning/
│           ├── datasources.yml
│           └── dashboards.yml
│
├── docs/
│   ├── adr/                                 # Architecture Decision Records
│   │   ├── 0001-modular-monolith.md
│   │   ├── 0002-gorm-plus-pgx-hybrid.md
│   │   ├── 0003-argon2id-over-bcrypt.md
│   │   ├── 0004-casbin-rbac.md
│   │   ├── 0005-asynq-background-jobs.md
│   │   ├── 0006-audit-log-partitioning.md
│   │   ├── 0007-double-entry-ledger.md
│   │   └── 0008-jwt-rotation-reuse-detect.md
│   ├── api/
│   │   ├── openapi.yaml                     # single source of truth for REST API
│   │   └── postman_collection.json
│   ├── diagrams/
│   │   ├── architecture.png
│   │   ├── er_diagram.png
│   │   ├── purchase_state_machine.png
│   │   ├── pos_flow.png
│   │   └── auth_refresh_flow.png
│   ├── runbook/
│   │   ├── incident_response.md
│   │   ├── db_backup_restore.md
│   │   └── deployment.md
│   └── README.md
│
├── tests/
│   ├── integration/
│   │   ├── auth_test.go
│   │   ├── purchase_flow_test.go
│   │   ├── sale_pos_test.go
│   │   ├── inventory_transfer_test.go
│   │   ├── ledger_posting_test.go
│   │   └── testcontainers.go                # Postgres + Redis container setup
│   ├── load/
│   │   ├── pos_checkout.js                  # k6 script
│   │   ├── analytics_dashboard.js
│   │   └── auth_burst.js
│   ├── fixtures/
│   │   ├── users.json
│   │   ├── medicines.json
│   │   └── batches.json
│   └── mocks/
│       ├── ai_client_mock.go
│       └── notification_mock.go
│
├── scripts/
│   ├── dev_up.sh                            # docker-compose up + wait for healthy
│   ├── dev_down.sh
│   ├── gen_swagger.sh                       # swaggo generation
│   ├── lint.sh                              # golangci-lint
│   ├── test.sh                              # unit + integration + coverage
│   └── generate_mocks.sh                    # mockery
│
├── .github/
│   └── workflows/
│       ├── ci.yml                           # lint + test + build on PR
│       ├── release.yml                      # tag → build + push Docker images
│       ├── security-scan.yml                # gosec + trivy + govulncheck
│       └── codeql.yml
│
├── config/
│   ├── config.yaml                          # default config (committed)
│   ├── config.dev.yaml
│   ├── config.docker.yaml
│   └── casbin_model.conf                    # Casbin model definition
│
├── .env.example
├── .gitignore
├── .golangci.yml
├── .dockerignore
├── Makefile                                 # make run, make test, make lint, make migrate-up, make seed
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```