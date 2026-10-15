package settings

import (
	"backend/internal/common"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Settings domain types

// SettingScope identifies the owner of a configuration value.
// Scope authorization and inheritance are enforced by the service layer.
type SettingScope string

const (
	SettingScopeSystem       SettingScope = "system"
	SettingScopeOrganization SettingScope = "organization"
)

// SettingValueType describes the JSON representation accepted for a setting.
// Values are validated against the authoritative SettingDefinition.
type SettingValueType string

const (
	SettingValueTypeString  SettingValueType = "string"
	SettingValueTypeInteger SettingValueType = "integer"
	SettingValueTypeBoolean SettingValueType = "boolean"
	SettingValueTypeFloat   SettingValueType = "float"
	SettingValueTypeJSON    SettingValueType = "json"
)

// DefinitionStatus controls whether a definition can be used for new mutations.
type DefinitionStatus string

const (
	DefinitionStatusActive     DefinitionStatus = "active"
	DefinitionStatusDeprecated DefinitionStatus = "deprecated"
)

// SettingValueSource identifies which layer supplied a resolved value.
type SettingValueSource string

const (
	SettingValueSourceSystem       SettingValueSource = "system"
	SettingValueSourceOrganization SettingValueSource = "organization"
	SettingValueSourceDefault      SettingValueSource = "default"
)

// SettingDefinition
// Table: setting_definitions
//
// Authoritative, application-controlled registry of permitted settings.
// Definitions are provisioned through reviewed code/seed changes, not arbitrary
// public API requests.

type SettingDefinition struct {
	common.BaseModel

	Key           string           `db:"key" json:"key"`
	ValueType     SettingValueType `db:"value_type" json:"value_type"`
	SchemaVersion int              `db:"schema_version" json:"schema_version"`

	// DefaultValue is an optional JSON-encoded value. It must be validated
	// against ValueType and the registered schema before it is used.
	DefaultValue json.RawMessage `db:"default_value" json:"default_value,omitempty"`

	// AllowedScopes is stored as PostgreSQL text[]; each entry must be one
	// of the Phase 1 SettingScope values.
	AllowedScopes []string `db:"allowed_scopes" json:"allowed_scopes"`

	Description *string `db:"description" json:"description,omitempty"`

	// IsSensitive is definition-owned disclosure metadata, never a
	// client-writable property of a persisted value.
	IsSensitive bool `db:"is_sensitive" json:"is_sensitive"`

	// SecretReferenceOnly means the setting may contain an approved secret
	// reference, never the actual credential or secret material.
	SecretReferenceOnly bool `db:"secret_reference_only" json:"secret_reference_only"`

	// ValidatorKey identifies an application-registered validator.
	// It is not executable code supplied by an API client.
	ValidatorKey *string `db:"validator_key" json:"validator_key,omitempty"`

	Status DefinitionStatus `db:"status" json:"status"`
}

// Scoped setting values
//
// Each table has its own ownership and uniqueness constraints. Do not merge
// these into one generic table with an unrestricted scope_id.

// SystemSetting stores the platform-wide value for a setting.
// Table: system_setting_values
//
// A row exists only when an explicit system-level override is persisted.
// Otherwise, a validated definition default may be used when permitted.
type SystemSetting struct {
	common.BaseModel

	Key   string          `db:"key" json:"key"`
	Value json.RawMessage `db:"value" json:"value"`

	// Version is monotonically incremented on each successful mutation.
	Version int64 `db:"version" json:"version"`

	UpdatedBy *uuid.UUID `db:"updated_by" json:"updated_by,omitempty"`
}

// OrganizationSetting stores an organization-specific override.
// Table: organization_setting_values
type OrganizationSetting struct {
	common.BaseModel

	OrganizationID uuid.UUID       `db:"organization_id" json:"organization_id"`
	Key            string          `db:"key" json:"key"`
	Value          json.RawMessage `db:"value" json:"value"`

	Version   int64      `db:"version" json:"version"`
	UpdatedBy *uuid.UUID `db:"updated_by" json:"updated_by,omitempty"`
}

// SettingRevision
// Table: setting_revisions
//
// Append-only configuration history. Ordinary application operations must
// insert revisions, never update or delete existing revision records.

type SettingRevision struct {
	ID uuid.UUID `db:"id" json:"id"`

	Key   string       `db:"key" json:"key"`
	Scope SettingScope `db:"scope" json:"scope"`

	// OrganizationID is nil for system scope and retained as an audit snapshot
	// for organization scope, even if the organization is later deleted.
	OrganizationID *uuid.UUID `db:"organization_id" json:"organization_id,omitempty"`

	Version int64 `db:"version" json:"version"`

	SchemaVersion int `db:"schema_version" json:"schema_version"`

	ActorID       *uuid.UUID `db:"actor_id" json:"actor_id,omitempty"`
	ChangeReason  *string    `db:"change_reason" json:"change_reason,omitempty"`
	CorrelationID *string    `db:"correlation_id" json:"correlation_id,omitempty"`

	// Sensitive values must be redacted or omitted before insertion.
	// These fields must never contain secret material.
	PreviousValue json.RawMessage `db:"previous_value" json:"previous_value,omitempty"`
	NewValue      json.RawMessage `db:"new_value" json:"new_value,omitempty"`

	// Records whether values were redacted for sensitivity reasons.
	ValuesRedacted bool `db:"values_redacted" json:"values_redacted"`

	ChangedAt time.Time `db:"changed_at" json:"changed_at"`
}
