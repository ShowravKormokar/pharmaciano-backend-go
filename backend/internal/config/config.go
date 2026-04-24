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
}

type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

type RedisConfig struct {
	Addr string
}

type JWTConfig struct {
	Secret     string
	AccessTTL  int // minutes
	RefreshTTL int // minutes
}

type SuperAdminConfig struct {
	Email    string
	Password string
}

var Cfg *Config

func Load() {
	_ = godotenv.Load() // ignore if .env missing

	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))
	accessTTL, _ := strconv.Atoi(getEnv("JWT_ACCESS_TTL", "15"))
	refreshTTL, _ := strconv.Atoi(getEnv("JWT_REFRESH_TTL", "43200"))

	Cfg = &Config{
		AppEnv:  getEnv("APP_ENV", "development"),
		AppPort: getEnv("APP_PORT", "8080"),

		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     dbPort,
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", ""),
			Name:     getEnv("DB_NAME", "pharmaciano"),
		},

		Redis: RedisConfig{
			Addr: getEnv("REDIS_ADDR", "localhost:6379"),
		},

		JWT: JWTConfig{
			Secret:     getEnv("JWT_SECRET", "default-change-me"),
			AccessTTL:  accessTTL,
			RefreshTTL: refreshTTL,
		},

		Super: SuperAdminConfig{
			Email:    getEnv("SUPER_ADMIN_EMAIL", "superadmin@pharmaciano.com"),
			Password: getEnv("SUPER_ADMIN_PASSWORD", ""),
		},
	}
}

func getEnv(key, def string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return def
}
