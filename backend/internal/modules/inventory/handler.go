package inventory

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

func New(database *db.DB, cursorSigningKey string, v *validator.Validator, log *zap.Logger, metrics ...*telemetry.Metrics) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("inventory handler: database is required")
	}
	codec, err := pagination.NewCursorCodec([]byte(cursorSigningKey))
	if err != nil {
		return nil, fmt.Errorf("inventory handler: configure cursor signing: %w", err)
	}
	repo := NewRepository(database, codec)
	var moduleMetrics *telemetry.Metrics
	if len(metrics) > 0 {
		moduleMetrics = metrics[0]
	}
	return &Handler{svc: NewService(repo, moduleMetrics), val: v, log: log}, nil
}

func (h *Handler) ListBatches(c *gin.Context) {
	allowed := map[string]struct{}{
		"branch_id": {}, "warehouse_id": {}, "variant_id": {}, "status": {},
		"condition": {}, "q": {}, "cursor": {}, "limit": {},
	}
	for key := range c.Request.URL.Query() {
		if _, ok := allowed[key]; !ok {
			httpx.Error(c, h.log, errs.Validation("unsupported inventory query parameter"))
			return
		}
	}
	var query ListBatchesQuery
	if err := httpx.BindQuery(c, h.val, &query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	result, err := h.svc.ListBatches(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, result.Items, pagination.Meta{
		Limit: query.Limit, HasMore: result.HasMore, NextCursor: result.NextCursor,
	})
}
