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
