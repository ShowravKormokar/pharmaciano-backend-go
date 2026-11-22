package notification

import (
	"time"

	"github.com/google/uuid"
)

type NotificationDTO struct {
	ID          uuid.UUID      `json:"id"`
	Type        string         `json:"type"`
	Priority    Priority       `json:"priority"`
	Title       string         `json:"title"`
	Message     string         `json:"message"`
	Data        map[string]any `json:"data,omitempty"`
	ActionURL   *string        `json:"action_url,omitempty"`
	BranchID    *uuid.UUID     `json:"branch_id,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	ReadAt      *time.Time     `json:"read_at,omitempty"`
	DismissedAt *time.Time     `json:"dismissed_at,omitempty"`
}

type UnreadCountDTO struct {
	Count     int64     `json:"count"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReadAllResponse struct {
	Affected  int64     `json:"affected"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BroadcastRequest struct {
	Scope     BroadcastScope `json:"scope" binding:"required"`
	TargetID  *uuid.UUID     `json:"target_id"`
	Title     string         `json:"title" binding:"required"`
	Message   string         `json:"message" binding:"required"`
	Priority  Priority       `json:"priority"`
	ActionURL *string        `json:"action_url"`
}

type BroadcastResponse struct {
	ID     uuid.UUID       `json:"id"`
	Status BroadcastStatus `json:"status"`
}

type BroadcastDetailDTO struct {
	ID              uuid.UUID       `json:"id"`
	Scope           BroadcastScope  `json:"scope"`
	TargetID        *uuid.UUID      `json:"target_id,omitempty"`
	Title           string          `json:"title"`
	Message         string          `json:"message"`
	Priority        Priority        `json:"priority"`
	ActionURL       *string         `json:"action_url,omitempty"`
	Status          BroadcastStatus `json:"status"`
	RequestedAt     time.Time       `json:"requested_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	AudienceCount   int             `json:"audience_count"`
	InsertedCount   int             `json:"inserted_count"`
	SuppressedCount int             `json:"suppressed_count"`
	FailedCount     int             `json:"failed_count"`
}

type InboxFilter struct {
	IsRead   *bool
	Type     string
	Priority Priority
	From     *time.Time
	To       *time.Time
	Limit    int
	Cursor   string
}
