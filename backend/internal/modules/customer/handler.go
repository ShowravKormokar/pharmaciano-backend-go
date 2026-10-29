package customer

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the customer module.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the customer module (repository → service → handler).
func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	repo := NewRepository(database)
	svc := NewService(repo, database)
	return &Handler{svc: svc, val: v, log: log}
}

func parseIDParam(c *gin.Context, key string) (uuid.UUID, error) {
	s := c.Param(key)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, errs.Validation("invalid uuid", errs.FieldError{Field: key})
	}
	return id, nil
}

// Create handles POST /api/v1/customers.
func (h *Handler) Create(c *gin.Context) {
	var req CreateCustomerRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	cust, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, cust)
}

// List handles GET /api/v1/customers.
func (h *Handler) List(c *gin.Context) {
	var q ListCustomersQuery
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

// Get handles GET /api/v1/customers/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	cust, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, cust)
}

// Update handles PATCH /api/v1/customers/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateCustomerRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	cust, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, cust)
}

// Archive handles DELETE /api/v1/customers/{id}.
func (h *Handler) Archive(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ArchiveRequest
	_ = httpx.BindJSON(c, h.val, &req)
	if err := h.svc.Archive(c.Request.Context(), id, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ListConsents handles GET /api/v1/customers/{id}/consents.
func (h *Handler) ListConsents(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, err := h.svc.ListConsents(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, items)
}

// AddConsent handles POST /api/v1/customers/{id}/consents.
func (h *Handler) AddConsent(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ConsentRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	item, err := h.svc.AddConsent(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, item)
}

// GetBranchVisibility handles GET /api/v1/customers/{id}/branches.
func (h *Handler) GetBranchVisibility(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, err := h.svc.GetBranchVisibility(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, items)
}

// GrantBranchVisibility handles POST /api/v1/customers/{id}/branches.
func (h *Handler) GrantBranchVisibility(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req GrantBranchVisibilityRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.GrantBranchVisibility(c.Request.Context(), id, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// RevokeBranchVisibility handles DELETE /api/v1/customers/{id}/branches.
func (h *Handler) RevokeBranchVisibility(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req RevokeBranchVisibilityRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.RevokeBranchVisibility(c.Request.Context(), id, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}
