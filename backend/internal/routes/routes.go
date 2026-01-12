package routes

import (
	"backend/internal/handlers"
	"backend/internal/middlewares"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.Engine) {

	// --------------------
	// Public Routes
	// --------------------
	r.POST("/login", handlers.Login)

	// --------------------
	// Protected Routes
	// --------------------
	api := r.Group("/api")
	api.Use(middlewares.JWTAuth())
	{
		api.GET("/me", handlers.Me)

		// User module
		RegisterUserRoutes(api)

	}
}