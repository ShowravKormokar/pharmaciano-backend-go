package cache

import (
	"context"
	"log"
	"time"

	"backend/internal/config"

	"github.com/redis/go-redis/v9"
)

var (
	RedisClient *redis.Client
	Ctx         = context.Background()
)

func ConnectRedis() {
	cfg := config.Cfg

	RedisClient = redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr,
		DB:   0,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(Ctx, 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		log.Fatal("❌ Failed to connect Redis:", err)
	}

	log.Println("✅ Redis connected successfully")
}
