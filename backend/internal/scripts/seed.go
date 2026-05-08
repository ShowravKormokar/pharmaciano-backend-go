package scripts

import (
	"log"
	"os"
	"time"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/models"
	"backend/internal/rbac"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func SeedAll() {
	seedPermissions()
	seedRoles()
	seedSuperAdmin()
	seedRolePermissions()
	syncCasbinPolicies()
}

func seedPermissions() {
	perms := []struct{ Module, Action string }{
		{"users", "read"}, {"users", "create"}, {"users", "update"}, {"users", "delete"}, {"users", "manage"},
		{"inventory", "read"}, {"inventory", "create"}, {"inventory", "update"}, {"inventory", "delete"}, {"inventory", "manage"},
		{"sales", "read"}, {"sales", "create"}, {"sales", "update"}, {"sales", "delete"}, {"sales", "manage"},
		{"reports", "read"}, {"reports", "generate"},
	}

	for _, p := range perms {
		var existing models.Permission
		err := database.DB.Where("module = ? AND action = ?", p.Module, p.Action).First(&existing).Error

		if err == gorm.ErrRecordNotFound {
			database.DB.Create(&models.Permission{
				Module: p.Module,
				Action: p.Action,
			})
		}
	}

	log.Println("✅ Permissions seeded")
}

func seedRoles() {
	// Standard roles
	createRoleIfNotExists("admin", "Full access administrator", true)
	createRoleIfNotExists("viewer", "Read-only access", true)
	// Super_Admin role (critical)
	createRoleIfNotExists("Super_Admin", "System super administrator", true)
	log.Println("✅ Roles seeded")
}

func createRoleIfNotExists(name, desc string, isSystem bool) {
	var existing models.Role
	err := database.DB.Where("name = ?", name).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		database.DB.Create(&models.Role{
			Name:        name,
			Description: desc,
			IsSystem:    isSystem,
		})
	}
}

func seedSuperAdmin() {
	// Use the email from .env; password also from .env (only for initial creation)
	email := config.Cfg.Super.Email
	password := os.Getenv("SUPER_ADMIN_PASSWORD") // fallback if config didn't set it

	var existingUser models.User
	err := database.DB.Where("email = ?", email).First(&existingUser).Error
	if err == nil {
		// User exists, ensure role is Super_Admin
		var superRole models.Role
		database.DB.Where("name = ?", "Super_Admin").First(&superRole)
		if existingUser.RoleID != superRole.ID {
			existingUser.RoleID = superRole.ID
			database.DB.Save(&existingUser)
		}
		return
	}

	if err != gorm.ErrRecordNotFound {
		log.Fatal("❌ Failed to check existing super admin:", err)
	}

	// Create Super Admin user
	var superRole models.Role
	database.DB.Where("name = ?", "Super_Admin").First(&superRole)

	hashed, err := auth.HashPassword(password)
	if err != nil {
		log.Fatal("❌ Failed to hash super admin password:", err)
	}

	user := models.User{
		OrganizationID: uuid.Nil, // no org
		BranchID:       nil,
		RoleID:         superRole.ID,
		Name:           "Super Admin",
		Email:          email,
		PasswordHash:   hashed,
		Status:         "active",
		JoiningDate:    time.Now(),
	}
	database.DB.Create(&user)
	log.Println("✅ Super Admin user created with email:", email)
}

func seedRolePermissions() {
	var perms []models.Permission
	database.DB.Find(&perms)

	var admin models.Role
	database.DB.Where("name = ?", "admin").First(&admin)

	var viewer models.Role
	database.DB.Where("name = ?", "viewer").First(&viewer)

	// Admin gets ALL permissions
	for _, p := range perms {
		database.DB.Exec(`
			INSERT INTO role_permissions (role_id, permission_id)
			VALUES (?, ?)
			ON CONFLICT DO NOTHING
		`, admin.ID, p.ID)
	}

	// Viewer gets READ only
	for _, p := range perms {
		if p.Action == "read" {
			database.DB.Exec(`
				INSERT INTO role_permissions (role_id, permission_id)
				VALUES (?, ?)
				ON CONFLICT DO NOTHING
			`, viewer.ID, p.ID)
		}
	}

	log.Println("✅ Role-Permissions mapped")
}

func syncCasbinPolicies() {
	// Instead of clearing all policies, we'll load existing and add only missing ones.
	// This prevents table drop/recreate because the adapter already handles table creation.
	type Result struct {
		Role   string
		Module string
		Action string
	}
	var results []Result
	database.DB.Raw(`
		SELECT r.name as role, p.module, p.action
		FROM roles r
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN permissions p ON p.id = rp.permission_id
	`).Scan(&results)

	for _, r := range results {
		// AddPolicy now checks HasPolicy so duplicates are safe
		rbac.AddPolicy(r.Role, r.Module, r.Action)
	}
	// Explicitly add Super_Admin policies (all modules, all actions)
	// We could just add all unique module:action pairs from permissions for Super_Admin
	var perms []models.Permission
	database.DB.Find(&perms)
	for _, p := range perms {
		rbac.AddPolicy("Super_Admin", p.Module, p.Action)
	}

	rbac.SavePolicies()
	log.Println("✅ Casbin policies synced")
}
