package models

import "time"

type Session struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	DeviceName  string    `json:"device_name"`  // e.g. "Chrome on Windows"
	DeviceFp    string    `json:"device_fp"`    // hash for binding
	IP          string    `json:"ip"`
	Location    string    `json:"location"`     // city, country
	UserAgent   string    `json:"user_agent"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}