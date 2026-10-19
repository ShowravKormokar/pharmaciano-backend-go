package brand

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the brand module.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the whole module (repository → service → handler).
func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	repo := NewRepository(database)
	svc := NewService(repo, database)
	return &Handler{svc: svc, val: v, log: log}
}

// Create handles POST /brands.
func (h *Handler) Create(c *gin.Context) {
	var req CreateBrandRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, brandResponse(b))
}

// List handles GET /brands.
func (h *Handler) List(c *gin.Context) {
	var q ListBrandsQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.List(c.Request.Context(), q, q.Offset)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	responses := make([]BrandResponse, len(items))
	for i := range items {
		responses[i] = brandResponse(&items[i])
	}
	httpx.List(c, responses, meta)
}

// Get handles GET /brands/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, brandResponse(b))
}

// Update handles PATCH /brands/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateBrandRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	b, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, brandResponse(b))
}

// Delete handles DELETE /brands/{id}.
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

func parseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}

func brandResponse(b *Brand) BrandResponse {
	return BrandResponse{
		ID:              b.ID,
		Code:            b.Code,
		Name:            b.Name,
		ManufacturerID:  b.ManufacturerID,
		CountryOfOrigin: b.CountryOfOrigin,
		Description:     b.Description,
		IsActive:        b.IsActive,
		CreatedAt:       b.CreatedAt,
		UpdatedAt:       b.UpdatedAt,
	}
}
