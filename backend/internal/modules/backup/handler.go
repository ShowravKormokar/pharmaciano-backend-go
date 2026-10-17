package backup

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
	"backend/pkg/response"
)

type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

func New(database *db.DB, metrics *telemetry.Metrics, cursorSigningKey string, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if database == nil {
		return nil, errs.Validation("database is required")
	}
	codec, err := pagination.NewCursorCodec([]byte(cursorSigningKey))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationError, "configure backup cursor signing")
	}
	repo := NewRepository(database, codec)
	// No worker, step-up provider, encryption pipeline, or backup-specific
	// artifact store is composed by the application yet. Keep this handler
	// disabled rather than accepting work it cannot execute.
	svc := NewService(repo, database, BackupServiceConfig{Enabled: false}, metrics, log, BackupServiceDependencies{})
	return &Handler{svc: svc, val: v, log: log}, nil
}

func (h *Handler) CreateBackupRun(c *gin.Context) {
	var req CreateBackupRunRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	actorID := appctx.UserID(c.Request.Context())
	run, err := h.svc.CreateBackupRun(c.Request.Context(), actorID, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := response.Accepted(c.Writer, httpx.RequestID(c), locationForBackupRun(run.ID), run); err != nil && h.log != nil {
		h.log.Warn("write backup accepted response", zap.Error(err))
	}
}

func (h *Handler) GetBackupRun(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	run, err := h.svc.GetBackupRun(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, run)
}

func (h *Handler) ListBackupRuns(c *gin.Context) {
	var query ListBackupRunsQuery
	if err := httpx.BindQuery(c, h.val, &query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	page, err := h.svc.ListBackupRuns(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, page)
}

func (h *Handler) RequestDownload(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	auth, err := h.svc.RequestDownload(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, auth)
}

func (h *Handler) RequestVerify(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	vType := VerificationType(c.Query("type"))
	if vType == "" {
		vType = VerificationTypeIntegrity
	}
	v, err := h.svc.RequestVerify(c.Request.Context(), id, vType)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := response.Accepted(c.Writer, httpx.RequestID(c), locationForVerification(v.ID), v); err != nil && h.log != nil {
		h.log.Warn("write backup verification accepted response", zap.Error(err))
	}
}

func (h *Handler) RequestDelete(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.RequestDelete(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

func parseID(c *gin.Context) (uuid.UUID, error) {
	raw := c.Param("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter id must be a valid UUID").WithCause(err)
	}
	return id, nil
}

func locationForBackupRun(id uuid.UUID) string {
	return "/api/v1/backups/" + id.String()
}

func locationForVerification(id uuid.UUID) string {
	return "/api/v1/backups/verifications/" + id.String()
}
