package cache

import (
	"context"
	"log"
	"time"

	"backend/internal/config"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client
var Ctx = context.Background()

func Connect() {
	cfg := config.Cfg.Redis
	RDB = redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	if err := RDB.Ping(Ctx).Err(); err != nil {
		log.Fatalf("❌ Redis connection failed: %v", err)
	}
	log.Println("✅ Redis connected")
}

func FlushAll() {
	if RDB == nil {
		log.Println("❌ Redis not initialised, cannot flush")
		return
	}
	err := RDB.FlushAll(Ctx).Err()
	if err != nil {
		log.Printf("❌ Failed to flush Redis: %v", err)
	} else {
		log.Println("🧹 Redis flushed successfully")
	}
}
