package branch

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the branch module: it binds and validates input,
// delegates to the service, and renders the result or error through httpx.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the whole module (repository → service → handler) and returns
// the handler, which also carries route registration.
func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	repo := NewRepository(database)
	svc := NewService(repo, database)
	return &Handler{svc: svc, val: v, log: log}
}

// Create handles POST /branches.
func (h *Handler) Create(c *gin.Context) {
	var req CreateBranchRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, b)
}

// List handles GET /branches?is_active=&city=&q=&page=&limit=&sort=.
func (h *Handler) List(c *gin.Context) {
	var q ListBranchesQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.List(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

// Get handles GET /branches/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, b)
}

// Update handles PATCH /branches/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateBranchRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, b)
}

// Replace handles PUT /branches/{id}.
func (h *Handler) Replace(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ReplaceBranchRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Replace(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, b)
}

// Delete handles DELETE /branches/{id} (soft-delete → 204).
func (h *Handler) Delete(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// parseID extracts and validates the {id} path parameter as a UUID, returning a
// 400 VALIDATION_ERROR (not 404) when the segment is malformed so a bad id is
// never confused with a missing resource.
func parseID(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter id must be a valid UUID").WithCause(err)
	}
	return id, nil
}
