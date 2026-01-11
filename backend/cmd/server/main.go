package main

import (
	"backend/internal/config"
	"backend/internal/database"

	"github.com/gin-gonic/gin"
)

func main() {
	// Set Gin mode
	gin.SetMode(gin.DebugMode) // DebugMode in dev
	//gin.SetMode(gin.TestMode) // TestMode in dev
	//gin.SetMode(gin.ReleaseMode) // change to ReleaseMode in prod

	// Load configuration
	config.LoadConfig()
	// Connebct to PostgreSQL
	database.ConnectPostgres()

	// Create router WITHOUT default middleware
	r := gin.New()

	// Middlewares
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Security: do not trust all proxies
	_ = r.SetTrustedProxies(nil)
	// r.SetTrustedProxies([]string{"192.168.1.0/24"}) // Example of setting trusted proxies

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
		})
	})

	// Start server
	r.Run(":8080")
}
