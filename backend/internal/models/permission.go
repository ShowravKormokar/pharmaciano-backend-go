package models

import "github.com/google/uuid"

type Permission struct {
	BaseModel

	RoleID uuid.UUID `gorm:"type:uuid;not null;index"`
	Module string    `gorm:"not null"` // users, inventory, sales
	Action string    `gorm:"not null"` // create, read, update, delete

	Role Role
}
