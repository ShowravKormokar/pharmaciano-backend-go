package settings

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	repository := NewRepository(database)
	return &Handler{svc: NewService(repository, database), val: v, log: log}
}

func (h *Handler) List(c *gin.Context) {
	var query ListSettingsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.List(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

func (h *Handler) Get(c *gin.Context) {
	var query GetSettingQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	item, err := h.svc.Get(c.Request.Context(), c.Param("key"), query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, item)
}

func (h *Handler) Update(c *gin.Context) {
	var request UpdateSettingRequest
	if err := httpx.BindJSON(c, h.val, &request); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	result, err := h.svc.Update(c.Request.Context(), c.Param("key"), &request)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, result)
}

func (h *Handler) BulkUpdate(c *gin.Context) {
	var request BulkUpdateSettingsRequest
	if err := httpx.BindJSON(c, h.val, &request); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	result, err := h.svc.BulkUpdate(c.Request.Context(), &request)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, result)
}
