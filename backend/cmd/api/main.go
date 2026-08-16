package main

import (
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
	"backend/internal/platform/redis"
	"context"

	"github.com/gin-gonic/gin"
)

func main() {
	// Context
	ctx := context.Background()

	// Config load
	cfg := config.MustLoad(config.LoadOptions{})

	// Logger initialize
	log, err := logger.New(cfg.Logging, cfg.App.Name, cfg.App.Version)
	if err != nil {
		panic("logger init failed: " + err.Error())
	}

	// PostgreSQL connection test
	pg, err := db.New(ctx, cfg.Database, log)
	if err != nil {
		log.Fatal("postgres: " + err.Error())
	}
	defer pg.Close()

	// Redis connection test
	rdb, err := redis.New(ctx, cfg.Redis, log)
	if err != nil {
		log.Fatal("redis: " + err.Error())
	}
	defer rdb.Close()

	log.Info("✅ All platform connections OK")

	// Gin router setup
	r := gin.Default()

	// Simple test route
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong!"})
	})

	// Run server
	r.Run(":8080")
}
