// Package-internal Report application service. Implements the generate /
// list / status / cancel / download flow and the worker execution entry point.
// See REPORT_CONTRACT §5, §8, §9, §10.
package report

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/modules/report/ports"
	"backend/internal/platform/config"
	"backend/internal/platform/outbox"
	"backend/internal/platform/storage"
	"backend/internal/platform/telemetry"
)

// Service wires the Report repository, source read port, storage and outbox
// into the report generation workflow.
type Service struct {
	repo           *Repository
	sales          ports.SalesReader
	storage        storage.Store
	outbox         *outbox.Store
	registry       *Registry
	cfg            config.ReportConfig
	log            *zap.Logger
	metrics        *telemetry.Metrics
	now            func() time.Time
	hmacKeyVersion int
}

// New assembles the Report service.
func NewService(repo *Repository, sales ports.SalesReader, storage storage.Store,
	outboxStore *outbox.Store, registry *Registry, cfg config.ReportConfig,
	log *zap.Logger, metrics *telemetry.Metrics) *Service {
	now := time.Now
	if now == nil {
		now = time.Now
	}
	if cfg.DefaultPageSize <= 0 {
		cfg.DefaultPageSize = 20
	}
	if cfg.MaxPageSize <= 0 {
		cfg.MaxPageSize = 100
	}
	if cfg.MaxExportRows <= 0 {
		cfg.MaxExportRows = constants.MaxExportRows
	}
	if cfg.MaxExecutionSeconds <= 0 {
		cfg.MaxExecutionSeconds = 300
	}
	if cfg.MaxFileSizeMB <= 0 {
		cfg.MaxFileSizeMB = 25
	}
	if cfg.MaxDateRangeDays <= 0 {
		cfg.MaxDateRangeDays = 90
	}
	if cfg.MaxConcurrencyPerOrg <= 0 {
		cfg.MaxConcurrencyPerOrg = 5
	}
	if cfg.DefaultRetentionDays <= 0 {
		cfg.DefaultRetentionDays = 7
	}
	if cfg.StorageNamespace == "" {
		cfg.StorageNamespace = "reports"
	}
	if cfg.HMACKeyVersion <= 0 {
		cfg.HMACKeyVersion = DefaultHMACKeyVersion
	}
	return &Service{
		repo:           repo,
		sales:          sales,
		storage:        storage,
		outbox:         outboxStore,
		registry:       registry,
		cfg:            cfg,
		log:            log,
		metrics:        metrics,
		now:            now,
		hmacKeyVersion: cfg.HMACKeyVersion,
	}
}

// Catalog returns currently enabled definitions (contract §5, GET /reports/catalog).
func (s *Service) Catalog(ctx context.Context) ([]CatalogEntry, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "view") {
		return nil, errs.Forbidden("reports:view is required")
	}
	codes := s.registry.Codes()
	out := make([]CatalogEntry, 0, len(codes))
	for _, code := range codes {
		def, _ := s.registry.Latest(code)
		if def == nil {
			continue
		}
		out = append(out, CatalogEntry{
			Code:              def.Code,
			Version:           def.Version,
			Title:             def.Title,
			Formats:           def.Formats,
			MaxRows:           def.MaxRows,
			MaxRuntimeSeconds: def.MaxRuntimeSeconds,
		})
	}
	return out, nil
}

// Generate enqueues a report generation job atomically with durable idempotency
// and an outbox event (contract §5.1, §9.1).
func (s *Service) Generate(ctx context.Context, req *GenerateRequest, idempotencyKey string) (*GenerateResponse, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "export") {
		return nil, errs.Forbidden("reports:export is required")
	}
	def, ok := s.registry.Latest(req.Type)
	if !ok {
		return nil, errs.NotFound("report type")
	}
	if !contains(def.Formats, req.Format) {
		return nil, errs.Validation("format is not supported for this report type", errs.FieldError{
			Field: "format", Rule: "oneof", Message: "unsupported format",
		})
	}
	if idempotencyKey == "" {
		return nil, errs.Validation("idempotency key is required", errs.FieldError{
			Field: constants.HeaderIdempotencyKey, Rule: "required",
		})
	}

	from, to, err := parseDateRange(req.Filters.From, req.Filters.To, s.cfg.MaxDateRangeDays)
	if err != nil {
		return nil, err
	}

	scope, err := ResolveScope(ctx, req.Filters.BranchIDs)
	if err != nil {
		return nil, err
	}

	filters := map[string]any{
		"type": req.Type, "format": req.Format, "from": from.UTC().Format("2006-01-02T15:04:05Z"),
		"to":         to.UTC().Format("2006-01-02T15:04:05Z"),
		"branch_ids": req.Filters.BranchIDs, "scope": scope.String(),
	}
	filterBytes, err := MarshalFilters(filters)
	if err != nil {
		return nil, errs.Internal(err)
	}
	requestHash := HashFilters(filterBytes)
	keyHMAC := HMACHex(idempotencyKey, s.hmacKeyVersion)

	orgID := appctx.OrgID(ctx)
	requestedBy := appctx.UserID(ctx)

	var jobID uuid.UUID
	var acceptedAt time.Time
	var replayed bool

	err = s.repo.db.WithTx(ctx, func(txCtx context.Context) error {
		idem := &Idempotency{
			OrganizationID: orgID, RequestedBy: requestedBy,
			KeyHMAC: keyHMAC, HMACKeyVersion: s.hmacKeyVersion,
			RequestHash: requestHash, ExpiresAt: s.now().Add(24 * time.Hour),
		}
		if err := s.repo.CreateIdempotency(txCtx, idem); err != nil {
			if errors.Is(err, ErrIdempotencyReplay) {
				// Same key + same request: return the original job.
				if idem.JobID.Valid {
					jobID = uuid.UUID(idem.JobID.Bytes)
					replayed = true
					return nil
				}
				return ErrIdempotencyConflict
			}
			if errors.Is(err, ErrIdempotencyConflict) {
				return err
			}
			return errs.DatabaseError(err)
		}
		if replayed {
			return nil
		}

		j := &Job{
			OrganizationID:    orgID,
			RequestedBy:       requestedBy,
			ReportCode:        req.Type,
			DefinitionVersion: def.Version,
			Format:            req.Format,
			NormalizedFilters: filterBytes,
			FiltersHash:       HashFilters(filterBytes),
			ScopeSnapshot:     []byte(scope.String()),
			ScopeHash:         HashFilters([]byte(scope.String())),
			Priority:          0,
			AvailableAt:       s.now().UTC(),
			RequestHash:       requestHash,
			AttemptCount:      0,
			MaxAttempts:       4,
		}
		if err := s.repo.CreateJob(txCtx, j); err != nil {
			return errs.DatabaseError(err)
		}
		jobID = j.ID
		acceptedAt = j.QueuedAt

		if err := s.repo.LinkIdempotencyJob(txCtx, orgID, requestedBy, keyHMAC, jobID); err != nil {
			return err
		}

		// Durable outbox event in the same transaction.
		payload, _ := json.Marshal(map[string]any{
			"job_id":       jobID.String(),
			"report_code":  req.Type,
			"format":       req.Format,
			"requested_by": requestedBy.String(),
			"organization": orgID.String(),
		})
		event := outbox.Event{
			ID:               uuid.New(),
			OrganizationID:   orgID,
			SourceModule:     "report",
			AggregateType:    "report_job",
			AggregateID:      jobID,
			AggregateVersion: 1,
			EventType:        "report.requested",
			SchemaVersion:    1,
			Payload:          payload,
			OccurredAt:       s.now().UTC(),
		}
		if err := s.outbox.Append(txCtx, event); err != nil {
			return errs.DatabaseError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if replayed {
		j, gerr := s.repo.GetJobByID(ctx, orgID, jobID)
		if gerr != nil {
			return nil, gerr
		}
		return &GenerateResponse{
			JobID:      j.ID.String(),
			StatusURL:  fmt.Sprintf("/api/v1/reports/jobs/%s", j.ID.String()),
			AcceptedAt: j.QueuedAt,
		}, nil
	}

	return &GenerateResponse{
		JobID:      jobID.String(),
		StatusURL:  fmt.Sprintf("/api/v1/reports/jobs/%s", jobID.String()),
		AcceptedAt: acceptedAt,
	}, nil
}

// ListJobs returns paginated, tenant-scoped jobs (contract §5, GET /reports/jobs).
func (s *Service) ListJobs(ctx context.Context, page, limit int) ([]*JobListItem, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "view") {
		return nil, errs.Forbidden("reports:view is required")
	}
	orgID := appctx.OrgID(ctx)
	requestedBy := appctx.UserID(ctx)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > s.cfg.MaxPageSize {
		limit = s.cfg.MaxPageSize
	}
	jobs, err := s.repo.ListJobs(ctx, orgID, requestedBy, page, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*JobListItem, 0, len(jobs))
	for _, j := range jobs {
		downloadAvailable := j.Status == StatusSucceeded
		expiresAt := j.QueuedAt.Add(time.Duration(s.cfg.DefaultRetentionDays) * 24 * time.Hour)
		out = append(out, &JobListItem{
			JobID:             j.ID.String(),
			ReportCode:        j.ReportCode,
			DefinitionVersion: j.DefinitionVersion,
			Format:            j.Format,
			Status:            j.Status,
			CreatedAt:         j.CreatedAt,
			QueuedAt:          j.QueuedAt,
			CompletedAt:       completedAtPtr(j),
			RowCount:          0,
			ExpiresAt:         expiresAt,
			DownloadAvailable: downloadAvailable,
		})
	}
	return out, nil
}

func completedAtPtr(j *Job) *time.Time {
	if j.CompletedAt.Valid {
		t := j.CompletedAt.Time
		return &t
	}
	return nil
}

// GetJobStatus returns safe status fields only (contract §5.2).
func (s *Service) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "view") {
		return nil, errs.Forbidden("reports:view is required")
	}
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return nil, errs.Validation("invalid job id", errs.FieldError{Field: "job_id", Rule: "uuid"})
	}
	orgID := appctx.OrgID(ctx)
	requestedBy := appctx.UserID(ctx)
	j, err := s.repo.GetJob(ctx, orgID, requestedBy, parsed)
	if err != nil {
		return nil, err
	}
	st := &JobStatus{
		JobID:             j.ID.String(),
		ReportCode:        j.ReportCode,
		DefinitionVersion: j.DefinitionVersion,
		Format:            j.Format,
		Status:            j.Status,
		CreatedAt:         j.CreatedAt,
		QueuedAt:          j.QueuedAt,
		Progress:          progressFor(j),
		ExpiresAt:         j.QueuedAt.Add(time.Duration(s.cfg.DefaultRetentionDays) * 24 * time.Hour),
		DownloadAvailable: j.Status == StatusSucceeded,
	}
	if j.StartedAt.Valid {
		t := j.StartedAt.Time
		st.StartedAt = &t
	}
	if j.CompletedAt.Valid {
		t := j.CompletedAt.Time
		st.CompletedAt = &t
		st.RowCount = 0 // filled from provenance at download time
	}
	if j.FailureCode.Valid {
		st.FailureCode = j.FailureCode.String
	}
	if j.FailureMessageSafe.Valid {
		st.FailureMessage = j.FailureMessageSafe.String
	}
	return st, nil
}

// CancelJob requests cooperative cancellation of a job (contract §5, §9.3).
func (s *Service) CancelJob(ctx context.Context, jobID string) (*CancelResponse, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "export") {
		return nil, errs.Forbidden("reports:export is required")
	}
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return nil, errs.Validation("invalid job id", errs.FieldError{Field: "job_id", Rule: "uuid"})
	}
	orgID := appctx.OrgID(ctx)
	requestedBy := appctx.UserID(ctx)
	j, err := s.repo.GetJob(ctx, orgID, requestedBy, parsed)
	if err != nil {
		return nil, err
	}
	if j.Status == StatusSucceeded || j.Status == StatusFailed || j.Status == StatusCancelled {
		return nil, errs.Business("", "job is already terminal")
	}
	ok, err := s.repo.CancelRequested(ctx, orgID, j.ID, j.LeaseGeneration, j.LeaseOwner.String, s.now().UTC())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.Conflict("job could not be cancelled; it may already be claimed by a worker")
	}
	return &CancelResponse{JobID: j.ID.String(), Status: j.Status}, nil
}

// Download authorizes and streams a ready artifact (contract §5.4).
func (s *Service) Download(ctx context.Context, jobID string) (io.ReadCloser, string, string, int64, error) {
	if !appctx.HasPermission(ctx, constants.ModuleReports, "view") {
		return nil, "", "", 0, errs.Forbidden("reports:view is required")
	}
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return nil, "", "", 0, errs.Validation("invalid job id", errs.FieldError{Field: "job_id", Rule: "uuid"})
	}
	orgID := appctx.OrgID(ctx)
	requestedBy := appctx.UserID(ctx)
	j, err := s.repo.GetJob(ctx, orgID, requestedBy, parsed)
	if err != nil {
		return nil, "", "", 0, err
	}
	if j.Status != StatusSucceeded {
		return nil, "", "", 0, errs.Business("", "job is not ready for download")
	}
	a, err := s.repo.GetArtifact(ctx, orgID, j.ID, j.Format)
	if err != nil {
		return nil, "", "", 0, err
	}
	if a.Status != ArtifactAvailable {
		return nil, "", "", 0, errs.Business("", "artifact is not available")
	}
	if s.now().UTC().After(a.ExpiresAt) {
		return nil, "", "", 0, errs.Business("", "artifact has expired")
	}
	rc, info, err := s.storage.Get(ctx, storage.ObjectRef{Namespace: a.StorageProvider, Key: a.StorageObjectKey})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, "", "", 0, errs.NotFound("report artifact")
		}
		return nil, "", "", 0, errs.Internal(err)
	}
	// Integrity: verify checksum before exposing bytes.
	if info.SHA256 != "" && info.SHA256 != a.ChecksumSHA256 {
		_ = rc.Close()
		return nil, "", "", 0, errs.Business("", "artifact integrity check failed")
	}
	return rc, info.ContentType, a.FileNameSafe, a.SizeBytes, nil
}

// Execute is the worker entry point for a report generation job. It claims,
// reauthorizes, extracts, renders, uploads and finalizes (contract §9.2, §10.2).
func (s *Service) Execute(ctx context.Context, jobID string) error {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return errs.Validation("invalid job id", errs.FieldError{Field: "job_id", Rule: "uuid"})
	}
	orgID := appctx.OrgID(ctx)
	j, err := s.repo.GetJobByID(ctx, orgID, parsed)
	if err != nil {
		return err
	}
	attempt := &Attempt{
		OrganizationID: orgID,
		JobID:          j.ID,
		AttemptNumber:  j.AttemptCount + 1,
		TaskID:         "report:generate",
		OutcomeClass:   "failed",
		StartedAt:      s.now().UTC(),
	}
	defer func() {
		attempt.EndedAt = pgtypeTimestamptz(s.now().UTC())
		_ = s.repo.CreateAttempt(context.Background(), attempt)
	}()

	// Claim with fencing.
	owner := fmt.Sprintf("report-worker-%s", jobID[:8])
	lease := 5 * time.Minute
	claimed, err := s.repo.ClaimJob(ctx, orgID, j.ID, owner, lease, s.now().UTC())
	if err != nil {
		attempt.OutcomeClass = "failed"
		attempt.SafeErrorCode = pgtypeText("REPORT_CLAIM_FAILED")
		if errors.Is(err, ErrJobNotClaimable) {
			return nil // another worker owns it or not claimable
		}
		return err
	}
	attempt.LeaseGeneration = claimed.LeaseGeneration

	// Reauthorize: org + requester still active (stored scope is provenance, not a grant).
	if err := s.reauthorize(ctx, orgID, j.RequestedBy, claimed.ScopeSnapshot); err != nil {
		attempt.SafeErrorCode = pgtypeText("REPORT_SCOPE_FORBIDDEN")
		return err
	}

	def, ok := s.registry.Latest(j.ReportCode)
	if !ok {
		attempt.SafeErrorCode = pgtypeText("REPORT_TYPE_UNSUPPORTED")
		return errs.NotFound("report type")
	}
	if !contains(def.Formats, j.Format) {
		attempt.SafeErrorCode = pgtypeText("REPORT_FORMAT_UNSUPPORTED")
		return errs.Validation("format unsupported", errs.FieldError{Field: "format"})
	}

	// Parse filters from normalized_filters JSON.
	var filters struct {
		From      string      `json:"from"`
		To        string      `json:"to"`
		BranchIDs []uuid.UUID `json:"branch_ids"`
	}
	if err := json.Unmarshal(j.NormalizedFilters, &filters); err != nil {
		attempt.SafeErrorCode = pgtypeText("REPORT_FILTER_INVALID")
		return errs.Validation("invalid filters", errs.FieldError{Field: "filters"})
	}
	from, to, err := parseDateRange(filters.From, filters.To, s.cfg.MaxDateRangeDays)
	if err != nil {
		attempt.SafeErrorCode = pgtypeText("REPORT_RANGE_EXCEEDED")
		return err
	}

	// Read source data through the typed port (bounded by max rows + 1).
	limit := s.cfg.MaxExportRows + 1
	lines, err := s.sales.SalesRegister(ctx, orgID, branchIDsFromScope(claimed), from, to, limit)
	if err != nil {
		attempt.SafeErrorCode = pgtypeText("REPORT_SOURCE_UNAVAILABLE")
		return errs.DatabaseError(err)
	}
	truncated := len(lines) > s.cfg.MaxExportRows
	if truncated {
		lines = lines[:s.cfg.MaxExportRows]
	}

	// Render CSV into a bounded buffer.
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"sale_id", "invoice_no", "sold_at", "branch_id", "cashier_id", "customer_id",
		"status", "medicine_id", "variant_id", "batch_id", "batch_no", "quantity",
		"unit_price_minor", "cost_price_minor", "line_total_minor"})
	for _, l := range lines {
		row := []string{
			l.SaleID.String(), l.InvoiceNo, l.SoldAt.UTC().Format("2006-01-02T15:04:05Z"),
			l.BranchID.String(), str(l.WarehouseID), l.CashierID.String(),
			str(l.CustomerID), l.Status, l.MedicineID.String(),
			str(l.VariantID), l.BatchID.String(), l.BatchNo,
			strconv.Itoa(l.Quantity), strconv.FormatInt(l.UnitPriceMin, 10),
			strconv.FormatInt(l.CostPriceMin, 10), strconv.FormatInt(l.LineTotalMin, 10),
		}
		// Formula-injection defense: neutralize untrusted cells.
		for i, cell := range row {
			row[i] = neutralizeFormula(cell)
		}
		if err := w.Write(row); err != nil {
			attempt.SafeErrorCode = pgtypeText("RENDER_FAILED")
			return errs.Internal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		attempt.SafeErrorCode = pgtypeText("RENDER_FAILED")
		return errs.Internal(err)
	}
	payload := buf.Bytes()
	if int64(len(payload)) > int64(s.cfg.MaxFileSizeMB)*1024*1024 {
		attempt.SafeErrorCode = pgtypeText("REPORT_RANGE_EXCEEDED")
		return errs.Business("", "artifact exceeds max file size")
	}

	sum := sha256Sum(payload)
	checksum := hex.EncodeToString(sum[:])
	fileName := artifactFileName(j.ReportCode, from, to, j.ID)

	// Upload to private storage (staging object key).
	ref, err := s.storage.Put(ctx, storage.ObjectInput{
		Namespace:    s.cfg.StorageNamespace,
		OriginalName: fileName,
		ContentType:  "text/csv",
		Reader:       bytes.NewReader(payload),
		SizeHint:     int64(len(payload)),
	})
	if err != nil {
		attempt.SafeErrorCode = pgtypeText("REPORT_SOURCE_UNAVAILABLE")
		return errs.Internal(err)
	}
	expiresAt := s.now().UTC().Add(time.Duration(s.cfg.DefaultRetentionDays) * 24 * time.Hour)
	artifact := &Artifact{
		OrganizationID:   orgID,
		JobID:            j.ID,
		Format:           j.Format,
		StorageProvider:  "local",
		StorageObjectKey: ref.Key,
		MimeType:         "text/csv",
		FileNameSafe:     fileName,
		SizeBytes:        int64(len(payload)),
		ChecksumSHA256:   checksum,
		ExpiresAt:        expiresAt,
	}
	if err := s.repo.CreateArtifactStaging(ctx, artifact); err != nil {
		return errs.DatabaseError(err)
	}

	// Provenance.
	prov := &Provenance{
		OrganizationID:        orgID,
		JobID:                 j.ID,
		DefinitionVersion:     j.DefinitionVersion,
		RendererVersion:       "csv/v1",
		NormalizedRequestHash: j.RequestHash,
		ScopeSnapshot:         j.ScopeSnapshot,
		SourceWatermarks:      []byte("{}"),
		DataAsOf:              pgtypeTimestamptz(to),
		ConsistencyMode:       ConsistencyBestEffort,
		Timezone:              "UTC",
		ResolvedFrom:          pgtypeTimestamptz(from),
		ResolvedTo:            pgtypeTimestamptz(to),
		RowCount:              int64(len(lines)),
		WarningCodes:          nil,
		Completeness:          CompletenessComplete,
		ChecksumSHA256:        checksum,
	}
	if truncated {
		prov.Completeness = CompletenessPartial
		prov.WarningCodes = []string{"ROWS_TRUNCATED"}
	}

	if err := s.repo.FinalizeSucceeded(ctx, orgID, j.ID, claimed.LeaseGeneration, artifact, prov); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			return nil // stale worker; do not overwrite
		}
		return err
	}
	attempt.OutcomeClass = "succeeded"
	return nil
}

func (s *Service) reauthorize(ctx context.Context, orgID, requesterID uuid.UUID, scopeSnapshot []byte) error {
	// Reverify the owning organization is active.
	var active bool
	err := s.repo.db.FromCtx(ctx).QueryRow(ctx, `SELECT is_active FROM organizations WHERE id = $1`, orgID).Scan(&active)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !active {
		return errs.Forbidden("organization is not active")
	}
	// Reverify the requester is active.
	var reqActive bool
	err = s.repo.db.FromCtx(ctx).QueryRow(ctx, `SELECT status = 'active' FROM users WHERE id = $1`, requesterID).Scan(&reqActive)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !reqActive {
		return errs.Forbidden("requester is not active")
	}
	_ = scopeSnapshot
	return nil
}

func branchIDsFromScope(j *Job) []uuid.UUID {
	var scope struct {
		BranchIDs []uuid.UUID `json:"branch_ids"`
	}
	if err := json.Unmarshal(j.ScopeSnapshot, &scope); err != nil {
		return nil
	}
	return scope.BranchIDs
}

func str(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func neutralizeFormula(cell string) string {
	if cell == "" {
		return cell
	}
	// Neutralize spreadsheet formula triggers per contract §11.2.
	first := strings.TrimSpace(cell)
	if len(first) > 0 && (first[0] == '=' || first[0] == '+' || first[0] == '-' ||
		first[0] == '@' || first[0] == '\t' || first[0] == '\r') {
		return "'" + cell
	}
	return cell
}

func artifactFileName(code string, from, to time.Time, jobID uuid.UUID) string {
	short := jobID.String()[:8]
	return fmt.Sprintf("pharmaciano_%s_%s_to_%s_%s.csv",
		code, from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"), short)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func progressFor(j *Job) int {
	switch j.Status {
	case StatusQueued:
		return 5
	case StatusRunning, StatusCancelReq:
		return 50
	case StatusSucceeded:
		return 100
	default:
		return 0
	}
}

func parseDateRange(from, to string, maxDays int) (time.Time, time.Time, error) {
	parse := func(s string) (time.Time, error) {
		s = strings.TrimSpace(s)
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), nil
		}
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
		return time.Time{}, errs.Validation("invalid date", errs.FieldError{Field: "date", Rule: "datetime"})
	}
	f, err := parse(from)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	t, err := parse(to)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !f.Before(t) {
		return time.Time{}, time.Time{}, errs.Validation("from must be earlier than to", errs.FieldError{Field: "from"})
	}
	if t.Sub(f) > time.Duration(maxDays)*24*time.Hour {
		return time.Time{}, time.Time{}, errs.Validation("date range exceeds maximum", errs.FieldError{Field: "range"})
	}
	return f, t, nil
}

// Small pgtype helpers for nullable scans in this package.
func pgtypeTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
func pgtypeText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}
