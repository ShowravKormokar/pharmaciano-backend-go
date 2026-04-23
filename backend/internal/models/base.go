package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BaseModel struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CreatedAt time.Time      `gorm:"autoCreateTime;not null"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime;not null"`
	DeletedAt gorm.DeletedAt `gorm:"index"` // Optional: for soft delete
}

// BeforeCreate generates UUID if not set
func (b *BaseModel) BeforeCreate(tx *gorm.DB) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return nil
}
