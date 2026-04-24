package errors

import "net/http"

type AppError struct {
	Code    int    `json:"-"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

func NewAppError(code int, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

var (
	ErrInvalidCredentials = NewAppError(http.StatusUnauthorized, "invalid email or password", nil)
	ErrInactiveAccount    = NewAppError(http.StatusForbidden, "account is inactive", nil)
	ErrTokenValidation    = NewAppError(http.StatusUnauthorized, "invalid or expired token", nil)
)
