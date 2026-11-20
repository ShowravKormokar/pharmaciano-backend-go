// Package report implements the read-only Report module: versioned report
// definitions, asynchronous generation jobs, private artifact metadata,
// execution provenance, and attempt history.
//
// Report is a read-only consumer of source-domain facts. It owns no
// business-domain tables and never mutates Sale, Purchase, Inventory or
// Ledger records. See docs/adr/REPORT_CONTRACT.md.
package report

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Job is the durable, tenant-owned report generation job record. PostgreSQL is
// the authoritative job-state authority; Redis/Asynq is only a transport.
type Job struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	RequestedBy        uuid.UUID
	ReportCode         string
	DefinitionVersion  int
	Format             string
	NormalizedFilters  []byte // JSONB
	FiltersHash        []byte
	ScopeSnapshot      []byte // JSONB
	ScopeHash          []byte
	Status             string
	Priority           int16
	AvailableAt        time.Time
	RequestHash        []byte
	AttemptCount       int
	MaxAttempts        int
	QueuedAt           time.Time
	StartedAt          pgtype.Timestamptz
	CompletedAt        pgtype.Timestamptz
	CancelRequestedAt  pgtype.Timestamptz
	LeaseOwner         pgtype.Text
	LeaseUntil         pgtype.Timestamptz
	LeaseGeneration    int64
	HeartbeatAt        pgtype.Timestamptz
	FailureCode        pgtype.Text
	FailureMessageSafe pgtype.Text
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Version            int64
}

// Artifact is tenant-scoped private artifact metadata. Object keys are opaque
// and never public URLs.
type Artifact struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	JobID            uuid.UUID
	Format           string
	StorageProvider  string
	StorageObjectKey string
	MimeType         string
	FileNameSafe     string
	SizeBytes        int64
	ChecksumSHA256   string
	EncryptionKeyRef pgtype.Text
	Status           string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	DeletedAt        pgtype.Timestamptz
}

// Provenance is immutable execution evidence for a completed job.
type Provenance struct {
	ID                    uuid.UUID
	OrganizationID        uuid.UUID
	JobID                 uuid.UUID
	DefinitionVersion     int
	RendererVersion       string
	NormalizedRequestHash []byte
	ScopeSnapshot         []byte // JSONB
	SourceWatermarks      []byte // JSONB
	DataAsOf              pgtype.Timestamptz
	ConsistencyMode       string
	Timezone              string
	ResolvedFrom          pgtype.Timestamptz
	ResolvedTo            pgtype.Timestamptz
	RowCount              int64
	WarningCodes          []string
	Completeness          string
	ChecksumSHA256        string
	GeneratedAt           time.Time
}

// Attempt is append-only per-execution-attempt history.
type Attempt struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	JobID            uuid.UUID
	AttemptNumber    int
	LeaseGeneration  int64
	TaskID           string
	CorrelationID    pgtype.Text
	WorkerIdentity   pgtype.Text
	StartedAt        time.Time
	EndedAt          pgtype.Timestamptz
	OutcomeClass     string
	SafeErrorCode    pgtype.Text
	SafeErrorMessage pgtype.Text
	RetryDelayMs     pgtype.Int8
	CreatedAt        time.Time
}

// Idempotency is the durable request identity for POST /reports/generate.
type Idempotency struct {
	OrganizationID uuid.UUID
	RequestedBy    uuid.UUID
	KeyHMAC        string
	HMACKeyVersion int
	RequestHash    []byte
	JobID          pgtype.UUID
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// JobStatus is the safe, client-visible status DTO (contract §5.2).
type JobStatus struct {
	JobID             string     `json:"job_id"`
	ReportCode        string     `json:"report_code"`
	DefinitionVersion int        `json:"definition_version"`
	Format            string     `json:"format"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	Progress          int        `json:"progress"` // coarse 0..100
	RowCount          int64      `json:"row_count,omitempty"`
	SizeBytes         int64      `json:"size_bytes,omitempty"`
	ExpiresAt         time.Time  `json:"expires_at"`
	FailureCode       string     `json:"failure_code,omitempty"`
	FailureMessage    string     `json:"failure_message,omitempty"`
	DownloadAvailable bool       `json:"download_available"`
	DataAsOf          *time.Time `json:"data_as_of,omitempty"`
}

// Table names.
const (
	TableJobs        = "report_jobs"
	TableIdempotency = "report_idempotency"
	TableArtifacts   = "report_artifacts"
	TableProvenance  = "report_run_provenance"
	TableAttempts    = "report_job_attempts"
)

// Lifecycle statuses (contract §5.2).
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusRetryWait = "retry_wait"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusCancelReq = "cancel_requested"
)

// Terminal statuses.
var TerminalStatuses = map[string]struct{}{
	StatusSucceeded: {},
	StatusFailed:    {},
	StatusCancelled: {},
}

// Claimable statuses for the worker queue index.
var ClaimableStatuses = map[string]struct{}{
	StatusQueued:    {},
	StatusRetryWait: {},
}

// Artifact statuses (contract §12).
const (
	ArtifactStaging   = "staging"
	ArtifactAvailable = "available"
	ArtifactRevoked   = "revoked"
	ArtifactDeleting  = "deleting"
	ArtifactDeleted   = "deleted"
	ArtifactCorrupt   = "corrupt"
	ArtifactExpired   = "expired"
)

// Provenance completeness values.
const (
	CompletenessComplete = "complete"
	CompletenessPartial  = "partial"
	CompletenessDegraded = "degraded"
)

// Consistency modes (contract §7.1 / §10.3).
const (
	ConsistencyRepeatableRead = "repeatable_read"
	ConsistencySnapshot       = "snapshot"
	ConsistencyBestEffort     = "best_effort"
)

// Formats (contract §11).
const (
	FormatCSV  = "csv"
	FormatXLSX = "xlsx"
	FormatPDF  = "pdf"
)

// DefaultHMACKeyVersion is the starting server-keyed HMAC key version for
// client idempotency keys. Rotate by incrementing and keeping old versions
// verifiable during the overlap window.
const DefaultHMACKeyVersion = 1
