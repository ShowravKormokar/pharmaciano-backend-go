package main

import (
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/logger"
	"backend/internal/routes"
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	// Set Gin mode
	gin.SetMode(gin.DebugMode) // DebugMode in dev
	//gin.SetMode(gin.TestMode) // TestMode in dev
	//gin.SetMode(gin.ReleaseMode) // change to ReleaseMode in prod

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

	// Create router WITHOUT default middleware
	r := gin.New()

	// Middlewares
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Security: do not trust all proxies
	_ = r.SetTrustedProxies(nil)
	// r.SetTrustedProxies([]string{"192.168.1.0/24"}) // Example of setting trusted proxies

	routes.RegisterRoutes(r)

	// Test Redis
	cache.RedisClient.Set(cache.Ctx, "ping", "pong", time.Minute)
	val, _ := cache.RedisClient.Get(cache.Ctx, "ping").Result()
	log.Println(val)

	// Start server
	r.Run(":8080")
}
