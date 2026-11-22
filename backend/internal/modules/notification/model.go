package notification

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityNormal   Priority = "normal"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical:
		return true
	}
	return false
}

type BroadcastScope string

const (
	BroadcastScopeOrg    BroadcastScope = "org"
	BroadcastScopeBranch BroadcastScope = "branch"
	BroadcastScopeRole   BroadcastScope = "role"
	BroadcastScopeUser   BroadcastScope = "user"
)

func (s BroadcastScope) Valid() bool {
	switch s {
	case BroadcastScopeOrg, BroadcastScopeBranch, BroadcastScopeRole, BroadcastScopeUser:
		return true
	}
	return false
}

type BroadcastStatus string

const (
	BroadcastStatusPending    BroadcastStatus = "pending"
	BroadcastStatusProcessing BroadcastStatus = "processing"
	BroadcastStatusCompleted  BroadcastStatus = "completed"
	BroadcastStatusPartial    BroadcastStatus = "partial"
	BroadcastStatusFailed     BroadcastStatus = "failed"
	BroadcastStatusCancelled  BroadcastStatus = "cancelled"
)

type BatchStatus string

const (
	BatchStatusPending    BatchStatus = "pending"
	BatchStatusProcessing BatchStatus = "processing"
	BatchStatusCompleted  BatchStatus = "completed"
	BatchStatusRetry      BatchStatus = "retry"
	BatchStatusFailed     BatchStatus = "failed"
	BatchStatusCancelled  BatchStatus = "cancelled"
)

type ReceiptResult string

const (
	ReceiptResultCreated    ReceiptResult = "created"
	ReceiptResultSuppressed ReceiptResult = "suppressed"
	ReceiptResultIgnored    ReceiptResult = "ignored"
)

type Notification struct {
	common.BaseModel

	OrganizationID      uuid.UUID      `db:"organization_id" json:"-"`
	BranchID            *uuid.UUID     `db:"branch_id" json:"-"`
	EventID             *uuid.UUID     `db:"event_id" json:"-"`
	SourceModule        string         `db:"source_module" json:"-"`
	SourceEventType     string         `db:"source_event_type" json:"-"`
	SourceAggregateType string         `db:"source_aggregate_type" json:"-"`
	SourceAggregateID   *uuid.UUID     `db:"source_aggregate_id" json:"-"`
	SourceAggregateVer  *int64         `db:"source_aggregate_version" json:"-"`
	Type                string         `db:"type" json:"-"`
	Priority            Priority       `db:"priority" json:"-"`
	Title               string         `db:"title" json:"-"`
	Message             string         `db:"message" json:"-"`
	Data                common.JSONMap `db:"data" json:"-"`
	ActionURL           *string        `db:"action_url" json:"-"`
	CreatedByUserID     *uuid.UUID     `db:"created_by_user_id" json:"-"`
	ContentVersion      int            `db:"content_version" json:"-"`
	ExpiresAt           *time.Time     `db:"expires_at" json:"-"`
}

type NotificationRecipient struct {
	ID             uuid.UUID  `db:"id" json:"-"`
	OrganizationID uuid.UUID  `db:"organization_id" json:"-"`
	NotificationID uuid.UUID  `db:"notification_id" json:"-"`
	UserID         uuid.UUID  `db:"user_id" json:"-"`
	ReadAt         *time.Time `db:"read_at" json:"-"`
	DismissedAt    *time.Time `db:"dismissed_at" json:"-"`
	CreatedAt      time.Time  `db:"created_at" json:"-"`
}

type NotificationEventReceipt struct {
	OrganizationID uuid.UUID     `db:"organization_id" json:"-"`
	ConsumerName   string        `db:"consumer_name" json:"-"`
	EventID        uuid.UUID     `db:"event_id" json:"-"`
	EventType      string        `db:"event_type" json:"-"`
	ProcessedAt    time.Time     `db:"processed_at" json:"-"`
	Result         ReceiptResult `db:"result" json:"-"`
	NotificationID *uuid.UUID    `db:"notification_id" json:"-"`
	PayloadHash    *string       `db:"payload_hash" json:"-"`
}

type NotificationBroadcast struct {
	common.BaseModel

	OrganizationID  uuid.UUID       `db:"organization_id" json:"-"`
	CreatorUserID   uuid.UUID       `db:"created_by_user_id" json:"-"`
	Scope           BroadcastScope  `db:"scope" json:"-"`
	TargetID        *uuid.UUID      `db:"target_id" json:"-"`
	Title           string          `db:"title" json:"-"`
	Message         string          `db:"message" json:"-"`
	Priority        Priority        `db:"priority" json:"-"`
	ActionURL       *string         `db:"action_url" json:"-"`
	Status          BroadcastStatus `db:"status" json:"-"`
	RequestedAt     time.Time       `db:"requested_at" json:"-"`
	StartedAt       *time.Time      `db:"started_at" json:"-"`
	FinishedAt      *time.Time      `db:"finished_at" json:"-"`
	AudienceCount   int             `db:"audience_count" json:"-"`
	InsertedCount   int             `db:"inserted_count" json:"-"`
	SuppressedCount int             `db:"suppressed_count" json:"-"`
	FailedCount     int             `db:"failed_count" json:"-"`
	IdempotencyKey  string          `db:"idempotency_key" json:"-"`
	RequestHash     string          `db:"request_hash" json:"-"`
	FailureCode     *string         `db:"failure_code" json:"-"`
}

type NotificationBroadcastBatch struct {
	common.BaseModel

	OrganizationID  uuid.UUID   `db:"organization_id" json:"-"`
	BroadcastID     uuid.UUID   `db:"broadcast_id" json:"-"`
	BatchNumber     int         `db:"batch_number" json:"-"`
	Status          BatchStatus `db:"status" json:"-"`
	LeaseOwner      *string     `db:"lease_owner" json:"-"`
	LeaseUntil      *time.Time  `db:"lease_until" json:"-"`
	LeaseGeneration int         `db:"lease_generation" json:"-"`
	AttemptCount    int         `db:"attempt_count" json:"-"`
	NextAttemptAt   *time.Time  `db:"next_attempt_at" json:"-"`
	LastErrorCode   *string     `db:"last_error_code" json:"-"`
}

type NotificationPreference struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"-"`
	UserID         uuid.UUID `db:"user_id" json:"-"`
	Category       string    `db:"category" json:"-"`
	Channel        string    `db:"channel" json:"-"`
	Enabled        bool      `db:"enabled" json:"-"`
	Version        int       `db:"version" json:"-"`
}

type CategoryPolicy struct {
	Category         string   `db:"category" json:"-"`
	OwnerModule      string   `db:"owner_module" json:"-"`
	DefaultPriority  Priority `db:"default_priority" json:"-"`
	Mandatory        bool     `db:"mandatory" json:"-"`
	PreferencePolicy string   `db:"preference_policy" json:"-"`
	ContentVersion   int      `db:"content_version" json:"-"`
}
