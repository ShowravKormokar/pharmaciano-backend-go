package models

type Organization struct {
	BaseModel
	Name              string `gorm:"not null"`
	TradeLicenseNo    string
	DrugLicenseNo     string
	VATRegistrationNo string
	SubscriptionPlan  string `gorm:"type:varchar(20);default:'free'"`
	IsActive          bool   `gorm:"default:true"`

	ContactPhone string
	ContactEmail string
	AddressLine1 string
	AddressLine2 string
	City         string
	State        string
	PostalCode   string
	Country      string

	Branches []Branch `gorm:"foreignKey:OrganizationID"`
}
