package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validTestEvent() Event {
	return Event{
		ID:               uuid.New(),
		OrganizationID:   uuid.New(),
		SourceModule:     "sales",
		AggregateType:    "sale",
		AggregateID:      uuid.New(),
		AggregateVersion: 1,
		EventType:        "sale.completed",
		SchemaVersion:    1,
		Payload:          json.RawMessage(`{"total_minor":1250}`),
		OccurredAt:       time.Now().UTC(),
	}
}

func TestEventValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Event)
		maxSize int
	}{
		{name: "valid", maxSize: 1024},
		{name: "missing organization", maxSize: 1024, mutate: func(e *Event) { e.OrganizationID = uuid.Nil }},
		{name: "missing source module", maxSize: 1024, mutate: func(e *Event) { e.SourceModule = "" }},
		{name: "invalid event type", maxSize: 1024, mutate: func(e *Event) { e.EventType = "Sale Completed" }},
		{name: "missing version", maxSize: 1024, mutate: func(e *Event) { e.AggregateVersion = 0 }},
		{name: "non-object payload", maxSize: 1024, mutate: func(e *Event) { e.Payload = json.RawMessage(`[]`) }},
		{name: "oversized payload", maxSize: 2},
		{name: "missing occurrence time", maxSize: 1024, mutate: func(e *Event) { e.OccurredAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := validTestEvent()
			if tt.mutate != nil {
				tt.mutate(&event)
			}
			err := event.validate(tt.maxSize)
			if tt.name == "valid" && err != nil {
				t.Fatalf("valid event rejected: %v", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
}

func TestRegistryRejectsInvalidAndDuplicateHandlers(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("sale.completed", func(_ context.Context, _ Event) error { return nil }); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	if err := registry.Register("sale.completed", func(_ context.Context, _ Event) error { return nil }); err == nil {
		t.Fatal("duplicate handler was accepted")
	}
	if err := registry.Register("invalid event", func(_ context.Context, _ Event) error { return nil }); err == nil {
		t.Fatal("invalid event type was accepted")
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	minDelay := time.Second
	maxDelay := time.Minute
	for attempt := 1; attempt <= 20; attempt++ {
		delay := retryDelay(attempt, minDelay, maxDelay)
		if delay < minDelay/2 || delay > maxDelay {
			t.Fatalf("attempt %d delay %s outside bounds", attempt, delay)
		}
	}
}
