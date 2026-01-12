package models

type Permission struct {
	BaseModel

	RoleID uint   `gorm:"not null;index"`
	Module string `gorm:"not null"` // users, inventory, sales
	Action string `gorm:"not null"` // create, read, update, delete

	Role Role
}
