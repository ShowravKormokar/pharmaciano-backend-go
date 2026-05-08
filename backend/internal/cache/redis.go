package cache

import (
	"context"
	"crypto/tls"
	"log"
	"time"

	"backend/internal/config"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client
var Ctx = context.Background()

func Connect() {
	cfg := config.Cfg.Redis

	options := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}

	// PRODUCTION → Upstash Redis (TLS)
	if config.Cfg.AppEnv == "production" {

		options.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}

		log.Println("🚀 Using Upstash Redis (Production)")

	} else {

		// DEVELOPMENT → Local Redis

		log.Println("🖥️ Using Local Redis (Development)")
	}

	RDB = redis.NewClient(options)

	if err := RDB.Ping(Ctx).Err(); err != nil {
		log.Fatalf("❌ Redis connection failed: %v", err)
	}

	log.Println("✅ Redis connected")
}

// Flush only in development
func FlushAll() {

	if config.Cfg.AppEnv == "production" {
		log.Println("⚠️ FlushAll disabled in production")
		return
	}

	if RDB == nil {
		log.Println("❌ Redis not initialized")
		return
	}

	err := RDB.FlushAll(Ctx).Err()

	if err != nil {
		log.Printf("❌ Redis flush failed: %v", err)
		return
	}

	log.Println("🧹 Redis flushed successfully")
}
