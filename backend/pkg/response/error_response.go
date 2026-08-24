package response

import (
	"net/http"
	"strconv"

	"backend/internal/common/constants"
)

// ErrorBody is the shape of the "error" field in the envelope.
type ErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message,omitempty"`
	Value   any    `json:"value,omitempty"`
}

const (
	codeValidationError = "VALIDATION_ERROR"
	codeUnauthenticated = "UNAUTHENTICATED"
	codeForbidden       = "FORBIDDEN"
	codeNotFound        = "NOT_FOUND"
	codeConflict        = "CONFLICT"
	codeIdempotency     = "IDEMPOTENCY_KEY_CONFLICT"
	codeResourceLocked  = "RESOURCE_LOCKED"
	codeRateLimited     = "RATE_LIMITED"
	codeInternal        = "INTERNAL_ERROR"
	codeServiceDown     = "SERVICE_UNAVAILABLE"
)

// Error writes an error envelope with the given HTTP status and code.
func Error(w Writer, requestID string, status int, code, msg string, details ...FieldError) error {
	return writeJSON(w, requestID, status, Envelope{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: msg, Details: details},
		Meta:    newMeta(requestID),
	})
}

// BadRequest writes 400 VALIDATION_ERROR.
func BadRequest(w Writer, rid, msg string, d ...FieldError) error {
	return Error(w, rid, http.StatusBadRequest, codeValidationError, msg, d...)
}

// Unauthenticated writes 401.
func Unauthenticated(w Writer, rid string) error {
	return Error(w, rid, http.StatusUnauthorized, codeUnauthenticated, "authentication required")
}

// Forbidden writes 403.
func Forbidden(w Writer, rid string) error {
	return Error(w, rid, http.StatusForbidden, codeForbidden, "you do not have permission to perform this action")
}

// NotFound writes 404.
func NotFound(w Writer, rid, msg string) error {
	if msg == "" {
		msg = "resource not found"
	}
	return Error(w, rid, http.StatusNotFound, codeNotFound, msg)
}

// Conflict writes 409 CONFLICT (generic state conflict — optimistic locking, duplicate key).
func Conflict(w Writer, rid, msg string) error {
	return Error(w, rid, http.StatusConflict, codeConflict, msg)
}

// IdempotencyConflict writes 409 IDEMPOTENCY_KEY_CONFLICT
func IdempotencyConflict(w Writer, rid string) error {
	return Error(w, rid, http.StatusConflict, codeIdempotency,
		"the same Idempotency-Key was used with a different request body")
}

func UnprocessableEntity(w Writer, rid, code, msg string, d ...FieldError) error {
	return Error(w, rid, http.StatusUnprocessableEntity, code, msg, d...)
}

// Locked writes 423 RESOURCE_LOCKED (concurrent-edit conflict).
func Locked(w Writer, rid, msg string) error {
	if msg == "" {
		msg = "resource is locked by a concurrent operation"
	}
	return Error(w, rid, http.StatusLocked, codeResourceLocked, msg)
}

type RateLimitInfo struct {
	Limit         int
	Remaining     int
	ResetUnix     int64 // seconds since epoch
	RetryAfterSec int
}

// TooManyRequests writes 429 with all four documented rate-limit headers — the original signature only ever set Retry-After, silently dropping X-RateLimit-Limit/Remaining/Reset.
func TooManyRequests(w Writer, rid string, info RateLimitInfo) error {
	h := w.Header()
	if info.RetryAfterSec > 0 {
		h.Set(constants.HeaderRetryAfter, strconv.Itoa(info.RetryAfterSec))
	}
	h.Set(constants.HeaderRateLimitLimit, strconv.Itoa(info.Limit))
	h.Set(constants.HeaderRateLimitRemain, strconv.Itoa(info.Remaining))
	h.Set(constants.HeaderRateLimitReset, strconv.FormatInt(info.ResetUnix, 10))
	return Error(w, rid, http.StatusTooManyRequests, codeRateLimited, "rate limit exceeded")
}

// Internal writes 500. Never pass the underlying cause's message here — log it separately (see internal/errors.Internal + telemetry.LoggerFromContext).
func Internal(w Writer, rid string) error {
	return Error(w, rid, http.StatusInternalServerError, codeInternal, "internal server error")
}

// ServiceUnavailable writes 503.
func ServiceUnavailable(w Writer, rid, msg string) error {
	if msg == "" {
		msg = "service temporarily unavailable"
	}
	return Error(w, rid, http.StatusServiceUnavailable, codeServiceDown, msg)
}
