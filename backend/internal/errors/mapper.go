package errors

import (
	"context"
	stderrors "errors"
	"net/http"
)

// HTTPStatus returns the HTTP status code that best matches err.
func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	if ae := As(err); ae != nil {
		if s, ok := codeToStatus[ae.Code]; ok {
			return s
		}
		return http.StatusInternalServerError
	}

	switch {
	case stderrors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case stderrors.Is(err, context.Canceled):
		return 499 // "Client Closed Request", Nginx-style — not a real IANA status
	default:
		return http.StatusInternalServerError
	}
}

var codeToStatus = map[Code]int{
	// 400
	CodeValidationError: http.StatusBadRequest,
	CodeInvalidCursor:   http.StatusBadRequest,
	CodeInvalidUUID:     http.StatusBadRequest,
	CodeInvalidQuery:    http.StatusBadRequest,

	// 401
	CodeUnauthenticated:    http.StatusUnauthorized,
	CodeInvalidCredentials: http.StatusUnauthorized,
	CodeTokenExpired:       http.StatusUnauthorized,
	CodeTokenInvalid:       http.StatusUnauthorized,
	CodeTokenReuseDetected: http.StatusUnauthorized,
	CodeMFARequired:        http.StatusUnauthorized,
	CodeMFAInvalid:         http.StatusUnauthorized,

	// 403
	CodeForbidden:         http.StatusForbidden,
	CodeAccountLocked:     http.StatusForbidden,
	CodeAccountInactive:   http.StatusForbidden,
	CodeAccountSuspended:  http.StatusForbidden,
	CodeBranchScopeDenied: http.StatusForbidden,
	CodeTenantScopeDenied: http.StatusForbidden,

	// 404
	CodeNotFound: http.StatusNotFound,

	// 409
	CodeAlreadyExists:         http.StatusConflict,
	CodeConflict:              http.StatusConflict,
	CodeIdempotencyConflict:   http.StatusConflict,
	CodeStateTransitionDenied: http.StatusConflict,

	// 422
	CodeBusinessRuleViolation: http.StatusUnprocessableEntity,
	CodeInsufficientStock:     http.StatusUnprocessableEntity,
	CodeBatchExpired:          http.StatusUnprocessableEntity,
	CodeBatchInactive:         http.StatusUnprocessableEntity,
	CodePriceInvalid:          http.StatusUnprocessableEntity,
	CodeLedgerUnbalanced:      http.StatusUnprocessableEntity,
	CodeApprovalRequired:      http.StatusUnprocessableEntity,
	CodePaymentInvalid:        http.StatusUnprocessableEntity,
	CodeCouponInvalid:         http.StatusUnprocessableEntity,
	CodeReturnWindowClosed:    http.StatusUnprocessableEntity,
	CodeAIInsufficientHistory: http.StatusUnprocessableEntity,

	// 423
	CodeResourceLocked: http.StatusLocked,

	// 429
	CodeRateLimited:       http.StatusTooManyRequests,
	CodeAICostCapExceeded: http.StatusTooManyRequests,

	// 500
	CodeInternal:      http.StatusInternalServerError,
	CodeDatabaseError: http.StatusInternalServerError,
	CodeCacheError:    http.StatusInternalServerError,
	CodeQueueError:    http.StatusInternalServerError,

	// 501
	CodeNotImplemented: http.StatusNotImplemented,

	// 502
	CodeUpstreamError:   http.StatusBadGateway,
	CodeAIProviderError: http.StatusBadGateway,

	// 503
	CodeServiceUnavailable: http.StatusServiceUnavailable,
	CodeFeatureDisabled:    http.StatusServiceUnavailable,

	// 504
	CodeTimeout: http.StatusGatewayTimeout,
}

func init() {
	seen := make(map[Code]bool, len(All))
	for _, c := range All {
		seen[c] = true
		if _, ok := codeToStatus[c]; !ok {
			panic("internal/errors: Code " + string(c) + " has no HTTP status mapping in mapper.go's codeToStatus")
		}
	}
	for c := range codeToStatus {
		if !seen[c] {
			panic("internal/errors: codeToStatus maps Code " + string(c) + " which is not declared in codes.go's All")
		}
	}
}

// LogLevel classifies severity for the logger — 5xx logs error, 4xx logs
// info/warn. Keep the mapping small and predictable so log volume is stable.
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// String makes LogLevel satisfy fmt.Stringer, mainly for debug/test output.
func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "debug"
	case LogLevelInfo:
		return "info"
	case LogLevelWarn:
		return "warn"
	case LogLevelError:
		return "error"
	default:
		return "unknown"
	}
}

// LevelFor returns the log level that should be used for err. Pair with telemetry.
func LevelFor(err error) LogLevel {
	status := HTTPStatus(err)
	switch {
	case status >= 500:
		return LogLevelError
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusTooManyRequests,
		status == http.StatusLocked,
		status == http.StatusConflict:
		return LogLevelWarn
	default:
		return LogLevelInfo
	}
}

// RetryAfter extracts a "retry_after_seconds" hint from AppError.Meta. Returns 0 when absent.
func RetryAfter(err error) int {
	e := As(err)
	if e == nil || e.Meta == nil {
		return 0
	}
	switch v := e.Meta["retry_after_seconds"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func AllRegisteredCodes() []Code {
	out := make([]Code, 0, len(codeToStatus))
	for c := range codeToStatus {
		out = append(out, c)
	}
	return out
}