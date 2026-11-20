// Package-level request/response DTOs for the Report HTTP API.
// See REPORT_CONTRACT §5.
package report

import (
	"time"

	"github.com/google/uuid"
)

// GenerateRequest is the POST /reports/generate body.
type GenerateRequest struct {
	Type    string          `json:"type" validate:"required"`
	Filters GenerateFilters `json:"filters" validate:"required"`
	Format  string          `json:"format" validate:"required"`
}

// GenerateFilters holds the strict, bounded filter shape.
type GenerateFilters struct {
	From         string      `json:"from" validate:"required"`
	To           string      `json:"to" validate:"required"`
	BranchIDs    []uuid.UUID `json:"branch_ids" validate:"uuid"`
	WarehouseIDs []uuid.UUID `json:"warehouse_ids" validate:"uuid"`
}

// GenerateResponse is the 202 Accepted body.
type GenerateResponse struct {
	JobID      string    `json:"job_id"`
	StatusURL  string    `json:"status_url"`
	AcceptedAt time.Time `json:"accepted_at"`
}

// CatalogEntry is a client-visible catalogue item (GET /reports/catalog).
type CatalogEntry struct {
	Code              string   `json:"code"`
	Version           int      `json:"version"`
	Title             string   `json:"title"`
	Formats           []string `json:"formats"`
	MaxRows           int      `json:"max_rows"`
	MaxRuntimeSeconds int      `json:"max_runtime_seconds"`
}

// JobListItem is a row of GET /reports/jobs.
type JobListItem struct {
	JobID             string     `json:"job_id"`
	ReportCode        string     `json:"report_code"`
	DefinitionVersion int        `json:"definition_version"`
	Format            string     `json:"format"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	QueuedAt          time.Time  `json:"queued_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	RowCount          int64      `json:"row_count,omitempty"`
	ExpiresAt         time.Time  `json:"expires_at"`
	DownloadAvailable bool       `json:"download_available"`
}

// CancelResponse is the POST /reports/jobs/{id}/cancel body.
type CancelResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// Schedule DTOs are deferred to Phase 4 per REPORT_CONTRACT §22. Placeholder
// shapes are intentionally not added to avoid an unowned API surface.
