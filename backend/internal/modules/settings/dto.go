package settings

import (
	"encoding/json"
	"strconv"
	"strings"

	"backend/pkg/pagination"
)

const (
	maxSettingKeyBytes    = 120
	maxSettingValueBytes  = 64 << 10
	maxBulkSettingItems   = 100
	maxBulkValueBytes     = 1 << 20
	maxSettingReasonRunes = 500
	maxSettingSearchRunes = 120
)

// SettingReadMode controls whether a read returns the effective value or a
// specific authorized scope. Scope identity is always taken from auth context.
type SettingReadMode string

const (
	SettingReadModeEffective    SettingReadMode = "effective"
	SettingReadModeSystem       SettingReadMode = "system"
	SettingReadModeOrganization SettingReadMode = "organization"
)

// ListSettingsQuery binds the query string of GET /api/v1/settings.
// It supports optional search, scope selection, sensitive-value disclosure
// requests (privileged callers only), and standard offset pagination.
type ListSettingsQuery struct {
	pagination.Offset

	// Scope defaults to effective. Explicit system/organization reads require
	// authorization for that scope; callers cannot supply an organization ID.
	Scope SettingReadMode `form:"scope"`

	// Q is a free-text search over setting keys and descriptions.
	Q string `form:"q"`

	// IncludeSensitive controls whether sensitive setting values are included
	// in the response. This requires the privileged policy (SUPER_ADMIN).
	// When false (default), sensitive values are redacted.
	IncludeSensitive *bool `form:"include_sensitive"`
}

// NormalizeAndValidate normalizes and validates the query parameters.
func (q *ListSettingsQuery) NormalizeAndValidate() []error {
	var errs []error

	q.Q = strings.TrimSpace(q.Q)

	if len([]rune(q.Q)) > maxSettingSearchRunes {
		errs = append(errs, errFieldMaxSize("q", maxSettingSearchRunes))
	}

	if q.Scope == "" {
		q.Scope = SettingReadModeEffective
	}
	if q.Scope != SettingReadModeEffective &&
		q.Scope != SettingReadModeSystem &&
		q.Scope != SettingReadModeOrganization {
		errs = append(errs, errInvalidScope("scope"))
	}

	if _, err := pagination.ParseSort(q.Sort, map[string]struct{}{"key": {}}); err != nil {
		errs = append(errs, &FieldError{Field: "sort", Rule: "oneof", Message: "unsupported sort field"})
	}

	q.Normalize()
	return errs
}

// GetSettingQuery binds optional read controls for GET /api/v1/settings/{key}.
type GetSettingQuery struct {
	Scope            SettingReadMode `form:"scope"`
	IncludeSensitive *bool           `form:"include_sensitive"`
}

// NormalizeAndValidate applies defaults and validates the requested read mode.
func (q *GetSettingQuery) NormalizeAndValidate() []error {
	if q.Scope == "" {
		q.Scope = SettingReadModeEffective
	}
	if q.Scope != SettingReadModeEffective &&
		q.Scope != SettingReadModeSystem &&
		q.Scope != SettingReadModeOrganization {
		return []error{errInvalidScope("scope")}
	}
	return nil
}

// SettingResponse represents a single setting value as exposed by the API.
// It includes the effective value, type metadata, and resolution scope.
type SettingResponse struct {
	// Key is the stable unique identifier of the setting.
	Key string `json:"key"`

	// Value is the JSON-encoded setting value.
	// For sensitive settings, this is redacted unless the caller has
	// the privileged policy and IncludeSensitive=true.
	Value json.RawMessage `json:"value"`

	// ValueType is the declared type of the setting value.
	ValueType SettingValueType `json:"value_type"`

	// EffectiveScope identifies the requested/effective scope, not necessarily
	// the scope that supplied the value.
	EffectiveScope SettingScope `json:"effective_scope"`

	// SourceScope indicates where the value originated:
	// "system", "organization", or "default" (definition default).
	SourceScope SettingValueSource `json:"source_scope"`

	// Version is the monotonically increasing version of this value.
	// Used for optimistic concurrency on updates.
	Version int64 `json:"version"`

	// SchemaVersion is the schema version of the setting definition.
	SchemaVersion int `json:"schema_version"`

	// IsSensitive indicates whether this setting is classified as sensitive.
	// Sensitive values are redacted in responses unless the caller has
	// the privileged policy.
	IsSensitive bool `json:"is_sensitive"`

	// Description is the optional human-readable description from the definition.
	Description *string `json:"description,omitempty"`

	// SecretReferenceOnly means this setting only stores an approved secret
	// reference, never the actual credential.
	SecretReferenceOnly bool `json:"secret_reference_only"`
}

// SettingsListResponse is the response envelope for GET /api/v1/settings.
type SettingsListResponse struct {
	Items []SettingResponse `json:"items"`

	// Pagination is populated by the handler using pkg/pagination.BuildOffsetMeta.
	// It is not part of this struct but added to the response envelope.
}

// UpdateSettingRequest is the body of PUT /api/v1/settings/{key}.
// It accepts only client-writable fields for a single setting update.
type UpdateSettingRequest struct {
	// Value is the new setting value as a JSON literal.
	// It must be valid JSON matching the setting's declared ValueType.
	// Examples:
	//   "hello"           (string)
	//   42                (integer)
	//   true              (boolean)
	//   3.14              (float)
	//   {"key": "value"}  (json)
	Value json.RawMessage `json:"value" validate:"required"`

	// ExpectedVersion is the version the client expects the setting to have.
	// A mismatch is rejected with a 409 CONFLICT. It is required; 0 means
	// the setting must not already exist.
	ExpectedVersion *int64 `json:"expected_version" validate:"required,gte=0"`

	// Reason is an optional human-readable reason for the change. The
	// service may require it for sensitive or high-risk definitions.
	// Maximum 500 characters.
	Reason *string `json:"reason,omitempty" validate:"omitempty,max=500"`

	// Scope selects system or organization mutation. Ownership identifiers
	// are never accepted from the client.
	Scope SettingScope `json:"scope" validate:"required,oneof=system organization"`
}

// Validate validates the request body.
// It ensures the value is valid JSON and enforces size limits.
func (r *UpdateSettingRequest) Validate() []error {
	var errs []error

	if r.Scope != SettingScopeSystem && r.Scope != SettingScopeOrganization {
		errs = append(errs, errInvalidScope("scope"))
	}

	if r.ExpectedVersion == nil {
		errs = append(errs, errFieldRequired("expected_version"))
	} else if *r.ExpectedVersion < 0 {
		errs = append(errs, errFieldMin("expected_version", 0))
	}

	if err := validateJSONValue(r.Value, "value"); err != nil {
		errs = append(errs, err)
	}

	if r.Reason != nil {
		*r.Reason = strings.TrimSpace(*r.Reason)
		if *r.Reason == "" {
			r.Reason = nil
		} else if len([]rune(*r.Reason)) > maxSettingReasonRunes {
			errs = append(errs, errFieldMaxSize("reason", maxSettingReasonRunes))
		}
	}

	return errs
}

// BulkUpdateSettingsRequest is the body of POST /api/v1/settings/bulk.
// It accepts a bounded list of setting update entries.
type BulkUpdateSettingsRequest struct {
	// Scope applies to every item; ownership identities come from auth context.
	Scope SettingScope `json:"scope" validate:"required,oneof=system organization"`

	// Items is the list of settings to create or update atomically.
	// Maximum 100 entries per request.
	Items []BulkUpdateEntry `json:"items" validate:"required,min=1,max=100,dive"`
}

// BulkUpdateEntry represents one setting update within a bulk request.
type BulkUpdateEntry struct {
	// Key is the setting key to update.
	// Must be a registered, active definition that permits the caller's scope.
	Key string `json:"key" validate:"required,min=1,max=120"`

	// Value is the new setting value as a JSON literal.
	// Same format and validation as UpdateSettingRequest.Value.
	Value json.RawMessage `json:"value" validate:"required"`

	// ExpectedVersion is the version the client expects for this specific key.
	// A mismatch rejects the entire bulk operation with a 409 CONFLICT.
	// It is required; 0 means the setting must not already exist.
	ExpectedVersion *int64 `json:"expected_version" validate:"required,gte=0"`

	// Reason is an optional human-readable reason for this specific change.
	// Maximum 500 characters.
	Reason *string `json:"reason,omitempty" validate:"omitempty,max=500"`
}

// Validate validates the bulk request.
// It checks batch size, duplicate keys, and validates each entry.
func (r *BulkUpdateSettingsRequest) Validate() []error {
	var errs []error

	if r.Scope != SettingScopeSystem && r.Scope != SettingScopeOrganization {
		errs = append(errs, errInvalidScope("scope"))
	}

	if len(r.Items) == 0 {
		errs = append(errs, errFieldRequired("items"))
		return errs
	}

	if len(r.Items) > maxBulkSettingItems {
		errs = append(errs, errFieldMaxSize("items", maxBulkSettingItems))
		return errs
	}

	// Check for duplicate keys (case-sensitive per definition)
	seen := make(map[string]struct{}, len(r.Items))
	var totalValueBytes int
	totalValueLimitReported := false
	for i := range r.Items {
		item := &r.Items[i]
		item.Key = strings.TrimSpace(item.Key)
		if item.Key == "" {
			errs = append(errs, errFieldRequiredAt("items", i, "key"))
		} else if len(item.Key) > maxSettingKeyBytes {
			errs = append(errs, errFieldMaxSizeAt("items", i, "key", maxSettingKeyBytes))
		} else if !settingKeyPattern.MatchString(item.Key) {
			errs = append(errs, &FieldError{
				Field: "items[" + strconv.Itoa(i) + "].key",
				Rule:  "pattern", Message: "must be a lowercase dotted identifier",
			})
		}
		if _, dup := seen[item.Key]; dup {
			errs = append(errs, errDuplicateKey("items", item.Key))
		}
		seen[item.Key] = struct{}{}

		if item.ExpectedVersion == nil {
			errs = append(errs, errFieldRequiredAt("items", i, "expected_version"))
		} else if *item.ExpectedVersion < 0 {
			errs = append(errs, errFieldMinAt("items", i, "expected_version", 0))
		}

		if err := validateJSONValue(item.Value, "items["+strconv.Itoa(i)+"].value"); err != nil {
			errs = append(errs, err)
		}
		totalValueBytes += len(item.Value)
		if totalValueBytes > maxBulkValueBytes && !totalValueLimitReported {
			errs = append(errs, errFieldMaxSize("items", maxBulkValueBytes))
			totalValueLimitReported = true
		}

		if item.Reason != nil {
			*item.Reason = strings.TrimSpace(*item.Reason)
			if *item.Reason == "" {
				item.Reason = nil
			} else if len([]rune(*item.Reason)) > maxSettingReasonRunes {
				errs = append(errs, errFieldMaxSizeAt("items", i, "reason", maxSettingReasonRunes))
			}
		}
	}

	return errs
}

// BulkUpdateSettingsResponse is the response for a successful bulk update.
// Since bulk updates are atomic, this represents full success.
type BulkUpdateSettingsResponse struct {
	// Updated is the list of settings that were successfully updated.
	// Each entry contains the key, new version, and mutated scope.
	Updated []BulkUpdateResult `json:"updated"`
}

// BulkUpdateResult represents the result of one setting update in a bulk operation.
type BulkUpdateResult struct {
	// Key is the setting key that was updated.
	Key string `json:"key"`

	// Version is the new version after the update.
	Version int64 `json:"version"`

	// Scope is the scope that was updated ("system" or "organization").
	Scope SettingScope `json:"scope"`
}

// Helper functions for consistent validation error messages

func errFieldRequired(field string) error {
	return &FieldError{Field: field, Rule: "required", Message: "this field is required"}
}

func errFieldRequiredAt(parent string, index int, field string) error {
	return &FieldError{Field: parent + "[" + strconv.Itoa(index) + "]." + field, Rule: "required", Message: "this field is required"}
}

func errFieldMaxSize(field string, max int) error {
	return &FieldError{Field: field, Rule: "max_size", Message: "exceeds maximum allowed size", Value: max}
}

func errFieldMaxSizeAt(parent string, index int, field string, max int) error {
	return &FieldError{Field: parent + "[" + strconv.Itoa(index) + "]." + field, Rule: "max_size", Message: "exceeds maximum allowed size", Value: max}
}

func errInvalidJSON(field, detail string) error {
	return &FieldError{Field: field, Rule: "json", Message: "invalid JSON value: " + detail}
}

func errInvalidJSONAt(parent string, index int, field, detail string) error {
	return &FieldError{Field: parent + "[" + strconv.Itoa(index) + "]." + field, Rule: "json", Message: "invalid JSON value: " + detail}
}

func errDuplicateKey(parent, key string) error {
	return &FieldError{Field: parent, Rule: "unique", Message: "duplicate key in batch: " + key}
}

func errFieldMin(field string, min int64) error {
	return &FieldError{Field: field, Rule: "min", Message: "is below the minimum allowed value", Value: min}
}

func errFieldMinAt(parent string, index int, field string, min int64) error {
	return &FieldError{Field: parent + "[" + strconv.Itoa(index) + "]." + field, Rule: "min", Message: "is below the minimum allowed value", Value: min}
}

func errInvalidScope(field string) error {
	return &FieldError{Field: field, Rule: "oneof", Message: "must be system or organization"}
}

func validateJSONValue(value json.RawMessage, field string) error {
	if len(value) == 0 {
		return errFieldRequired(field)
	}
	if len(value) > maxSettingValueBytes {
		return &FieldError{Field: field, Rule: "max_size", Message: "exceeds maximum allowed size", Value: maxSettingValueBytes}
	}
	if !json.Valid(value) {
		return errInvalidJSON(field, "malformed JSON or multiple JSON values")
	}
	return nil
}

// FieldError is a validation error detail matching pkg/response.FieldError.
// It is defined here to avoid circular imports; the handler maps to response.FieldError.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message,omitempty"`
	Value   any    `json:"value,omitempty"`
}

func (e *FieldError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "validation error on field " + e.Field
}

// --- Mapper functions --------------------------------------------------------

// ToSettingResponse converts a resolved setting (from service) to a response DTO.
// The service is responsible for effective-value resolution, sensitivity redaction,
// and populating EffectiveScope/SourceScope.
func ToSettingResponse(key string, value json.RawMessage, valueType SettingValueType,
	effectiveScope SettingScope, sourceScope SettingValueSource, version int64, schemaVersion int,
	isSensitive bool, description *string, secretRefOnly bool) SettingResponse {

	return SettingResponse{
		Key:                 key,
		Value:               value,
		ValueType:           valueType,
		EffectiveScope:      effectiveScope,
		SourceScope:         sourceScope,
		Version:             version,
		SchemaVersion:       schemaVersion,
		IsSensitive:         isSensitive,
		Description:         description,
		SecretReferenceOnly: secretRefOnly,
	}
}

// ToBulkUpdateResult converts a bulk update result to a response DTO.
func ToBulkUpdateResult(key string, version int64, scope SettingScope) BulkUpdateResult {
	return BulkUpdateResult{
		Key:     key,
		Version: version,
		Scope:   scope,
	}
}

// RedactSensitiveValue returns a redacted JSON value for sensitive settings.
// The actual redaction format follows the project convention: a JSON string
// with a fixed placeholder message.
func RedactSensitiveValue() json.RawMessage {
	return json.RawMessage(`"<redacted>"`)
}
