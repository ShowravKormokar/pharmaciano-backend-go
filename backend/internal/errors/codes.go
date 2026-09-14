package errors

type Code string

// String makes Code satisfy fmt.Stringer.
func (c Code) String() string {
	return string(c)
}

// Codes mirror /docs/api/API.md so the API contract has a single source of truth.
const (
	// Validation / input
	CodeValidationError Code = "VALIDATION_ERROR"
	CodeInvalidCursor   Code = "INVALID_CURSOR"
	CodeInvalidUUID     Code = "INVALID_UUID"
	CodeInvalidQuery    Code = "INVALID_QUERY"

	// Authentication
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
	CodeInvalidCredentials Code = "INVALID_CREDENTIALS"
	CodeTokenExpired       Code = "TOKEN_EXPIRED"
	CodeTokenInvalid       Code = "TOKEN_INVALID"
	CodeTokenReuseDetected Code = "TOKEN_REUSE_DETECTED"
	CodeMFARequired        Code = "MFA_REQUIRED"
	CodeMFAInvalid         Code = "MFA_INVALID"
	// CodePasswordChangeRequired is returned when the credential is valid and the
	// account's status permits login but the account's policy demands a password
	// change before any session is issued (users.must_change_password=true). It
	// mirrors MFA_REQUIRED: the response body is a generic 401 and the single-use
	// change token rides in the X-Password-Change-Token header for the client to
	// redeem at /auth/password/force-change together with the current + new
	// password. No session or access token is minted until the change completes.
	CodePasswordChangeRequired Code = "PASSWORD_CHANGE_REQUIRED"

	// Authorization
	CodeForbidden          Code = "FORBIDDEN"
	CodeAccountLocked      Code = "ACCOUNT_LOCKED"
	CodeAccountInactive    Code = "ACCOUNT_INACTIVE"
	CodeAccountSuspended   Code = "ACCOUNT_SUSPENDED"
	CodeBranchScopeDenied  Code = "BRANCH_SCOPE_DENIED"
	CodeTenantScopeDenied  Code = "TENANT_SCOPE_DENIED"
	// CodeCrossOriginRequest is returned by the OriginGuard middleware when a
	// state-changing (unsafe-method) request carries an Origin or Referer header
	// that is not on the CORS allowlist. It is the CSRF defense-in-depth layer
	// (ADR §12): a browser cannot forge an Origin, so a cross-site form/fetch
	// POST that bypasses SameSite is still refused.
	CodeCrossOriginRequest Code = "CROSS_ORIGIN_REQUEST"

	// Concurrency / capacity bounds
	// CodeTooManySessions is returned by a login attempt when the account already
	// holds the configured maximum number of concurrent active sessions
	// (session.max_concurrent_per_user) and the new login is refused. The client
	// should present the live-session list (GET /auth/sessions) and revoke a device.
	CodeTooManySessions Code = "TOO_MANY_SESSIONS"

	// Resource lifecycle
	CodeNotFound            Code = "NOT_FOUND"
	CodeAlreadyExists       Code = "ALREADY_EXISTS"
	CodeConflict            Code = "CONFLICT"
	CodeResourceLocked      Code = "RESOURCE_LOCKED"
	CodeIdempotencyConflict Code = "IDEMPOTENCY_KEY_CONFLICT"

	// Business rules-
	CodeBusinessRuleViolation Code = "BUSINESS_RULE_VIOLATION"
	CodeInsufficientStock     Code = "INSUFFICIENT_STOCK"
	CodeBatchExpired          Code = "BATCH_EXPIRED"
	CodeBatchInactive         Code = "BATCH_INACTIVE"
	CodePriceInvalid          Code = "PRICE_INVALID"
	CodeLedgerUnbalanced      Code = "LEDGER_UNBALANCED"
	CodeStateTransitionDenied Code = "STATE_TRANSITION_DENIED"
	CodeApprovalRequired      Code = "APPROVAL_REQUIRED"
	CodePaymentInvalid        Code = "PAYMENT_INVALID"
	CodeCouponInvalid         Code = "COUPON_INVALID"
	CodeReturnWindowClosed    Code = "RETURN_WINDOW_CLOSED"

	// Rate limits
	CodeRateLimited Code = "RATE_LIMITED"

	// Server-side
	CodeInternal           Code = "INTERNAL_ERROR"
	CodeUpstreamError      Code = "UPSTREAM_ERROR"
	CodeServiceUnavailable Code = "SERVICE_UNAVAILABLE"
	CodeTimeout            Code = "TIMEOUT"
	CodeDatabaseError      Code = "DATABASE_ERROR"
	CodeCacheError         Code = "CACHE_ERROR"
	CodeQueueError         Code = "QUEUE_ERROR"

	// AI-specific
	CodeAIProviderError       Code = "AI_PROVIDER_ERROR"
	CodeAICostCapExceeded     Code = "AI_COST_CAP_EXCEEDED"
	CodeAIInsufficientHistory Code = "AI_INSUFFICIENT_HISTORY"

	// Feature-flag / config
	CodeFeatureDisabled Code = "FEATURE_DISABLED"
	CodeNotImplemented  Code = "NOT_IMPLEMENTED"
)

var All = []Code{
	CodeValidationError, CodeInvalidCursor, CodeInvalidUUID, CodeInvalidQuery,

	CodeUnauthenticated, CodeInvalidCredentials, CodeTokenExpired, CodeTokenInvalid,
	CodeTokenReuseDetected, CodeMFARequired, CodeMFAInvalid, CodePasswordChangeRequired,

	CodeForbidden, CodeAccountLocked, CodeAccountInactive, CodeAccountSuspended,
	CodeBranchScopeDenied, CodeTenantScopeDenied, CodeCrossOriginRequest,

	CodeTooManySessions,

	CodeNotFound, CodeAlreadyExists, CodeConflict, CodeResourceLocked, CodeIdempotencyConflict,

	CodeBusinessRuleViolation, CodeInsufficientStock, CodeBatchExpired, CodeBatchInactive,
	CodePriceInvalid, CodeLedgerUnbalanced, CodeStateTransitionDenied, CodeApprovalRequired,
	CodePaymentInvalid, CodeCouponInvalid, CodeReturnWindowClosed,

	CodeRateLimited,

	CodeInternal, CodeUpstreamError, CodeServiceUnavailable, CodeTimeout,
	CodeDatabaseError, CodeCacheError, CodeQueueError,

	CodeAIProviderError, CodeAICostCapExceeded, CodeAIInsufficientHistory,

	CodeFeatureDisabled, CodeNotImplemented,
}
