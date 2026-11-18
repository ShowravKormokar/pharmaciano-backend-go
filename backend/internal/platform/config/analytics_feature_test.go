package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestAnalyticsReadAPIFeatureDefaultsDisabled(t *testing.T) {
	settings := viper.New()
	applyDefaults(settings)

	if settings.GetBool("features.analytics_read_api") {
		t.Fatal("features.analytics_read_api defaulted to enabled")
	}
}

func TestAnalyticsReadAPIRequiresExplicitConfiguration(t *testing.T) {
	t.Setenv("FEATURES_ANALYTICS_READ_API", "true")
	settings := viper.New()
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	applyDefaults(settings)

	var cfg Config
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal feature config: %v", err)
	}
	if !cfg.Features.AnalyticsReadAPI {
		t.Fatal("explicit FEATURES_ANALYTICS_READ_API=true was not applied")
	}
}

func TestOutboxConfigIsDisabledByDefaultAndEnvironmentOverridable(t *testing.T) {
	settings := viper.New()
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	applyDefaults(settings)

	var cfg Config
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal defaults: %v", err)
	}
	if cfg.Outbox.Enabled {
		t.Fatal("outbox worker defaulted to enabled before handlers are wired")
	}
	if cfg.Outbox.MaxAttempts <= 0 || cfg.Outbox.LeaseDuration <= cfg.Outbox.HandlerTimeout {
		t.Fatalf("invalid outbox defaults: %+v", cfg.Outbox)
	}

	t.Setenv("OUTBOX_ENABLED", "true")
	settings.AutomaticEnv()
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal environment override: %v", err)
	}
	if !cfg.Outbox.Enabled {
		t.Fatal("OUTBOX_ENABLED=true was not applied")
	}
}

func TestAsynqQueueWeightsAreEnvironmentOverridable(t *testing.T) {
	t.Setenv("ASYNQ_QUEUES_DEFAULT", "8")
	settings := viper.New()
	settings.SetConfigType("yaml")
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	if err := settings.ReadConfig(strings.NewReader("asynq:\n  queues:\n    critical: 6\n    default: 3\n    low: 1\n")); err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	bindEnvVars(settings)
	applyAsynqQueueEnvironment(settings)

	var cfg Config
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal Asynq config: %v", err)
	}
	if cfg.Asynq.Queues["default"] != 8 {
		t.Fatalf("default queue weight = %d, want 8", cfg.Asynq.Queues["default"])
	}
}
