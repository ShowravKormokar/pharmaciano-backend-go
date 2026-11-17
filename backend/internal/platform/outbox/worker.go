package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

type taskPayload struct {
	EventID    uuid.UUID `json:"event_id"`
	LeaseToken uuid.UUID `json:"lease_token"`
}

type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func Permanent(err error) error {
	if err == nil {
		return errors.New("outbox: Permanent called with nil error")
	}
	return &permanentError{err: err}
}

type TaskHandler struct {
	database *db.DB
	store    *Store
	cfg      config.OutboxConfig
	handlers map[string]Handler
	log      *zap.Logger
	metrics  *telemetry.Metrics
}

func NewTaskHandler(database *db.DB, cfg config.OutboxConfig, registry *Registry, log *zap.Logger, metrics *telemetry.Metrics) (*TaskHandler, error) {
	if database == nil {
		return nil, errors.New("outbox: database is required")
	}
	if !cfg.Enabled {
		return nil, errors.New("outbox: worker is disabled")
	}
	if cfg.PollInterval <= 0 ||
		cfg.DispatchBatchSize < 1 || cfg.MaxAttempts < 1 || cfg.MaxPayloadBytes < 1 ||
		cfg.RetryMin <= 0 || cfg.RetryMax < cfg.RetryMin ||
		cfg.HandlerTimeout <= 0 || cfg.LeaseDuration <= cfg.HandlerTimeout+10*time.Second {
		return nil, errors.New("outbox: invalid worker configuration")
	}
	if registry == nil || len(registry.handlers) == 0 {
		return nil, errors.New("outbox: at least one event handler is required when enabled")
	}
	store, err := NewStore(database, cfg.MaxPayloadBytes)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = zap.NewNop()
	}
	handlers := make(map[string]Handler, len(registry.handlers))
	for eventType, handler := range registry.handlers {
		handlers[eventType] = handler
	}
	return &TaskHandler{
		database: database,
		store:    store,
		cfg:      cfg,
		handlers: handlers,
		log:      log,
		metrics:  metrics,
	}, nil
}

func (h *TaskHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return errors.New("outbox: task is nil")
	}
	var payload taskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("outbox: decode task payload: %w", err)
	}
	if payload.EventID == uuid.Nil || payload.LeaseToken == uuid.Nil {
		return errors.New("outbox: task payload has an empty event ID or lease token")
	}

	delivery := &OutboxEvent{
		Event:      Event{ID: payload.EventID},
		LeaseToken: payload.LeaseToken,
	}
	handlerToken := uuid.New()
	attempt, err := h.store.beginAttempt(ctx, delivery, handlerToken, h.cfg.LeaseDuration, h.cfg.MaxAttempts)
	if err != nil {
		if errors.Is(err, ErrLeaseLost) {
			h.observe("unknown", "lease_lost", 0)
			return nil
		}
		if errors.Is(err, ErrAttemptsExhausted) {
			h.observe("unknown", "failed", 0)
			return nil
		}
		return err
	}
	delivery.LeaseToken = handlerToken
	delivery.RetryCount = attempt

	started := time.Now()
	var eventType string
	options := db.DefaultTxOptions
	options.Timeout = h.cfg.HandlerTimeout
	err = h.database.WithTxOptions(ctx, options, func(txCtx context.Context) error {
		event, err := h.store.loadClaimed(txCtx, delivery)
		if err != nil {
			return err
		}
		eventType = event.EventType
		handler, ok := h.handlers[event.EventType]
		if !ok {
			return Permanent(fmt.Errorf("outbox: no handler registered for %q", event.EventType))
		}
		if err := handler(txCtx, event); err != nil {
			return err
		}
		return h.store.complete(txCtx, delivery)
	})
	if err == nil {
		h.observe(eventType, "completed", time.Since(started))
		return nil
	}
	if errors.Is(err, ErrLeaseLost) {
		h.observe(eventType, "lease_lost", time.Since(started))
		return nil
	}
	if ctx.Err() != nil {
		return err
	}

	class := "handler_error"
	permanent := false
	var terminal *permanentError
	if errors.As(err, &terminal) {
		class = "permanent_error"
		permanent = true
	}

	failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failed, failErr := h.store.fail(
		failureCtx, delivery, h.cfg.MaxAttempts,
		retryDelay(delivery.RetryCount, h.cfg.RetryMin, h.cfg.RetryMax),
		class, permanent,
	)
	if errors.Is(failErr, ErrLeaseLost) {
		h.observe(eventType, "lease_lost", time.Since(started))
		return nil
	}
	if failErr != nil {
		return fmt.Errorf("outbox: persist handler failure: %w", failErr)
	}
	result := "retry"
	if failed {
		result = "failed"
	}
	h.observe(eventType, result, time.Since(started))
	h.log.Warn("outbox handler failed",
		zap.String("event_id", delivery.ID.String()),
		zap.String("event_type", eventType),
		zap.Int("attempt", delivery.RetryCount),
		zap.String("error_class", class),
		zap.String("result", result),
	)
	return nil
}

func (h *TaskHandler) observe(eventType, result string, duration time.Duration) {
	if h.metrics == nil {
		return
	}
	if eventType == "" {
		eventType = "unresolved"
	}
	h.metrics.OutboxEventsTotal.WithLabelValues(eventType, result).Inc()
	if duration > 0 {
		h.metrics.OutboxProcessingDuration.WithLabelValues(eventType, result).Observe(duration.Seconds())
	}
}

func retryDelay(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	delay := minDelay
	for i := 1; i < attempt && delay < maxDelay; i++ {
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	jittered := time.Duration(float64(delay) * (0.5 + rand.Float64()))
	if jittered > maxDelay {
		return maxDelay
	}
	return jittered
}
