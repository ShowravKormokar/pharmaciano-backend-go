package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/logger"
	"backend/internal/middlewares"
	"backend/internal/routes"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	config.LoadConfig()
	logger.InitLogger()
	defer logger.Log.Sync()

	database.ConnectPostgres()
	database.RunMigrations()
	cache.ConnectRedis()

	if config.Cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	r.Use(
		middlewares.SecurityHeadersMiddleware(),
		middlewares.RateLimitMiddleware(),
		middlewares.TenantMiddleware(),
		middlewares.AuditMiddleware(logger.Log),
	)

	// CORS from ENV
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{os.Getenv("FRONTEND_URL")},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	r.Use(gin.Logger(), gin.Recovery())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	routes.RegisterRoutes(r)

	// Server config
	srv := &http.Server{
		Addr:    ":" + config.Cfg.AppPort,
		Handler: r,
	}

	// Run server in goroutine
	go func() {
		log.Println("🚀 Server running on port:", config.Cfg.AppPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	if err := database.DB.Exec("SELECT 1 FROM organizations LIMIT 1").Error; err != nil {
		log.Fatal("❌ Database not migrated. Run migrations first.")
	} else {
		log.Println("✅ Database verification passed. Server is fully ready.")
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("✅ Server exited properly")
}
