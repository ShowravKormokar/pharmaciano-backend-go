package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
)

type ArtifactStore interface {
	Put(ctx context.Context, key string, src io.Reader, sizeHint int64) (ObjectRef, error)
	Open(ctx context.Context, ref ObjectRef) (io.ReadCloser, error)
	Head(ctx context.Context, ref ObjectRef) (ObjectInfo, error)
	Delete(ctx context.Context, ref ObjectRef) error
	CreateDownloadAuthorization(ctx context.Context, ref ObjectRef, ttl time.Duration) (DownloadAuthorization, error)
}

type ObjectRef struct {
	Provider  string `json:"provider"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

type ObjectInfo struct {
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type DownloadAuthorization struct {
	Ref ObjectRef     `json:"-"`
	TTL time.Duration `json:"-"`
}

func MarshalObjectRef(ref ObjectRef) (string, error) {
	b, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func UnmarshalObjectRef(s string) (ObjectRef, error) {
	var ref ObjectRef
	if err := json.Unmarshal([]byte(s), &ref); err != nil {
		return ObjectRef{}, err
	}
	return ref, nil
}

type BackupServiceConfig struct {
	Enabled bool
}

// BackupRuntime is provided only by a fully configured worker runtime. Ready
// must include durable queue-loss reconciliation, pg_dump and verification
// handlers, authenticated encryption, private durable artifact storage, and
// worker audit events; task payloads contain only IDs.
type BackupRuntime interface {
	Ready() bool
	EnqueueBackup(context.Context, uuid.UUID) error
	EnqueueVerification(context.Context, uuid.UUID) error
}

// StepUpAuthorizer proves a fresh, session-bound operator verification.
type StepUpAuthorizer interface {
	RequireRecentStepUp(context.Context) error
}

type BackupServiceDependencies struct {
	Runtime BackupRuntime
	StepUp  StepUpAuthorizer
}

type Service struct {
	repo    *Repository
	db      *db.DB
	cfg     BackupServiceConfig
	runtime BackupRuntime
	stepUp  StepUpAuthorizer
	log     *zap.Logger
	metrics *telemetry.Metrics
}

func NewService(repo *Repository, database *db.DB, cfg BackupServiceConfig, metrics *telemetry.Metrics, log *zap.Logger, dependencies BackupServiceDependencies) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		repo: repo, db: database, cfg: cfg,
		runtime: dependencies.Runtime, stepUp: dependencies.StepUp,
		metrics: metrics, log: log,
	}
}

func (s *Service) available() bool {
	return s != nil && s.cfg.Enabled && s.runtime != nil && s.runtime.Ready()
}

func (s *Service) routesAvailable() bool {
	return s.available()
}

func (s *Service) requireEnabled() error {
	if !s.available() {
		return errs.FeatureDisabled("backup")
	}
	return nil
}

func (s *Service) requireStepUp(ctx context.Context) error {
	if s.stepUp == nil {
		return errs.FeatureDisabled("backup_step_up")
	}
	return s.stepUp.RequireRecentStepUp(ctx)
}

func (s *Service) requireSuperAdmin(ctx context.Context) error {
	if !appctx.IsSuperAdmin(ctx) {
		return errs.Forbidden("physical backup operations require the SUPER_ADMIN role")
	}
	return nil
}

func (s *Service) CreateBackupRun(ctx context.Context, actorID uuid.UUID, req *CreateBackupRunRequest) (*BackupRunResponse, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if validation := req.Validate(); len(validation) > 0 {
		return nil, errs.Validation("backup request validation failed", dtoFieldErrors(validation)...)
	}
	var run BackupRun
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.CreateQueued(ctx, BackupTriggerManual, &actorID, req.Reason)
		if err != nil {
			return errs.DatabaseError(err)
		}
		run = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.runtime.EnqueueBackup(ctx, run.ID); err != nil {
		s.log.Error("backup task enqueue failed; run remains queued for runtime reconciliation",
			zap.String("backup_id", run.ID.String()))
		return nil, errs.Internal(errors.New("backup task enqueue failed"))
	}
	if s.metrics != nil {
		s.metrics.CountBackupRun(string(BackupTriggerManual), string(BackupTypeFull), string(BackupRunStatusQueued))
	}
	resp := ToBackupRunResponse(run)
	return &resp, nil
}

func (s *Service) GetBackupRun(ctx context.Context, id uuid.UUID) (*BackupRunResponse, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	run, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("backup run")
		}
		return nil, errs.DatabaseError(err)
	}
	resp := ToBackupRunResponse(run)
	return &resp, nil
}

func (s *Service) ListBackupRuns(ctx context.Context, query *ListBackupRunsQuery) (*BackupRunPageResponse, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return nil, errs.Validation("backup list query validation failed", dtoFieldErrors(validation)...)
	}
	page, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	resp := &BackupRunPageResponse{
		Items:      make([]BackupRunResponse, 0, len(page.Items)),
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}
	for _, run := range page.Items {
		item := ToBackupRunResponse(run)
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

func (s *Service) RequestDownload(ctx context.Context, id uuid.UUID) (*DownloadAuthorization, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.requireStepUp(ctx); err != nil {
		return nil, err
	}
	return nil, errs.FeatureDisabled("backup_authenticated_download")
}

func (s *Service) RequestVerify(ctx context.Context, id uuid.UUID, vType VerificationType) (*BackupVerification, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if vType != VerificationTypeIntegrity && vType != VerificationTypeRestoreDrill {
		return nil, errs.Validation("unsupported verification type")
	}
	if err := s.requireStepUp(ctx); err != nil {
		return nil, err
	}
	v, err := s.repo.CreateVerification(ctx, id, vType)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("backup run")
		}
		return nil, errs.DatabaseError(err)
	}
	if err := s.runtime.EnqueueVerification(ctx, v.ID); err != nil {
		s.log.Error("backup verification enqueue failed; verification remains queued for runtime reconciliation",
			zap.String("verification_id", v.ID.String()))
		return nil, errs.Internal(errors.New("backup verification enqueue failed"))
	}
	if s.metrics != nil {
		s.metrics.CountBackupVerification(string(vType), string(VerificationStatusQueued))
	}
	return &v, nil
}

func (s *Service) RequestDelete(ctx context.Context, id uuid.UUID) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	if err := s.requireStepUp(ctx); err != nil {
		return err
	}
	return errs.FeatureDisabled("backup_safe_deletion")
}

func (s *Service) SetLegalHold(ctx context.Context, id uuid.UUID, legalHold bool) (*BackupRunResponse, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.requireStepUp(ctx); err != nil {
		return nil, err
	}
	return nil, errs.FeatureDisabled("backup_audit")
}

func (s *Service) ClaimRun(ctx context.Context, id uuid.UUID, workerID string, leaseUntil time.Time) (*BackupRun, error) {
	run, err := s.repo.ClaimRun(ctx, id, workerID, leaseUntil)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.Conflict("backup run is not queued or already claimed")
		}
		return nil, errs.DatabaseError(err)
	}
	if s.metrics != nil {
		s.metrics.CountBackupRun(string(run.Trigger), string(run.Type), string(BackupRunStatusInProgress))
	}
	return &run, nil
}

func (s *Service) RenewLease(ctx context.Context, id uuid.UUID, workerID string, generation int64, leaseUntil time.Time) (bool, error) {
	ok, err := s.repo.RenewLease(ctx, id, workerID, generation, leaseUntil)
	if err != nil {
		return false, errs.DatabaseError(err)
	}
	return ok, nil
}

func (s *Service) CompleteSucceeded(ctx context.Context, run BackupRun, workerID string, generation int64) (*BackupRunResponse, error) {
	completed, err := s.repo.CompleteSucceeded(ctx, run, workerID, generation)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.Conflict("backup run is not in progress or lease generation mismatch")
		}
		return nil, errs.DatabaseError(err)
	}
	if s.metrics != nil {
		s.metrics.CountBackupRun(string(run.Trigger), string(run.Type), string(BackupRunStatusSucceeded))
	}
	resp := ToBackupRunResponse(completed)
	return &resp, nil
}

func (s *Service) CompleteFailed(ctx context.Context, id uuid.UUID, workerID string, generation int64, errorCode, errorMessage string) (*BackupRunResponse, error) {
	run, err := s.repo.CompleteFailed(ctx, id, workerID, generation, errorCode, errorMessage)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.Conflict("backup run is not in progress or lease generation mismatch")
		}
		return nil, errs.DatabaseError(err)
	}
	if s.metrics != nil {
		s.metrics.CountBackupRun(string(run.Trigger), string(run.Type), string(BackupRunStatusFailed))
	}
	resp := ToBackupRunResponse(run)
	return &resp, nil
}

func (s *Service) StartVerification(ctx context.Context, id uuid.UUID) (*BackupVerification, error) {
	v, err := s.repo.StartVerification(ctx, id)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("verification")
		}
		return nil, errs.DatabaseError(err)
	}
	if s.metrics != nil {
		s.metrics.CountBackupVerification(string(v.Type), string(VerificationStatusInProgress))
	}
	return &v, nil
}

func (s *Service) CompleteVerification(ctx context.Context, result BackupVerification) (*BackupVerification, error) {
	v, err := s.repo.CompleteVerification(ctx, result)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("verification")
		}
		return nil, errs.DatabaseError(err)
	}
	if s.metrics != nil {
		s.metrics.CountBackupVerification(string(v.Type), string(v.Status))
	}
	return &v, nil
}

func dtoFieldErrors(source []error) []errs.FieldError {
	out := make([]errs.FieldError, 0, len(source))
	for _, e := range source {
		if fe, ok := e.(*backupDTOFieldError); ok {
			out = append(out, errs.FieldError{Field: fe.Field, Rule: fe.Rule, Message: fe.Message})
		}
	}
	return out
}
