package outbox

import (
	"context"
	"errors"
	"fmt"
)

type Handler func(ctx context.Context, event Event) error

type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(eventType string, handler Handler) error {
	if r == nil {
		return errors.New("outbox: registry is nil")
	}
	if !validName(eventType, 128, true) {
		return fmt.Errorf("outbox: invalid event type %q", eventType)
	}
	if handler == nil {
		return fmt.Errorf("outbox: handler for %q is nil", eventType)
	}
	if _, exists := r.handlers[eventType]; exists {
		return fmt.Errorf("outbox: handler for %q is already registered", eventType)
	}
	r.handlers[eventType] = handler
	return nil
}
