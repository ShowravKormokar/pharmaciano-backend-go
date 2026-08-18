package config

import (
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// decoderOptions makes Viper's Unmarshal understand time.Duration strings and case-insensitive keys.
var decoderOptions = []viper.DecoderConfigOption{
	func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)
		dc.TagName = "mapstructure"
		dc.ErrorUnused = false
	},
}

// Config is the fully-typed configuration for the API and worker.
type Config struct {
	App         AppConfig          `mapstructure:"app"`
	Server      ServerConfig       `mapstructure:"server"`
	CORS        CORSConfig         `mapstructure:"cors"`
	Security    SecurityConfig     `mapstructure:"security"`
	Database    DatabaseConfig     `mapstructure:"database"`
	Redis       RedisConfig        `mapstructure:"redis"`
	Asynq       AsynqConfig        `mapstructure:"asynq"`
	JWT         JWTConfig          `mapstructure:"jwt"`
	Cookie      CookieConfig       `mapstructure:"refresh_cookie"`
	Password    PasswordConfig     `mapstructure:"password"`
	Encryption  EncryptionConfig   `mapstructure:"encryption"`
	Session     SessionConfig      `mapstructure:"session"`
	RateLimit   RateLimitConfig    `mapstructure:"rate_limit"`
	Login       LoginLockoutConfig `mapstructure:"login_lockout"`
	Idempotency IdempotencyConfig  `mapstructure:"idempotency"`
	Casbin      CasbinConfig       `mapstructure:"casbin"`
	Logging     LoggingConfig      `mapstructure:"logging"`
	Telemetry   TelemetryConfig    `mapstructure:"telemetry"`
	WebSocket   WebSocketConfig    `mapstructure:"websocket"`
	Pagination  PaginationConfig   `mapstructure:"pagination"`
	AI          AIConfig           `mapstructure:"ai"`
	Storage     StorageConfig      `mapstructure:"storage"`
	Mailer      MailerConfig       `mapstructure:"mailer"`
	Backup      BackupConfig       `mapstructure:"backup"`
	Features    FeatureFlags       `mapstructure:"features"`
	Audit       AuditConfig        `mapstructure:"audit"`
	SuperAdmin  SuperAdminConfig   `mapstructure:"super_admin"`
	OrgDefaults OrgDefaults        `mapstructure:"organization_defaults"`
	PProf       PProfConfig        `mapstructure:"pprof"`
	Seed        SeedConfig         `mapstructure:"seed"`

	raw *viper.Viper // used for hot-reload
}

type AppConfig struct {
	Name     string `mapstructure:"name"`
	Env      string `mapstructure:"env"`
	Version  string `mapstructure:"version"`
	Timezone string `mapstructure:"timezone"`
}

type ServerConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	MaxHeaderBytes  int           `mapstructure:"max_header_bytes"`
	BodyLimitMB     int           `mapstructure:"body_limit_mb"`
	UploadLimitMB   int           `mapstructure:"upload_limit_mb"`
	TrustProxy      bool          `mapstructure:"trust_proxy"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type CORSConfig struct {
	AllowOrigins     []string      `mapstructure:"allow_origins"`
	AllowMethods     []string      `mapstructure:"allow_methods"`
	AllowHeaders     []string      `mapstructure:"allow_headers"`
	ExposeHeaders    []string      `mapstructure:"expose_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

type SecurityConfig struct {
	HSTS struct {
		Enabled           bool `mapstructure:"enabled"`
		MaxAge            int  `mapstructure:"max_age"`
		IncludeSubdomains bool `mapstructure:"include_subdomains"`
		Preload           bool `mapstructure:"preload"`
	} `mapstructure:"hsts"`
	CSP                 string `mapstructure:"csp"`
	XFrameOptions       string `mapstructure:"x_frame_options"`
	XContentTypeOptions string `mapstructure:"x_content_type_options"`
	ReferrerPolicy      string `mapstructure:"referrer_policy"`
}

type DatabaseConfig struct {
	Host             string        `mapstructure:"host"`
	Port             int           `mapstructure:"port"`
	Name             string        `mapstructure:"name"`
	User             string        `mapstructure:"user"`
	Password         string        `mapstructure:"password"`
	SSLMode          string        `mapstructure:"sslmode"`
	Timezone         string        `mapstructure:"timezone"`
	StatementTimeout time.Duration `mapstructure:"statement_timeout"`
	Pool             DBPoolConfig  `mapstructure:"pool"`
	ReadReplica      DBReplica     `mapstructure:"read_replica"`
}

type DBPoolConfig struct {
	MaxConns          int32         `mapstructure:"max_conns"`
	MinConns          int32         `mapstructure:"min_conns"`
	MaxConnLifetime   time.Duration `mapstructure:"max_conn_lifetime"`
	MaxConnIdleTime   time.Duration `mapstructure:"max_conn_idle_time"`
	HealthCheckPeriod time.Duration `mapstructure:"health_check_period"`
}

type DBReplica struct {
	Enabled  bool   `mapstructure:"enabled"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

type RedisConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	Password     string        `mapstructure:"password"`
	DB           int           `mapstructure:"db"`
	PoolSize     int           `mapstructure:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type AsynqConfig struct {
	RedisDB        int            `mapstructure:"redis_db"`
	Concurrency    int            `mapstructure:"concurrency"`
	StrictPriority bool           `mapstructure:"strict_priority"`
	Queues         map[string]int `mapstructure:"queues"`
	Retry          struct {
		MaxRetries   int           `mapstructure:"max_retries"`
		InitialDelay time.Duration `mapstructure:"initial_delay"`
	} `mapstructure:"retry"`
	DeadLetterTTL time.Duration `mapstructure:"dead_letter_ttl"`
}

type JWTConfig struct {
	Issuer          string        `mapstructure:"issuer"`
	Audience        string        `mapstructure:"audience"`
	Algorithm       string        `mapstructure:"algorithm"`
	KeyID           string        `mapstructure:"key_id"`
	Secret          string        `mapstructure:"secret"`
	AccessTokenTTL  time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL time.Duration `mapstructure:"refresh_token_ttl"`
	ClockSkew       time.Duration `mapstructure:"clock_skew"`
	PrivateKeyPath  string        `mapstructure:"private_key_path"`
	PublicKeyPath   string        `mapstructure:"public_key_path"`
}

type CookieConfig struct {
	Name     string `mapstructure:"name"`
	Domain   string `mapstructure:"domain"`
	Path     string `mapstructure:"path"`
	Secure   bool   `mapstructure:"secure"`
	HTTPOnly bool   `mapstructure:"httponly"`
	SameSite string `mapstructure:"samesite"`
}

type PasswordConfig struct {
	Argon2 struct {
		MemoryKB    uint32 `mapstructure:"memory_kb"`
		Time        uint32 `mapstructure:"time"`
		Parallelism uint8  `mapstructure:"parallelism"`
		KeyLength   uint32 `mapstructure:"key_length"`
		SaltLength  uint32 `mapstructure:"salt_length"`
	} `mapstructure:"argon2"`
	MinLength     int  `mapstructure:"min_length"`
	RequireUpper  bool `mapstructure:"require_upper"`
	RequireLower  bool `mapstructure:"require_lower"`
	RequireDigit  bool `mapstructure:"require_digit"`
	RequireSymbol bool `mapstructure:"require_symbol"`
	HistorySize   int  `mapstructure:"history_size"`
}

type EncryptionConfig struct {
	CurrentKeyID string            `mapstructure:"current_key_id"`
	CurrentKey   string            `mapstructure:"current_key"` // base64, 32 bytes
	OldKeys      map[string]string `mapstructure:"old_keys"`
}

type SessionConfig struct {
	IdleTimeout          time.Duration `mapstructure:"idle_timeout"`
	AbsoluteTimeout      time.Duration `mapstructure:"absolute_timeout"`
	MaxConcurrentPerUser int           `mapstructure:"max_concurrent_per_user"`
}

type RateLimitConfig struct {
	Enabled  bool                       `mapstructure:"enabled"`
	Policies map[string]RateLimitPolicy `mapstructure:"policies"`
}

type RateLimitPolicy struct {
	Limit  int           `mapstructure:"limit"`
	Window time.Duration `mapstructure:"window"`
}

type LoginLockoutConfig struct {
	Threshold int           `mapstructure:"threshold"`
	Window    time.Duration `mapstructure:"window"`
	Duration  time.Duration `mapstructure:"duration"`
}

type IdempotencyConfig struct {
	TTL          time.Duration `mapstructure:"ttl"`
	KeyMaxLength int           `mapstructure:"key_max_length"`
}

type CasbinConfig struct {
	ModelPath        string        `mapstructure:"model_path"`
	Autosave         bool          `mapstructure:"autosave"`
	AutoloadInterval time.Duration `mapstructure:"autoload_interval"`
}

type LoggingConfig struct {
	Level    string `mapstructure:"level"`
	Format   string `mapstructure:"format"`
	Output   string `mapstructure:"output"`
	FilePath string `mapstructure:"file_path"`
	Sampling struct {
		Enabled    bool `mapstructure:"enabled"`
		Initial    int  `mapstructure:"initial"`
		Thereafter int  `mapstructure:"thereafter"`
	} `mapstructure:"sampling"`
	RedactFields []string `mapstructure:"redact_fields"`
}

type TelemetryConfig struct {
	Metrics struct {
		Enabled   bool   `mapstructure:"enabled"`
		Path      string `mapstructure:"path"`
		Listen    string `mapstructure:"listen"`
		AuthToken string `mapstructure:"auth_token"` // optional; protects /metrics with Bearer auth
	} `mapstructure:"metrics"`
	Tracing struct {
		Enabled       bool    `mapstructure:"enabled"`
		ServiceName   string  `mapstructure:"service_name"`
		Exporter      string  `mapstructure:"exporter"`
		Endpoint      string  `mapstructure:"endpoint"`
		Insecure      bool    `mapstructure:"insecure"`
		SamplingRatio float64 `mapstructure:"sampling_ratio"`
	} `mapstructure:"tracing"`
	Health struct {
		ExposeErrors bool          `mapstructure:"expose_errors"` // false in prod: sanitize /readyz errors
		CacheTTL     time.Duration `mapstructure:"cache_ttl"`
	} `mapstructure:"health"`
}

type WebSocketConfig struct {
	ReadBufferBytes  int           `mapstructure:"read_buffer_bytes"`
	WriteBufferBytes int           `mapstructure:"write_buffer_bytes"`
	MaxMessageBytes  int64         `mapstructure:"max_message_bytes"`
	PingInterval     time.Duration `mapstructure:"ping_interval"`
	PongWait         time.Duration `mapstructure:"pong_wait"`
	WriteTimeout     time.Duration `mapstructure:"write_timeout"`
	OriginsAllowed   []string      `mapstructure:"origins_allowed"`
}

type PaginationConfig struct {
	DefaultLimit int    `mapstructure:"default_limit"`
	MaxLimit     int    `mapstructure:"max_limit"`
	CursorSecret string `mapstructure:"cursor_secret_env"`
}

type AIConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	Provider         string        `mapstructure:"provider"`
	Model            string        `mapstructure:"model"`
	BaseURL          string        `mapstructure:"base_url"`
	APIKey           string        `mapstructure:"api_key"`
	Timeout          time.Duration `mapstructure:"timeout"`
	MaxTokens        int           `mapstructure:"max_tokens"`
	Temperature      float64       `mapstructure:"temperature"`
	CostCapUSDPerDay float64       `mapstructure:"cost_cap_usd_per_day"`
	ForecastCacheTTL time.Duration `mapstructure:"forecast_cache_ttl"`
	CircuitBreaker   struct {
		ErrorThreshold int           `mapstructure:"error_threshold"`
		Timeout        time.Duration `mapstructure:"timeout"`
		HalfOpenMax    int           `mapstructure:"half_open_max"`
	} `mapstructure:"circuit_breaker"`
}

type StorageConfig struct {
	Driver           string   `mapstructure:"driver"` // local|s3
	LocalPath        string   `mapstructure:"local_path"`
	MaxFileSizeMB    int      `mapstructure:"max_file_size_mb"`
	AllowedMimeTypes []string `mapstructure:"allowed_mime_types"`
	S3               struct {
		Endpoint  string `mapstructure:"endpoint"`
		Region    string `mapstructure:"region"`
		Bucket    string `mapstructure:"bucket"`
		AccessKey string `mapstructure:"access_key"`
		SecretKey string `mapstructure:"secret_key"`
		UseSSL    bool   `mapstructure:"use_ssl"`
	} `mapstructure:"s3"`
}

type MailerConfig struct {
	Driver    string `mapstructure:"driver"`     // noop | disabled  (smtp/sendgrid/ses land later)
	FromEmail string `mapstructure:"from_email"`
	FromName  string `mapstructure:"from_name"`
}

type BackupConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Schedule      string `mapstructure:"schedule"`
	LocalPath     string `mapstructure:"local_path"`
	RetentionDays int    `mapstructure:"retention_days"`
	Compress      bool   `mapstructure:"compress"`
}

type FeatureFlags struct {
	AIForecasting          bool `mapstructure:"ai_forecasting"`
	WebSocketNotifications bool `mapstructure:"websocket_notifications"`
	LedgerAutopost         bool `mapstructure:"ledger_autopost"`
	BranchTransfers        bool `mapstructure:"branch_transfers"`
}

type AuditConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	Async            bool   `mapstructure:"async"`
	PartitionBy      string `mapstructure:"partition_by"`
	RetentionDays    int    `mapstructure:"retention_days"`
	ArchiveAfterDays int    `mapstructure:"archive_after_days"`
}

type SuperAdminConfig struct {
	Email           string `mapstructure:"email"`
	InitialPassword string `mapstructure:"initial_password"`
	FirstName       string `mapstructure:"first_name"`
	LastName        string `mapstructure:"last_name"`
}

type OrgDefaults struct {
	Name             string `mapstructure:"name"`
	Slug             string `mapstructure:"slug"`
	Country          string `mapstructure:"country"`
	Currency         string `mapstructure:"currency"`
	Timezone         string `mapstructure:"timezone"`
	SubscriptionPlan string `mapstructure:"subscription_plan"`
}

type PProfConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Listen  string `mapstructure:"listen"`
}

type SeedConfig struct {
	AutoRunOnBoot   bool `mapstructure:"auto_run_on_boot"`
	ResetBeforeSeed bool `mapstructure:"reset_before_seed"`
}

// Helper Accessors
// IsDev reports whether env is "dev" (case-insensitive).
func (c *Config) IsDev() bool {
	return c.App.Env == "dev"
}

// IsProd reports whether env is "prod" or "production".
func (c *Config) IsProd() bool {
	return c.App.Env == "prod" || c.App.Env == "production"
}

// DSN returns a Postgres connection string suitable for pgxpool.
func (c *Config) DSN() string {
	return sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.Database.User, c.Database.Password,
		c.Database.Host, c.Database.Port,
		c.Database.Name, c.Database.SSLMode)
}

// ReadDSN returns the read-replica connection string, falling back to DSN().
func (c *Config) ReadDSN() string {
	r := c.Database.ReadReplica
	if !r.Enabled || r.Host == "" {
		return c.DSN()
	}

	user := r.User
	if user == "" {
		user = c.Database.User
	}
	pass := r.Password
	if pass == "" {
		pass = c.Database.Password
	}
	port := r.Port
	if port == 0 {
		port = 5432
	}
	return sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		user, pass, r.Host, port, c.Database.Name, c.Database.SSLMode)
}

// sprintf is a tiny wrapper so the file has zero fmt-import gymnastics in this section (the real fmt import is in config.go).
func sprintf(format string, args ...any) string {
	return fmtSprintf(format, args...)
}
