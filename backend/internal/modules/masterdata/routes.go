package masterdata

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the master-data endpoints under /master/{resource}.
//
// Chain order matches the canonical pattern (see warehouse/routes.go): rate-limit
// outermost, then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use the "auth_read" policy, writes
// "auth_write". Every route is authorized against the "masterdata" module.
//
// All eleven resources expose the same five verbs (contract §4), so they are
// registered through registerResource rather than spelled out one by one.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/master")
	registerResource(grp, mw, "product-categories",
		h.CreateProductCategory, h.GetProductCategory, h.ListProductCategories,
		h.UpdateProductCategory, h.DeleteProductCategory)
	registerResource(grp, mw, "routes",
		h.CreateRoute, h.GetRoute, h.ListRoutes,
		h.UpdateRoute, h.DeleteRoute)
	registerResource(grp, mw, "dosage-forms",
		h.CreateDosageForm, h.GetDosageForm, h.ListDosageForms,
		h.UpdateDosageForm, h.DeleteDosageForm)
	registerResource(grp, mw, "package-types",
		h.CreatePackageType, h.GetPackageType, h.ListPackageTypes,
		h.UpdatePackageType, h.DeletePackageType)
	registerResource(grp, mw, "unit-types",
		h.CreateUnitType, h.GetUnitType, h.ListUnitTypes,
		h.UpdateUnitType, h.DeleteUnitType)
	registerResource(grp, mw, "storage-conditions",
		h.CreateStorageCondition, h.GetStorageCondition, h.ListStorageConditions,
		h.UpdateStorageCondition, h.DeleteStorageCondition)
	registerResource(grp, mw, "tax-rates",
		h.CreateTaxRate, h.GetTaxRate, h.ListTaxRates,
		h.UpdateTaxRate, h.DeleteTaxRate)
	registerResource(grp, mw, "drug-groups",
		h.CreateDrugGroup, h.GetDrugGroup, h.ListDrugGroups,
		h.UpdateDrugGroup, h.DeleteDrugGroup)
	registerResource(grp, mw, "drug-classes",
		h.CreateDrugClass, h.GetDrugClass, h.ListDrugClasses,
		h.UpdateDrugClass, h.DeleteDrugClass)
	registerResource(grp, mw, "therapeutic-classes",
		h.CreateTherapeuticClass, h.GetTherapeuticClass, h.ListTherapeuticClasses,
		h.UpdateTherapeuticClass, h.DeleteTherapeuticClass)
	registerResource(grp, mw, "generic-medicines",
		h.CreateGenericMedicine, h.GetGenericMedicine, h.ListGenericMedicines,
		h.UpdateGenericMedicine, h.DeleteGenericMedicine)
}

// registerResource mounts the standard five endpoints (collection create/list
// and item get/update/delete) for one resource path segment, e.g.
// "/product-categories" and "/product-categories/:id".
func registerResource(
	grp *gin.RouterGroup,
	mw *middleware.Middleware,
	path string,
	create, get, list, update, del gin.HandlerFunc,
) {
	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("masterdata", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("masterdata", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp.POST(path, write("create", create)...)
	grp.GET(path, read("view", list)...)
	item := grp.Group(path + "/:id")
	item.GET("", read("view", get)...)
	item.PATCH("", write("update", update)...)
	item.DELETE("", write("delete", del)...)
}
