package masterdata

import (
	"backend/internal/common"

	"github.com/google/uuid"
)

// Scope: the eleven central master-data resources only. The `manufacturers`,
// `brands` and `suppliers` tables (migrations/000005) are owned by their own
// modules and are deliberately not represented here — see contract C-02.

type ProductCategory struct {
	common.BaseModel
	Code        string     `db:"code"        json:"code"`
	Name        string     `db:"name"        json:"name"`
	ParentID    *uuid.UUID `db:"parent_id"   json:"parent_id,omitempty"`
	Description *string    `db:"description" json:"description,omitempty"`
	SortOrder   int        `db:"sort_order"  json:"sort_order"`
	IsActive    bool       `db:"is_active"   json:"is_active"`
}

type DosageForm struct {
	common.BaseModel
	Code        string     `db:"code"        json:"code"`
	Name        string     `db:"name"        json:"name"`
	RouteID     *uuid.UUID `db:"route_id"    json:"route_id,omitempty"`
	Description *string    `db:"description" json:"description,omitempty"`
	IsActive    bool       `db:"is_active"   json:"is_active"`
}

type Route struct {
	common.BaseModel
	Code        string  `db:"code"        json:"code"`
	Name        string  `db:"name"        json:"name"`
	Description *string `db:"description" json:"description,omitempty"`
	IsActive    bool    `db:"is_active"   json:"is_active"`
}

type PackageType struct {
	common.BaseModel
	Code        string  `db:"code"        json:"code"`
	Name        string  `db:"name"        json:"name"`
	Description *string `db:"description" json:"description,omitempty"`
	IsActive    bool    `db:"is_active"   json:"is_active"`
}

type UnitType struct {
	common.BaseModel
	Code             string     `db:"code"              json:"code"`
	Name             string     `db:"name"              json:"name"`
	Symbol           *string    `db:"symbol"            json:"symbol,omitempty"`
	BaseUnitID       *uuid.UUID `db:"base_unit_id"      json:"base_unit_id,omitempty"`
	ConversionFactor *float64   `db:"conversion_factor" json:"conversion_factor,omitempty"`
	IsActive         bool       `db:"is_active"         json:"is_active"`
}

type StorageCondition struct {
	common.BaseModel
	Code           string   `db:"code"            json:"code"`
	Name           string   `db:"name"            json:"name"`
	MinTempCelsius *float64 `db:"min_temp_celsius" json:"min_temp_celsius,omitempty"`
	MaxTempCelsius *float64 `db:"max_temp_celsius" json:"max_temp_celsius,omitempty"`
	Description    *string  `db:"description"     json:"description,omitempty"`
	IsActive       bool     `db:"is_active"       json:"is_active"`
}

type TaxRate struct {
	common.BaseModel
	Code        string  `db:"code"        json:"code"`
	Name        string  `db:"name"        json:"name"`
	RatePercent float64 `db:"rate_percent" json:"rate_percent"`
	IsDefault   bool    `db:"is_default"   json:"is_default"`
	IsActive    bool    `db:"is_active"    json:"is_active"`
}

type DrugGroup struct {
	common.BaseModel
	Code        string  `db:"code"        json:"code"`
	Name        string  `db:"name"        json:"name"`
	Description *string `db:"description" json:"description,omitempty"`
	IsActive    bool    `db:"is_active"   json:"is_active"`
}

type DrugClass struct {
	common.BaseModel
	Code        string     `db:"code"          json:"code"`
	Name        string     `db:"name"          json:"name"`
	DrugGroupID *uuid.UUID `db:"drug_group_id" json:"drug_group_id,omitempty"`
	Description *string    `db:"description"   json:"description,omitempty"`
	IsActive    bool       `db:"is_active"     json:"is_active"`
}

type TherapeuticClass struct {
	common.BaseModel
	Code        string     `db:"code"          json:"code"`
	Name        string     `db:"name"          json:"name"`
	DrugClassID *uuid.UUID `db:"drug_class_id" json:"drug_class_id,omitempty"`
	Description *string    `db:"description"   json:"description,omitempty"`
	IsActive    bool       `db:"is_active"     json:"is_active"`
}

type GenericMedicine struct {
	common.BaseModel
	Code               string     `db:"code"                 json:"code"`
	Name               string     `db:"name"                 json:"name"`
	Description        *string    `db:"description"          json:"description,omitempty"`
	TherapeuticClassID *uuid.UUID `db:"therapeutic_class_id" json:"therapeutic_class_id,omitempty"`
	IsControlled       bool       `db:"is_controlled"        json:"is_controlled"`
	IsActive           bool       `db:"is_active"            json:"is_active"`
}
