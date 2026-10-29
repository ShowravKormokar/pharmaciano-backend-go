package customer

import (
	"backend/internal/common"
	"time"

	"github.com/google/uuid"
)

// Customer represents a minimal customer identity owned by an organization.
// Financial fields (CreditLimit, CurrentDue) are not authoritative in this module;
// ownership lies with Ledger. LoyaltyPoints is exposed as-is per the provided model.
type Customer struct {
	common.BaseModel
	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`

	Code          *string    `db:"code"           json:"code,omitempty"` // auto-generated
	Name          string     `db:"name"           json:"name"`
	Phone         *string    `db:"phone"          json:"phone,omitempty"`
	Email         *string    `db:"email"          json:"email,omitempty"`
	Address       *string    `db:"address"        json:"address,omitempty"`
	City          *string    `db:"city"           json:"city,omitempty"`
	DateOfBirth   *time.Time `db:"date_of_birth"  json:"date_of_birth,omitempty"`
	Gender        *string    `db:"gender"         json:"gender,omitempty"`
	BloodGroup    *string    `db:"blood_group"    json:"blood_group,omitempty"`
	LoyaltyPoints int        `db:"loyalty_points" json:"loyalty_points"`
	CreditLimit   float64    `db:"credit_limit"   json:"credit_limit"`
	CurrentDue    float64    `db:"current_due"    json:"current_due"`
	Notes         *string    `db:"notes"          json:"notes,omitempty"`
	IsActive      bool       `db:"is_active"      json:"is_active"`
}
