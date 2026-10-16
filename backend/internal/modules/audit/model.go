package audit

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// AuditOutcome is the persisted outcome constraint on audit_logs.
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
)

// Valid reports whether the value satisfies the database outcome constraint.
func (o AuditOutcome) Valid() bool {
	return o == AuditOutcomeSuccess || o == AuditOutcomeFailure
}

// AuditLog is the persistence projection of one row in the existing,
// time-partitioned audit_logs table. It is immutable evidence, not a normal
// lifecycle-managed entity, and must be mapped to an authorized response DTO
// rather than serialized directly.
//
// OccurredAt maps to the database's created_at partition key. The database
// primary key is (id, created_at); id alone is not a globally constrained key.
// This type intentionally does not embed common.BaseModel or
// common.AuditColumns: audit_logs has neither updated_at/deleted_at nor
// created_by/updated_by columns.
// IP remains a nullable string to match the repository's established INET
// convention: cast writes to ::inet and select host(ip) for string scans.
type AuditLog struct {
	ID         uuid.UUID `db:"id" json:"-"`
	OccurredAt time.Time `db:"created_at" json:"-"`

	OrganizationID *uuid.UUID `db:"organization_id" json:"-"`
	BranchID       *uuid.UUID `db:"branch_id" json:"-"`
	UserID         *uuid.UUID `db:"user_id" json:"-"`
	RoleName       *string    `db:"role_name" json:"-"`
	SessionID      *uuid.UUID `db:"session_id" json:"-"`
	RequestID      *string    `db:"request_id" json:"-"`

	Module string `db:"module" json:"-"`
	Action string `db:"action" json:"-"`

	EntityType *string    `db:"entity_type" json:"-"`
	EntityID   *uuid.UUID `db:"entity_id" json:"-"`

	IP        *string `db:"ip" json:"-"`
	Browser   *string `db:"browser" json:"-"`
	OS        *string `db:"os" json:"-"`
	Device    *string `db:"device" json:"-"`
	Location  *string `db:"location" json:"-"`
	UserAgent *string `db:"user_agent" json:"-"`

	Details    common.JSONMap `db:"details" json:"-"`
	BeforeData common.JSONMap `db:"before_data" json:"-"`
	AfterData  common.JSONMap `db:"after_data" json:"-"`

	Outcome    AuditOutcome `db:"outcome" json:"-"`
	ErrorCode  *string      `db:"error_code" json:"-"`
	DurationMS *int32       `db:"duration_ms" json:"-"`
}

// AuditLogArchive is the archival-table projection. The current migration
// copies the audit_logs columns into audit_log_archive but gives the archive a
// separate primary key on id; archival does not change the event payload.
type AuditLogArchive AuditLog
