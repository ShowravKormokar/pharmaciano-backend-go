package models

type Customer struct {
	BaseModel

	Name    string
	Phone   string
	Email   string
	Address string

	// Relations
	Sales []Sale
}
