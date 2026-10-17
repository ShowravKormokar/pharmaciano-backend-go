package backup

import (
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	defaultBackupPageSize = 20
	maxBackupPageSize     = 100
	maxBackupReasonRunes  = 500
)

// CreateBackupRunRequest accepts no infrastructure choices. The service takes
// the database target, storage, executable, and key material from trusted
// server-side configuration only.
type CreateBackupRunRequest struct {
	Reason *string `json:"reason,omitempty" validate:"omitempty,max=500"`
}

func (r *CreateBackupRunRequest) Validate() []error {
	if r == nil {
		return []error{backupFieldError("request", "required", "request is required")}
	}
	if r.Reason == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*r.Reason)
	if trimmed == "" {
		r.Reason = nil
		return nil
	}
	if len([]rune(trimmed)) > maxBackupReasonRunes {
		return []error{backupFieldError("reason", "max", "exceeds the maximum length")}
	}
	if strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return []error{backupFieldError("reason", "characters", "contains unsupported control characters")}
	}
	r.Reason = &trimmed
	return nil
}

// ListBackupRunsQuery is system-scoped. It accepts no organization, branch,
// storage, or artifact-reference selector.
type ListBackupRunsQuery struct {
	Status  BackupRunStatus `form:"status" json:"status,omitempty"`
	Type    BackupType      `form:"type" json:"type,omitempty"`
	Trigger BackupTrigger   `form:"trigger" json:"trigger,omitempty"`
	From    *time.Time      `form:"from" json:"from,omitempty"`
	To      *time.Time      `form:"to" json:"to,omitempty"`
	Cursor  string          `form:"cursor" json:"cursor,omitempty"`
	Limit   int             `form:"limit" json:"limit,omitempty"`
}

func (q *ListBackupRunsQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{backupFieldError("query", "required", "query is required")}
	}
	var errs []error
	if q.Status != "" && !q.Status.Valid() {
		errs = append(errs, backupFieldError("status", "oneof", "must be a supported backup status"))
	}
	if q.Type != "" && !q.Type.Valid() {
		errs = append(errs, backupFieldError("type", "oneof", "only full is supported"))
	}
	if q.Trigger != "" && !q.Trigger.Valid() {
		errs = append(errs, backupFieldError("trigger", "oneof", "must be manual or scheduled"))
	}
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		errs = append(errs, backupFieldError("from", "before", "must be earlier than to"))
	}
	q.Cursor = strings.TrimSpace(q.Cursor)
	if len(q.Cursor) > 1024 {
		errs = append(errs, backupFieldError("cursor", "max", "cursor exceeds the maximum length"))
	}
	if q.Limit == 0 {
		q.Limit = defaultBackupPageSize
	} else if q.Limit < 1 || q.Limit > maxBackupPageSize {
		errs = append(errs, backupFieldError("limit", "range", "must be between 1 and 100"))
	}
	return errs
}

type BackupRunListItem struct {
	ID                       uuid.UUID          `json:"id"`
	Type                     BackupType         `json:"backup_type"`
	Status                   BackupRunStatus    `json:"status"`
	Trigger                  BackupTrigger      `json:"trigger"`
	StartedAt                *time.Time         `json:"started_at,omitempty"`
	FinishedAt               *time.Time         `json:"finished_at,omitempty"`
	SizeBytes                *int64             `json:"size_bytes,omitempty"`
	LatestVerificationStatus VerificationStatus `json:"latest_verification_status"`
	VerifiedAt               *time.Time         `json:"verified_at,omitempty"`
	CreatedAt                time.Time          `json:"created_at"`
}

// BackupRunResponse excludes object references, wrapped keys, leases, and raw
// process diagnostics even for privileged callers.
type BackupRunResponse struct {
	BackupRunListItem
	TriggeredBy         *uuid.UUID `json:"triggered_by,omitempty"`
	Reason              *string    `json:"reason,omitempty"`
	SHA256              *string    `json:"sha256,omitempty"`
	EncryptionAlgorithm *string    `json:"encryption_algorithm,omitempty"`
	EncryptionKeyID     *string    `json:"encryption_key_id,omitempty"`
	PostgresVersion     *string    `json:"postgres_version,omitempty"`
	PGDumpVersion       *string    `json:"pg_dump_version,omitempty"`
	SchemaVersion       *int64     `json:"schema_version,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	LegalHold           bool       `json:"legal_hold"`
	ArtifactDeletedAt   *time.Time `json:"artifact_deleted_at,omitempty"`
	ErrorCode           *string    `json:"error_code,omitempty"`
	ErrorMessage        *string    `json:"error_message,omitempty"`
}

func ToBackupRunListItem(run BackupRun) BackupRunListItem {
	return BackupRunListItem{
		ID: run.ID, Type: run.Type, Status: run.Status, Trigger: run.Trigger,
		StartedAt: cloneTime(run.StartedAt), FinishedAt: cloneTime(run.FinishedAt),
		SizeBytes: cloneInt64(run.SizeBytes), LatestVerificationStatus: run.LatestVerificationStatus,
		VerifiedAt: cloneTime(run.VerifiedAt), CreatedAt: run.CreatedAt,
	}
}

func ToBackupRunResponse(run BackupRun) BackupRunResponse {
	return BackupRunResponse{
		BackupRunListItem:   ToBackupRunListItem(run),
		TriggeredBy:         cloneUUID(run.TriggeredBy),
		Reason:              cloneString(run.Reason),
		SHA256:              cloneString(run.SHA256),
		EncryptionAlgorithm: cloneString(run.EncryptionAlgorithm),
		EncryptionKeyID:     cloneString(run.EncryptionKeyID),
		PostgresVersion:     cloneString(run.PostgresVersion),
		PGDumpVersion:       cloneString(run.PGDumpVersion),
		SchemaVersion:       cloneInt64(run.SchemaVersion),
		ExpiresAt:           cloneTime(run.ExpiresAt),
		LegalHold:           run.LegalHold,
		ArtifactDeletedAt:   cloneTime(run.ArtifactDeletedAt),
		ErrorCode:           cloneString(run.ErrorCode),
		ErrorMessage:        cloneString(run.ErrorMessage),
	}
}

type backupDTOFieldError struct {
	Field, Rule, Message string
}

func (e *backupDTOFieldError) Error() string { return e.Message }

func backupFieldError(field, rule, message string) error {
	return &backupDTOFieldError{Field: field, Rule: rule, Message: message}
}

// BackupRunPageResponse is the paginated list payload for GET /backups.
type BackupRunPageResponse struct {
	Items      []BackupRunResponse `json:"items"`
	HasMore    bool                `json:"has_more"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
