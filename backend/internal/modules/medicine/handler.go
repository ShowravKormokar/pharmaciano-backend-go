package medicine

// HTTP edge of the Medicine module.
//
// The handler does three things and nothing else: it binds the request onto
// the request DTOs, it calls the service, and it maps the result onto the
// shared response envelope (contract §9, "Handler: HTTP ↔ DTO, validation,
// service invocation, shared response/error mapping only").
//
// It never touches the repository, never builds SQL and never decides a
// business rule. The tenant comes from the authenticated context, so no
// request can name an organization it does not belong to (contract §3).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

// Handler is the HTTP edge of the Medicine module.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the whole module (repository → service → handler).
//
// The cursor signing key is required: the catalogue pages with signed,
// opaque cursors, and a module that cannot sign them would silently degrade
// to offset pagination. Failing at construction keeps that impossible.
func New(database *db.DB, cursorSigningKey string, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("medicine handler: database is required")
	}
	codec, err := NewCursorCodec([]byte(cursorSigningKey))
	if err != nil {
		return nil, fmt.Errorf("medicine handler: configure cursor signing: %w", err)
	}
	repo := NewRepository(database, codec)
	svc := NewService(repo, database)
	return &Handler{svc: svc, val: v, log: log}, nil
}

// Create handles POST /medicines.
func (h *Handler) Create(c *gin.Context) {
	var req CreateMedicineRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	m, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, medicineResponse(m, nil))
}

// List handles GET /medicines.
//
// A request without a cursor returns the offset page with the total count; a
// request with ?cursor= returns the next keyset page.
func (h *Handler) List(c *gin.Context) {
	var q ListMedicinesQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	list, err := h.svc.List(c.Request.Context(), q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, medicineResponses(list), list.Meta)
}

// Get handles GET /medicines/{id}.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	m, labels, err := h.svc.GetWithReferences(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, medicineResponse(m, labels))
}

// Update handles PATCH /medicines/{id}.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateMedicineRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	m, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, medicineResponse(m, nil))
}

// Delete handles DELETE /medicines/{id}.
//
// The delete is an archive: a product that operational records reference is
// not deleted at all, and the service reports that as a conflict.
func (h *Handler) Delete(c *gin.Context) {
	id, err := parseMedicineIDParam(c, "id")
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

// Search handles GET /medicines/search?q=.
func (h *Handler) Search(c *gin.Context) {
	var q SearchMedicinesQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	list, err := h.svc.Search(c.Request.Context(), q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, medicineResponses(list), list.Meta)
}

// Lookup handles POST /medicines/lookup.
func (h *Handler) Lookup(c *gin.Context) {
	var req LookupMedicineRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	result, err := h.svc.Lookup(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, result)
}

// ListVariants handles GET /medicines/{id}/variants.
func (h *Handler) ListVariants(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	limit, cursor, err := pageParams(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	page, meta, err := h.svc.ListVariants(c.Request.Context(), medicineID, limit, cursor)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, medicineVariantResponses(page.Items), meta)
}

// CreateVariant handles POST /medicines/{id}/variants.
func (h *Handler) CreateVariant(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req CreateMedicineVariantRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	variant, err := h.svc.CreateVariant(c.Request.Context(), medicineID, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, medicineVariantResponse(variant))
}

// UpdateVariant handles PATCH /medicines/{id}/variants/{variant_id}.
func (h *Handler) UpdateVariant(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	variantID, err := parseMedicineIDParam(c, "variant_id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateMedicineVariantRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	variant, err := h.svc.UpdateVariant(c.Request.Context(), medicineID, variantID, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, medicineVariantResponse(variant))
}

// DeleteVariant handles DELETE /medicines/{id}/variants/{variant_id}.
func (h *Handler) DeleteVariant(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	variantID, err := parseMedicineIDParam(c, "variant_id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteVariant(c.Request.Context(), medicineID, variantID); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ListBarcodes handles GET /medicines/{id}/barcodes.
func (h *Handler) ListBarcodes(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	limit, cursor, err := pageParams(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	page, meta, err := h.svc.ListBarcodes(c.Request.Context(), medicineID, limit, cursor)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, medicineBarcodeResponses(page.Items), meta)
}

// CreateBarcode handles POST /medicines/{id}/barcodes.
func (h *Handler) CreateBarcode(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req CreateMedicineBarcodeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	barcode, err := h.svc.CreateBarcode(c.Request.Context(), medicineID, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, medicineBarcodeResponse(barcode))
}

// DeleteBarcode handles DELETE /medicines/{id}/barcodes/{bc_id}.
func (h *Handler) DeleteBarcode(c *gin.Context) {
	medicineID, err := parseMedicineIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	barcodeID, err := parseMedicineIDParam(c, "bc_id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteBarcode(c.Request.Context(), medicineID, barcodeID); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// PriceHistory handles GET /medicines/{id}/price-history.
//
// The endpoint exists in the route inventory but stays blocked: price history
// belongs to the pricing module and there is no approved Pricing interface to
// delegate to, so serving the medicine_price_history table from here would
// create a second, competing source of truth (contract §1.12, §8).
func (h *Handler) PriceHistory(c *gin.Context) {
	if _, err := parseMedicineIDParam(c, "id"); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Error(c, h.log, errs.New(errs.CodeNotImplemented,
		"medicine price history is owned by the pricing module and no pricing interface is available yet"))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseMedicineIDParam reads a UUID path parameter.
func parseMedicineIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}

// pageParams reads the shared cursor pagination parameters.
//
// The limit is clamped into [pagination.MinLimit, pagination.MaxLimit]; a
// cursor is passed through untouched, because it is an opaque signed token
// whose validity is decided by the repository, not by a parser here.
func pageParams(c *gin.Context) (int, string, error) {
	limit := pagination.DefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, "", errs.Validation("limit must be an integer").WithCause(err)
		}
		limit = parsed
	}
	if limit < pagination.MinLimit {
		limit = pagination.MinLimit
	}
	if limit > pagination.MaxLimit {
		limit = pagination.MaxLimit
	}
	return limit, c.Query("cursor"), nil
}

// medicineResponses maps a page of catalogue entries onto the response DTOs.
func medicineResponses(list MedicineList) []MedicineResponse {
	out := make([]MedicineResponse, 0, len(list.Items))
	for i := range list.Items {
		item := list.Items[i]
		var labels *MedicineReferenceLabels
		if label, ok := list.Labels[item.ID]; ok {
			copied := label
			labels = &copied
		}
		out = append(out, medicineResponse(&item, labels))
	}
	return out
}

// medicineResponse maps one catalogue entry onto the response DTO.
//
// The DTO is declared explicitly, so a column that is added to the table
// cannot become part of the public API by accident — and the columns that
// belong to other modules (price, cost, reorder thresholds) are absent on
// purpose (contract §2, §17).
func medicineResponse(m *Medicine, labels *MedicineReferenceLabels) MedicineResponse {
	if m == nil {
		return MedicineResponse{}
	}
	return MedicineResponse{
		ID:      m.ID,
		SKU:     m.SKU,
		Barcode: m.Barcode,

		Name:        m.Name,
		DisplayName: m.DisplayName,
		Description: m.Description,

		GenericMedicineID:  m.GenericMedicineID,
		BrandID:            m.BrandID,
		CategoryID:         m.CategoryID,
		DosageFormID:       m.DosageFormID,
		RouteID:            m.RouteID,
		ManufacturerID:     m.ManufacturerID,
		StorageConditionID: m.StorageConditionID,

		Strength:      m.Strength,
		PackSize:      m.PackSize,
		UnitsPerPack:  m.UnitsPerPack,
		UnitsPerStrip: m.UnitsPerStrip,
		PackageTypeID: m.PackageTypeID,
		SalesUnitID:   m.SalesUnitID,

		RequiresPrescription: m.RequiresPrescription,
		IsControlled:         m.IsControlled,
		IsRefrigerated:       m.IsRefrigerated,
		IsImported:           m.IsImported,
		CountryOfOrigin:      m.CountryOfOrigin,
		ImporterName:         m.ImporterName,
		ImportLicenseNo:      m.ImportLicenseNo,

		TaxRateID: m.TaxRateID,
		HSNCode:   m.HSNCode,

		ShelfLifeMonths: m.ShelfLifeMonths,
		ImageURL:        m.ImageURL,

		IsActive: m.IsActive,

		References: labels,

		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// medicineVariantResponses maps a page of variants onto the response DTOs.
func medicineVariantResponses(items []MedicineVariant) []MedicineVariantResponse {
	out := make([]MedicineVariantResponse, 0, len(items))
	for i := range items {
		out = append(out, medicineVariantResponse(&items[i]))
	}
	return out
}

// medicineVariantResponse maps one variant onto the response DTO.
func medicineVariantResponse(v *MedicineVariant) MedicineVariantResponse {
	if v == nil {
		return MedicineVariantResponse{}
	}
	return MedicineVariantResponse{
		ID:           v.ID,
		MedicineID:   v.MedicineID,
		Name:         v.Name,
		SKU:          v.SKU,
		Barcode:      v.Barcode,
		Strength:     v.Strength,
		PackSize:     v.PackSize,
		UnitsPerPack: v.UnitsPerPack,
		IsActive:     v.IsActive,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
}

// medicineBarcodeResponses maps a page of barcodes onto the response DTOs.
func medicineBarcodeResponses(items []MedicineBarcode) []MedicineBarcodeResponse {
	out := make([]MedicineBarcodeResponse, 0, len(items))
	for i := range items {
		out = append(out, medicineBarcodeResponse(&items[i]))
	}
	return out
}

// medicineBarcodeResponse maps one barcode onto the response DTO.
func medicineBarcodeResponse(b *MedicineBarcode) MedicineBarcodeResponse {
	if b == nil {
		return MedicineBarcodeResponse{}
	}
	return MedicineBarcodeResponse{
		ID:          b.ID,
		MedicineID:  b.MedicineID,
		VariantID:   b.VariantID,
		Barcode:     b.Barcode,
		BarcodeType: b.BarcodeType,
		IsPrimary:   b.IsPrimary,
		CreatedAt:   b.CreatedAt,
		UpdatedAt:   b.UpdatedAt,
	}
}
