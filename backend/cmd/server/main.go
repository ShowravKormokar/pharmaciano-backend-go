package main

import (
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/logger"
	"backend/internal/middlewares"
	"backend/internal/rbac"
	"backend/internal/routes"
	"backend/internal/scripts"

	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	config.Load()
	logger.Init()
	defer logger.Log.Sync()

	database.ConnectPostgres()
	database.RunMigrations() // golang-migrate embedded migrations
	cache.Connect()
	//  Uncomment the line below to flush Redis on startup (for development only)
	// cache.FlushAll()

	rbac.Init()
	scripts.SeedAll()

	if config.Cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// Global middleware stack
	r.Use(
		middlewares.SecurityHeaders(),
		// middlewares.RateLimit(100), // 100 req/hour per IP
		middlewares.AuditMiddleware(logger.Log),
	)

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{os.Getenv("FRONTEND_URL")},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	if config.Cfg.AppEnv == "production" {
		r.SetTrustedProxies(nil)

	} else {
		r.SetTrustedProxies([]string{
			"127.0.0.1",
		})
	}

	r.Use(gin.Logger(), gin.Recovery())

	routes.Register(r)

	srv := &http.Server{
		Addr:    ":" + config.Cfg.AppPort,
		Handler: r,
	}

	// Graceful startup/shutdown
	go func() {
		log.Printf("🚀 Server running on port %s", config.Cfg.AppPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// DB verification – optional
	if err := database.DB.Exec("SELECT 1").Error; err != nil {
		log.Fatalf("Database not reachable: %v", err)
	}
	log.Println("✅ Database verified, server ready")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("🛑 Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("✅ Server exited")
}
