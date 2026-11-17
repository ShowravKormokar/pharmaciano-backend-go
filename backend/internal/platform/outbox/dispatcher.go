package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"backend/internal/common/constants"
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

type Enqueuer interface {
	EnqueueContext(context.Context, *asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error)
}

type Dispatcher struct {
	database   *db.DB
	store      *Store
	cfg        config.OutboxConfig
	jobsCfg    config.AsynqConfig
	client     Enqueuer
	eventTypes []string
	log        *zap.Logger
	metrics    *telemetry.Metrics
}

func NewDispatcher(database *db.DB, cfg config.OutboxConfig, jobsCfg config.AsynqConfig, registry *Registry, client Enqueuer, log *zap.Logger, metrics *telemetry.Metrics) (*Dispatcher, error) {
	if database == nil || client == nil {
		return nil, errors.New("outbox: database and job client are required")
	}
	if !cfg.Enabled {
		return nil, errors.New("outbox: dispatcher is disabled")
	}
	if jobsCfg.RedisDB < 0 || jobsCfg.Retry.MaxRetries < 0 ||
		jobsCfg.Queues[constants.QueueDefault] < 1 {
		return nil, errors.New("outbox: Asynq Redis DB, retries, and default queue must be configured")
	}
	if cfg.PollInterval <= 0 || cfg.DispatchBatchSize < 1 ||
		cfg.MaxAttempts < 1 || cfg.MaxPayloadBytes < 1 ||
		cfg.RetryMin <= 0 || cfg.RetryMax < cfg.RetryMin ||
		cfg.HandlerTimeout <= 0 || cfg.LeaseDuration <= cfg.HandlerTimeout+10*time.Second {
		return nil, errors.New("outbox: invalid dispatcher configuration")
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
	eventTypes := make([]string, 0, len(registry.handlers))
	for eventType := range registry.handlers {
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)
	return &Dispatcher{
		database:   database,
		store:      store,
		cfg:        cfg,
		jobsCfg:    jobsCfg,
		client:     client,
		eventTypes: eventTypes,
		log:        log,
		metrics:    metrics,
	}, nil
}

func (d *Dispatcher) Run(ctx context.Context) error {
	lastDepthUpdate := time.Time{}
	for ctx.Err() == nil {
		didWork := false
		for i := 0; i < d.cfg.DispatchBatchSize; i++ {
			dispatched, err := d.dispatchOne(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				d.log.Error("outbox dispatch failed", zap.Error(err))
				break
			}
			if !dispatched {
				break
			}
			didWork = true
		}
		if d.metrics != nil && time.Since(lastDepthUpdate) >= 15*time.Second {
			if err := d.refreshQueueDepth(ctx); err != nil && ctx.Err() == nil {
				d.log.Warn("outbox queue depth refresh failed", zap.Error(err))
			}
			lastDepthUpdate = time.Now()
		}
		if !didWork && !wait(ctx, d.cfg.PollInterval) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (d *Dispatcher) dispatchOne(ctx context.Context) (bool, error) {
	token := uuid.New()
	var delivery *OutboxEvent
	if err := d.database.WithTx(ctx, func(txCtx context.Context) error {
		var err error
		delivery, err = d.store.claim(
			txCtx, d.eventTypes, token, d.cfg.LeaseDuration, d.cfg.MaxAttempts,
		)
		return err
	}); err != nil {
		return false, err
	}
	if delivery == nil {
		return false, nil
	}

	payload, err := json.Marshal(taskPayload{
		EventID: delivery.ID, LeaseToken: delivery.LeaseToken,
	})
	if err == nil {
		task := asynq.NewTask(constants.TaskOutboxPublish, payload)
		_, err = d.client.EnqueueContext(ctx, task,
			asynq.Queue(constants.QueueDefault),
			asynq.MaxRetry(d.jobsCfg.Retry.MaxRetries),
			asynq.Timeout(d.cfg.HandlerTimeout+10*time.Second),
		)
	}
	if err != nil {
		failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		releaseErr := d.store.releaseDispatchFailure(
			failureCtx, delivery,
			retryDelay(delivery.DispatchCount, d.cfg.RetryMin, d.cfg.RetryMax),
		)
		if errors.Is(releaseErr, ErrLeaseLost) {
			d.observe(delivery.EventType, "dispatch_ambiguous")
			return true, nil
		}
		if releaseErr != nil {
			return true, fmt.Errorf("outbox: release failed dispatch: %w", releaseErr)
		}
		d.observe(delivery.EventType, "broker_error")
		d.log.Warn("outbox event enqueue failed",
			zap.String("event_id", delivery.ID.String()),
			zap.String("event_type", delivery.EventType),
			zap.Int("dispatch_count", delivery.DispatchCount),
			zap.Error(err),
		)
		return true, nil
	}

	d.observe(delivery.EventType, "dispatched")
	return true, nil
}

func (d *Dispatcher) refreshQueueDepth(ctx context.Context) error {
	depth, err := d.store.queueDepth(ctx, d.eventTypes)
	if err != nil {
		return err
	}
	for _, eventType := range d.eventTypes {
		d.metrics.OutboxQueueDepth.WithLabelValues(eventType).Set(float64(depth[eventType]))
	}
	return nil
}

func (d *Dispatcher) observe(eventType, result string) {
	if d.metrics != nil {
		d.metrics.OutboxEventsTotal.WithLabelValues(eventType, result).Inc()
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
