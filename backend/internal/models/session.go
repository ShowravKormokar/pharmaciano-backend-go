package models

import "time"

type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	DeviceName string    `json:"device_name"`
	DeviceFp   string    `json:"device_fp"`
	IP         string    `json:"ip"`
	Location   string    `json:"location"`
	UserAgent  string    `json:"user_agent"`
	LastSeen   time.Time `json:"last_seen"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
