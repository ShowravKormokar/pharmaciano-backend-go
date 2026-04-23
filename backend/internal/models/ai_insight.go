package models

import "github.com/google/uuid"

type AIInsight struct {
	BaseModel

	BranchID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	MedicineID     *uuid.UUID `gorm:"type:uuid;index"`
	InsightType    string     `gorm:"not null"` // demand, trend, stock
	PredictedValue float64
	Confidence     float64
	Recommendation string

	// Relations
	Branch   Branch
	Medicine *Medicine
}
