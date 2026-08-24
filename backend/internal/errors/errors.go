package errors

import (
	stderrors "errors"
	"fmt"
)

type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message,omitempty"`
	Value   any    `json:"value,omitempty"`
}

type AppError struct {
	Code    Code           `json:"code"`              // machine-readable identifier
	Message string         `json:"message"`           // human-readable, safe to expose
	Details []FieldError   `json:"details,omitempty"` // optional field-level breakdown
	Meta    map[string]any `json:"meta,omitempty"`    // extra fields (e.g. retry_after_seconds)
	Cause   error          `json:"-"`                 // wrapped root cause — NEVER serialize
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Message, e.Cause.Error())
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap allows errors.Is / errors.As to reach the cause.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AppError) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	var other *AppError
	if stderrors.As(target, &other) {
		return e.Code == other.Code
	}
	return false
}

func (e *AppError) clone() *AppError {
	if e == nil {
		return nil
	}
	out := *e
	if e.Details != nil {
		out.Details = append([]FieldError(nil), e.Details...)
	}
	if e.Meta != nil {
		out.Meta = make(map[string]any, len(e.Meta))
		for k, v := range e.Meta {
			out.Meta[k] = v
		}
	}
	return &out
}

// Constructors — cover 95% of call sites with a one-liner.

// New builds a fresh AppError.
func New(code Code, msg string) *AppError { return &AppError{Code: code, Message: msg} }

// Newf is New with fmt.Sprintf.
func Newf(code Code, format string, args ...any) *AppError {
	return &AppError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func Wrap(err error, code Code, msg string) *AppError {
	if err == nil {
		return nil
	}
	var existing *AppError
	if stderrors.As(err, &existing) {
		out := existing.clone()
		if code != "" {
			out.Code = code
		}
		if msg != "" {
			out.Message = msg
		}
		out.Cause = err
		return out
	}
	return &AppError{Code: code, Message: msg, Cause: err}
}

// Wrapf is Wrap with fmt.Sprintf.
func Wrapf(err error, code Code, format string, args ...any) *AppError {
	return Wrap(err, code, fmt.Sprintf(format, args...))
}

// WithDetails returns a copy of e with details appended. Non-mutating — see clone().
func (e *AppError) WithDetails(details ...FieldError) *AppError {
	if e == nil || len(details) == 0 {
		return e
	}
	out := e.clone()
	out.Details = append(out.Details, details...)
	return out
}

func (e *AppError) WithMeta(key string, value any) *AppError {
	if e == nil {
		return nil
	}
	out := e.clone()
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	out.Meta[key] = value
	return out
}

// WithCause returns a copy of e with its Cause set/replaced. Non-mutating — see clone().
func (e *AppError) WithCause(err error) *AppError {
	if e == nil {
		return nil
	}
	out := e.clone()
	out.Cause = err
	return out
}

// Convenience factories — read like the corresponding HTTP outcome.
// Validation reports a 400 with field-level details.
func Validation(msg string, details ...FieldError) *AppError {
	if msg == "" {
		msg = "request validation failed"
	}
	return New(CodeValidationError, msg).WithDetails(details...)
}

// NotFound reports a 404 for a specific resource.
func NotFound(resource string) *AppError {
	return Newf(CodeNotFound, "%s not found", resource)
}

// AlreadyExists reports a 409 for a duplicate key / unique violation.
func AlreadyExists(resource string) *AppError {
	return Newf(CodeAlreadyExists, "%s already exists", resource)
}

// Conflict reports a 409 for a state conflict (e.g. optimistic locking).
func Conflict(msg string) *AppError { return New(CodeConflict, msg) }

// Forbidden reports a 403.
func Forbidden(msg string) *AppError {
	if msg == "" {
		msg = "you do not have permission to perform this action"
	}
	return New(CodeForbidden, msg)
}

// Unauthenticated reports a 401 with a generic message.
func Unauthenticated() *AppError {
	return New(CodeUnauthenticated, "authentication required")
}

// InvalidCredentials reports a 401 for the login endpoint specifically.
func InvalidCredentials() *AppError {
	return New(CodeInvalidCredentials, "invalid email or password")
}

// TooManyRequests reports 429 with a Retry-After hint.
func TooManyRequests(retryAfterSec int) *AppError {
	return New(CodeRateLimited, "rate limit exceeded").
		WithMeta("retry_after_seconds", retryAfterSec)
}

// Internal reports a 500. cause is preserved in the Unwrap chain for logs
// but never surfaced — the message is always the fixed, generic string
// below regardless of what cause says.
func Internal(cause error) *AppError {
	return Wrap(cause, CodeInternal, "internal server error")
}

// Timeout reports a 504-equivalent when a downstream took too long.
func Timeout(op string, cause error) *AppError {
	return Wrap(cause, CodeTimeout, fmt.Sprintf("%s timed out", op))
}

// DatabaseError wraps a repository error with the DB code so the mapper picks the right HTTP status (usually 500).
func DatabaseError(cause error) *AppError {
	return Wrap(cause, CodeDatabaseError, "database error")
}

// Business is the general 422 helper.
func Business(code Code, msg string) *AppError {
	if code == "" {
		code = CodeBusinessRuleViolation
	}
	return New(code, msg)
}

// FeatureDisabled reports 503 (see codeToStatus in mapper.go).
func FeatureDisabled(feature string) *AppError {
	return Newf(CodeFeatureDisabled, "feature %q is disabled", feature)
}

// Introspection helpers As unwraps err into an *AppError or returns nil.
func As(err error) *AppError {
	if err == nil {
		return nil
	}
	var e *AppError
	if stderrors.As(err, &e) {
		return e
	}
	return nil
}

// CodeOf returns the code embedded in err, or CodeInternal for any non-AppError, non-nil error. Returns "" for a nil err.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if e := As(err); e != nil {
		return e.Code
	}
	return CodeInternal
}

// Has reports whether err carries the given Code — the ergonomic,
// allocation-free way to check "is this a NOT_FOUND?" without constructing
// a throwaway *AppError just to hand it to errors.Is.
func Has(err error, code Code) bool {
	return CodeOf(err) == code
}