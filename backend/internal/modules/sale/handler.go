package sale

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

type saleHTTPService interface {
	GetSale(context.Context, SaleScope, uuid.UUID) (*SaleDetail, error)
	ListSales(context.Context, SaleScope, ListSalesQuery) ([]Sale, int64, bool, error)
}

type invoiceHTTPService interface {
	GetInvoice(context.Context, SaleScope, uuid.UUID) (*SaleInvoiceResponse, error)
}

// Handler exposes only read endpoints. Sale mutations are intentionally not
// registered until their transaction and migration release gates are met.
type Handler struct {
	svc       saleHTTPService
	invoices  invoiceHTTPService
	val       *validator.Validator
	log       *zap.Logger
	cursorKey *pagination.CursorCodec
}

func newHandler(
	svc saleHTTPService,
	invoices invoiceHTTPService,
	cursorCodec *pagination.CursorCodec,
	v *validator.Validator,
	log *zap.Logger,
) (*Handler, error) {
	if svc == nil || invoices == nil || cursorCodec == nil {
		return nil, fmt.Errorf("sale handler: service, invoice service, and cursor codec are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		svc:       svc,
		invoices:  invoices,
		val:       v,
		log:       log,
		cursorKey: cursorCodec,
	}, nil
}

// New assembles the read-only Sale HTTP API. The application composition root
// does not mount this handler until the normalized Sale schema is available.
func New(database *db.DB, cursorSigningKey []byte, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("sale handler: database is required")
	}
	cursorCodec, err := pagination.NewCursorCodec(cursorSigningKey)
	if err != nil {
		return nil, fmt.Errorf("sale handler: create cursor codec: %w", err)
	}
	repo := NewRepository(database, cursorCodec)
	return newHandler(NewService(repo, nil), NewInvoiceService(repo), cursorCodec, v, log)
}

func (h *Handler) List(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if err := validateSaleQueryParameters(c.Request.URL.Query()); err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	var query ListSalesQuery
	if err := httpx.BindQuery(c, h.val, &query); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if validationErrors := query.NormalizeAndValidate(); len(validationErrors) > 0 {
		httpx.Error(c, h.log, errs.Validation(validationErrors[0].Error()))
		return
	}

	scope, err := saleScopeFromContext(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if query.Cursor != "" {
		cursor, err := h.cursorKey.Decode(query.Cursor)
		if err != nil || cursor.ValidateSortKey(saleListSortKey(scope, query)) != nil || cursor.CreatedAt.IsZero() {
			httpx.Error(c, h.log, errs.Validation("invalid Sale pagination cursor"))
			return
		}
	}
	sales, total, hasMore, err := h.svc.ListSales(c.Request.Context(), scope, query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	items := make([]SaleListItem, 0, len(sales))
	if len(sales) > query.Limit {
		httpx.Error(c, h.log, errs.Internal(fmt.Errorf("sale service returned an oversized page")))
		return
	}
	for i := range sales {
		items = append(items, saleListItemFromModel(&sales[i]))
	}
	nextCursor := ""
	if hasMore {
		if len(sales) != query.Limit {
			httpx.Error(c, h.log, errs.Internal(fmt.Errorf("sale service returned an inconsistent page")))
			return
		}
		last := sales[len(sales)-1]
		if last.ID == uuid.Nil || last.SoldAt.IsZero() {
			httpx.Error(c, h.log, errs.Internal(fmt.Errorf("sale service returned an invalid cursor boundary")))
			return
		}
		nextCursor = h.cursorKey.Encode(pagination.Cursor{
			CreatedAt: last.SoldAt,
			ID:        last.ID,
			SortKey:   saleListSortKey(scope, query),
		})
	}
	httpx.List(c, items, pagination.Meta{
		Limit:      query.Limit,
		Total:      total,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func (h *Handler) Get(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	id, err := parseSaleIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	scope, err := saleScopeFromContext(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	detail, err := h.svc.GetSale(c.Request.Context(), scope, id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if detail == nil || detail.Sale == nil {
		httpx.Error(c, h.log, errs.Internal(fmt.Errorf("sale service returned an empty detail")))
		return
	}
	httpx.OK(c, saleResponseFromDetail(detail))
}

func (h *Handler) GetInvoice(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	id, err := parseSaleIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	scope, err := saleScopeFromContext(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	invoice, err := h.invoices.GetInvoice(c.Request.Context(), scope, id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if invoice == nil {
		httpx.Error(c, h.log, errs.Internal(fmt.Errorf("invoice service returned an empty invoice")))
		return
	}
	httpx.OK(c, invoice)
}

func saleScopeFromContext(ctx context.Context) (SaleScope, error) {
	principal, ok := appctx.CurrentPrincipal(ctx)
	if !ok {
		return SaleScope{}, errs.Unauthenticated()
	}
	if principal.OrgID == uuid.Nil {
		return SaleScope{}, errs.Forbidden("active organization scope is required")
	}

	branchIDs := principal.BranchIDs
	if len(branchIDs) == 0 && principal.BranchID != nil {
		branchIDs = []uuid.UUID{*principal.BranchID}
	}
	scope := SaleScope{
		OrganizationID: principal.OrgID,
		BranchIDs:      branchIDs,
		OrgWide:        len(branchIDs) == 0,
	}
	if err := scope.validate(); err != nil {
		return SaleScope{}, errs.Forbidden("valid Sale branch scope is required")
	}
	return scope, nil
}

func validateSaleQueryParameters(values url.Values) error {
	allowed := map[string]struct{}{
		"cursor": {}, "limit": {}, "branch_id": {}, "cashier_id": {},
		"customer_id": {}, "status": {}, "payment_status": {},
		"invoice_status": {}, "from": {}, "to": {}, "q": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 {
			return errs.Validation("unsupported or repeated Sale query parameter")
		}
	}
	return nil
}

func parseSaleIDParam(c *gin.Context, key string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(key))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + key + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}

func saleListItemFromModel(s *Sale) SaleListItem {
	return SaleListItem{
		ID:            s.ID,
		SaleNo:        s.SaleNo,
		SaleType:      s.SaleType,
		Status:        s.Status,
		PaymentStatus: s.PaymentStatus,
		InvoiceStatus: s.InvoiceStatus,
		CustomerID:    s.CustomerID,
		CashierID:     s.CashierID,
		CashSessionID: s.CashSessionID,
		CurrencyCode:  s.CurrencyCode,
		TotalMinor:    s.TotalMinor,
		PaidMinor:     s.PaidMinor,
		SoldAt:        s.SoldAt,
		CompletedAt:   s.CompletedAt,
		CreatedAt:     s.CreatedAt,
	}
}

func saleResponseFromDetail(detail *SaleDetail) *SaleResponse {
	if detail == nil || detail.Sale == nil {
		return nil
	}
	s := detail.Sale
	response := &SaleResponse{
		ID:                  s.ID,
		OrganizationID:      s.OrganizationID,
		BranchID:            s.BranchID,
		WarehouseID:         s.WarehouseID,
		CashierID:           s.CashierID,
		CustomerID:          s.CustomerID,
		CashSessionID:       s.CashSessionID,
		OperationID:         s.OperationID,
		SaleNo:              s.SaleNo,
		SaleType:            s.SaleType,
		Status:              s.Status,
		PaymentStatus:       s.PaymentStatus,
		InvoiceStatus:       s.InvoiceStatus,
		CurrencyCode:        s.CurrencyCode,
		SubtotalMinor:       s.SubtotalMinor,
		DiscountMinor:       s.DiscountMinor,
		CouponDiscountMinor: s.CouponDiscountMinor,
		TaxMinor:            s.TaxMinor,
		OtherChargesMinor:   s.OtherChargesMinor,
		TotalMinor:          s.TotalMinor,
		PaidMinor:           s.PaidMinor,
		ChangeMinor:         s.ChangeMinor,
		CouponID:            s.CouponID,
		PrescriptionID:      s.PrescriptionID,
		Notes:               s.Notes,
		PricingSnapshot:     s.PricingSnapshot,
		TaxSnapshot:         s.TaxSnapshot,
		PolicyVersion:       s.PolicyVersion,
		SoldAt:              s.SoldAt,
		CompletedAt:         s.CompletedAt,
		VoidedAt:            s.VoidedAt,
		VoidedBy:            s.VoidedBy,
		VoidReason:          s.VoidReason,
		CreatedAt:           s.CreatedAt,
		UpdatedAt:           s.UpdatedAt,
		Items:               make([]SaleItemResponse, 0, len(detail.Items)),
		Tenders:             make([]SaleTenderResponse, 0, len(detail.Tenders)),
	}
	for _, item := range detail.Items {
		response.Items = append(response.Items, SaleItemResponse{
			ID:                        item.ID,
			LineNo:                    item.LineNo,
			MedicineID:                item.MedicineID,
			VariantID:                 item.VariantID,
			UnitID:                    item.UnitID,
			EnteredQuantity:           item.EnteredQuantity,
			BaseQuantity:              item.BaseQuantity,
			UnitConversionNumerator:   item.UnitConversionNumerator,
			UnitConversionDenominator: item.UnitConversionDenominator,
			UnitPriceMinor:            item.UnitPriceMinor,
			DiscountMinor:             item.DiscountMinor,
			TaxMinor:                  item.TaxMinor,
			LineTotalMinor:            item.LineTotalMinor,
			DiscountRateBPS:           int32(item.DiscountRateBPS),
			TaxRateBPS:                int32(item.TaxRateBPS),
			ProductSnapshot:           item.ProductSnapshot,
			UnitSnapshot:              item.UnitSnapshot,
			TaxSnapshot:               item.TaxSnapshot,
			CreatedAt:                 item.CreatedAt,
		})
	}
	for _, tender := range detail.Tenders {
		response.Tenders = append(response.Tenders, SaleTenderResponse{
			ID:            tender.ID,
			TenderNo:      tender.TenderNo,
			Method:        tender.Method,
			AmountMinor:   tender.AmountMinor,
			CurrencyCode:  tender.CurrencyCode,
			ReferenceNo:   tender.ReferenceNo,
			ReceivedAt:    tender.ReceivedAt,
			ReceivedBy:    tender.ReceivedBy,
			SettlementRef: tender.SettlementRef,
		})
	}
	if detail.Invoice != nil {
		response.Invoice = saleInvoiceResponseFromModel(detail.Invoice)
	}
	return response
}

func saleInvoiceResponseFromModel(invoice *SaleInvoice) *SaleInvoiceResponse {
	if invoice == nil {
		return nil
	}
	return &SaleInvoiceResponse{
		ID:              invoice.ID,
		Series:          invoice.Series,
		FiscalKey:       invoice.FiscalKey,
		InvoiceNo:       invoice.InvoiceNo,
		IssuedAt:        invoice.IssuedAt,
		CurrencyCode:    invoice.CurrencyCode,
		Snapshot:        invoice.Snapshot,
		SnapshotHash:    invoice.SnapshotHash,
		TemplateVersion: invoice.TemplateVersion,
	}
}
