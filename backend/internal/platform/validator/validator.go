// Package validator centralizes go-playground/validator with app-specific
// rules, so every module's DTOs (CreateUserRequest, PurchaseItem,
// POSCheckoutRequest, ...) get consistent, JSON-safe, nested-path-aware
// validation errors instead of each handler rolling its own

package validator

import (
	"backend/internal/platform/config"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

type Validator struct {
	v         *validator.Validate
	sensitive []string
}

// ------------- Construction ------------
// Option configures New. See WithPasswordPolicy and WithSensitiveFields.
type Option func(*options)

type options struct {
	passwordPolicy  config.PasswordConfig
	sensitiveFields []string
}

var defaultSensitiveFields = []string{
	"password", "pass", "secret", "token", "otp", "mfa", "pin",
	"cvv", "card_number", "nid", "national_id",
	"bank_account", "account_number", "salary", "ssn",
}

func defaultOptions() options {
	return options{
		passwordPolicy: config.PasswordConfig{
			MinLength:     10,
			RequireUpper:  true,
			RequireLower:  true,
			RequireDigit:  true,
			RequireSymbol: true,
		},
		sensitiveFields: append([]string(nil), defaultSensitiveFields...),
	}
}

func WithPasswordPolicy(cfg config.PasswordConfig) Option {
	return func(o *options) {
		o.passwordPolicy = cfg
	}
}

func WithSensitiveFields(extra ...string) Option {
	return func(o *options) {
		o.sensitiveFields = append(o.sensitiveFields, extra...)
	}
}

// New builds a Validator with all custom rules registered.
func New(opts ...Option) (*Validator, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return fld.Name
		}
		return name
	})

	if err := registerCustomRules(v, o.passwordPolicy); err != nil {
		return nil, fmt.Errorf("validator: register custom rules: %w", err)
	}

	sensitive := make([]string, 0, len(o.sensitiveFields))
	for _, s := range o.sensitiveFields {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			sensitive = append(sensitive, s)
		}
	}

	return &Validator{v: v, sensitive: sensitive}, nil
}

// ---------------- Struct / Var entry points-----------------

func (val *Validator) Struct(s any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("validator: panic during validation: %v", r)
		}
	}()

	if s == nil {
		return errors.New("validator: nil struct")
	}
	if rv := reflect.ValueOf(s); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return fmt.Errorf("validator: nil pointer of type %s", rv.Type().Elem())
	}

	verr := val.v.Struct(s)
	if verr == nil {
		return nil
	}

	var ve validator.ValidationErrors
	if !errors.As(verr, &ve) {
		return verr
	}
	return val.toFieldErrors(ve)
}

func (val *Validator) Var(v any, tag string) error {
	return val.VarNamed(v, tag, "value")
}

func (val *Validator) VarNamed(v any, tag, fieldName string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("validator: panic during validation: %v", r)
		}
	}()

	verr := val.v.Var(v, tag)
	if verr == nil {
		return nil
	}

	var ve validator.ValidationErrors
	if !errors.As(verr, &ve) {
		return verr
	}

	out := make(FieldErrors, 0, len(ve))
	for _, fe := range ve {
		fieldErr := FieldError{
			Field:   fieldName,
			Rule:    fe.Tag(),
			Param:   fe.Param(),
			Message: humanMessage(fe, fieldName),
		}
		if !val.isSensitive(fieldName, fe.Tag()) {
			fieldErr.Value = truncate(fmt.Sprintf("%v", fe.Value()), maxValueLen)
		}
		out = append(out, fieldErr)
	}
	return out
}

func (val *Validator) RegisterStructValidation(fn validator.StructLevelFunc, types ...any) {
	val.v.RegisterStructValidation(fn, types...)
}

// Underlying returns the raw validator instance for callers that need it.
func (val *Validator) Underlying() *validator.Validate {
	return val.v
}

// ------------------------ Error Types ------------------
// FieldError describes a single failing rule.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Param   string `json:"param,omitempty"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message,omitempty"`
}

// FieldErrors is a slice of FieldError that satisfies the `error` interface.
type FieldErrors []FieldError

func (fe FieldErrors) Error() string {
	parts := make([]string, 0, len(fe))
	for _, e := range fe {
		parts = append(parts, fmt.Sprintf("%s: %s", e.Field, e.Message))
	}
	return strings.Join(parts, "; ")
}

// AsFieldErrors extracts a FieldErrors slice from err if present.
func AsFieldErrors(err error) (FieldErrors, bool) {
	var fe FieldErrors
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}

// ---------------------- Internals ----------------------
const maxValueLen = 200

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

func (val *Validator) toFieldErrors(ve validator.ValidationErrors) FieldErrors {
	out := make(FieldErrors, 0, len(ve))
	for _, fe := range ve {
		path := fieldPath(fe)
		fieldErr := FieldError{
			Field:   path,
			Rule:    fe.Tag(),
			Param:   fe.Param(),
			Message: humanMessage(fe, fe.Field()),
		}
		if !val.isSensitive(path, fe.Tag()) {
			fieldErr.Value = truncate(fmt.Sprintf("%v", fe.Value()), maxValueLen)
		}
		out = append(out, fieldErr)
	}
	return out
}

// sensitive-field list.
func (val *Validator) isSensitive(path, rule string) bool {
	if rule == "password_policy" {
		return true
	}
	lp := strings.ToLower(path)
	for _, s := range val.sensitive {
		if strings.Contains(lp, s) {
			return true
		}
	}
	return false
}

// since it's not part of the JSON body.
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if idx := strings.IndexByte(ns, '.'); idx >= 0 && idx+1 < len(ns) {
		return ns[idx+1:]
	}
	return fe.Field()
}

// humanMessage builds a friendly message for a failing rule
func humanMessage(fe validator.FieldError, name string) string {
	switch fe.Tag() {
	case "required", "required_if", "required_unless",
		"required_with", "required_without",
		"required_with_all", "required_without_all":
		return fmt.Sprintf("%s is required", name)
	case "email":
		return "must be a valid email address"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "len":
		return fmt.Sprintf("must be exactly %s characters/items long", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "gt", "gte", "lt", "lte":
		return fmt.Sprintf("must be %s %s", fe.Tag(), fe.Param())
	case "eqfield":
		return fmt.Sprintf("must match %s", fe.Param())
	case "nefield":
		return fmt.Sprintf("must not match %s", fe.Param())
	case "alpha":
		return "must contain letters only"
	case "alphanum":
		return "must contain letters and numbers only"
	case "numeric":
		return "must be numeric"
	case "url":
		return "must be a valid URL"
	case "datetime":
		return fmt.Sprintf("must match date/time format %q", fe.Param())
	case "unique":
		return "must not contain duplicate values"
	case "phone_bd":
		return "must be a valid Bangladeshi phone number"
	case "nid":
		return "must be a valid NID (10, 13, or 17 digits)"
	case "batch_no":
		return "batch number contains invalid characters"
	case "strength":
		return "strength must look like '500 mg', '5 mg/ml', or '0.5 %'"
	case "iso_currency":
		return "must be an ISO-4217 3-letter currency code"
	case "iana_tz":
		return "must be a valid IANA timezone"
	case "password_policy":
		return "does not meet the password policy"
	case "not_blank":
		return fmt.Sprintf("%s must not be blank", name)
	default:
		return fmt.Sprintf("failed rule %q", fe.Tag())
	}
}
