package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// LoadOption controls where the loader looks for files
type LoadOptions struct {
	// ConfigDir is the directory containing config.yml
	ConfigDir string
	// EnvOverride forces APP_ENV. Empty = read from env or fall back to "dev".
	EnvOverride string
}

// Load reads defaults, overlays per-env YAML, then applies environment variables.
func Load(opts LoadOptions) (*Config, error) {

	_ = godotenv.Load()

	if opts.ConfigDir == "" {
		opts.ConfigDir = "config"
	}

	if opts.ConfigDir == "" {
		opts.ConfigDir = "config"
	}

	env := strings.ToLower(strings.TrimSpace(opts.EnvOverride))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	}
	if env == "" {
		env = "dev"
	}

	v := viper.New()
	v.SetConfigType("yaml")

	// Defaults (config/config.yaml)
	base := filepath.Join(opts.ConfigDir, "config.yaml")
	v.SetConfigFile(base)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", base, err)
	}

	// Per-env overlay
	envFile := filepath.Join(opts.ConfigDir, "config."+env+".yaml")
	if _, err := os.Stat(envFile); err != nil {
		vEnv := viper.New()
		vEnv.SetConfigType("yaml")
		vEnv.SetConfigFile(envFile)

		if err := vEnv.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("config: read %s: %w", envFile, err)
		}
		if err := v.MergeConfigMap(vEnv.AllSettings()); err != nil {
			return nil, fmt.Errorf("config: merge %s: %w", envFile, err)
		}
	}

	// Environment variable
	v.SetEnvPrefix("")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	bindEnvVars(v) // explicit binds for keys that don't follow the naming rule

	// Sensible defaults for anything YAML forgot
	applyDefaults(v)

	// Unmarshal into typed struct
	cfg := &Config{}
	if err := v.Unmarshal(cfg, decoderOptions...); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	cfg.App.Env = env
	cfg.raw = v

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// MustLoad is Load that panics on error. Use only in main().
func MustLoad(opts LoadOptions) *Config {
	c, err := Load(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(2)
	}
	return c
}

// ApplyDefaults sets fallback values for keys that must not be zero.
func applyDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "pharmaciano-erp")
	v.SetDefault("app.timezone", "Asia/Dhaka")
	v.SetDefault("app.version", "0.0.0")

	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", 15*time.Second)
	v.SetDefault("server.write_timeout", 30*time.Second)
	v.SetDefault("server.idle_timeout", 60*time.Second)
	v.SetDefault("server.shutdown_timeout", 20*time.Second)
	v.SetDefault("server.body_limit_mb", 5)
	v.SetDefault("server.upload_limit_mb", 25)

	v.SetDefault("database.pool.max_conns", 25)
	v.SetDefault("database.pool.min_conns", 2)
	v.SetDefault("database.pool.max_conn_lifetime", time.Hour)
	v.SetDefault("database.pool.max_conn_idle_time", 30*time.Minute)
	v.SetDefault("database.pool.health_check_period", time.Minute)
	v.SetDefault("database.statement_timeout", 30*time.Second)

	v.SetDefault("redis.pool_size", 20)
	v.SetDefault("redis.min_idle_conns", 2)
	v.SetDefault("redis.dial_timeout", 5*time.Second)
	v.SetDefault("redis.read_timeout", 3*time.Second)
	v.SetDefault("redis.write_timeout", 3*time.Second)

	v.SetDefault("jwt.algorithm", "HS256")
	v.SetDefault("jwt.access_token_ttl", 15*time.Minute)
	v.SetDefault("jwt.refresh_token_ttl", 168*time.Hour)
	v.SetDefault("jwt.clock_skew", 30*time.Second)

	v.SetDefault("password.argon2.memory_kb", 65536)
	v.SetDefault("password.argon2.time", 3)
	v.SetDefault("password.argon2.parallelism", 2)
	v.SetDefault("password.argon2.key_length", 32)
	v.SetDefault("password.argon2.salt_length", 16)

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")

	v.SetDefault("pagination.default_limit", 20)
	v.SetDefault("pagination.max_limit", 100)
}

// bindEnvVars maps env var names that don't map cleanly to dotted keys.
// (Viper turns "server.port" → "SERVER_PORT" automatically; anything else that we want to accept goes here.)
func bindEnvVars(v *viper.Viper) {
	binds := map[string]string{
		"app.env":      "APP_ENV",
		"app.name":     "APP_NAME",
		"app.version":  "APP_VERSION",
		"app.timezone": "APP_TIMEZONE",

		"database.host":                  "DB_HOST",
		"database.port":                  "DB_PORT",
		"database.name":                  "DB_NAME",
		"database.user":                  "DB_USER",
		"database.password":              "DB_PASSWORD",
		"database.sslmode":               "DB_SSLMODE",
		"database.read_replica.host":     "DB_READ_HOST",
		"database.read_replica.port":     "DB_READ_PORT",
		"database.read_replica.user":     "DB_READ_USER",
		"database.read_replica.password": "DB_READ_PASSWORD",

		"redis.host":     "REDIS_HOST",
		"redis.port":     "REDIS_PORT",
		"redis.password": "REDIS_PASSWORD",
		"redis.db":       "REDIS_DB",

		"jwt.secret":           "JWT_SECRET",
		"jwt.key_id":           "JWT_KEY_ID",
		"jwt.algorithm":        "JWT_ALGORITHM",
		"jwt.private_key_path": "JWT_PRIVATE_KEY_PATH",
		"jwt.public_key_path":  "JWT_PUBLIC_KEY_PATH",

		"encryption.current_key_id": "FIELD_ENCRYPTION_KEY_ID",
		"encryption.current_key":    "FIELD_ENCRYPTION_KEY",

		"ai.api_key":  "AI_API_KEY",
		"ai.base_url": "AI_BASE_URL",
		"ai.model":    "AI_MODEL",

		"super_admin.email":            "SUPER_ADMIN_EMAIL",
		"super_admin.initial_password": "SUPER_ADMIN_INITIAL_PASSWORD",

		"telemetry.metrics.enabled":    "METRICS_ENABLED",
		"telemetry.metrics.path":       "METRICS_PATH",
		"telemetry.metrics.listen":     "METRICS_LISTEN",
		"telemetry.metrics.auth_token": "METRICS_AUTH_TOKEN",

		"telemetry.tracing.enabled":        "TRACING_ENABLED",
		"telemetry.tracing.service_name":   "OTEL_SERVICE_NAME",
		"telemetry.tracing.endpoint":       "OTEL_EXPORTER_OTLP_ENDPOINT",
		"telemetry.tracing.insecure":       "OTEL_EXPORTER_OTLP_INSECURE",
		"telemetry.tracing.sampling_ratio": "OTEL_SAMPLING_RATIO",

		"mailer.driver":     "MAILER_DRIVER",
		"mailer.from_email": "MAILER_FROM_EMAIL",
		"mailer.from_name":  "MAILER_FROM_NAME",

		"telemetry.health.expose_errors": "HEALTH_EXPOSE_ERRORS",
	}

	for key, env := range binds {
		_ = v.BindEnv(key, env)
	}
}
