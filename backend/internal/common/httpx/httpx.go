package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
	"backend/pkg/response"
)

// RequestID returns the request id bound to the request context by the request-id middleware (empty string if unset — response helpers then mint one).
func RequestID(c *gin.Context) string {
	return appctx.RequestID(c.Request.Context())
}

func BindJSON(c *gin.Context, v *validator.Validator, dst any) error {
	if err := c.ShouldBindJSON(dst); err != nil {
		return errs.Validation("request body is not valid JSON").WithCause(err)
	}
	return Validate(v, dst)
}

func BindQuery(c *gin.Context, v *validator.Validator, dst any) error {
	if err := c.ShouldBindQuery(dst); err != nil {
		return errs.Validation("invalid query parameters").WithCause(err)
	}
	return Validate(v, dst)
}

func Validate(v *validator.Validator, dst any) error {
	if v == nil {
		return nil
	}
	err := v.Struct(dst)
	if err == nil {
		return nil
	}
	if fe, ok := validator.AsFieldErrors(err); ok {
		return errs.Validation("request validation failed", validatorDetails(fe)...)
	}
	// Not a field-level failure (e.g. a nil pointer) — surface generically.
	return errs.Validation("request validation failed").WithCause(err)
}

// OK writes a 200 envelope.
func OK(c *gin.Context, data any) { _ = response.OK(c.Writer, RequestID(c), data) }

// Created writes a 201 envelope.
func Created(c *gin.Context, data any) { _ = response.Created(c.Writer, RequestID(c), data) }

// NoContent writes a 204 (no body) — used by soft-delete endpoints.
func NoContent(c *gin.Context) { _ = response.NoContent(c.Writer, RequestID(c)) }

// List writes a 200 envelope with a pagination block.
func List(c *gin.Context, data any, meta pagination.Meta) {
	_ = response.List(c.Writer, RequestID(c), data, meta)
}

func Error(c *gin.Context, log *zap.Logger, err error) {
	rid := RequestID(c)
	status := errs.HTTPStatus(err)
	code := errs.CodeOf(err)

	if log != nil {
		fields := []zap.Field{
			zap.Int("status", status),
			zap.String("error_code", string(code)),
			zap.Error(err),
		}
		switch errs.LevelFor(err) {
		case errs.LogLevelError:
			log.Error("request failed", fields...)
		case errs.LogLevelWarn:
			log.Warn("request rejected", fields...)
		default:
			log.Info("request rejected", fields...)
		}
	}

	// 5xx: fixed, generic body. The cause is in the log, never on the wire.
	if status >= 500 {
		_ = response.Internal(c.Writer, rid)
		return
	}

	ae := errs.As(err)
	if ae == nil {
		_ = response.Error(c.Writer, rid, status, string(errs.CodeInternal), "internal server error")
		return
	}

	if ra := errs.RetryAfter(err); ra > 0 {
		c.Writer.Header().Set(constants.HeaderRetryAfter, strconv.Itoa(ra))
	}
	_ = response.Error(c.Writer, rid, status, string(ae.Code), ae.Message, responseDetails(ae.Details)...)
}

// validatorDetails converts validator.FieldErrors into the errors package shape.
func validatorDetails(in validator.FieldErrors) []errs.FieldError {
	if len(in) == 0 {
		return nil
	}
	out := make([]errs.FieldError, len(in))
	for i, f := range in {
		out[i] = errs.FieldError{
			Field:   f.Field,
			Rule:    f.Rule,
			Param:   f.Param,
			Message: f.Message,
			Value:   f.Value,
		}
	}
	return out
}

// responseDetails converts the errors package FieldError slice into the response package shape (the two are intentionally decoupled types).
func responseDetails(in []errs.FieldError) []response.FieldError {
	if len(in) == 0 {
		return nil
	}
	out := make([]response.FieldError, len(in))
	for i, f := range in {
		out[i] = response.FieldError{
			Field:   f.Field,
			Rule:    f.Rule,
			Message: f.Message,
			Value:   f.Value,
		}
	}
	return out
}
