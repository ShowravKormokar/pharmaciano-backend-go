package report

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the gated Report endpoints under /reports.
// Routes remain unmounted until the report module's release gates pass
// (REPORT_CONTRACT §22 Phase 0 / Phase 1).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	view := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("report_view")},
			append(mw.Protected(constants.ModuleReports, constants.ActionView), handler)...)
	}
	export := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("report_export")},
			append(mw.Protected(constants.ModuleReports, constants.ActionExport), handler)...)
	}
	generate := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("report_generate")},
			append(mw.Protected(constants.ModuleReports, constants.ActionView), handler)...)
	}

	group := rg.Group("/reports")
	group.GET("/catalog", view(h.GetCatalog)...)
	group.POST("/generate", generate(h.Generate)...)
	group.GET("/jobs", view(h.ListJobs)...)
	group.GET("/jobs/:id", view(h.GetJobStatus)...)
	group.POST("/jobs/:id/cancel", export(h.CancelJob)...)
	group.GET("/jobs/:id/download", export(h.Download)...)
}
