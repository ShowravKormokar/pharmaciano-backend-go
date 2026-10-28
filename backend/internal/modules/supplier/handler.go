package supplier

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the supplier module: it binds and validates
// input, delegates to the service, and renders the result or error through
// httpx.
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

// Create handles POST /suppliers.
func (h *Handler) Create(c *gin.Context) {
	var req CreateSupplierRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	s, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, s)
}

// List handles GET /suppliers?q=&city=&is_active=&page=&limit=&sort=.
func (h *Handler) List(c *gin.Context) {
	var q ListSuppliersQuery
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

// Get handles GET /suppliers/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	s, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, s)
}

// Update handles PATCH /suppliers/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateSupplierRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	s, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, s)
}

// Delete handles DELETE /suppliers/{id} (soft-delete/archive → 204).
func (h *Handler) Delete(c *gin.Context) {
	id, err := parseIDParam(c, "id")
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

// parseIDParam extracts and validates a UUID path parameter.
func parseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}