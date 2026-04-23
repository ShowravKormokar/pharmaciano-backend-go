package models

import "github.com/google/uuid"

type AuditLog struct {
	BaseModel

	UserID uuid.UUID
	Action string
	Module string
	IP     string
}
