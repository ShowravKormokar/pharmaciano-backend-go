package models

import "time"

type AIInsight struct {
    ID               uint      `gorm:"primaryKey"`
    BranchID         uint      `gorm:"index;not null"`
    MedicineID       uint      `gorm:"index"`
    InsightType      string    `gorm:"not null"` // demand, trend, stock
    PredictedValue   float64   
    Confidence       float64   
    Recommendation   string    
    GeneratedAt      time.Time
    
    // Relations
    Branch   Branch   `gorm:"foreignKey:BranchID"`
    Medicine Medicine `gorm:"foreignKey:MedicineID"`
}