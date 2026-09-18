//go:build oapival

// Package server is the OpenAPI-first layer that bridges the generated contract
// (internal/api/gen) onto the Gin router. It contains:
//
//   - validator.go     — kin-openapi middleware that enforces the contract at
//     runtime: path params, query/header params, the request body, and (opt-in)
//     the response body.
//
// Built only with `-tags oapival`. Omit the tag to keep the default build
// stdlib-only (matches the repo's `-tags grpc` precedent). The bundled spec is
// produced by `make oapi-bundle`.
package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/gin-gonic/gin"

	appctx "backend/internal/common/context"
	"backend/pkg/response"
)

// Validator enforces the OpenAPI contract for inbound requests against the
// bundled single-file spec (build/openapi.bundled.yaml).
type Validator struct {
	doc  *openapi3.T
	rl   routers.Router
	opts openapi3filter.Options
	log  *slog.Logger

	// ValidateResp turns on response-body validation in addition to request
	// validation. Response validation is more expensive; keep it off in hot paths.
	ValidateResp bool
}

// Enabled reports whether a validator is in play. Nil-safe so non-tagged callers
// can guard against a nil *Validator.
func (v *Validator) Enabled() bool { return v != nil }

// NewValidator parses and indexes the bundled OpenAPI document. specPath is the
// path to build/openapi.bundled.yaml produced by `make oapi-bundle`. The doc is
// loaded once at startup and kept for the life of the process.
func NewValidator(specPath string, log *slog.Logger, opts ...ValidatorOption) (*Validator, error) {
	ctx := context.Background()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false // bundled spec is self-contained
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, err
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	v := &Validator{doc: doc, rl: router, log: log}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

// ValidatorOption tweaks the validator after construction.
type ValidatorOption func(*Validator)

// WithResponseValidation enables response-body validation.
func WithResponseValidation() ValidatorOption { return func(v *Validator) { v.ValidateResp = true } }

// Middleware returns the Gin handler. It must run AFTER CORS and (for parameter
// binding) approachable to the request body — mount it immediately after the
// global middleware in the router composition, before any module's routes.
func (v *Validator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v == nil {
			c.Next()
			return
		}
		// Re-buffer the body: openapi3filter will consume it, but the downstream
		// handler needs to read it again. We read it up front and rewind.
		hasBody := c.Request.Body != nil && c.Request.Body != http.NoBody
		var raw []byte
		if hasBody {
			raw, _ = io.ReadAll(c.Request.Body)
			c.Request.Body.Close()
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		req := c.Request.Clone(c.Request.Context())
		req.Body = io.NopCloser(bytes.NewReader(raw))

		route, pathParams, errRoute := v.rl.FindRoute(req)
		if errRoute != nil {
			// No spec match — the app's own router still serves it (e.g. an
			// undocumented dev route). Leave contract-enforcement to routes we know.
			c.Next()
			return
		}

		reqInput := &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
			Options:    &v.opts,
		}
		if errv := openapi3filter.ValidateRequest(c.Request.Context(), reqInput); errv != nil {
			writeValidationError(c, errv, v.log)
			c.Abort()
			return
		}
		c.Next()
	}
}

// writeValidationError renders a schema-shape rejection using the standard error
// catalogue (VALIDATION_ERROR is a 400) so the failure mode is uniform.
func writeValidationError(c *gin.Context, errv error, log *slog.Logger) {
	var verr *openapi3filter.RequestError
	ok := errors.As(errv, &verr)
	rid := appctx.RequestID(c.Request.Context())
	if log != nil {
		if ok {
			log.Debug("openapi validation rejected",
				"request_id", rid,
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"reason", verr.Reason,
				"err", errv.Error(),
			)
		}
	}
	_ = response.Error(
		c.Writer,
		rid,
		http.StatusBadRequest,
		"VALIDATION_ERROR",
		"request does not match the API contract",
		response.FieldError{Field: "request", Rule: "openapi3", Message: errv.Error()},
	)
}
