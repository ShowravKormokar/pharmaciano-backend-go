package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/platform/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	database        *db.DB
	maxPayloadBytes int
}

func NewStore(database *db.DB, maxPayloadBytes int) (*Store, error) {
	if database == nil {
		return nil, errors.New("outbox: database is required")
	}
	if maxPayloadBytes <= 0 {
		return nil, errors.New("outbox: maximum payload size must be positive")
	}
	return &Store{database: database, maxPayloadBytes: maxPayloadBytes}, nil
}

// Append writes the event through the transaction already attached to ctx.
// The event and its source fact therefore commit or roll back together.
func (s *Store) Append(ctx context.Context, event Event) error {
	if !s.database.InTransaction(ctx) {
		return ErrTransactionRequired
	}
	if err := event.validate(s.maxPayloadBytes); err != nil {
		return err
	}
	const insert = `
		INSERT INTO outbox_events (
			id, organization_id, source_module, branch_id, correlation_id,
			causation_id, aggregate_type, aggregate_id, aggregate_version,
			event_type, schema_version, payload, occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`
	var inserted uuid.UUID
	err := s.database.FromCtx(ctx).QueryRow(ctx, insert,
		event.ID, event.OrganizationID, event.SourceModule, event.BranchID,
		event.CorrelationID, event.CausationID, event.AggregateType, event.AggregateID,
		event.AggregateVersion, event.EventType, event.SchemaVersion,
		string(event.Payload), event.OccurredAt,
	).Scan(&inserted)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("outbox: append event: %w", err)
	}

	const verify = `
		SELECT organization_id = $2
		   AND source_module = $3
		   AND branch_id IS NOT DISTINCT FROM $4
		   AND correlation_id IS NOT DISTINCT FROM $5
		   AND causation_id IS NOT DISTINCT FROM $6
		   AND aggregate_type = $7
		   AND aggregate_id = $8
		   AND aggregate_version = $9
		   AND event_type = $10
		   AND schema_version = $11
		   AND payload = $12::jsonb
		   AND occurred_at = $13
		FROM outbox_events
		WHERE id = $1`
	var same bool
	if err := s.database.FromCtx(ctx).QueryRow(ctx, verify,
		event.ID, event.OrganizationID, event.SourceModule, event.BranchID,
		event.CorrelationID, event.CausationID, event.AggregateType, event.AggregateID,
		event.AggregateVersion, event.EventType, event.SchemaVersion,
		string(event.Payload), event.OccurredAt,
	).Scan(&same); err != nil {
		return fmt.Errorf("outbox: verify duplicate event: %w", err)
	}
	if !same {
		return ErrEventIDConflict
	}
	return nil
}

func (s *Store) claim(ctx context.Context, eventTypes []string, token uuid.UUID, lease time.Duration, maxAttempts int) (*OutboxEvent, error) {
	if len(eventTypes) == 0 {
		return nil, nil
	}
	const failExhausted = `
		UPDATE outbox_events
		SET status = 'failed',
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error_class = 'attempts_exhausted',
		    last_error_at = COALESCE(last_error_at, clock_timestamp()),
		    failed_at = clock_timestamp()
		WHERE retry_count >= $1
		  AND (
		    status = 'pending'
		    OR (status = 'processing' AND lease_expires_at <= clock_timestamp())
		  )`
	if _, err := s.database.FromCtx(ctx).Exec(ctx, failExhausted, maxAttempts); err != nil {
		return nil, fmt.Errorf("outbox: quarantine exhausted events: %w", err)
	}

	const claim = `
		WITH candidate AS (
			SELECT event.id
			FROM outbox_events AS event
			WHERE event.event_type = ANY($1::text[])
			  AND event.retry_count < $2
			  AND (
			    (event.status = 'pending' AND event.next_retry_at <= clock_timestamp())
			    OR
			    (event.status = 'processing' AND event.lease_expires_at <= clock_timestamp())
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM outbox_events AS earlier
			    WHERE earlier.organization_id = event.organization_id
			      AND earlier.aggregate_type = event.aggregate_type
			      AND earlier.aggregate_id = event.aggregate_id
			      AND earlier.aggregate_version < event.aggregate_version
			      AND earlier.status <> 'completed'
			  )
			ORDER BY event.next_retry_at, event.occurred_at, event.id
			LIMIT 1
			FOR UPDATE OF event SKIP LOCKED
		)
		UPDATE outbox_events AS event
		SET status = 'processing',
		    dispatch_count = event.dispatch_count + 1,
		    lease_token = $3,
		    lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 microsecond')
		FROM candidate
		WHERE event.id = candidate.id
		RETURNING event.id, event.organization_id, event.source_module,
		          event.branch_id, event.correlation_id, event.causation_id,
		          event.aggregate_type, event.aggregate_id, event.aggregate_version, event.event_type,
		          event.schema_version, event.payload, event.occurred_at,
		          event.status, event.retry_count, event.dispatch_count,
		          event.next_retry_at,
		          event.lease_token, event.lease_expires_at,
		          event.last_error_class, event.processed_at, event.failed_at,
		          event.created_at`

	d := &OutboxEvent{}
	var branchID, correlationID, causationID pgtype.UUID
	var lastErrorClass pgtype.Text
	var processedAt, failedAt pgtype.Timestamptz
	var leaseExpiresAt, nextRetryAt time.Time
	var leaseToken uuid.UUID
	var status string
	err := s.database.FromCtx(ctx).QueryRow(ctx, claim,
		eventTypes, maxAttempts, token, lease.Microseconds(),
	).Scan(
		&d.ID, &d.OrganizationID, &d.SourceModule,
		&branchID, &correlationID, &causationID,
		&d.AggregateType, &d.AggregateID, &d.AggregateVersion,
		&d.EventType, &d.SchemaVersion, &d.Payload, &d.OccurredAt,
		&status, &d.RetryCount, &d.DispatchCount, &nextRetryAt,
		&leaseToken, &leaseExpiresAt, &lastErrorClass,
		&processedAt, &failedAt, &d.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("outbox: claim event: %w", err)
	}
	d.Status = Status(status)
	d.LeaseToken = leaseToken
	d.LeaseExpiresAt = &leaseExpiresAt
	d.NextRetryAt = nextRetryAt
	if branchID.Valid {
		id := uuid.UUID(branchID.Bytes)
		d.BranchID = &id
	}
	if correlationID.Valid {
		id := uuid.UUID(correlationID.Bytes)
		d.CorrelationID = &id
	}
	if causationID.Valid {
		id := uuid.UUID(causationID.Bytes)
		d.CausationID = &id
	}
	if lastErrorClass.Valid {
		d.LastErrorClass = &lastErrorClass.String
	}
	if processedAt.Valid {
		d.ProcessedAt = &processedAt.Time
	}
	if failedAt.Valid {
		d.FailedAt = &failedAt.Time
	}
	return d, nil
}

func (s *Store) beginAttempt(ctx context.Context, delivery *OutboxEvent, handlerToken uuid.UUID, lease time.Duration, maxAttempts int) (int, error) {
	var exhausted bool
	var attempt int
	err := s.database.WithTx(ctx, func(txCtx context.Context) error {
		const lock = `
			SELECT retry_count
			FROM outbox_events
			WHERE id = $1
			  AND status = 'processing'
			  AND lease_token = $2
			  AND lease_expires_at > clock_timestamp()
			FOR UPDATE`
		var retryCount int
		err := s.database.FromCtx(txCtx).QueryRow(txCtx, lock,
			delivery.ID, delivery.LeaseToken,
		).Scan(&retryCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return fmt.Errorf("outbox: lock delivery attempt: %w", err)
		}
		if retryCount >= maxAttempts {
			const quarantine = `
				UPDATE outbox_events
				SET status = 'failed',
				    lease_token = NULL,
				    lease_expires_at = NULL,
				    last_error_class = 'attempts_exhausted',
				    last_error_at = clock_timestamp(),
				    failed_at = clock_timestamp()
				WHERE id = $1 AND status = 'processing' AND lease_token = $2`
			if _, err := s.database.FromCtx(txCtx).Exec(txCtx, quarantine, delivery.ID, delivery.LeaseToken); err != nil {
				return fmt.Errorf("outbox: quarantine exhausted event: %w", err)
			}
			exhausted = true
			return nil
		}

		const begin = `
			UPDATE outbox_events
			SET retry_count = retry_count + 1,
			    lease_token = $3,
			    lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 microsecond')
			WHERE id = $1 AND status = 'processing' AND lease_token = $2`
		tag, err := s.database.FromCtx(txCtx).Exec(txCtx, begin,
			delivery.ID, delivery.LeaseToken, handlerToken, lease.Microseconds(),
		)
		if err != nil {
			return fmt.Errorf("outbox: begin handler attempt: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrLeaseLost
		}
		attempt = retryCount + 1
		return nil
	})
	if err != nil {
		return 0, err
	}
	if exhausted {
		return 0, ErrAttemptsExhausted
	}
	return attempt, nil
}

func (s *Store) loadClaimed(ctx context.Context, delivery *OutboxEvent) (Event, error) {
	const query = `
		SELECT id, organization_id, source_module, branch_id,
		       correlation_id, causation_id, aggregate_type, aggregate_id,
		       aggregate_version, event_type, schema_version, payload, occurred_at
		FROM outbox_events
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
		FOR UPDATE`
	var event Event
	var branchID, correlationID, causationID pgtype.UUID
	err := s.database.FromCtx(ctx).QueryRow(ctx, query,
		delivery.ID, delivery.LeaseToken,
	).Scan(
		&event.ID, &event.OrganizationID, &event.SourceModule,
		&branchID, &correlationID, &causationID,
		&event.AggregateType, &event.AggregateID,
		&event.AggregateVersion, &event.EventType, &event.SchemaVersion,
		&event.Payload, &event.OccurredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrLeaseLost
	}
	if err != nil {
		return Event{}, fmt.Errorf("outbox: load claimed event: %w", err)
	}
	if branchID.Valid {
		id := uuid.UUID(branchID.Bytes)
		event.BranchID = &id
	}
	if correlationID.Valid {
		id := uuid.UUID(correlationID.Bytes)
		event.CorrelationID = &id
	}
	if causationID.Valid {
		id := uuid.UUID(causationID.Bytes)
		event.CausationID = &id
	}
	return event, nil
}

func (s *Store) complete(ctx context.Context, delivery *OutboxEvent) error {
	const query = `
		UPDATE outbox_events
		SET status = 'completed',
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error_class = NULL,
		    processed_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2`
	tag, err := s.database.FromCtx(ctx).Exec(ctx, query, delivery.ID, delivery.LeaseToken)
	if err != nil {
		return fmt.Errorf("outbox: complete event: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) fail(ctx context.Context, delivery *OutboxEvent, maxAttempts int, delay time.Duration, class string, permanent bool) (bool, error) {
	const query = `
		UPDATE outbox_events
		SET status = CASE WHEN $3 OR retry_count >= $4 THEN 'failed' ELSE 'pending' END,
		    next_retry_at = CASE
		      WHEN $3 OR retry_count >= $4 THEN next_retry_at
		      ELSE clock_timestamp() + ($5::bigint * interval '1 microsecond')
		    END,
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error_class = $6,
		    last_error_at = clock_timestamp(),
		    failed_at = CASE WHEN $3 OR retry_count >= $4 THEN clock_timestamp() ELSE NULL END
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2
		RETURNING status = 'failed'`
	var failed bool
	err := s.database.FromCtx(ctx).QueryRow(ctx, query,
		delivery.ID, delivery.LeaseToken, permanent, maxAttempts,
		delay.Microseconds(), normalizeErrorClass(class),
	).Scan(&failed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("outbox: record event failure: %w", err)
	}
	return failed, nil
}

func (s *Store) releaseDispatchFailure(ctx context.Context, delivery *OutboxEvent, delay time.Duration) error {
	const query = `
		UPDATE outbox_events
		SET status = 'pending',
		    next_retry_at = clock_timestamp() + ($3::bigint * interval '1 microsecond'),
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error_class = 'broker_error',
		    last_error_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2`
	tag, err := s.database.FromCtx(ctx).Exec(ctx, query,
		delivery.ID, delivery.LeaseToken, delay.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("outbox: record dispatch failure: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) queueDepth(ctx context.Context, eventTypes []string) (map[string]int64, error) {
	const query = `
		SELECT event_type, count(*)
		FROM outbox_events
		WHERE event_type = ANY($1::text[])
		  AND status IN ('pending', 'processing')
		GROUP BY event_type`
	rows, err := s.database.FromCtx(ctx).Query(ctx, query, eventTypes)
	if err != nil {
		return nil, fmt.Errorf("outbox: query queue depth: %w", err)
	}
	defer rows.Close()
	depth := make(map[string]int64, len(eventTypes))
	for rows.Next() {
		var eventType string
		var count int64
		if err := rows.Scan(&eventType, &count); err != nil {
			return nil, fmt.Errorf("outbox: scan queue depth: %w", err)
		}
		depth[eventType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: iterate queue depth: %w", err)
	}
	return depth, nil
}
