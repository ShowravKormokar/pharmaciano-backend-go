package config

import (
	"strings"
	"testing"
)

func TestBackupCannotBeEnabledBeforeRuntimeIsReady(t *testing.T) {
	cfg := &Config{}
	cfg.Backup.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() accepted backup.enabled=true without the required runtime")
	}
	if !strings.Contains(err.Error(), "backup.enabled cannot be true") {
		t.Fatalf("Validate() error = %q, want explicit backup readiness failure", err)
	}
}
