package models

import "time"

type Supplier struct {
    ID            uint      `gorm:"primaryKey"`
    Name          string    `gorm:"not null"`
    ContactPerson string    
    Phone         string    
    Email         string    
    Address       string    
    CreatedAt     time.Time
    
    // Relations
    Purchases []Purchase
}