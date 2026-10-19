package brand

import (
	"github.com/google/uuid"

	"backend/internal/common"
)

// Brand represents a pharmaceutical brand in the existing brands table.
type Brand struct {
	common.BaseModel
	OrganizationID  uuid.UUID `db:"organization_id"`
	Code            string    `db:"code"`
	Name            string    `db:"name"`
	ManufacturerID  uuid.UUID `db:"manufacturer_id"`
	CountryOfOrigin *string   `db:"country_of_origin"`
	Description     *string   `db:"description"`
	IsActive        bool      `db:"is_active"`
}
