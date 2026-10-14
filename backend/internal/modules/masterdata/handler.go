package masterdata

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the master-data module: it binds and validates
// input, delegates to the service, and renders the result or error through
// httpx — mirroring backend/internal/modules/warehouse/handler.go.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the whole module (repository → service → handler) and returns
// the handler, which also carries route registration. Limits is left at its
// zero value so the service starts with no configured business maximum; wiring
// the real value from config is deferred to the Phase that owns the config key.
func New(database *db.DB, v *validator.Validator, log *zap.Logger) *Handler {
	repo := NewRepository(database)
	svc := NewService(repo, database, Limits{})
	return &Handler{svc: svc, val: v, log: log}
}

// parseIDParam extracts and validates a UUID path parameter, returning a 400
// VALIDATION_ERROR (not 404) when the segment is malformed so a bad id is never
// confused with a missing resource.
func parseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// ProductCategory
// ---------------------------------------------------------------------------

// CreateProductCategory handles POST /master/product-categories.
func (h *Handler) CreateProductCategory(c *gin.Context) {
	var req CreateProductCategoryRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pc, err := h.svc.CreateProductCategory(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewProductCategoryResponse(*pc))
}

// GetProductCategory handles GET /master/product-categories/{id}.
func (h *Handler) GetProductCategory(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pc, err := h.svc.GetProductCategory(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewProductCategoryResponse(*pc))
}

// ListProductCategories handles GET /master/product-categories.
func (h *Handler) ListProductCategories(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListProductCategories(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, ProductCategoryResponses(items), meta)
}

// UpdateProductCategory handles PATCH /master/product-categories/{id}.
func (h *Handler) UpdateProductCategory(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateProductCategoryRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pc, err := h.svc.UpdateProductCategory(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewProductCategoryResponse(*pc))
}

// DeleteProductCategory handles DELETE /master/product-categories/{id}.
func (h *Handler) DeleteProductCategory(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteProductCategory(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// Route
// ---------------------------------------------------------------------------

// CreateRoute handles POST /master/routes.
func (h *Handler) CreateRoute(c *gin.Context) {
	var req CreateRouteRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	r, err := h.svc.CreateRoute(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewRouteResponse(*r))
}

// GetRoute handles GET /master/routes/{id}.
func (h *Handler) GetRoute(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	r, err := h.svc.GetRoute(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewRouteResponse(*r))
}

// ListRoutes handles GET /master/routes.
func (h *Handler) ListRoutes(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListRoutes(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, RouteResponses(items), meta)
}

// UpdateRoute handles PATCH /master/routes/{id}.
func (h *Handler) UpdateRoute(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateRouteRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	r, err := h.svc.UpdateRoute(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewRouteResponse(*r))
}

// DeleteRoute handles DELETE /master/routes/{id}.
func (h *Handler) DeleteRoute(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteRoute(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// DosageForm
// ---------------------------------------------------------------------------

// CreateDosageForm handles POST /master/dosage-forms.
func (h *Handler) CreateDosageForm(c *gin.Context) {
	var req CreateDosageFormRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	df, err := h.svc.CreateDosageForm(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewDosageFormResponse(*df))
}

// GetDosageForm handles GET /master/dosage-forms/{id}.
func (h *Handler) GetDosageForm(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	df, err := h.svc.GetDosageForm(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDosageFormResponse(*df))
}

// ListDosageForms handles GET /master/dosage-forms.
func (h *Handler) ListDosageForms(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListDosageForms(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, DosageFormResponses(items), meta)
}

// UpdateDosageForm handles PATCH /master/dosage-forms/{id}.
func (h *Handler) UpdateDosageForm(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateDosageFormRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	df, err := h.svc.UpdateDosageForm(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDosageFormResponse(*df))
}

// DeleteDosageForm handles DELETE /master/dosage-forms/{id}.
func (h *Handler) DeleteDosageForm(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteDosageForm(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// PackageType
// ---------------------------------------------------------------------------

// CreatePackageType handles POST /master/package-types.
func (h *Handler) CreatePackageType(c *gin.Context) {
	var req CreatePackageTypeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pt, err := h.svc.CreatePackageType(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewPackageTypeResponse(*pt))
}

// GetPackageType handles GET /master/package-types/{id}.
func (h *Handler) GetPackageType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pt, err := h.svc.GetPackageType(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewPackageTypeResponse(*pt))
}

// ListPackageTypes handles GET /master/package-types.
func (h *Handler) ListPackageTypes(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListPackageTypes(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, PackageTypeResponses(items), meta)
}

// UpdatePackageType handles PATCH /master/package-types/{id}.
func (h *Handler) UpdatePackageType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdatePackageTypeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	pt, err := h.svc.UpdatePackageType(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewPackageTypeResponse(*pt))
}

// DeletePackageType handles DELETE /master/package-types/{id}.
func (h *Handler) DeletePackageType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeletePackageType(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// UnitType
// ---------------------------------------------------------------------------

// CreateUnitType handles POST /master/unit-types.
func (h *Handler) CreateUnitType(c *gin.Context) {
	var req CreateUnitTypeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	ut, err := h.svc.CreateUnitType(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewUnitTypeResponse(*ut))
}

// GetUnitType handles GET /master/unit-types/{id}.
func (h *Handler) GetUnitType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	ut, err := h.svc.GetUnitType(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewUnitTypeResponse(*ut))
}

// ListUnitTypes handles GET /master/unit-types.
func (h *Handler) ListUnitTypes(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListUnitTypes(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, UnitTypeResponses(items), meta)
}

// UpdateUnitType handles PATCH /master/unit-types/{id}.
func (h *Handler) UpdateUnitType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateUnitTypeRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	ut, err := h.svc.UpdateUnitType(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewUnitTypeResponse(*ut))
}

// DeleteUnitType handles DELETE /master/unit-types/{id}.
func (h *Handler) DeleteUnitType(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteUnitType(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// StorageCondition
// ---------------------------------------------------------------------------

// CreateStorageCondition handles POST /master/storage-conditions.
func (h *Handler) CreateStorageCondition(c *gin.Context) {
	var req CreateStorageConditionRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	sc, err := h.svc.CreateStorageCondition(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewStorageConditionResponse(*sc))
}

// GetStorageCondition handles GET /master/storage-conditions/{id}.
func (h *Handler) GetStorageCondition(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	sc, err := h.svc.GetStorageCondition(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewStorageConditionResponse(*sc))
}

// ListStorageConditions handles GET /master/storage-conditions.
func (h *Handler) ListStorageConditions(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListStorageConditions(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, StorageConditionResponses(items), meta)
}

// UpdateStorageCondition handles PATCH /master/storage-conditions/{id}.
func (h *Handler) UpdateStorageCondition(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateStorageConditionRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	sc, err := h.svc.UpdateStorageCondition(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewStorageConditionResponse(*sc))
}

// DeleteStorageCondition handles DELETE /master/storage-conditions/{id}.
func (h *Handler) DeleteStorageCondition(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteStorageCondition(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// TaxRate
// ---------------------------------------------------------------------------

// CreateTaxRate handles POST /master/tax-rates.
func (h *Handler) CreateTaxRate(c *gin.Context) {
	var req CreateTaxRateRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tr, err := h.svc.CreateTaxRate(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewTaxRateResponse(*tr))
}

// GetTaxRate handles GET /master/tax-rates/{id}.
func (h *Handler) GetTaxRate(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tr, err := h.svc.GetTaxRate(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewTaxRateResponse(*tr))
}

// ListTaxRates handles GET /master/tax-rates.
func (h *Handler) ListTaxRates(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListTaxRates(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, TaxRateResponses(items), meta)
}

// UpdateTaxRate handles PATCH /master/tax-rates/{id}.
func (h *Handler) UpdateTaxRate(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateTaxRateRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tr, err := h.svc.UpdateTaxRate(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewTaxRateResponse(*tr))
}

// DeleteTaxRate handles DELETE /master/tax-rates/{id}.
func (h *Handler) DeleteTaxRate(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteTaxRate(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// DrugGroup
// ---------------------------------------------------------------------------

// CreateDrugGroup handles POST /master/drug-groups.
func (h *Handler) CreateDrugGroup(c *gin.Context) {
	var req CreateDrugGroupRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dg, err := h.svc.CreateDrugGroup(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewDrugGroupResponse(*dg))
}

// GetDrugGroup handles GET /master/drug-groups/{id}.
func (h *Handler) GetDrugGroup(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dg, err := h.svc.GetDrugGroup(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDrugGroupResponse(*dg))
}

// ListDrugGroups handles GET /master/drug-groups.
func (h *Handler) ListDrugGroups(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListDrugGroups(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, DrugGroupResponses(items), meta)
}

// UpdateDrugGroup handles PATCH /master/drug-groups/{id}.
func (h *Handler) UpdateDrugGroup(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateDrugGroupRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dg, err := h.svc.UpdateDrugGroup(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDrugGroupResponse(*dg))
}

// DeleteDrugGroup handles DELETE /master/drug-groups/{id}.
func (h *Handler) DeleteDrugGroup(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteDrugGroup(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// DrugClass
// ---------------------------------------------------------------------------

// CreateDrugClass handles POST /master/drug-classes.
func (h *Handler) CreateDrugClass(c *gin.Context) {
	var req CreateDrugClassRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dc, err := h.svc.CreateDrugClass(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewDrugClassResponse(*dc))
}

// GetDrugClass handles GET /master/drug-classes/{id}.
func (h *Handler) GetDrugClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dc, err := h.svc.GetDrugClass(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDrugClassResponse(*dc))
}

// ListDrugClasses handles GET /master/drug-classes.
func (h *Handler) ListDrugClasses(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListDrugClasses(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, DrugClassResponses(items), meta)
}

// UpdateDrugClass handles PATCH /master/drug-classes/{id}.
func (h *Handler) UpdateDrugClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateDrugClassRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	dc, err := h.svc.UpdateDrugClass(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewDrugClassResponse(*dc))
}

// DeleteDrugClass handles DELETE /master/drug-classes/{id}.
func (h *Handler) DeleteDrugClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteDrugClass(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// TherapeuticClass
// ---------------------------------------------------------------------------

// CreateTherapeuticClass handles POST /master/therapeutic-classes.
func (h *Handler) CreateTherapeuticClass(c *gin.Context) {
	var req CreateTherapeuticClassRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tc, err := h.svc.CreateTherapeuticClass(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewTherapeuticClassResponse(*tc))
}

// GetTherapeuticClass handles GET /master/therapeutic-classes/{id}.
func (h *Handler) GetTherapeuticClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tc, err := h.svc.GetTherapeuticClass(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewTherapeuticClassResponse(*tc))
}

// ListTherapeuticClasses handles GET /master/therapeutic-classes.
func (h *Handler) ListTherapeuticClasses(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListTherapeuticClasses(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, TherapeuticClassResponses(items), meta)
}

// UpdateTherapeuticClass handles PATCH /master/therapeutic-classes/{id}.
func (h *Handler) UpdateTherapeuticClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateTherapeuticClassRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	tc, err := h.svc.UpdateTherapeuticClass(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewTherapeuticClassResponse(*tc))
}

// DeleteTherapeuticClass handles DELETE /master/therapeutic-classes/{id}.
func (h *Handler) DeleteTherapeuticClass(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteTherapeuticClass(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ---------------------------------------------------------------------------
// GenericMedicine
// ---------------------------------------------------------------------------

// CreateGenericMedicine handles POST /master/generic-medicines.
func (h *Handler) CreateGenericMedicine(c *gin.Context) {
	var req CreateGenericMedicineRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	gm, err := h.svc.CreateGenericMedicine(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, NewGenericMedicineResponse(*gm))
}

// GetGenericMedicine handles GET /master/generic-medicines/{id}.
func (h *Handler) GetGenericMedicine(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	gm, err := h.svc.GetGenericMedicine(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewGenericMedicineResponse(*gm))
}

// ListGenericMedicines handles GET /master/generic-medicines.
func (h *Handler) ListGenericMedicines(c *gin.Context) {
	var q ListMasterDataQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListGenericMedicines(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, GenericMedicineResponses(items), meta)
}

// UpdateGenericMedicine handles PATCH /master/generic-medicines/{id}.
func (h *Handler) UpdateGenericMedicine(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateGenericMedicineRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	gm, err := h.svc.UpdateGenericMedicine(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, NewGenericMedicineResponse(*gm))
}

// DeleteGenericMedicine handles DELETE /master/generic-medicines/{id}.
func (h *Handler) DeleteGenericMedicine(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteGenericMedicine(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}
