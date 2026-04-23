package models

type Supplier struct {
	BaseModel

	Name          string
	ContactPerson string
	Phone         string
	Email         string
	Address       string

	// Relations
	Purchases []Purchase
}
