package v1

import (
	"backend/internal/handlers"
	"backend/internal/middlewares"

	"github.com/gin-gonic/gin"
)

func Register(api *gin.RouterGroup) {
	v1 := api.Group("/v1")

	authH := handlers.NewAuthHandler()
	auth := v1.Group("/auth")
	{
		auth.POST("/login",
			middlewares.LoginRateLimit(),
			middlewares.LoginEmailRateLimit(),
			authH.Login,
		)
		auth.POST("/refresh", middlewares.GeneralRateLimit(1000), authH.RefreshToken)
		auth.POST("/logout", middlewares.JWTAuth(), authH.Logout)
	}

	protected := v1.Group("")
	protected.Use(middlewares.JWTAuth(), middlewares.GeneralRateLimit(1000))
	{
		protected.GET("/users/me", handlers.GetMe)
		protected.POST("/auth/logout-all", authH.LogoutAll)
		protected.GET("/auth/login-history", authH.LoginHistory)
		protected.GET("/auth/sessions", authH.ActiveSessions)
		protected.DELETE("/auth/sessions/:session_id", authH.RevokeSession)
		protected.GET("/auth/security-status", authH.SecurityStatus)
	}
}
