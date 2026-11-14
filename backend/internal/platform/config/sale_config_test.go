package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestSaleEnvironmentOverrides(t *testing.T) {
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_POS_CHECKOUT_RPM", "17")
	t.Setenv("IDEMPOTENCY_TTL", "12h")
	t.Setenv("IDEMPOTENCY_KEY_MAX_LENGTH", "64")

	settings := viper.New()
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	settings.SetDefault("rate_limit.policies.pos_checkout.window", time.Minute)
	bindEnvVars(settings)
	applyDefaults(settings)

	var cfg Config
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}

	if !cfg.RateLimit.Enabled {
		t.Fatal("RATE_LIMIT_ENABLED was not applied")
	}
	if got := cfg.RateLimit.Policies["pos_checkout"].Limit; got != 17 {
		t.Fatalf("POS checkout limit = %d, want 17", got)
	}
	if got := cfg.Idempotency.TTL; got != 12*time.Hour {
		t.Fatalf("idempotency TTL = %s, want 12h", got)
	}
	if got := cfg.Idempotency.KeyMaxLength; got != 64 {
		t.Fatalf("idempotency key max length = %d, want 64", got)
	}
}
