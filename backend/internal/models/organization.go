package models

type Organization struct {
	BaseModel

	Name              string `gorm:"not null"`
	TradeLicenseNo    string
	DrugLicenseNo     string
	VATRegistrationNo string
	SubscriptionPlan  string
	IsActive          bool `gorm:"default:true"`

	ContactPhone string
	ContactEmail string

	Branches []Branch
	Users    []User
}
