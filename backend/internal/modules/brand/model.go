package brand

import (
	"github.com/google/uuid"

	"backend/internal/common"
)

// Brand represents a pharmaceutical brand in the existing brands table.
type Brand struct {
	common.BaseModel
	Code            string    `db:"code" json:"code"`
	Name            string    `db:"name" json:"name"`
	ManufacturerID  uuid.UUID `db:"manufacturer_id" json:"manufacturer_id"`
	CountryOfOrigin *string   `db:"country_of_origin" json:"country_of_origin,omitempty"`
	Description     *string   `db:"description" json:"description,omitempty"`
	IsActive        bool      `db:"is_active" json:"is_active"`
}
