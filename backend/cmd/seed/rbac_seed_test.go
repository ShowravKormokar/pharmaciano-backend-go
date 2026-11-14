package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSalePermissionSeedCatalog(t *testing.T) {
	var permissions struct {
		Permissions []PermissionSeed `yaml:"permissions"`
	}
	readSeedYAML(t, "permissions.yaml", &permissions)

	actions := make(map[string]bool, len(permissions.Permissions))
	for _, permission := range permissions.Permissions {
		if permission.Module == "sales" {
			actions[permission.Action] = true
		}
	}

	for _, action := range []string{
		"create", "view", "manage-prices", "override-discount", "void",
		"refund", "collect-payment", "open-cash", "close-cash", "cash-adjust",
		"cash-reopen", "prescription-view", "prescription-review", "export",
	} {
		if !actions[action] {
			t.Errorf("missing sales:%s permission", action)
		}
	}
	if actions["update"] {
		t.Error("unsafe generic sales:update permission must not be seeded")
	}

	var roles struct {
		Roles []RoleSeed `yaml:"roles"`
	}
	readSeedYAML(t, "roles.yaml", &roles)

	restricted := map[string]bool{
		"sales:manage-prices":       true,
		"sales:override-discount":   true,
		"sales:collect-payment":     true,
		"sales:open-cash":           true,
		"sales:close-cash":          true,
		"sales:cash-adjust":         true,
		"sales:cash-reopen":         true,
		"sales:prescription-view":   true,
		"sales:prescription-review": true,
	}
	for _, role := range roles.Roles {
		for _, permission := range role.Permissions {
			if restricted[permission] {
				t.Errorf("restricted permission %q must not be directly granted to role %q", permission, role.Name)
			}
			if permission == "sales:update" {
				t.Errorf("retired permission must not be granted to role %q", role.Name)
			}
		}
	}
}

func readSeedYAML(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "seed", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}
