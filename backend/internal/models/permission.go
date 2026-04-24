package models

type Permission struct {
	BaseModel
	Module string `gorm:"not null;uniqueIndex:idx_module_action"`
	Action string `gorm:"not null;uniqueIndex:idx_module_action"`
}
