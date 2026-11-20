package report

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/validator"
)

const idempotencyKeyHeader = "Idempotency-Key"

type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

func NewHandler(svc *Service, val *validator.Validator, log *zap.Logger) *Handler {
	if svc == nil {
		panic("report handler: service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, val: val, log: log}
}

// Generate godoc
// @Summary Generate a report
// @Description Queues a report generation job and returns 202 Accepted.
// @Tags reports
// @Accept json
// @Produce json
// @Param request body GenerateRequest true "report generation request"
// @Param Idempotency-Key header string false "idempotency key"
// @Success 202 {object} GenerateResponse
// @Failure 400 {object} errs.ErrorResponse
// @Failure 403 {object} errs.ErrorResponse
// @Failure 409 {object} errs.ErrorResponse
// @Failure 429 {object} errs.ErrorResponse
// @Router /reports/generate [post]
func (h *Handler) Generate(c *gin.Context) {
	var req GenerateRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader(idempotencyKeyHeader))
	resp, err := h.svc.Generate(c.Request.Context(), &req, idempotencyKey)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusAccepted, resp)
}

// ListJobs godoc
// @Summary List report jobs
// @Description Lists the caller's report generation jobs for their organization.
// @Tags reports
// @Produce json
// @Param page query int false "page number" default(1)
// @Param page_size query int false "page size" default(20) maximum(100)
// @Success 200 {array} JobListItem
// @Failure 403 {object} errs.ErrorResponse
// @Failure 429 {object} errs.ErrorResponse
// @Router /reports/jobs [get]
func (h *Handler) ListJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	items, err := h.svc.ListJobs(c.Request.Context(), page, limit)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, items)
}

// GetJobStatus godoc
// @Summary Get report job status
// @Description Returns the status of a report generation job.
// @Tags reports
// @Produce json
// @Param id path string true "job id"
// @Success 200 {object} JobStatus
// @Failure 403 {object} errs.ErrorResponse
// @Failure 404 {object} errs.ErrorResponse
// @Router /reports/jobs/{id} [get]
func (h *Handler) GetJobStatus(c *gin.Context) {
	jobID := c.Param("id")
	if _, err := uuid.Parse(jobID); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid job id", errs.FieldError{Field: "id", Rule: "uuid"}))
		return
	}
	status, err := h.svc.GetJobStatus(c.Request.Context(), jobID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, status)
}

// CancelJob godoc
// @Summary Cancel a report job
// @Description Requests cancellation of a pending or running report job.
// @Tags reports
// @Produce json
// @Param id path string true "job id"
// @Success 200 {object} CancelResponse
// @Failure 403 {object} errs.ErrorResponse
// @Failure 404 {object} errs.ErrorResponse
// @Failure 409 {object} errs.ErrorResponse
// @Router /reports/jobs/{id}/cancel [post]
func (h *Handler) CancelJob(c *gin.Context) {
	jobID := c.Param("id")
	if _, err := uuid.Parse(jobID); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid job id", errs.FieldError{Field: "id", Rule: "uuid"}))
		return
	}
	resp, err := h.svc.CancelJob(c.Request.Context(), jobID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, resp)
}

// Download godoc
// @Summary Download a completed report artifact
// @Description Streams a completed report artifact (CSV) to the caller.
// @Tags reports
// @Produce application/octet-stream
// @Param id path string true "job id"
// @Success 200 {file} binary "report artifact"
// @Failure 403 {object} errs.ErrorResponse
// @Failure 404 {object} errs.ErrorResponse
// @Failure 410 {object} errs.ErrorResponse
// @Router /reports/jobs/{id}/download [get]
func (h *Handler) Download(c *gin.Context) {
	jobID := c.Param("id")
	if _, err := uuid.Parse(jobID); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid job id", errs.FieldError{Field: "id", Rule: "uuid"}))
		return
	}
	rc, contentType, fileName, sizeBytes, err := h.svc.Download(c.Request.Context(), jobID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	defer rc.Close()

	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", strconv.FormatInt(sizeBytes, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Stream(func(w io.Writer) bool {
		_, err := io.Copy(w, rc)
		return err != nil
	})
	c.Status(http.StatusOK)
}

// GetCatalog godoc
// @Summary Get report catalog
// @Description Returns available report definitions and their capabilities.
// @Tags reports
// @Produce json
// @Success 200 {array} CatalogEntry
// @Router /reports/catalog [get]
func (h *Handler) GetCatalog(c *gin.Context) {
	entries, err := h.svc.Catalog(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, entries)
}
