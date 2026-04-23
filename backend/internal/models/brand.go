package models

type Brand struct {
	BaseModel

	Name         string
	Manufacturer string
	Country      string

	// Relations
	Medicines []Medicine
}
