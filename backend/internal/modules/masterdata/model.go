package masterdata

import (
	"backend/internal/common"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// ===== File: internal/modules/masterdata/model.go ==========================
// ---------------------------------------------------------------------------

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

type Manufacturer struct {
	common.BaseModel
	Code          string  `db:"code"           json:"code"`
	Name          string  `db:"name"           json:"name"`
	Country       *string `db:"country"        json:"country,omitempty"`
	Address       *string `db:"address"        json:"address,omitempty"`
	ContactPerson *string `db:"contact_person" json:"contact_person,omitempty"`
	Phone         *string `db:"phone"          json:"phone,omitempty"`
	Email         *string `db:"email"          json:"email,omitempty"`
	Website       *string `db:"website"        json:"website,omitempty"`
	DrugLicenseNo *string `db:"drug_license_no" json:"drug_license_no,omitempty"`
	IsActive      bool    `db:"is_active"      json:"is_active"`
}

type Brand struct {
	common.BaseModel
	Code            string    `db:"code"               json:"code"`
	Name            string    `db:"name"               json:"name"`
	ManufacturerID  uuid.UUID `db:"manufacturer_id"    json:"manufacturer_id"`
	CountryOfOrigin *string   `db:"country_of_origin"  json:"country_of_origin,omitempty"`
	Description     *string   `db:"description"        json:"description,omitempty"`
	IsActive        bool      `db:"is_active"          json:"is_active"`
}

type Supplier struct {
	common.BaseModel
	Code             string   `db:"code"              json:"code"`
	Name             string   `db:"name"              json:"name"`
	ContactPerson    *string  `db:"contact_person"    json:"contact_person,omitempty"`
	Phone            *string  `db:"phone"             json:"phone,omitempty"`
	Email            *string  `db:"email"             json:"email,omitempty"`
	Address          *string  `db:"address"           json:"address,omitempty"`
	City             *string  `db:"city"              json:"city,omitempty"`
	Country          *string  `db:"country"           json:"country,omitempty"`
	DrugLicenseNo    *string  `db:"drug_license_no"   json:"drug_license_no,omitempty"`
	TIN              *string  `db:"tin"               json:"tin,omitempty"`
	CreditLimit      *float64 `db:"credit_limit"      json:"credit_limit,omitempty"`
	CreditPeriodDays *int     `db:"credit_period_days" json:"credit_period_days,omitempty"`
	OpeningBalance   float64  `db:"opening_balance"    json:"opening_balance"`
	CurrentBalance   float64  `db:"current_balance"    json:"current_balance"`
	IsActive         bool     `db:"is_active"          json:"is_active"`
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
