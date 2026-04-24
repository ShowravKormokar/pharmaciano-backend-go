package models

type Role struct {
	BaseModel
	Name        string `gorm:"uniqueIndex;not null"`
	Description string
	IsActive    bool `gorm:"default:true"`
	IsSystem    bool `gorm:"default:false"` // true = can't be deleted

	Permissions []Permission `gorm:"many2many:role_permissions;constraint:OnDelete:CASCADE"`
}
