package models

import "time"

type Organization struct {
    ID                uint      `gorm:"primaryKey"`
    Name              string    `gorm:"not null"`
    TradeLicenseNo    string    
    DrugLicenseNo     string    
    VATRegistrationNo string    
    Address           string    
    Contact           Contact   `gorm:"embedded"`
    SubscriptionPlan  string    
    IsActive          bool      `gorm:"default:true"`
    CreatedAt         time.Time
    
    // Relations
    Branches []Branch
}

type Contact struct {
    Phone string
    Email string
}