package cache

import (
	"context"
	"encoding/json"
	"time"

	"backend/internal/database"
	"backend/internal/dto"
)

const (
	RolePermissionsTTL = 30 * time.Minute
)

// CachedPermissions stores permissions for a role
type CachedPermissions struct {
	Permissions []dto.PermissionBrief `json:"permissions"`
}

// GetRolePermissions returns permissions for a role, with Redis caching
func GetRolePermissions(ctx context.Context, roleName string) ([]dto.PermissionBrief, error) {
	key := RolePermissionsKey(roleName)
	cached, err := RDB.Get(ctx, key).Result()
	if err == nil {
		var cp CachedPermissions
		if json.Unmarshal([]byte(cached), &cp) == nil {
			return cp.Permissions, nil
		}
	}

	// Load from database
	type permRow struct {
		Module string
		Action string
	}
	var rows []permRow
	database.DB.Raw(`
        SELECT p.module, p.action
        FROM permissions p
        JOIN role_permissions rp ON rp.permission_id = p.id
        JOIN roles r ON r.id = rp.role_id
        WHERE r.name = ?`, roleName).Scan(&rows)

	permissions := make([]dto.PermissionBrief, 0, len(rows))
	for _, row := range rows {
		permissions = append(permissions, dto.PermissionBrief{Module: row.Module, Action: row.Action})
	}

	// Store in Redis
	cp := CachedPermissions{Permissions: permissions}
	data, _ := json.Marshal(cp)
	RDB.Set(ctx, key, data, RolePermissionsTTL)

	return permissions, nil
}

// InvalidateRolePermissions removes cached permissions for a role
func InvalidateRolePermissions(ctx context.Context, roleName string) {
	RDB.Del(ctx, RolePermissionsKey(roleName))
}
