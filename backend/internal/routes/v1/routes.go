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
		// Apply both IP-based and email-based rate limits on login
		auth.POST("/login",
			middlewares.LoginRateLimit(),      // 10/min per IP
			middlewares.LoginEmailRateLimit(), //  5/min per email
			authH.Login,
		)
		auth.POST("/refresh", middlewares.GeneralRateLimit(1000), authH.RefreshToken)
		auth.POST("/logout", middlewares.JWTAuth(), authH.Logout)
	}

	protected := v1.Group("")
	protected.Use(middlewares.JWTAuth(), middlewares.GeneralRateLimit(1000))
	{
		protected.GET("/users/me", handlers.GetMe)
	}
}

// Use when rate limiting is desired on all routes, including auth 100 req/hour per IP
// func Register(api *gin.RouterGroup) {
// 	v1 := api.Group("/v1")

// 	// Auth routes (login has its own stricter rate limit)
// 	authH := handlers.NewAuthHandler()
// 	auth := v1.Group("/auth")
// 	{
// 		auth.POST("/login", middlewares.LoginRateLimit(), authH.Login)
// 		auth.POST("/refresh", authH.RefreshToken)
// 		auth.POST("/logout", middlewares.JWTAuth(), authH.Logout)
// 	}

// 	// Protected routes
// 	protected := v1.Group("")
// 	protected.Use(middlewares.JWTAuth())
// 	{
// 		protected.GET("/users/me", handlers.GetMe)
// 	}
// }
