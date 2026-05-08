package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv  string
	AppPort string

	DB    DBConfig
	Redis RedisConfig
	JWT   JWTConfig
	Super SuperAdminConfig

	TokenStrategy string
}

type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string

	// Production
	URL string
}

type RedisConfig struct {
	Addr     string
	Password string

	// Production TLS
	IsTLS bool
}

type JWTConfig struct {
	Secret     string
	AccessTTL  int
	RefreshTTL int
}

type SuperAdminConfig struct {
	Email    string
	Password string
}

var Cfg *Config

func Load() {
	_ = godotenv.Load()

	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))

	accessTTL, _ := strconv.Atoi(
		getEnv("JWT_ACCESS_TTL", "15"),
	)

	refreshTTL, _ := strconv.Atoi(
		getEnv("JWT_REFRESH_TTL", "43200"),
	)

	Cfg = &Config{
		AppEnv:  getEnv("APP_ENV", "development"),
		AppPort: getEnv("APP_PORT", "8080"),

		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     dbPort,
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", ""),
			Name:     getEnv("DB_NAME", "pharmaciano"),

			// production
			URL: getEnv("DATABASE_URL", ""),
		},

		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			IsTLS:    getEnv("REDIS_TLS", "false") == "true",
		},

		JWT: JWTConfig{
			Secret:     getEnv("JWT_SECRET", "change-me"),
			AccessTTL:  accessTTL,
			RefreshTTL: refreshTTL,
		},

		Super: SuperAdminConfig{
			Email:    getEnv("SUPER_ADMIN_EMAIL", ""),
			Password: getEnv("SUPER_ADMIN_PASSWORD", ""),
		},

		TokenStrategy: getEnv("TOKEN_STRATEGY", "header"),
	}
}

func getEnv(key, def string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return def
}
