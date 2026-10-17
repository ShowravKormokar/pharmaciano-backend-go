package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var (
	ErrInvalidRepositoryRequest = errors.New("backup: invalid repository request")
	ErrInvalidBackupCursor      = errors.New("backup: invalid cursor")
)

// Repository manages system-scoped backup metadata. It never accepts a tenant
// scope, path, connection string, command, or storage credential from callers.
type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

const backupRunColumns = `id, backup_type, status, trigger, triggered_by, reason,
	started_at, finished_at, size_bytes, sha256, storage_provider, storage_ref,
	manifest_ref, encryption_algorithm, encryption_key_id, wrapped_dek,
	encryption_format_version, postgres_version, pg_dump_version, schema_version,
	latest_verification_status, verified_at, expires_at, legal_hold,
	artifact_deleted_at, error_code, error_message, attempt_count, lease_generation,
	lease_owner, lease_expires_at, created_at, updated_at, deleted_at`

const backupVerificationColumns = `id, backup_id, verification_type, status,
	started_at, finished_at, checksum_ok, decrypt_ok, restore_ok,
	schema_version_expected, schema_version_restored, sanity_check_summary,
	measured_recovery_point, measured_restore_time_ms, scratch_target_id,
	cleanup_completed_at, error_code, error_message, worker_version, created_at`

type scanner interface {
	Scan(...any) error
}

func scanBackupRun(row scanner) (BackupRun, error) {
	var run BackupRun
	err := row.Scan(
		&run.ID, &run.Type, &run.Status, &run.Trigger, &run.TriggeredBy, &run.Reason,
		&run.StartedAt, &run.FinishedAt, &run.SizeBytes, &run.SHA256,
		&run.StorageProvider, &run.StorageRef, &run.ManifestRef,
		&run.EncryptionAlgorithm, &run.EncryptionKeyID, &run.WrappedDEK,
		&run.EncryptionFormatVersion, &run.PostgresVersion, &run.PGDumpVersion,
		&run.SchemaVersion, &run.LatestVerificationStatus, &run.VerifiedAt,
		&run.ExpiresAt, &run.LegalHold, &run.ArtifactDeletedAt, &run.ErrorCode,
		&run.ErrorMessage, &run.AttemptCount, &run.LeaseGeneration, &run.LeaseOwner,
		&run.LeaseExpiresAt, &run.CreatedAt, &run.UpdatedAt, &run.DeletedAt,
	)
	return run, err
}

func scanBackupVerification(row scanner) (BackupVerification, error) {
	var verification BackupVerification
	err := row.Scan(
		&verification.ID, &verification.BackupID, &verification.Type, &verification.Status,
		&verification.StartedAt, &verification.FinishedAt, &verification.ChecksumOK,
		&verification.DecryptOK, &verification.RestoreOK,
		&verification.SchemaVersionExpected, &verification.SchemaVersionRestored,
		&verification.SanityCheckSummary, &verification.MeasuredRecoveryPoint,
		&verification.MeasuredRestoreTimeMS, &verification.ScratchTargetID,
		&verification.CleanupCompletedAt, &verification.ErrorCode, &verification.ErrorMessage,
		&verification.WorkerVersion, &verification.CreatedAt,
	)
	return verification, err
}

// CreateQueued creates metadata before a task is enqueued. The partial unique
// index permits only one queued/in-progress full backup across all instances.
func (r *Repository) CreateQueued(ctx context.Context, trigger BackupTrigger, triggeredBy *uuid.UUID, reason *string) (BackupRun, error) {
	if err := r.requireDB(); err != nil {
		return BackupRun{}, err
	}
	if !trigger.Valid() {
		return BackupRun{}, fmt.Errorf("%w: unsupported trigger", ErrInvalidRepositoryRequest)
	}
	const query = `INSERT INTO system_backup_runs
		(backup_type, status, trigger, triggered_by, reason)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query,
		BackupTypeFull, BackupRunStatusQueued, trigger, triggeredBy, reason,
	))
}

// FindByID uses the primary pool so authorization and destructive-action
// decisions never rely on replica-lagged metadata.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	const query = `SELECT ` + backupRunColumns + ` FROM system_backup_runs
		WHERE id = $1 AND deleted_at IS NULL`
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id))
}

// FindByIDForUpdate is for service transactions deciding retention, hold, or
// artifact-deletion policy.
func (r *Repository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	const query = `SELECT ` + backupRunColumns + ` FROM system_backup_runs
		WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id))
}

type BackupRunPage struct {
	Items      []BackupRun
	HasMore    bool
	NextCursor string
}

// List returns a bounded, newest-first keyset page. Cursors are signed and
// bound to the active filter set, preventing cross-query cursor reuse.
func (r *Repository) List(ctx context.Context, query *ListBackupRunsQuery) (BackupRunPage, error) {
	if err := r.requireDB(); err != nil {
		return BackupRunPage{}, err
	}
	if query == nil {
		return BackupRunPage{}, fmt.Errorf("%w: query is required", ErrInvalidRepositoryRequest)
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return BackupRunPage{}, errors.Join(validation...)
	}
	if r.cursorCodec == nil {
		return BackupRunPage{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRepositoryRequest)
	}

	filterKey := backupCursorFilterKey(*query)
	var cursor *pagination.Cursor
	if query.Cursor != "" {
		decoded, err := r.cursorCodec.Decode(query.Cursor)
		if err != nil || decoded.ValidateSortKey(filterKey) != nil || decoded.CreatedAt.IsZero() {
			return BackupRunPage{}, ErrInvalidBackupCursor
		}
		cursor = &decoded
	}

	where, args := buildBackupRunWhere(*query, cursor)
	args = append(args, query.Limit+1)
	sql := `SELECT ` + backupRunColumns + ` FROM system_backup_runs WHERE ` + where +
		` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sql, args...)
	if err != nil {
		return BackupRunPage{}, fmt.Errorf("backup list query: %w", err)
	}
	defer rows.Close()

	page := BackupRunPage{Items: make([]BackupRun, 0, query.Limit)}
	var last BackupRun
	for rows.Next() {
		run, err := scanBackupRun(rows)
		if err != nil {
			return BackupRunPage{}, fmt.Errorf("scan backup run: %w", err)
		}
		if len(page.Items) == query.Limit {
			page.HasMore = true
			page.NextCursor = r.cursorCodec.Encode(pagination.Cursor{
				CreatedAt: last.CreatedAt, ID: last.ID, SortKey: filterKey,
			})
			break
		}
		page.Items = append(page.Items, run)
		last = run
	}
	if err := rows.Err(); err != nil {
		return BackupRunPage{}, fmt.Errorf("iterate backup runs: %w", err)
	}
	return page, nil
}

func buildBackupRunWhere(query ListBackupRunsQuery, cursor *pagination.Cursor) (string, []any) {
	conditions := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 7)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if query.Status != "" {
		add("status = $%d", query.Status)
	}
	if query.Type != "" {
		add("backup_type = $%d", query.Type)
	}
	if query.Trigger != "" {
		add("trigger = $%d", query.Trigger)
	}
	if query.From != nil {
		add("created_at >= $%d", query.From.UTC())
	}
	if query.To != nil {
		add("created_at < $%d", query.To.UTC())
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt.UTC(), cursor.ID)
		position := len(args) - 1
		conditions = append(conditions, fmt.Sprintf("(created_at, id) < ($%d, $%d)", position, position+1))
	}
	return strings.Join(conditions, " AND "), args
}

func backupCursorFilterKey(query ListBackupRunsQuery) string {
	from, to := "", ""
	if query.From != nil {
		from = query.From.UTC().Format(time.RFC3339Nano)
	}
	if query.To != nil {
		to = query.To.UTC().Format(time.RFC3339Nano)
	}
	payload := strings.Join([]string{"backup:v1", string(query.Status), string(query.Type), string(query.Trigger), from, to}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return "backup:v1:" + hex.EncodeToString(sum[:])
}

// ClaimRun transitions a queued run exactly once and increments its fencing
// generation. Duplicate task delivery receives db.ErrNoRows.
func (r *Repository) ClaimRun(ctx context.Context, id uuid.UUID, workerID string, leaseUntil time.Time) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || leaseUntil.IsZero() {
		return BackupRun{}, fmt.Errorf("%w: worker id and lease expiry are required", ErrInvalidRepositoryRequest)
	}
	const query = `UPDATE system_backup_runs SET
		status = 'in_progress', started_at = COALESCE(started_at, now()),
		attempt_count = attempt_count + 1, lease_generation = lease_generation + 1,
		lease_owner = $2, lease_expires_at = $3
		WHERE id = $1 AND status = 'queued' AND deleted_at IS NULL
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id, workerID, leaseUntil.UTC()))
}

// RenewLease returns false when another worker owns the run, the run became
// terminal, or the fencing generation no longer matches.
func (r *Repository) RenewLease(ctx context.Context, id uuid.UUID, workerID string, generation int64, leaseUntil time.Time) (bool, error) {
	if err := r.requireID(id); err != nil {
		return false, err
	}
	if strings.TrimSpace(workerID) == "" || generation < 1 || leaseUntil.IsZero() {
		return false, fmt.Errorf("%w: valid lease parameters are required", ErrInvalidRepositoryRequest)
	}
	const query = `UPDATE system_backup_runs SET lease_expires_at = $4
		WHERE id = $1 AND status = 'in_progress' AND lease_owner = $2
		AND lease_generation = $3 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, id, workerID, generation, leaseUntil.UTC())
	return tag.RowsAffected() == 1, err
}

// CompleteSucceeded persists only a fully finalised encrypted artifact. The
// schema also rejects incomplete success rows; a stale worker cannot overwrite
// a newer lease generation.
func (r *Repository) CompleteSucceeded(ctx context.Context, run BackupRun, workerID string, generation int64) (BackupRun, error) {
	if err := r.validateSuccessfulRun(run, workerID, generation); err != nil {
		return BackupRun{}, err
	}
	const query = `UPDATE system_backup_runs SET
		status = 'succeeded', finished_at = now(), size_bytes = $4, sha256 = $5,
		storage_provider = $6, storage_ref = $7, manifest_ref = $8,
		encryption_algorithm = $9, encryption_key_id = $10, wrapped_dek = $11,
		encryption_format_version = $12, postgres_version = $13, pg_dump_version = $14,
		schema_version = $15, expires_at = $16, error_code = NULL, error_message = NULL,
		lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND status = 'in_progress' AND lease_owner = $2
		AND lease_generation = $3 AND deleted_at IS NULL
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query,
		run.ID, workerID, generation, run.SizeBytes, run.SHA256, run.StorageProvider,
		run.StorageRef, run.ManifestRef, run.EncryptionAlgorithm, run.EncryptionKeyID,
		run.WrappedDEK, run.EncryptionFormatVersion, run.PostgresVersion, run.PGDumpVersion,
		run.SchemaVersion, run.ExpiresAt,
	))
}

// CompleteFailed records bounded, already-sanitized diagnostics and releases
// the lease. It never accepts raw subprocess output.
func (r *Repository) CompleteFailed(ctx context.Context, id uuid.UUID, workerID string, generation int64, errorCode, errorMessage string) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	if strings.TrimSpace(workerID) == "" || generation < 1 {
		return BackupRun{}, fmt.Errorf("%w: invalid failure result", ErrInvalidRepositoryRequest)
	}
	errorCode, errorMessage = safeBackupFailure(errorCode)
	const query = `UPDATE system_backup_runs SET
		status = 'failed', finished_at = now(), error_code = $4, error_message = $5,
		lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND status = 'in_progress' AND lease_owner = $2
		AND lease_generation = $3 AND deleted_at IS NULL
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id, workerID, generation, errorCode, errorMessage))
}

func (r *Repository) SetLegalHold(ctx context.Context, id uuid.UUID, legalHold bool) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	const query = `UPDATE system_backup_runs SET legal_hold = $2
		WHERE id = $1 AND status NOT IN ('queued', 'in_progress') AND deleted_at IS NULL
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id, legalHold))
}

// MarkArtifactDeleted records deletion only after the storage adapter confirms
// removal. The immutable operational metadata remains as audit evidence.
func (r *Repository) MarkArtifactDeleted(ctx context.Context, id uuid.UUID) (BackupRun, error) {
	if err := r.requireID(id); err != nil {
		return BackupRun{}, err
	}
	const query = `UPDATE system_backup_runs SET artifact_deleted_at = now()
		WHERE id = $1 AND status = 'succeeded' AND legal_hold = FALSE
		AND artifact_deleted_at IS NULL AND deleted_at IS NULL
		RETURNING ` + backupRunColumns
	return scanBackupRun(r.db.FromCtx(ctx).QueryRow(ctx, query, id))
}

// CreateVerification creates separate historical evidence. Scratch-target
// selection belongs to trusted server configuration, not this method.
func (r *Repository) CreateVerification(ctx context.Context, backupID uuid.UUID, verificationType VerificationType) (BackupVerification, error) {
	if err := r.requireID(backupID); err != nil {
		return BackupVerification{}, err
	}
	if verificationType != VerificationTypeIntegrity && verificationType != VerificationTypeRestoreDrill {
		return BackupVerification{}, fmt.Errorf("%w: unsupported verification type", ErrInvalidRepositoryRequest)
	}
	const query = `INSERT INTO system_backup_verifications (backup_id, verification_type, status)
		SELECT id, $2, 'queued' FROM system_backup_runs
		WHERE id = $1 AND status = 'succeeded' AND artifact_deleted_at IS NULL AND deleted_at IS NULL
		RETURNING ` + backupVerificationColumns
	return scanBackupVerification(r.db.FromCtx(ctx).QueryRow(ctx, query, backupID, verificationType))
}

func (r *Repository) StartVerification(ctx context.Context, id uuid.UUID) (BackupVerification, error) {
	if err := r.requireID(id); err != nil {
		return BackupVerification{}, err
	}
	const query = `UPDATE system_backup_verifications SET
		status = 'in_progress', started_at = COALESCE(started_at, now())
		WHERE id = $1 AND status = 'queued'
		RETURNING ` + backupVerificationColumns
	return scanBackupVerification(r.db.FromCtx(ctx).QueryRow(ctx, query, id))
}

// CompleteVerification commits terminal evidence and refreshes the denormalized
// latest-verification fields in one transaction. A late older drill cannot
// mask a newer verification result.
func (r *Repository) CompleteVerification(ctx context.Context, result BackupVerification) (BackupVerification, error) {
	if err := r.validateVerificationResult(result); err != nil {
		return BackupVerification{}, err
	}
	if result.Status != VerificationStatusSucceeded {
		code := ""
		if result.ErrorCode != nil {
			code = *result.ErrorCode
		}
		safeCode, safeMessage := safeBackupFailure(code)
		result.ErrorCode = &safeCode
		result.ErrorMessage = &safeMessage
	}
	var completed BackupVerification
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const completeSQL = `UPDATE system_backup_verifications SET
			status = $2, finished_at = now(), checksum_ok = $3, decrypt_ok = $4,
			restore_ok = $5, schema_version_expected = $6, schema_version_restored = $7,
			sanity_check_summary = $8, measured_recovery_point = $9,
			measured_restore_time_ms = $10, scratch_target_id = $11,
			cleanup_completed_at = $12, error_code = $13, error_message = $14,
			worker_version = $15
			WHERE id = $1 AND status = 'in_progress'
			RETURNING ` + backupVerificationColumns
		var err error
		completed, err = scanBackupVerification(r.db.FromCtx(txCtx).QueryRow(txCtx, completeSQL,
			result.ID, result.Status, result.ChecksumOK, result.DecryptOK, result.RestoreOK,
			result.SchemaVersionExpected, result.SchemaVersionRestored, result.SanityCheckSummary,
			result.MeasuredRecoveryPoint, result.MeasuredRestoreTimeMS, result.ScratchTargetID,
			result.CleanupCompletedAt, result.ErrorCode, result.ErrorMessage, result.WorkerVersion,
		))
		if err != nil {
			return err
		}
		const refreshSQL = `UPDATE system_backup_runs AS run SET
			latest_verification_status = $2,
			verified_at = CASE WHEN $2 = 'succeeded' THEN $3 ELSE NULL END
			WHERE run.id = $1 AND NOT EXISTS (
				SELECT 1 FROM system_backup_verifications AS newer
				WHERE newer.backup_id = run.id
				AND (newer.created_at, newer.id) > ($4, $5)
			)`
		_, err = r.db.FromCtx(txCtx).Exec(txCtx, refreshSQL,
			completed.BackupID, completed.Status, completed.FinishedAt,
			completed.CreatedAt, completed.ID,
		)
		return err
	})
	if err != nil {
		return BackupVerification{}, err
	}
	return completed, nil
}

func (r *Repository) validateSuccessfulRun(run BackupRun, workerID string, generation int64) error {
	if err := r.requireID(run.ID); err != nil {
		return err
	}
	if strings.TrimSpace(workerID) == "" || generation < 1 || run.SizeBytes == nil || *run.SizeBytes < 0 ||
		run.SHA256 == nil || len(*run.SHA256) != 64 || *run.SHA256 != strings.ToLower(*run.SHA256) ||
		run.StorageProvider == nil || strings.TrimSpace(*run.StorageProvider) == "" ||
		run.StorageRef == nil || strings.TrimSpace(*run.StorageRef) == "" ||
		run.EncryptionAlgorithm == nil || strings.TrimSpace(*run.EncryptionAlgorithm) == "" ||
		run.EncryptionKeyID == nil || strings.TrimSpace(*run.EncryptionKeyID) == "" || len(run.WrappedDEK) == 0 ||
		run.EncryptionFormatVersion == nil || *run.EncryptionFormatVersion < 1 {
		return fmt.Errorf("%w: incomplete successful backup", ErrInvalidRepositoryRequest)
	}
	if _, err := hex.DecodeString(*run.SHA256); err != nil {
		return fmt.Errorf("%w: successful backup checksum must be hexadecimal", ErrInvalidRepositoryRequest)
	}
	ref, err := UnmarshalObjectRef(*run.StorageRef)
	if err != nil || strings.TrimSpace(ref.Provider) == "" || ref.Provider != *run.StorageProvider ||
		strings.TrimSpace(ref.Namespace) == "" || strings.TrimSpace(ref.Key) == "" {
		return fmt.Errorf("%w: successful backup requires a valid opaque object reference", ErrInvalidRepositoryRequest)
	}
	return nil
}

func (r *Repository) validateVerificationResult(result BackupVerification) error {
	if result.ID == uuid.Nil {
		return fmt.Errorf("%w: verification id must be non-zero", ErrInvalidRepositoryRequest)
	}
	if result.Type != VerificationTypeIntegrity && result.Type != VerificationTypeRestoreDrill {
		return fmt.Errorf("%w: unsupported verification type", ErrInvalidRepositoryRequest)
	}
	if result.Status != VerificationStatusSucceeded && result.Status != VerificationStatusFailed && result.Status != VerificationStatusUnavailable {
		return fmt.Errorf("%w: verification result must be terminal", ErrInvalidRepositoryRequest)
	}
	if result.Status == VerificationStatusSucceeded {
		if result.ChecksumOK == nil || !*result.ChecksumOK || result.DecryptOK == nil || !*result.DecryptOK {
			return fmt.Errorf("%w: successful verification requires a matching checksum and authenticated decryption", ErrInvalidRepositoryRequest)
		}
		if result.Type == VerificationTypeIntegrity && result.RestoreOK != nil {
			return fmt.Errorf("%w: integrity verification cannot include a restore result", ErrInvalidRepositoryRequest)
		}
		if result.Type == VerificationTypeRestoreDrill &&
			(result.RestoreOK == nil || !*result.RestoreOK ||
				result.ScratchTargetID == nil || strings.TrimSpace(*result.ScratchTargetID) == "" ||
				result.CleanupCompletedAt == nil) {
			return fmt.Errorf("%w: successful restore drill requires restore and cleanup evidence", ErrInvalidRepositoryRequest)
		}
	}
	if (result.ErrorCode != nil && len(*result.ErrorCode) > 100) ||
		(result.ErrorMessage != nil && len(*result.ErrorMessage) > 500) {
		return fmt.Errorf("%w: verification diagnostic exceeds bounds", ErrInvalidRepositoryRequest)
	}
	return nil
}

func safeBackupFailure(code string) (string, string) {
	switch strings.TrimSpace(code) {
	case "PG_DUMP_FAILED":
		return "PG_DUMP_FAILED", "The PostgreSQL backup process failed."
	case "PG_DUMP_TIMEOUT":
		return "PG_DUMP_TIMEOUT", "The PostgreSQL backup process timed out."
	case "ENCRYPTION_FAILED":
		return "ENCRYPTION_FAILED", "Backup artifact encryption failed."
	case "STORAGE_FAILED":
		return "STORAGE_FAILED", "Backup artifact storage failed."
	case "FINALIZE_FAILED":
		return "FINALIZE_FAILED", "Backup metadata finalization failed."
	case "CHECKSUM_MISMATCH":
		return "CHECKSUM_MISMATCH", "Backup artifact checksum validation failed."
	case "DECRYPT_FAILED":
		return "DECRYPT_FAILED", "Backup artifact authentication failed."
	case "RESTORE_FAILED":
		return "RESTORE_FAILED", "The isolated backup restore drill failed."
	case "SCRATCH_CLEANUP_FAILED":
		return "SCRATCH_CLEANUP_FAILED", "The isolated restore target cleanup failed."
	default:
		return "BACKUP_FAILED", "The backup operation failed."
	}
}

func (r *Repository) requireDB() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("%w: database is not configured", ErrInvalidRepositoryRequest)
	}
	return nil
}

func (r *Repository) requireID(id uuid.UUID) error {
	if err := r.requireDB(); err != nil {
		return err
	}
	if id == uuid.Nil {
		return fmt.Errorf("%w: id must be non-zero", ErrInvalidRepositoryRequest)
	}
	return nil
}
