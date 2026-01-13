package main

import (
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/logger"
	"backend/internal/routes"
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration
	config.LoadConfig()

	// Connect to Logger(zap)
	logger.InitLogger()
	defer logger.Log.Sync()

	// Connect to PostgreSQL
	database.ConnectPostgres()

	// Run Migration
	database.RunMigrations()

	// Connect to Redis
	cache.ConnectRedis()

	// Create ONE Gin instance
	r := gin.New()

	// Apply CORS middleware FIRST
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Then add other middlewares
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Security: do not trust all proxies
	_ = r.SetTrustedProxies(nil)

	// Register routes
	routes.RegisterRoutes(r)

	// Test Redis
	cache.RedisClient.Set(cache.Ctx, "ping", "pong", time.Minute)
	val, _ := cache.RedisClient.Get(cache.Ctx, "ping").Result()
	log.Println(val)

	// Start server
	log.Println("🚀 Server running on http://localhost:8080")
	r.Run(":8080")
}