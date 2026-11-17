package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

var (
	ErrTransactionRequired = errors.New("outbox: append requires the source transaction")
	ErrEventIDConflict     = errors.New("outbox: event ID already exists with different contents")
	ErrLeaseLost           = errors.New("outbox: delivery lease lost")
	ErrAttemptsExhausted   = errors.New("outbox: handler attempts exhausted")
)

type Event struct {
	ID               uuid.UUID       `json:"id"`
	OrganizationID   uuid.UUID       `json:"organization_id"`
	SourceModule     string          `json:"source_module"`
	BranchID         *uuid.UUID      `json:"branch_id,omitempty"`
	CorrelationID    *uuid.UUID      `json:"correlation_id,omitempty"`
	CausationID      *uuid.UUID      `json:"causation_id,omitempty"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      uuid.UUID       `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	EventType        string          `json:"event_type"`
	SchemaVersion    int             `json:"schema_version"`
	Payload          json.RawMessage `json:"payload"`
	OccurredAt       time.Time       `json:"occurred_at"`
}

type OutboxEvent struct {
	Event
	Status         Status
	RetryCount     int
	DispatchCount  int
	LastErrorClass *string
	NextRetryAt    time.Time
	LeaseToken     uuid.UUID
	LeaseExpiresAt *time.Time
	ProcessedAt    *time.Time
	FailedAt       *time.Time
	CreatedAt      time.Time
}

func (e Event) validate(maxPayloadBytes int) error {
	if e.ID == uuid.Nil {
		return errors.New("outbox: event ID is required")
	}
	if e.OrganizationID == uuid.Nil {
		return errors.New("outbox: organization ID is required")
	}
	if !validName(e.SourceModule, 64, false) {
		return errors.New("outbox: source module is invalid")
	}
	if e.BranchID != nil && *e.BranchID == uuid.Nil {
		return errors.New("outbox: branch ID cannot be nil")
	}
	if e.CorrelationID != nil && *e.CorrelationID == uuid.Nil {
		return errors.New("outbox: correlation ID cannot be nil")
	}
	if e.CausationID != nil && *e.CausationID == uuid.Nil {
		return errors.New("outbox: causation ID cannot be nil")
	}
	if e.AggregateID == uuid.Nil {
		return errors.New("outbox: aggregate ID is required")
	}
	if !validName(e.AggregateType, 64, false) {
		return errors.New("outbox: aggregate type is invalid")
	}
	if !validName(e.EventType, 128, true) {
		return errors.New("outbox: event type is invalid")
	}
	if e.AggregateVersion <= 0 {
		return errors.New("outbox: aggregate version must be positive")
	}
	if e.SchemaVersion <= 0 {
		return errors.New("outbox: schema version must be positive")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("outbox: occurrence time is required")
	}
	if maxPayloadBytes <= 0 || len(e.Payload) == 0 || len(e.Payload) > maxPayloadBytes {
		return fmt.Errorf("outbox: payload size must be between 1 and %d bytes", maxPayloadBytes)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &object); err != nil || object == nil {
		return errors.New("outbox: payload must be a JSON object")
	}
	return nil
}

func validName(value string, max int, allowSeparators bool) bool {
	if len(value) == 0 || len(value) > max || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, c := range value[1:] {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if allowSeparators && (c == '.' || c == '-' || c == '_') {
			continue
		}
		if !allowSeparators && c == '_' {
			continue
		}
		return false
	}
	return true
}

func normalizeErrorClass(class string) string {
	class = strings.ToLower(strings.TrimSpace(class))
	if !validName(class, 48, true) {
		return "handler_error"
	}
	return class
}
