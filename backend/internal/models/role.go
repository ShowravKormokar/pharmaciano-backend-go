package models

import "time"

type Role struct {
	ID          uint        `gorm:"primaryKey"`
	Name        string      `gorm:"unique;not null"`
	Permissions Permissions `gorm:"embedded"`
	Description string
	CreatedAt   time.Time

	// Relations
	Users []User
}

type Permissions struct {
	Users      []string `gorm:"type:text[]"`
	Inventory  []string `gorm:"type:text[]"`
	Sales      []string `gorm:"type:text[]"`
	Purchase   []string `gorm:"type:text[]"`
	Accounting []string `gorm:"type:text[]"`
	Reports    []string `gorm:"type:text[]"`
	AI         []string `gorm:"type:text[]"`
	Settings   []string `gorm:"type:text[]"`
}
