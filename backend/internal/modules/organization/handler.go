package organization

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the organization module. It only parses and binds
// input, delegates to the service, and renders the result or error through
// httpx — no business logic lives here.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the whole module (repository → service → handler) from its
// external dependencies and returns the handler, which also carries the route
// registration. This is the single constructor the composition root calls.
func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	repo := NewRepository(database)
	svc := NewService(repo, database)
	return &Handler{svc: svc, val: v, log: log}
}

// GetCurrent handles GET /organizations/current — the caller's own organization,
// resolved from the token's tenant scope (no id in the URL).
func (h *Handler) GetCurrent(c *gin.Context) {
	org, err := h.svc.GetCurrent(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, org)
}

// Get handles GET /organizations/{id}. Non-super-admins may only address their
// own organization; anything else is reported as NotFound by the service.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	org, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, org)
}

// Update handles PATCH /organizations/{id} — a partial profile update.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateOrganizationRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	org, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, org)
}

// Summary handles GET /organizations/{id}/summary — dashboard usage counters.
func (h *Handler) Summary(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	sum, err := h.svc.Summary(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, sum)
}

// parseID extracts and validates the {id} path parameter as a UUID, returning a
// 400 VALIDATION_ERROR (not a 404) when the segment is not a well-formed UUID so
// a malformed id is never confused with a missing resource.
func parseID(c *gin.Context) (uuid.UUID, error) {
	raw := c.Param("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter id must be a valid UUID").WithCause(err)
	}
	return id, nil
}
