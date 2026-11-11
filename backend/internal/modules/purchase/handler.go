package purchase

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

type purchaseHTTPService interface {
	Get(context.Context, uuid.UUID) (*PurchaseOrderResponse, error)
	List(context.Context, *ListPurchaseOrdersQuery) ([]PurchaseOrderResponse, pagination.Meta, error)
}

// Handler is the HTTP edge for the purchase-order read API.
type Handler struct {
	svc purchaseHTTPService
	val *validator.Validator
	log *zap.Logger
}

func newHandler(svc purchaseHTTPService, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if svc == nil {
		return nil, fmt.Errorf("purchase handler: service is required")
	}
	return &Handler{svc: svc, val: v, log: log}, nil
}

// New assembles the read-only purchase-order API. Routes are mounted only
// when the operator explicitly enables the feature after verifying the
// normalized Purchase schema is installed.
func New(database *db.DB, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("purchase handler: database is required")
	}
	service := NewService(NewRepository(database), database, nil)
	return newHandler(service, v, log)
}

// List handles GET /purchases.
func (h *Handler) List(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	allowed := map[string]struct{}{
		"page": {}, "limit": {}, "sort": {}, "branch_id": {}, "supplier_id": {},
		"order_status": {}, "receipt_status": {}, "invoice_status": {},
		"closure_status": {}, "from": {}, "to": {}, "q": {},
	}
	queryValues := c.Request.URL.Query()
	for key, values := range queryValues {
		if _, ok := allowed[key]; !ok {
			httpx.Error(c, h.log, errs.Validation("unsupported purchase query parameter"))
			return
		}
		if len(values) != 1 {
			httpx.Error(c, h.log, errs.Validation("purchase query parameters must not be repeated"))
			return
		}
	}

	var query ListPurchaseOrdersQuery
	if err := httpx.BindQuery(c, h.val, &query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orders, meta, err := h.svc.List(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, orders, meta)
}

// Get handles GET /purchases/:id.
func (h *Handler) Get(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	id, err := parsePurchaseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	order, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, order)
}

func parsePurchaseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}
