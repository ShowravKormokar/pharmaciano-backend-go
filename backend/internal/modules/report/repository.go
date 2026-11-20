// Package-internal repository: tenant-scoped Report data access. Every method
// requires organization_id and enforces it in SQL via composite keys and
// mandatory org predicates. See REPORT_CONTRACT §8 and §4.1.
package report

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// Repository is the tenant-scoped Report data access layer.
type Repository struct {
	db *db.DB
}

// NewRepository builds the Report repository.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// JobRow is the column list for report_jobs selects (order must match scanJob).
const JobRow = `
	id, organization_id, requested_by, report_code, definition_version,
	format, normalized_filters, filters_hash, scope_snapshot, scope_hash,
	status, priority, available_at, request_hash, attempt_count, max_attempts,
	queued_at, started_at, completed_at, cancel_requested_at, lease_owner,
	lease_until, lease_generation, heartbeat_at, failure_code,
	failure_message_safe, created_at, updated_at, version`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	var filtersHash, scopeHash, requestHash []byte
	err := row.Scan(
		&j.ID, &j.OrganizationID, &j.RequestedBy, &j.ReportCode, &j.DefinitionVersion,
		&j.Format, &j.NormalizedFilters, &filtersHash, &j.ScopeSnapshot, &scopeHash,
		&j.Status, &j.Priority, &j.AvailableAt, &requestHash, &j.AttemptCount, &j.MaxAttempts,
		&j.QueuedAt, &j.StartedAt, &j.CompletedAt, &j.CancelRequestedAt, &j.LeaseOwner,
		&j.LeaseUntil, &j.LeaseGeneration, &j.HeartbeatAt, &j.FailureCode, &j.FailureMessageSafe,
		&j.CreatedAt, &j.UpdatedAt, &j.Version,
	)
	if err != nil {
		return nil, err
	}
	j.FiltersHash = filtersHash
	j.ScopeHash = scopeHash
	j.RequestHash = requestHash
	return &j, nil
}

// HashFilters returns a deterministic SHA-256 of the canonical filter bytes.
func HashFilters(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// HMACKey signs an idempotency key with a server secret.
func HMACKey(key string, version int) []byte {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("report-idempotency-v%d", version)))
	mac.Write([]byte(key))
	return mac.Sum(nil)
}

// HMACHex is the server-keyed HMAC hex stored (never the raw client key).
func HMACHex(key string, version int) string {
	return hex.EncodeToString(HMACKey(key, version))
}

// CreateIdempotency inserts a durable idempotency identity. Returns
// ErrIdempotencyConflict if the (org, requester, key_hmac) already exists with
// a different request hash (same key, different request).
func (r *Repository) CreateIdempotency(ctx context.Context, idem *Idempotency) error {
	const insert = `
	INSERT INTO report_idempotency (
		organization_id, requested_by, key_hmac, hmac_key_version,
		request_hash, job_id, expires_at
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (organization_id, requested_by, key_hmac) DO NOTHING
	RETURNING organization_id, requested_by, key_hmac, hmac_key_version,
	          request_hash, job_id, created_at, expires_at`
	var same bool
	err := r.db.FromCtx(ctx).QueryRow(ctx, insert,
		idem.OrganizationID, idem.RequestedBy, idem.KeyHMAC, idem.HMACKeyVersion,
		idem.RequestHash, idem.JobID, idem.ExpiresAt,
	).Scan(
		&idem.OrganizationID, &idem.RequestedBy, &idem.KeyHMAC, &idem.HMACKeyVersion,
		&idem.RequestHash, &idem.JobID, &idem.CreatedAt, &idem.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Key exists: check whether the stored request hash matches.
		const verify = `
			SELECT request_hash = $1
			FROM report_idempotency
			WHERE organization_id = $2 AND requested_by = $3 AND key_hmac = $4`
		err = r.db.FromCtx(ctx).QueryRow(ctx, verify,
			idem.RequestHash, idem.OrganizationID, idem.RequestedBy, idem.KeyHMAC,
		).Scan(&same)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if same {
			return ErrIdempotencyReplay
		}
		return ErrIdempotencyConflict
	}
	if err != nil {
		return errs.DatabaseError(err)
	}
	return nil
}

// LinkIdempotencyJob attaches an existing idempotency record to a job (called
// within the same enqueue transaction after the job insert).
func (r *Repository) LinkIdempotencyJob(ctx context.Context, orgID, requestedBy uuid.UUID, keyHMAC string, jobID uuid.UUID) error {
	const query = `
	UPDATE report_idempotency
	SET job_id = $1
	WHERE organization_id = $2 AND requested_by = $3 AND key_hmac = $4 AND job_id IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, jobID, orgID, requestedBy, keyHMAC)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrIdempotencyConflict
	}
	return nil
}

// ErrIdempotencyReplay is returned when the same key+same request is seen again.
var ErrIdempotencyReplay = errors.New("report: idempotent replay")
var ErrIdempotencyConflict = errors.New("report: idempotency key conflict")

// CreateJob inserts a queued job. Must run inside a short transaction alongside
// the idempotency record and outbox event.
func (r *Repository) CreateJob(ctx context.Context, j *Job) error {
	const insert = `
	INSERT INTO report_jobs (
		organization_id, requested_by, report_code, definition_version,
		format, normalized_filters, filters_hash, scope_snapshot, scope_hash,
		status, priority, available_at, request_hash, attempt_count, max_attempts
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued', $10, $11, $12, $13, $14)
	RETURNING id, queued_at, created_at, updated_at, version`
	err := r.db.FromCtx(ctx).QueryRow(ctx, insert,
		j.OrganizationID, j.RequestedBy, j.ReportCode, j.DefinitionVersion,
		j.Format, j.NormalizedFilters, j.FiltersHash, j.ScopeSnapshot, j.ScopeHash,
		j.Priority, j.AvailableAt, j.RequestHash, j.AttemptCount, j.MaxAttempts,
	).Scan(&j.ID, &j.QueuedAt, &j.CreatedAt, &j.UpdatedAt, &j.Version)
	if err != nil {
		return errs.DatabaseError(err)
	}
	return nil
}

// ListJobs returns paginated, tenant-scoped jobs visible to the caller
// (owner-only by default).
func (r *Repository) ListJobs(ctx context.Context, orgID, requestedBy uuid.UUID, page, limit int) ([]*Job, error) {
	offset := (page - 1) * limit
	rows, err := r.db.FromCtx(ctx).Query(ctx, JobRow+`
	FROM report_jobs
	WHERE organization_id = $1 AND requested_by = $2
	ORDER BY created_at DESC, id DESC
	LIMIT $3 OFFSET $4`,
		orgID, requestedBy, limit, offset)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, errs.DatabaseError(err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.DatabaseError(err)
	}
	if out == nil {
		out = []*Job{}
	}
	return out, nil
}

// GetJob loads a single tenant-scoped job. Returns ErrNotFound (non-disclosing)
// when absent or not owned by the org/requester.
func (r *Repository) GetJob(ctx context.Context, orgID, requestedBy, jobID uuid.UUID) (*Job, error) {
	j, err := scanJob(r.db.FromCtx(ctx).QueryRow(ctx, JobRow+`
	FROM report_jobs
	WHERE id = $1 AND organization_id = $2 AND requested_by = $3`,
		jobID, orgID, requestedBy))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.NotFound("report job")
		}
		return nil, errs.DatabaseError(err)
	}
	return j, nil
}

// GetJobByID org-scoped (for worker/internal use, ignores requester).
func (r *Repository) GetJobByID(ctx context.Context, orgID, jobID uuid.UUID) (*Job, error) {
	j, err := scanJob(r.db.FromCtx(ctx).QueryRow(ctx, JobRow+`
	FROM report_jobs
	WHERE id = $1 AND organization_id = $2`,
		jobID, orgID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.NotFound("report job")
		}
		return nil, errs.DatabaseError(err)
	}
	return j, nil
}

// ClaimJob atomically claims a queued job for a worker: conditional state
// transition queued/retry_wait -> running with lease fencing. Increments
// attempt_count and lease_generation.
func (r *Repository) ClaimJob(ctx context.Context, orgID, jobID uuid.UUID, owner string, lease time.Duration, now time.Time) (*Job, error) {
	const query = `
	WITH claimed AS (
		UPDATE report_jobs
		SET status = 'running',
		    attempt_count = attempt_count + 1,
		    lease_generation = lease_generation + 1,
		    lease_owner = $4,
		    lease_until = $5,
		    heartbeat_at = $5,
		    started_at = COALESCE(started_at, $5)
		WHERE id = $1
		  AND organization_id = $2
		  AND status IN ('queued', 'retry_wait')
		  AND available_at <= $5
		RETURNING id, organization_id, requested_by, report_code, definition_version,
		          format, normalized_filters, filters_hash, scope_snapshot, scope_hash,
		          status, priority, available_at, request_hash, attempt_count,
		          max_attempts, queued_at, started_at, completed_at,
		          cancel_requested_at, lease_owner, lease_until, lease_generation,
		          heartbeat_at, failure_code, failure_message_safe,
		          created_at, updated_at, version
	)
	SELECT * FROM claimed`
	j, err := scanJob(r.db.FromCtx(ctx).QueryRow(ctx, query,
		jobID, orgID, owner, now.Add(lease).UTC(), now.Add(lease).UTC()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrJobNotClaimable
		}
		return nil, errs.DatabaseError(err)
	}
	return j, nil
}

// ErrJobNotClaimable is returned when a job cannot be claimed (already claimed,
// terminal, or not yet available).
var ErrJobNotClaimable = errors.New("report: job not claimable")

// HeartbeatJob updates lease_until/heartbeat_at if the same lease generation
// still owns the job. Returns ErrLeaseLost otherwise.
func (r *Repository) HeartbeatJob(ctx context.Context, orgID, jobID uuid.UUID, leaseGeneration int64, owner string, until time.Time) error {
	const heartbeat = `
	UPDATE report_jobs
	SET lease_until = $4, heartbeat_at = $4
	WHERE id = $1 AND organization_id = $2 AND lease_generation = $3 AND lease_owner = $5`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, heartbeat, jobID, orgID, leaseGeneration, until, owner)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ErrLeaseLost is returned when a stale worker attempts to mutate a job it no
// longer owns (generation mismatch).
var ErrLeaseLost = errors.New("report: lease lost")

// CancelRequested atomically sets cancel_requested_at on a queued/running job
// if the same lease generation (or no lease for queued) owns it. Used by the
// cancel endpoint and workers.
func (r *Repository) CancelRequested(ctx context.Context, orgID, jobID uuid.UUID, leaseGeneration int64, owner string, at time.Time) (bool, error) {
	const query = `
	UPDATE report_jobs
	SET cancel_requested_at = $3
	WHERE id = $1 AND organization_id = $2
	  AND status IN ('queued', 'running', 'retry_wait')
	  AND (
	    (status = 'queued' AND lease_owner IS NULL)
	    OR (lease_generation = $4 AND lease_owner = $5)
	  )`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, jobID, orgID, at, leaseGeneration, owner)
	if err != nil {
		return false, errs.DatabaseError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// FinalizeSucceeded transitions a claimed job to succeeded, marks the artifact
// available, and inserts provenance in one short transaction. Only the current
// lease generation may finalize.
func (r *Repository) FinalizeSucceeded(ctx context.Context, orgID, jobID uuid.UUID, leaseGeneration int64, artifact *Artifact, prov *Provenance) error {
	return r.db.WithTx(ctx, func(txCtx context.Context) error {
		now := time.Now().UTC()
		const finalize = `
			UPDATE report_jobs
			SET status = 'succeeded', completed_at = $3, lease_owner = NULL,
			    lease_until = NULL, lease_generation = lease_generation + 1,
			    updated_at = $3
			WHERE id = $1 AND organization_id = $2 AND lease_generation = $4
			  AND status IN ('running', 'cancel_requested')`
		if _, err := r.db.FromCtx(txCtx).Exec(txCtx, finalize, jobID, orgID, now, leaseGeneration); err != nil {
			return errs.DatabaseError(err)
		}
		// artifact + provenance inserts
		if err := r.createArtifactTx(txCtx, artifact); err != nil {
			return err
		}
		if err := r.createProvenanceTx(txCtx, prov); err != nil {
			return err
		}
		return nil
	})
}

func (r *Repository) createArtifactTx(ctx context.Context, a *Artifact) error {
	const insert = `
	INSERT INTO report_artifacts (
		organization_id, job_id, format, storage_provider, storage_object_key,
		mime_type, file_name_safe, size_bytes, checksum_sha256, encryption_key_ref,
		status, expires_at
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'available', $11)
	RETURNING id, created_at`
	return r.db.FromCtx(ctx).QueryRow(ctx, insert,
		a.OrganizationID, a.JobID, a.Format, a.StorageProvider, a.StorageObjectKey,
		a.MimeType, a.FileNameSafe, a.SizeBytes, a.ChecksumSHA256, a.EncryptionKeyRef,
		a.ExpiresAt,
	).Scan(&a.ID, &a.CreatedAt)
}

func (r *Repository) createProvenanceTx(ctx context.Context, p *Provenance) error {
	const insert = `
	INSERT INTO report_run_provenance (
		organization_id, job_id, definition_version, renderer_version,
		normalized_request_hash, scope_snapshot, source_watermarks, data_as_of,
		consistency_mode, timezone, resolved_from, resolved_to, row_count,
		warning_codes, completeness, checksum_sha256
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, insert,
		p.OrganizationID, p.JobID, p.DefinitionVersion, p.RendererVersion,
		p.NormalizedRequestHash, p.ScopeSnapshot, p.SourceWatermarks, p.DataAsOf,
		p.ConsistencyMode, p.Timezone, p.ResolvedFrom, p.ResolvedTo, p.RowCount,
		p.WarningCodes, p.Completeness, p.ChecksumSHA256,
	)
	return err
}

// MarkJobFailed records a terminal failure (safe code/message only).
func (r *Repository) MarkJobFailed(ctx context.Context, orgID, jobID uuid.UUID, leaseGeneration int64, code, message string, at time.Time) error {
	const query = `
	UPDATE report_jobs
	SET status = 'failed', completed_at = $3, failure_code = $4,
	    failure_message_safe = $5, lease_owner = NULL, lease_until = NULL,
	    lease_generation = lease_generation + 1, updated_at = $3
	WHERE id = $1 AND organization_id = $2 AND lease_generation = $6
	  AND status IN ('running', 'cancel_requested', 'retry_wait')`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, jobID, orgID, at, code, message, leaseGeneration)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RetryWait requeues a failed job for retry (running/retry_wait -> retry_wait).
func (r *Repository) RetryWait(ctx context.Context, orgID, jobID uuid.UUID, leaseGeneration int64, availableAt time.Time, maxAttempts int) error {
	const query = `
	UPDATE report_jobs
	SET status = 'retry_wait', available_at = $3, max_attempts = $4,
	    lease_owner = NULL, lease_until = NULL, lease_generation = lease_generation + 1,
	    updated_at = $3
	WHERE id = $1 AND organization_id = $2 AND lease_generation = $5
	  AND status = 'running'`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, jobID, orgID, availableAt, maxAttempts, leaseGeneration)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// CreateArtifactStaging inserts a non-downloadable staging artifact row before
// upload begins.
func (r *Repository) CreateArtifactStaging(ctx context.Context, a *Artifact) error {
	const insert = `
	INSERT INTO report_artifacts (
		organization_id, job_id, format, storage_provider, storage_object_key,
		mime_type, file_name_safe, size_bytes, checksum_sha256, encryption_key_ref,
		status, expires_at
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'staging', $11)
	RETURNING id, created_at`
	return r.db.FromCtx(ctx).QueryRow(ctx, insert,
		a.OrganizationID, a.JobID, a.Format, a.StorageProvider, a.StorageObjectKey,
		a.MimeType, a.FileNameSafe, a.SizeBytes, a.ChecksumSHA256, a.EncryptionKeyRef,
		a.ExpiresAt,
	).Scan(&a.ID, &a.CreatedAt)
}

// GetArtifact loads tenant-scoped artifact metadata by job+format.
func (r *Repository) GetArtifact(ctx context.Context, orgID, jobID uuid.UUID, format string) (*Artifact, error) {
	var a Artifact
	var deletedAt pgtype.Timestamptz
	err := r.db.FromCtx(ctx).QueryRow(ctx, `
		SELECT id, organization_id, job_id, format, storage_provider,
		       storage_object_key, mime_type, file_name_safe, size_bytes,
		       checksum_sha256, encryption_key_ref, status, created_at,
		       expires_at, deleted_at
		FROM report_artifacts
		WHERE job_id = $1 AND organization_id = $2 AND format = $3`,
		jobID, orgID, format,
	).Scan(
		&a.ID, &a.OrganizationID, &a.JobID, &a.Format, &a.StorageProvider,
		&a.StorageObjectKey, &a.MimeType, &a.FileNameSafe, &a.SizeBytes,
		&a.ChecksumSHA256, &a.EncryptionKeyRef, &a.Status, &a.CreatedAt,
		&a.ExpiresAt, &deletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.NotFound("report artifact")
		}
		return nil, errs.DatabaseError(err)
	}
	a.DeletedAt = deletedAt
	return &a, nil
}

// CreateAttempt appends an execution attempt record.
func (r *Repository) CreateAttempt(ctx context.Context, att *Attempt) error {
	const insert = `
	INSERT INTO report_job_attempts (
		organization_id, job_id, attempt_number, lease_generation, task_id,
		correlation_id, worker_identity, started_at, ended_at, outcome_class,
		safe_error_code, safe_error_message, retry_delay_ms
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	RETURNING id, created_at`
	return r.db.FromCtx(ctx).QueryRow(ctx, insert,
		att.OrganizationID, att.JobID, att.AttemptNumber, att.LeaseGeneration,
		att.TaskID, att.CorrelationID, att.WorkerIdentity, att.StartedAt,
		att.EndedAt, att.OutcomeClass, att.SafeErrorCode, att.SafeErrorMessage,
		att.RetryDelayMs,
	).Scan(&att.ID, &att.CreatedAt)
}

// jsonNullRoundTrip safely serializes a nil/empty slice to '{}'::jsonb so the
// NOT NULL jsonb columns never receive SQL NULL.
func jsonNullRoundTrip(v []byte) []byte {
	if v == nil {
		return []byte("{}")
	}
	return v
}

// MarshalFilters canonicalizes filters to stable JSON bytes.
func MarshalFilters(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
