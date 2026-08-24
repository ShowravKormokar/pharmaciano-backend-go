package constants

// Package constants holds values that are compared against by name across modules and MUST stay in lock-step with seed data and migrations.

// Application identity — MUST match config.yaml's app.name / .env's
// APP_NAME. There is no automated check tying these together (config is
// loaded at runtime via Viper; this is a compile-time constant used in
// places that run before config is loaded, e.g. CLI --version output), so
// keep them in sync by hand whenever the product is renamed.
const (
	AppName    = "Pharmaciano ERP"
	AppSlug    = "pharmaciano-erp"
	APIVersion = "v1"
)

// Role names — must match seed/roles.yaml exactly.
const (
	RoleSuperAdmin    = "SUPER_ADMIN"
	RoleAdmin         = "ADMIN"
	RoleBranchManager = "BRANCH_MANAGER"
	RoleAccountant    = "ACCOUNTANT"
	RolePharmacist    = "PHARMACIST"
	RoleCashier       = "CASHIER"
	RoleWarehouse     = "WAREHOUSE"
	RoleAuditor       = "AUDITOR"
)

// SystemRoles lists every role that is seeded and cannot be deleted through
// the API. Used by rbac/service.go for guardrails. Deliberately a []string,
// not a typed enum in internal/common/enums — unlike the closed-set domain
// values there, custom roles can be created dynamically through the RBAC
// API, so "role name" is an open-ended set and a Valid()-style check would
// be wrong here.
var SystemRoles = []string{
	RoleSuperAdmin,
	RoleAdmin,
	RoleBranchManager,
	RoleAccountant,
	RolePharmacist,
	RoleCashier,
	RoleWarehouse,
	RoleAuditor,
}

// RBAC wildcard tokens — used by rbac/casbin.go when seeding policies and by
// appctx.HasPermission's in-memory fast-path check. Defined once here so
// both stay in lock-step; a mismatch would silently break wildcard
// permission checks (e.g. SUPER_ADMIN's "*" grant) with no compiler error
// to catch it.
const (
	PermissionWildcard   = "*"  // grants every module and action
	ModuleWildcardSuffix = ":*" // appended to a module name to grant every action within it
)

// Permission module names — used by RBAC middleware to name the "obj".
const (
	ModuleUsers            = "users"
	ModuleRoles            = "roles"
	ModulePermissions      = "permissions"
	ModuleSessions         = "sessions"
	ModuleOrganizations    = "organizations"
	ModuleBranches         = "branches"
	ModuleWarehouses       = "warehouses"
	ModuleMasterData       = "masterdata"
	ModuleBrands           = "brands"
	ModuleManufacturers    = "manufacturers"
	ModuleSuppliers        = "suppliers"
	ModuleMedicines        = "medicines"
	ModuleInventory        = "inventory"
	ModulePurchases        = "purchases"
	ModulePurchasePayments = "purchase_payments"
	ModuleSales            = "sales"
	ModuleSalesReturns     = "sales_returns"
	ModuleCustomers        = "customers"
	ModuleCoupons          = "coupons"
	ModuleLedger           = "ledger"
	ModuleTargets          = "targets"
	ModuleReports          = "reports"
	ModuleAnalytics        = "analytics"
	ModuleAI               = "ai"
	ModuleNotifications    = "notifications"
	ModuleAudit            = "audit"
	ModuleSettings         = "settings"
	ModuleFeatureFlags     = "feature_flags"
	ModuleBackup           = "backup"
)

// Permission action names — the "act" component.
const (
	ActionCreate     = "create"
	ActionView       = "view"
	ActionUpdate     = "update"
	ActionDelete     = "delete"
	ActionAssign     = "assign"
	ActionRevoke     = "revoke"
	ActionExport     = "export"
	ActionImport     = "import"
	ActionSubmit     = "submit"
	ActionApprove    = "approve"
	ActionReject     = "reject"
	ActionReceive    = "receive"
	ActionCancel     = "cancel"
	ActionVoid       = "void"
	ActionRefund     = "refund"
	ActionAdjust     = "adjust"
	ActionTransfer   = "transfer"
	ActionDeactivate = "deactivate"
	ActionPost       = "post"
	ActionSchedule   = "schedule"
	ActionForecast   = "forecast"
	ActionBroadcast  = "broadcast"
	ActionRead       = "read"
	ActionDismiss    = "dismiss"
	ActionRun        = "run"
	ActionRestore    = "restore"
	ActionToggle     = "toggle"
)

var AllModules = []string{
	ModuleUsers, ModuleRoles, ModulePermissions, ModuleSessions,
	ModuleOrganizations, ModuleBranches, ModuleWarehouses, ModuleMasterData,
	ModuleBrands, ModuleManufacturers, ModuleSuppliers, ModuleMedicines,
	ModuleInventory, ModulePurchases, ModulePurchasePayments, ModuleSales,
	ModuleSalesReturns, ModuleCustomers, ModuleCoupons, ModuleLedger,
	ModuleTargets, ModuleReports, ModuleAnalytics, ModuleAI,
	ModuleNotifications, ModuleAudit, ModuleSettings, ModuleFeatureFlags,
	ModuleBackup,
}

var AllActions = []string{
	ActionCreate, ActionView, ActionUpdate, ActionDelete, ActionAssign,
	ActionRevoke, ActionExport, ActionImport, ActionSubmit, ActionApprove,
	ActionReject, ActionReceive, ActionCancel, ActionVoid, ActionRefund,
	ActionAdjust, ActionTransfer, ActionDeactivate, ActionPost, ActionSchedule,
	ActionForecast, ActionBroadcast, ActionRead, ActionDismiss, ActionRun,
	ActionRestore, ActionToggle,
}

// HTTP headers (custom).
const (
	HeaderRequestID       = "X-Request-ID"
	HeaderBranchScope     = "X-Branch-ID"
	HeaderIdempotencyKey  = "Idempotency-Key"
	HeaderRateLimitLimit  = "X-RateLimit-Limit"
	HeaderRateLimitRemain = "X-RateLimit-Remaining"
	HeaderRateLimitReset  = "X-RateLimit-Reset"
	HeaderRetryAfter      = "Retry-After"
	HeaderDeprecation     = "Deprecation"
	HeaderSunset          = "Sunset"
	HeaderTotalCount      = "X-Total-Count"
	HeaderNextCursor      = "X-Next-Cursor"
	HeaderPage            = "X-Page"  
	HeaderLimit           = "X-Limit" 
	HeaderLink            = "Link"
)

// Cookies.
const (
	CookieRefreshToken = "mc_refresh"
	CookieCSRFToken    = "mc_csrf"
)

// Notification types — used as the `type` column in notifications.
const (
	NotifTypeLowStock             = "low_stock"
	NotifTypeExpirySoon           = "expiry_soon"
	NotifTypeExpiryCritical       = "expiry_critical"
	NotifTypePurchaseApprovalNeed = "purchase_approval_needed"
	NotifTypePurchaseApproved     = "purchase_approved"
	NotifTypePurchaseRejected     = "purchase_rejected"
	NotifTypePurchaseReceived     = "purchase_received"
	NotifTypeSaleCompleted        = "sale_completed"
	NotifTypeSaleReturned         = "sale_returned"
	NotifTypeSessionRevoked       = "session_revoked"
	NotifTypeTokenReuseDetected   = "token_reuse_detected"
	NotifTypeTargetProgress       = "target_progress"
	NotifTypeSystemAnnouncement   = "system_announcement"
	NotifTypeReportReady          = "report_ready"
	NotifTypeAIForecastReady      = "ai_forecast_ready"
	NotifTypeBackupCompleted      = "backup_completed"
	NotifTypeBackupFailed         = "backup_failed"
)

// Asynq queue names.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

// Task types (the Asynq task "type" string).
const (
	TaskAuditPersist       = "audit:persist"
	TaskNotificationSend   = "notification:send"
	TaskExpiryScan         = "inventory:expiry_scan"
	TaskLowStockScan       = "inventory:low_stock_scan"
	TaskReportGenerate     = "report:generate"
	TaskAIForecast         = "ai:forecast"
	TaskLedgerPost         = "ledger:post"
	TaskBackupRun          = "backup:run"
	TaskSessionCleanup     = "session:cleanup"
	TaskAuditPartitionMake = "audit:partition:make"
	TaskOutboxPublish      = "outbox:publish"
)

// Ledger — auto-post source module names (used in `journals.source_module`).
const (
	LedgerSourceSale        = "sale"
	LedgerSourcePurchase    = "purchase"
	LedgerSourceSaleReturn  = "sale_return"
	LedgerSourcePurchasePay = "purchase_payment"
	LedgerSourceSalePay     = "sale_payment"
	LedgerSourceTransfer    = "warehouse_transfer"
	LedgerSourceAdjustment  = "inventory_adjustment"
	LedgerSourceExpiry      = "inventory_expiry"
	LedgerSourceManual      = "manual"
)

// Default limits
const (
	DefaultPageSize   = 20
	MaxPageSize       = 100
	MaxExportRows     = 50_000
	MaxBroadcastFanout = 10_000
	MinPasswordLength = 10
	MaxLoginAttempts  = 5
	MinAIHistoryDays  = 30
	InvoiceNoPadding  = 6
	PurchaseNoPadding = 6
)