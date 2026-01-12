package models

type AuditLog struct {
	BaseModel

	UserID uint
	Action string
	Module string
	IP     string
}
