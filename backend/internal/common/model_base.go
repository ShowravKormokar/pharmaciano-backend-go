package common

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// BaseModel is embedded by every table that carries the standard identity and lifecycle columns.
type BaseModel struct {
	ID        uuid.UUID  `db:"id"         json:"id"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at" json:"deleted_at,omitempty"`
}

// IsDeleted reports whether the row has been soft-deleted.
func (b BaseModel) IsDeleted() bool { return b.DeletedAt != nil }

// AuditColumns is BaseModel plus who-created and who-last-updated the row.
type AuditColumns struct {
	BaseModel
	CreatedBy *uuid.UUID `db:"created_by" json:"created_by,omitempty"`
	UpdatedBy *uuid.UUID `db:"updated_by" json:"updated_by,omitempty"`
}

type JSONMap map[string]any

func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

// Scan populates the map from a JSONB column.
func (m *JSONMap) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}
	var b []byte
	switch t := src.(type) {
	case []byte:
		b = t
	case string:
		b = []byte(t)
	default:
		return errors.New("common.JSONMap.Scan: unsupported source type (want []byte or string)")
	}
	if len(b) == 0 {
		*m = nil
		return nil
	}
	return json.Unmarshal(b, m)
}

type Money string
