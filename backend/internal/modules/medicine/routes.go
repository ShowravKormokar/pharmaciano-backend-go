package medicine

// Route registration for the Medicine module.
//
// The route inventory follows docs/adr/MEDICINE_CONTRACT.md §8:
//
//	POST   /api/v1/medicines                       create
//	GET    /api/v1/medicines                       list
//	GET    /api/v1/medicines/:id                   detail
//	PATCH  /api/v1/medicines/:id                   partial update
//	DELETE /api/v1/medicines/:id                   archive
//	GET    /api/v1/medicines/search                bounded text search
//	POST   /api/v1/medicines/lookup                exact lookup
//	GET    /api/v1/medicines/:id/variants          list variants
//	POST   /api/v1/medicines/:id/variants          add variant
//	PATCH  /api/v1/medicines/:id/variants/:id      update variant
//	DELETE /api/v1/medicines/:id/variants/:id      archive variant
//	GET    /api/v1/medicines/:id/barcodes          list barcodes
//	POST   /api/v1/medicines/:id/barcodes          add barcode
//	DELETE /api/v1/medicines/:id/barcodes/:id      deactivate barcode
//	GET    /api/v1/medicines/:id/price-history     blocked (pricing adapter)
//
// The rate policies are the configured ones (config.yaml: auth_read, auth_write,
// search); the draft labels of the contract are not used, because an
// unregistered policy key silently disables the limiter (contract §8.1).
//
// Authorization is the native RBAC middleware: every route requires the
// matching medicines:<action> permission, and no role check is duplicated in
// the handler (contract §8.1).

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the medicine endpoints under the given /api/v1 group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModuleMedicines, action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected(constants.ModuleMedicines, action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}
	search := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("search")},
			append(mw.Protected(constants.ModuleMedicines, action), handler)...)
	}

	grp := rg.Group("/medicines")
	grp.POST("", write(constants.ActionCreate, h.Create)...)
	grp.GET("", read(constants.ActionView, h.List)...)
	grp.GET("/search", search(constants.ActionView, h.Search)...)
	grp.POST("/lookup", read(constants.ActionView, h.Lookup)...)
	grp.GET("/:id", read(constants.ActionView, h.Get)...)
	grp.PATCH("/:id", write(constants.ActionUpdate, h.Update)...)
	grp.DELETE("/:id", write(constants.ActionDelete, h.Delete)...)

	grp.GET("/:id/variants", read(constants.ActionView, h.ListVariants)...)
	grp.POST("/:id/variants", write(constants.ActionUpdate, h.CreateVariant)...)
	grp.PATCH("/:id/variants/:variant_id", write(constants.ActionUpdate, h.UpdateVariant)...)
	grp.DELETE("/:id/variants/:variant_id", write(constants.ActionUpdate, h.DeleteVariant)...)

	grp.GET("/:id/barcodes", read(constants.ActionView, h.ListBarcodes)...)
	grp.POST("/:id/barcodes", write(constants.ActionUpdate, h.CreateBarcode)...)
	grp.DELETE("/:id/barcodes/:bc_id", write(constants.ActionUpdate, h.DeleteBarcode)...)

	grp.GET("/:id/price-history", read(constants.ActionView, h.PriceHistory)...)
}
