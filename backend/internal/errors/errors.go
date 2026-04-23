// errors.go (improved version)
package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// Type is a string alias for error category
type Type string

const (
	Validation   Type = "VALIDATION_ERROR"
	NotFound     Type = "NOT_FOUND"
	Unauthorized Type = "UNAUTHORIZED"
	Forbidden    Type = "FORBIDDEN"
	Conflict     Type = "CONFLICT"
	Internal     Type = "INTERNAL_ERROR"
)

// DomainError now implements unwrapping and Is/As
type DomainError struct {
	Type    Type
	Message string
	Details map[string]interface{} // keep optional
	Code    int
	Err     error // wrapped error (optional)
}

// Error implements the standard error interface
func (e *DomainError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap supports errors.Unwrap
func (e *DomainError) Unwrap() error {
	return e.Err
}

// Is enables errors.Is to match by type and message (optional)
func (e *DomainError) Is(target error) bool {
	var t *DomainError
	if errors.As(target, &t) {
		return e.Type == t.Type && e.Message == t.Message
	}
	return false
}

// New creates a DomainError with optional wrapping
func New(errType Type, message string, wrap ...error) *DomainError {
	var wrapped error
	if len(wrap) > 0 {
		wrapped = wrap[0]
	}
	return &DomainError{
		Type:    errType,
		Message: message,
		Details: make(map[string]interface{}),
		Code:    httpStatusFromType(errType),
		Err:     wrapped,
	}
}

// WithDetail adds context (fluent)
func (e *DomainError) WithDetail(key string, value interface{}) *DomainError {
	e.Details[key] = value
	return e
}

// httpStatusFromType maps Type to HTTP status
func httpStatusFromType(t Type) int {
	switch t {
	case Validation:
		return http.StatusBadRequest
	case NotFound:
		return http.StatusNotFound
	case Unauthorized:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case Conflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// Predefined sentinel errors (usable with errors.Is)
var (
	ErrUserNotFound       = New(NotFound, "user not found")
	ErrInvalidCredentials = New(Unauthorized, "invalid email or password")
	ErrEmailAlreadyExists = New(Conflict, "email already exists")
	ErrUnauthorized       = New(Unauthorized, "unauthorized")
	ErrForbidden          = New(Forbidden, "forbidden")
	ErrInternal           = New(Internal, "internal server error")
)

// Helper to extract type from any error
func GetType(err error) Type {
	var domErr *DomainError
	if errors.As(err, &domErr) {
		return domErr.Type
	}
	return Internal // default
}
