package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestPurchaseReadAPIFeatureDefaultsDisabled(t *testing.T) {
	settings := viper.New()
	applyDefaults(settings)

	if settings.GetBool("features.purchase_read_api") {
		t.Fatal("features.purchase_read_api defaulted to enabled")
	}
}

func TestPurchaseReadAPIRequiresExplicitConfiguration(t *testing.T) {
	t.Setenv("FEATURES_PURCHASE_READ_API", "true")
	settings := viper.New()
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	applyDefaults(settings)

	var cfg Config
	if err := settings.Unmarshal(&cfg, decoderOptions...); err != nil {
		t.Fatalf("unmarshal feature config: %v", err)
	}
	if !cfg.Features.PurchaseReadAPI {
		t.Fatal("explicit FEATURES_PURCHASE_READ_API=true was not applied")
	}
}
