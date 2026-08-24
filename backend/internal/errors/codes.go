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

	// Authorization
	CodeForbidden         Code = "FORBIDDEN"
	CodeAccountLocked     Code = "ACCOUNT_LOCKED"
	CodeAccountInactive   Code = "ACCOUNT_INACTIVE"
	CodeAccountSuspended  Code = "ACCOUNT_SUSPENDED"
	CodeBranchScopeDenied Code = "BRANCH_SCOPE_DENIED"
	CodeTenantScopeDenied Code = "TENANT_SCOPE_DENIED"

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
	CodeTokenReuseDetected, CodeMFARequired, CodeMFAInvalid,

	CodeForbidden, CodeAccountLocked, CodeAccountInactive, CodeAccountSuspended,
	CodeBranchScopeDenied, CodeTenantScopeDenied,

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
