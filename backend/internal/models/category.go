package models

type Category struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"unique;not null"`
    Description string
    IsActive    bool   `gorm:"default:true"`
    
    // Relations
    Medicines []Medicine
}