package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// AppConfig holds all application configurations
type AppConfig struct {
	AppPort string

	DB     DatabaseConfig
	Redis  RedisConfig
	JWT    JWTConfig
	Casbin CasbinConfig
}

// Database configuration
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

// Redis configuration
type RedisConfig struct {
	Addr string
}

// JWT configuration
type JWTConfig struct {
	JWTSecret     string
	JWTAccessTTL  int // minutes
	JWTRefreshTTL int // minutes
}

// Casbin configuration
type CasbinConfig struct {
	ModelPath string
}

// Global config instance
var Cfg *AppConfig

// LoadConfig loads environment variables into AppConfig
func LoadConfig() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Fatal("❌ Failed to load .env file")
	}

	// Convert ports & TTLs
	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))
	accessTTL, _ := strconv.Atoi(getEnv("JWT_ACCESS_TTL", "15"))
	refreshTTL, _ := strconv.Atoi(getEnv("JWT_REFRESH_TTL", "43200"))

	Cfg = &AppConfig{
		AppPort: getEnv("APP_PORT", "8080"),

		DB: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     dbPort,
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", ""),
			Name:     getEnv("DB_NAME", ""),
		},

		Redis: RedisConfig{
			Addr: getEnv("REDIS_ADDR", "localhost:6379"),
		},

		JWT: JWTConfig{
			JWTSecret:     getEnv("JWT_SECRET", ""),
			JWTAccessTTL:  accessTTL,
			JWTRefreshTTL: refreshTTL,
		},

		Casbin: CasbinConfig{
			ModelPath: getEnv("CASBIN_MODEL_PATH", ""),
		},
	}
}

// Helper function to read env with default
func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
