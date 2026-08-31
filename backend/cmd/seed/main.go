package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"gopkg.in/yaml.v3"

	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
)

// SeedData holds the parsed YAML structures used by the bootstrap seed script.
type SeedData struct {
	Permissions []PermissionSeed `yaml:"permissions"`
	Roles       []RoleSeed       `yaml:"roles"`
	SuperAdmin  SuperAdminSeed   `yaml:"super_admin"`
	Settings    []SettingSeed    `yaml:"settings"`
}

type PermissionSeed struct {
	Module      string `yaml:"module"`
	Action      string `yaml:"action"`
	Description string `yaml:"description"`
	IsSystem    bool   `yaml:"is_system"`
}

type RoleSeed struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	IsSystem    bool     `yaml:"is_system"`
	IsActive    bool     `yaml:"is_active"`
	Priority    int      `yaml:"priority"`
	Scope       string   `yaml:"scope"`
	Permissions []string `yaml:"permissions"`
}

type SuperAdminSeed struct {
	Email          string `yaml:"email"`
	Username       string `yaml:"username"`
	EmployeeCode   string `yaml:"employee_code"`
	Phone          string `yaml:"phone"`
	FirstName      string `yaml:"first_name"`
	LastName       string `yaml:"last_name"`
	Status         string `yaml:"status"`
	Stage          string `yaml:"stage"`
	JoiningDate    string `yaml:"joining_date"`
	EmploymentType string `yaml:"employment_type"`
	Role           string `yaml:"role"`
	Profile        struct {
		FirstName string `yaml:"first_name"`
		LastName  string `yaml:"last_name"`
	} `yaml:"profile"`
}

type SettingSeed struct {
	Key         string `yaml:"key"`
	Value       string `yaml:"value"`
	ValueType   string `yaml:"value_type"`
	Description string `yaml:"description"`
	IsSensitive bool   `yaml:"is_sensitive"`
}

func main() {
	seedRole := flag.Bool("roles", true, "seed roles and permissions")
	seedUser := flag.Bool("user", true, "seed super admin user")
	seedOrg := flag.Bool("org", true, "seed organization, branch, warehouse")
	flag.Parse()

	cfg := config.MustLoad(config.LoadOptions{})

	loggr, err := logger.New(cfg.Logging, cfg.App.Name, cfg.App.Version)
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer loggr.Sync()

	ctx := context.Background()

	pg, err := db.New(ctx, cfg.Database, loggr)
	if err != nil {
		loggr.Fatal("db connect", zap.Error(err))
	}
	defer pg.Close()

	seedData, err := loadSeedData()
	if err != nil {
		loggr.Fatal("load seed data", zap.Error(err))
	}

	orgID := uuid.Nil
	if *seedOrg {
		orgID, err = ensureOrganization(ctx, pg.Pool(), cfg)
		if err != nil {
			loggr.Fatal("seed organization", zap.Error(err))
		}
		branchID, err := ensureBranch(ctx, pg.Pool(), orgID, "main", "Main Branch")
		if err != nil {
			loggr.Fatal("seed branch", zap.Error(err))
		}
		if _, err = ensureWarehouse(ctx, pg.Pool(), orgID, branchID, "main", "Main Warehouse"); err != nil {
			loggr.Fatal("seed warehouse", zap.Error(err))
		}
	}

	if *seedRole {
		if err := seedRBAC(ctx, pg.Pool(), seedData); err != nil {
			loggr.Fatal("seed rbac data", zap.Error(err))
		}
	}
	if err := seedSettings(ctx, pg.Pool(), seedData.Settings); err != nil {
		loggr.Fatal("seed system settings", zap.Error(err))
	}

	if *seedUser {
		if orgID == uuid.Nil {
			var exists bool
			orgID, exists, err = getDefaultOrganizationID(ctx, pg.Pool(), cfg.OrgDefaults.Slug)
			if err != nil {
				loggr.Fatal("resolve organization for super admin", zap.Error(err))
			}
			if !exists {
				loggr.Fatal("default organization does not exist; seed org first")
			}
		}
		if err := seedSuperAdmin(ctx, pg.Pool(), cfg, orgID, seedData.SuperAdmin); err != nil {
			loggr.Fatal("seed super admin", zap.Error(err))
		}
	}

	loggr.Info("seeding completed successfully")
}

func loadSeedData() (SeedData, error) {
	seedDir := findSeedDir()
	seedFiles := []string{
		"permissions.yaml",
		"roles.yaml",
		"super_admin.yaml",
		"system_settings.yaml",
	}

	var data SeedData
	for _, file := range seedFiles {
		path := filepath.Join(seedDir, file)
		b, err := os.ReadFile(path)
		if err != nil {
			return SeedData{}, fmt.Errorf("read %s: %w", path, err)
		}
		if err := yaml.Unmarshal(b, &data); err != nil {
			return SeedData{}, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return data, nil
}

func findSeedDir() string {
	candidates := []string{
		filepath.Join("seed"),
		filepath.Join("..", "seed"),
		filepath.Join("..", "..", "seed"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "seed"
}

func ensureOrganization(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1 AND deleted_at IS NULL`, cfg.OrgDefaults.Slug).Scan(&orgID)
	if err == nil {
		return orgID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, subscription_plan, currency, timezone, country)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		cfg.OrgDefaults.Name,
		cfg.OrgDefaults.Slug,
		cfg.OrgDefaults.SubscriptionPlan,
		cfg.OrgDefaults.Currency,
		cfg.OrgDefaults.Timezone,
		cfg.OrgDefaults.Country,
	).Scan(&orgID); err != nil {
		return uuid.Nil, err
	}

	return orgID, nil
}

func ensureBranch(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, code, name string) (uuid.UUID, error) {
	var branchID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM branches WHERE organization_id = $1 AND code = $2 AND deleted_at IS NULL`, orgID, code).Scan(&branchID)
	if err == nil {
		return branchID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO branches (organization_id, code, name, is_active, is_default)
		VALUES ($1, $2, $3, TRUE, TRUE)
		RETURNING id`,
		orgID, code, name,
	).Scan(&branchID); err != nil {
		return uuid.Nil, err
	}

	return branchID, nil
}

func ensureWarehouse(ctx context.Context, pool *pgxpool.Pool, orgID, branchID uuid.UUID, code, name string) (uuid.UUID, error) {
	var warehouseID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM warehouses WHERE organization_id = $1 AND branch_id = $2 AND code = $3 AND deleted_at IS NULL`, orgID, branchID, code).Scan(&warehouseID)
	if err == nil {
		return warehouseID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO warehouses (organization_id, branch_id, code, name, is_active, is_main)
		VALUES ($1, $2, $3, $4, TRUE, TRUE)
		RETURNING id`,
		orgID, branchID, code, name,
	).Scan(&warehouseID); err != nil {
		return uuid.Nil, err
	}

	return warehouseID, nil
}

func getDefaultOrganizationID(ctx context.Context, pool *pgxpool.Pool, slug string) (uuid.UUID, bool, error) {
	var orgID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1 AND deleted_at IS NULL`, slug).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return orgID, true, nil
}

func seedRBAC(ctx context.Context, pool *pgxpool.Pool, data SeedData) error {
	for _, permission := range data.Permissions {
		module, action := normalizePermission(permission.Module, permission.Action)
		if module == "" || action == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO permissions (module, action, description, is_system)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (module, action) DO UPDATE
			SET description = EXCLUDED.description, is_system = EXCLUDED.is_system`, module, action, permission.Description, permission.IsSystem); err != nil {
			return fmt.Errorf("insert permission %s:%s: %w", module, action, err)
		}
	}

	for _, role := range data.Roles {
		if strings.TrimSpace(role.Name) == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO roles (organization_id, name, description, is_active, is_system, priority)
			VALUES (NULL, $1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`, role.Name, role.Description, role.IsActive, role.IsSystem, role.Priority); err != nil {
			return fmt.Errorf("insert role %s: %w", role.Name, err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE roles SET description = $2, is_active = $3, is_system = $4, priority = $5
			WHERE organization_id IS NULL AND name = $1 AND deleted_at IS NULL`, role.Name, role.Description, role.IsActive, role.IsSystem, role.Priority); err != nil {
			return fmt.Errorf("update role %s: %w", role.Name, err)
		}
		for _, perm := range role.Permissions {
			if strings.TrimSpace(perm) == "*" {
				if _, err := pool.Exec(ctx, `
					INSERT INTO role_permissions (role_id, permission_id)
					SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
					WHERE r.organization_id IS NULL AND r.name = $1 AND r.deleted_at IS NULL
					  AND p.deleted_at IS NULL
					  AND NOT EXISTS (SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission_id = p.id)`, role.Name); err != nil {
					return fmt.Errorf("assign all permissions to role %s: %w", role.Name, err)
				}
				continue
			}
			module, action := normalizePermissionSpec(perm)
			if module == "" || action == "" {
				continue
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO role_permissions (role_id, permission_id)
				SELECT r.id, p.id
				FROM roles r
				JOIN permissions p ON p.module = $2 AND p.action = $3
				WHERE r.organization_id IS NULL
				  AND r.name = $1
				  AND r.deleted_at IS NULL
				  AND p.deleted_at IS NULL
				  AND NOT EXISTS (
					SELECT 1 FROM role_permissions rp
					JOIN roles rr ON rr.id = rp.role_id
					WHERE rr.name = $1 AND rr.organization_id IS NULL AND rp.permission_id = p.id
				)`, role.Name, module, action); err != nil {
				return fmt.Errorf("assign permission %s to role %s: %w", perm, role.Name, err)
			}
		}
	}
	return nil
}

func seedSettings(ctx context.Context, pool *pgxpool.Pool, settings []SettingSeed) error {
	for _, setting := range settings {
		key := strings.TrimSpace(setting.Key)
		if key == "" {
			continue
		}
		valueType := setting.ValueType
		if valueType == "duration" {
			valueType = "string"
		}
		var id uuid.UUID
		err := pool.QueryRow(ctx, `SELECT id FROM system_settings WHERE organization_id IS NULL AND key = $1 AND deleted_at IS NULL`, key).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = pool.QueryRow(ctx, `INSERT INTO system_settings (key, value, value_type, description, is_sensitive) VALUES ($1,$2,$3,$4,$5) RETURNING id`, key, setting.Value, valueType, setting.Description, setting.IsSensitive).Scan(&id)
		} else if err == nil {
			_, err = pool.Exec(ctx, `UPDATE system_settings SET value = $2, value_type = $3, description = $4, is_sensitive = $5, updated_at = now() WHERE id = $1`, id, setting.Value, valueType, setting.Description, setting.IsSensitive)
		}
		if err != nil {
			return fmt.Errorf("upsert setting %s: %w", key, err)
		}
	}
	return nil
}

func normalizePermission(module, action string) (string, string) {
	module = strings.TrimSpace(module)
	action = strings.TrimSpace(action)
	if module == "" && action != "" {
		parts := strings.SplitN(action, ":", 2)
		if len(parts) == 2 {
			module = parts[0]
			action = parts[1]
		}
	}
	if module == "" {
		module = strings.TrimSpace(strings.SplitN(action, ":", 2)[0])
	}
	if action == "" && strings.Contains(module, ":") {
		parts := strings.SplitN(module, ":", 2)
		module = strings.TrimSpace(parts[0])
		action = strings.TrimSpace(parts[1])
	}
	return module, action
}

func normalizePermissionSpec(value string) (string, string) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", ""
	}
	if v == "*" {
		return "*", "*"
	}
	if strings.Contains(v, ":") {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 {
			module := strings.TrimSpace(parts[0])
			action := strings.TrimSpace(parts[1])
			if module == "" || action == "" {
				return "", ""
			}
			return module, action
		}
	}
	if strings.Contains(v, "/") {
		parts := strings.SplitN(v, "/", 2)
		if len(parts) == 2 {
			module := strings.TrimSpace(parts[0])
			action := strings.TrimSpace(parts[1])
			if module != "" && action != "" {
				return module, action
			}
		}
	}
	return "", ""
}

func seedSuperAdmin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, orgID uuid.UUID, admin SuperAdminSeed) error {
	if strings.Contains(admin.Email, "${") || strings.TrimSpace(admin.Email) == "" {
		admin.Email = cfg.SuperAdmin.Email
	}
	if strings.Contains(admin.FirstName, "${") || strings.TrimSpace(admin.FirstName) == "" {
		admin.FirstName = admin.Profile.FirstName
	}
	if strings.Contains(admin.FirstName, "${") || strings.TrimSpace(admin.FirstName) == "" {
		admin.FirstName = cfg.SuperAdmin.FirstName
	}
	if strings.Contains(admin.LastName, "${") || strings.TrimSpace(admin.LastName) == "" {
		admin.LastName = admin.Profile.LastName
	}
	if strings.Contains(admin.LastName, "${") || strings.TrimSpace(admin.LastName) == "" {
		admin.LastName = cfg.SuperAdmin.LastName
	}
	if strings.TrimSpace(admin.Email) == "" {
		return fmt.Errorf("super admin email is empty")
	}
	if strings.TrimSpace(cfg.SuperAdmin.InitialPassword) == "" {
		return fmt.Errorf("SUPER_ADMIN_INITIAL_PASSWORD is not set")
	}

	var existingID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL`, strings.ToLower(admin.Email)).Scan(&existingID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	passwordHash, err := hashPassword(cfg.SuperAdmin.InitialPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	username := strings.ToLower(strings.TrimSpace(admin.Username))
	if username == "" || strings.Contains(username, "${") {
		username = strings.ToLower(strings.Split(admin.Email, "@")[0])
	}
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (
			organization_id, email, username, employee_code, phone, password_hash, status, stage,
			must_change_password, failed_attempts, mfa_enabled, joining_date, employment_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, 0, FALSE, $9, $10)
		RETURNING id`, orgID, strings.ToLower(admin.Email), username, admin.EmployeeCode, admin.Phone, passwordHash,
		valueOrDefault(admin.Status, "active"), valueOrDefault(admin.Stage, "verified"), admin.JoiningDate, admin.EmploymentType).Scan(&userID); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	if _, err = pool.Exec(ctx, `
		INSERT INTO user_profiles (user_id, first_name, last_name, display_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO NOTHING`, userID, strings.TrimSpace(admin.FirstName), strings.TrimSpace(admin.LastName), strings.TrimSpace(admin.FirstName)+" "+strings.TrimSpace(admin.LastName)); err != nil {
		return fmt.Errorf("insert user profile: %w", err)
	}

	var roleID uuid.UUID
	roleName := valueOrDefault(admin.Role, "SUPER_ADMIN")
	if err := pool.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1 AND organization_id IS NULL AND deleted_at IS NULL`, roleName).Scan(&roleID); err != nil {
		return fmt.Errorf("read super admin role: %w", err)
	}

	if _, err = pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, $2
		WHERE NOT EXISTS (
			SELECT 1 FROM user_roles WHERE user_id = $1 AND role_id = $2
		)`, userID, roleID); err != nil {
		return fmt.Errorf("assign super admin role: %w", err)
	}

	return nil
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func hashPassword(plain string) (string, error) {
	const (
		memory  = 64 * 1024
		time    = 3
		threads = 2
		keyLen  = 32
	)
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(plain), salt[:], time, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt[:]),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}
