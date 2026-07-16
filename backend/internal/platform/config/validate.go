package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Config) Validate() error {
	var errs errList

	// App
	if c.App.Name == "" {
		errs.add("app.name is required")
	}
	if c.App.Env == "" {
		errs.add("app.env is required")
	}
	if _, err := time.LoadLocation(c.App.Timezone); err != nil {
		errs.add("app.timezone %q is not a valid IANA timezone: %v", c.App.Timezone, err)
	}

	// Server
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs.add("server.port must be 1..65535 (got %d)", c.Server.Port)
	}
	if c.Server.BodyLimitMB <= 0 {
		errs.add("server.body_limit_mb must be > 0")
	}

	// Database
	if c.Database.Host == "" {
		errs.add("database.host is required")
	}
	if c.Database.Port <= 0 {
		errs.add("database.port must be > 0")
	}
	if c.Database.Name == "" {
		errs.add("database.name is required")
	}
	if c.Database.User == "" {
		errs.add("database.user is required")
	}
	if c.Database.Password == "" && !c.IsDev() {
		errs.add("database.password is required outside dev (set DB_PASSWORD)")
	}
	if c.Database.Pool.MaxConns <= 0 {
		errs.add("database.pool.max_conns must be > 0")
	}
	if c.Database.Pool.MinConns < 0 || c.Database.Pool.MinConns > c.Database.Pool.MaxConns {
		errs.add("database.pool.min_conns invalid: %d (max %d)", c.Database.Pool.MinConns, c.Database.Pool.MaxConns)
	}
	if !oneOf(c.Database.SSLMode, "disable", "allow", "prefer", "require", "verify-ca", "verify-full") {
		errs.add("database.sslmode %q is invalid", c.Database.SSLMode)
	}
	if c.IsProd() && c.Database.SSLMode == "disable" {
		errs.add("database.sslmode must not be 'disable' in production")
	}

	// Redis
	if c.Redis.Host == "" {
		errs.add("redis.host is required")
	}
	if c.Redis.Port <= 0 {
		errs.add("redis.port must be > 0")
	}
	if _, err := net.LookupPort("tcp", strconv.Itoa(c.Redis.Port)); err != nil {
		errs.add("redis.port invalid: %v", err)
	}

	// JWT
	alg := strings.ToUpper(c.JWT.Algorithm)
	if !oneOf(alg, "HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "ES256", "EdDSA") {
		errs.add("jwt.algorithm %q not supported", c.JWT.Algorithm)
	}
	if strings.HasPrefix(alg, "HS") {
		if len(c.JWT.Secret) < 32 {
			errs.add("jwt.secret must be at least 32 bytes for %s", alg)
		}
	} else {
		if c.JWT.PrivateKeyPath == "" || c.JWT.PublicKeyPath == "" {
			errs.add("jwt.private_key_path and jwt.public_key_path are required for %s", alg)
		}
	}
	if c.JWT.Issuer == "" {
		errs.add("jwt.issuer is required")
	}
	if c.JWT.Audience == "" {
		errs.add("jwt.audience is required")
	}
	if c.JWT.AccessTokenTTL <= 0 || c.JWT.AccessTokenTTL > 24*time.Hour {
		errs.add("jwt.access_token_ttl must be > 0 and ≤ 24h (got %s)", c.JWT.AccessTokenTTL)
	}
	if c.JWT.RefreshTokenTTL < c.JWT.AccessTokenTTL {
		errs.add("jwt.refresh_token_ttl must be ≥ jwt.access_token_ttl")
	}

	// Encryption
	if c.Encryption.CurrentKeyID == "" {
		errs.add("encryption.current_key_id is required (set FIELD_ENCRYPTION_KEY_ID)")
	}
	if raw, err := base64.StdEncoding.DecodeString(c.Encryption.CurrentKey); err != nil {
		errs.add("encryption.current_key is not valid base64 (set FIELD_ENCRYPTION_KEY)")
	} else if len(raw) != 32 {
		errs.add("encryption.current_key must decode to 32 bytes (got %d)", len(raw))
	}

	// Cookie
	if !oneOf(strings.ToLower(c.Cookie.SameSite), "strict", "lax", "none") {
		errs.add("refresh_cookie.samesite must be strict|lax|none")
	}
	if c.IsProd() && !c.Cookie.Secure {
		errs.add("refresh_cookie.secure must be true in production")
	}

	// Argon2
	if c.Password.Argon2.MemoryKB < 32*1024 { // 32 MiB minimum
		errs.add("password.argon2.memory_kb must be ≥ 32768 (32 MiB)")
	}
	if c.Password.Argon2.Time < 1 {
		errs.add("password.argon2.time must be ≥ 1")
	}
	if c.Password.Argon2.Parallelism < 1 {
		errs.add("password.argon2.parallelism must be ≥ 1")
	}

	// Logging
	if !oneOf(strings.ToLower(c.Logging.Level), "debug", "info", "warn", "error", "fatal") {
		errs.add("logging.level %q invalid", c.Logging.Level)
	}
	if !oneOf(strings.ToLower(c.Logging.Format), "json", "console") {
		errs.add("logging.format must be json|console")
	}

	// CORS
	for _, o := range c.CORS.AllowOrigins {
		if o == "*" && c.CORS.AllowCredentials {
			errs.add("cors.allow_origins cannot be '*' when allow_credentials=true")
		}
		if o != "*" {
			if _, err := url.Parse(o); err != nil {
				errs.add("cors.allow_origins %q not a valid URL: %v", o, err)
			}
		}
	}

	// AI
	if c.AI.Enabled {
		if c.AI.APIKey == "" {
			errs.add("ai.api_key is required when ai.enabled=true")
		}
		if c.AI.CostCapUSDPerDay < 0 {
			errs.add("ai.cost_cap_usd_per_day must be ≥ 0")
		}
	}

	// Storage
	switch c.Storage.Driver {
	case "local":
		if c.Storage.LocalPath == "" {
			errs.add("storage.local_path is required when driver=local")
		}
	case "s3":
		s := c.Storage.S3
		if s.Bucket == "" || s.AccessKey == "" || s.SecretKey == "" {
			errs.add("storage.s3 bucket/access_key/secret_key are required when driver=s3")
		}
	case "":
		// default local
	default:
		errs.add("storage.driver %q invalid (want local|s3)", c.Storage.Driver)
	}

	// Rate limit
	if c.RateLimit.Enabled {
		for name, p := range c.RateLimit.Policies {
			if p.Limit <= 0 {
				errs.add("rate_limit.policies.%s.limit must be > 0", name)
			}
			if p.Window <= 0 {
				errs.add("rate_limit.policies.%s.window must be > 0", name)
			}
		}
	}

	// Super admin seed
	if c.Seed.AutoRunOnBoot {
		if c.SuperAdmin.Email == "" {
			errs.add("super_admin.email is required when seed runs on boot")
		}
		if c.SuperAdmin.InitialPassword == "" {
			errs.add("super_admin.initial_password is required (set SUPER_ADMIN_INITIAL_PASSWORD)")
		}
		if len(c.SuperAdmin.InitialPassword) < 10 {
			errs.add("super_admin.initial_password must be ≥ 10 chars")
		}
	}

	if len(errs) > 0 {
		return errs.err()
	}

	return nil
}

// Small helpers
type errList []string

func (e *errList) add(format string, args ...any) {
	*e = append(*e, fmt.Sprintf(format, args...))
}

func (e errList) err() error {
	return errors.New("config: validation failed:\n  - " + strings.Join(e, "\n  - "))
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// fmtSprintf is aliased so types.go can build DSN strings without importing
var fmtSprintf = fmt.Sprintf
