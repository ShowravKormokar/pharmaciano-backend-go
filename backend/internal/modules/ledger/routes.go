package ledger

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/ledger")

	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("ledger", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("ledger", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp.GET("/accounts", read("view", h.ListAccounts)...)
	grp.POST("/accounts", write("manage-accounts", h.CreateAccount)...)
	grp.GET("/accounts/:id", read("view", h.GetAccount)...)
	grp.PATCH("/accounts/:id", write("manage-accounts", h.UpdateAccount)...)
	grp.POST("/accounts/:id/deactivate", write("manage-accounts", h.DeactivateAccount)...)
	grp.GET("/accounts/:id/balance", read("view", h.GetAccountBalance)...)

	grp.GET("/periods", read("view", h.ListPeriods)...)
	grp.POST("/periods", write("manage-periods", h.CreatePeriod)...)
	grp.POST("/periods/:id/close", write("close-period", h.ClosePeriod)...)
	grp.POST("/periods/:id/reopen", write("reopen-period", h.ReopenPeriod)...)

	grp.POST("/journals", write("create", h.CreateJournal)...)
	grp.GET("/journals", read("view", h.ListJournals)...)
	grp.GET("/journals/:id", read("view", h.GetJournal)...)
	grp.PATCH("/journals/:id", write("create", h.UpdateJournal)...)
	grp.POST("/journals/:id/post", write("post", h.PostJournal)...)
	grp.POST("/journals/:id/reverse", write("reverse", h.ReverseJournal)...)

	grp.GET("/reports/trial-balance", read("view", h.TrialBalance)...)
}
