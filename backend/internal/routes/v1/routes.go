package v1

import (
	"backend/internal/handlers"
	"backend/internal/middlewares"

	"github.com/gin-gonic/gin"
)

func Register(api *gin.RouterGroup) {
	v1 := api.Group("/v1")

	// Auth
	authH := handlers.NewAuthHandler()
	auth := v1.Group("/auth")
	{
		auth.POST("/login", authH.Login)
		auth.POST("/refresh", authH.RefreshToken)
		auth.POST("/logout", middlewares.JWTAuth(), authH.Logout)
	}

	// Protected
	protected := v1.Group("")
	protected.Use(middlewares.JWTAuth())
	{
		// User
		protected.GET("/users/me", handlers.GetMe)
	}
}
