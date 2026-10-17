// Package backup owns metadata for system-wide database recovery artifacts.
// It never owns the business data contained in an artifact.
package backup

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

type BackupType string

const (
	// BackupTypeFull is a PostgreSQL custom-format logical dump (`pg_dump -Fc`).
	// Incremental, WAL, and PITR artifacts are outside the initial scope.
	BackupTypeFull BackupType = "full"
)

func (t BackupType) Valid() bool { return t == BackupTypeFull }

type BackupRunStatus string

const (
	BackupRunStatusQueued     BackupRunStatus = "queued"
	BackupRunStatusInProgress BackupRunStatus = "in_progress"
	BackupRunStatusSucceeded  BackupRunStatus = "succeeded"
	BackupRunStatusFailed     BackupRunStatus = "failed"
	BackupRunStatusCancelled  BackupRunStatus = "cancelled"
	BackupRunStatusExpired    BackupRunStatus = "expired"
)

func (s BackupRunStatus) Valid() bool {
	switch s {
	case BackupRunStatusQueued, BackupRunStatusInProgress, BackupRunStatusSucceeded,
		BackupRunStatusFailed, BackupRunStatusCancelled, BackupRunStatusExpired:
		return true
	default:
		return false
	}
}

type BackupTrigger string

const (
	BackupTriggerManual    BackupTrigger = "manual"
	BackupTriggerScheduled BackupTrigger = "scheduled"
)

func (t BackupTrigger) Valid() bool {
	return t == BackupTriggerManual || t == BackupTriggerScheduled
}

type VerificationType string

const (
	VerificationTypeIntegrity    VerificationType = "integrity"
	VerificationTypeRestoreDrill VerificationType = "restore_drill"
)

type VerificationStatus string

const (
	VerificationStatusQueued      VerificationStatus = "queued"
	VerificationStatusInProgress  VerificationStatus = "in_progress"
	VerificationStatusSucceeded   VerificationStatus = "succeeded"
	VerificationStatusFailed      VerificationStatus = "failed"
	VerificationStatusUnavailable VerificationStatus = "unavailable"
)

func (s VerificationStatus) Valid() bool {
	switch s {
	case VerificationStatusQueued, VerificationStatusInProgress, VerificationStatusSucceeded,
		VerificationStatusFailed, VerificationStatusUnavailable:
		return true
	default:
		return false
	}
}

// BackupRun is the authoritative system-scoped record of one backup attempt.
// Recovery material is never serialised directly; handlers must use a safe DTO.
type BackupRun struct {
	common.BaseModel

	Type    BackupType      `db:"backup_type" json:"backup_type"`
	Status  BackupRunStatus `db:"status" json:"status"`
	Trigger BackupTrigger   `db:"trigger" json:"trigger"`

	TriggeredBy *uuid.UUID `db:"triggered_by" json:"triggered_by,omitempty"`
	Reason      *string    `db:"reason" json:"reason,omitempty"`
	StartedAt   *time.Time `db:"started_at" json:"started_at,omitempty"`
	FinishedAt  *time.Time `db:"finished_at" json:"finished_at,omitempty"`

	SizeBytes *int64  `db:"size_bytes" json:"size_bytes,omitempty"`
	SHA256    *string `db:"sha256" json:"sha256,omitempty"`

	StorageProvider *string `db:"storage_provider" json:"-"`
	StorageRef      *string `db:"storage_ref" json:"-"`
	ManifestRef     *string `db:"manifest_ref" json:"-"`

	EncryptionAlgorithm     *string `db:"encryption_algorithm" json:"encryption_algorithm,omitempty"`
	EncryptionKeyID         *string `db:"encryption_key_id" json:"encryption_key_id,omitempty"`
	WrappedDEK              []byte  `db:"wrapped_dek" json:"-"`
	EncryptionFormatVersion *int    `db:"encryption_format_version" json:"encryption_format_version,omitempty"`

	PostgresVersion *string `db:"postgres_version" json:"postgres_version,omitempty"`
	PGDumpVersion   *string `db:"pg_dump_version" json:"pg_dump_version,omitempty"`
	SchemaVersion   *int64  `db:"schema_version" json:"schema_version,omitempty"`

	LatestVerificationStatus VerificationStatus `db:"latest_verification_status" json:"latest_verification_status"`
	VerifiedAt               *time.Time         `db:"verified_at" json:"verified_at,omitempty"`

	ExpiresAt         *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	LegalHold         bool       `db:"legal_hold" json:"legal_hold"`
	ArtifactDeletedAt *time.Time `db:"artifact_deleted_at" json:"artifact_deleted_at,omitempty"`

	ErrorCode    *string `db:"error_code" json:"error_code,omitempty"`
	ErrorMessage *string `db:"error_message" json:"error_message,omitempty"`
	AttemptCount int     `db:"attempt_count" json:"attempt_count"`

	LeaseGeneration int64      `db:"lease_generation" json:"-"`
	LeaseOwner      *string    `db:"lease_owner" json:"-"`
	LeaseExpiresAt  *time.Time `db:"lease_expires_at" json:"-"`
}

// BackupVerification is historical evidence for integrity validation or an
// isolated restore drill. Completed evidence is immutable in the database.
type BackupVerification struct {
	ID       uuid.UUID `db:"id" json:"id"`
	BackupID uuid.UUID `db:"backup_id" json:"backup_id"`

	Type   VerificationType   `db:"verification_type" json:"verification_type"`
	Status VerificationStatus `db:"status" json:"status"`

	StartedAt  *time.Time `db:"started_at" json:"started_at,omitempty"`
	FinishedAt *time.Time `db:"finished_at" json:"finished_at,omitempty"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`

	ChecksumOK *bool `db:"checksum_ok" json:"checksum_ok,omitempty"`
	DecryptOK  *bool `db:"decrypt_ok" json:"decrypt_ok,omitempty"`
	RestoreOK  *bool `db:"restore_ok" json:"restore_ok,omitempty"`

	SchemaVersionExpected *int64         `db:"schema_version_expected" json:"schema_version_expected,omitempty"`
	SchemaVersionRestored *int64         `db:"schema_version_restored" json:"schema_version_restored,omitempty"`
	SanityCheckSummary    common.JSONMap `db:"sanity_check_summary" json:"sanity_check_summary,omitempty"`

	MeasuredRecoveryPoint *time.Time `db:"measured_recovery_point" json:"measured_recovery_point,omitempty"`
	MeasuredRestoreTimeMS *int64     `db:"measured_restore_time_ms" json:"measured_restore_time_ms,omitempty"`
	ScratchTargetID       *string    `db:"scratch_target_id" json:"scratch_target_id,omitempty"`
	CleanupCompletedAt    *time.Time `db:"cleanup_completed_at" json:"cleanup_completed_at,omitempty"`

	ErrorCode     *string `db:"error_code" json:"error_code,omitempty"`
	ErrorMessage  *string `db:"error_message" json:"error_message,omitempty"`
	WorkerVersion *string `db:"worker_version" json:"worker_version,omitempty"`
}
