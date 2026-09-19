package warehouse

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the warehouse module: it binds and validates
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

// Create handles POST /warehouses.
func (h *Handler) Create(c *gin.Context) {
	var req CreateWarehouseRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	w, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, w)
}

// List handles GET /warehouses?branch_id=&is_active=&is_main=&page=&limit=&sort=.
func (h *Handler) List(c *gin.Context) {
	var q ListWarehousesQuery
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

// ListByBranch handles the nested GET /branches/{id}/warehouses. The {id} path
// segment is the branch id (the route lives under the branch module's :id
// wildcard — see the path-parameter naming contract in routes.go). Any
// branch_id supplied in the query string is ignored: the path wins. Because the
// service list is org-scoped, a branch outside the caller's org yields an empty
// page rather than another tenant's warehouses.
func (h *Handler) ListByBranch(c *gin.Context) {
	branchID, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var q ListWarehousesQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	q.BranchID = &branchID // path parameter is authoritative over the query

	items, meta, err := h.svc.List(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

// Get handles GET /warehouses/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	w, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, w)
}

// Update handles PATCH /warehouses/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateWarehouseRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	w, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, w)
}

// Delete handles DELETE /warehouses/{id} (soft-delete → 204).
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

// parseIDParam extracts and validates a UUID path parameter, returning a 400
// VALIDATION_ERROR (not 404) when the segment is malformed so a bad id is never
// confused with a missing resource. The name argument lets the same helper serve
// both the own /warehouses/:id routes and the nested /branches/:id/warehouses.
func parseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}
