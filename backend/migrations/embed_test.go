package migrations

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationSourceLoads(t *testing.T) {
	if _, err := iofs.New(FS, "."); err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
}
