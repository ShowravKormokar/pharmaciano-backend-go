package scripts

import (
	"backend/internal/auth"
	"backend/internal/database"
	"backend/internal/models"
	"log"
)

func InitializeAdmin() {
	// Check if admin already exists
	var count int64
	database.DB.Model(&models.User{}).Where("email = ?", "admin@pharmaciano.com").Count(&count)
	if count > 0 {
		log.Println("✅ Admin user already exists")
		return
	}

	// Check if admin role exists, create if not
	var adminRole models.Role
	if err := database.DB.Where("name = ?", "admin").First(&adminRole).Error; err != nil {
		// Create admin role
		adminRole = models.Role{
			Name:        "admin",
			Description: "Administrator with full access",
		}
		if err := database.DB.Create(&adminRole).Error; err != nil {
			log.Printf("❌ Failed to create admin role: %v", err)
			return
		}
		log.Println("✅ Created admin role")
	}

	// Check if organization exists, create if not
	var organization models.Organization
	if err := database.DB.First(&organization).Error; err != nil {
		organization = models.Organization{
			Name:              "Main Pharmacy",
			TradeLicenseNo:    "DEMO-001",
			DrugLicenseNo:     "DRUG-DEMO-001",
			VATRegistrationNo: "VAT-DEMO-001",
			SubscriptionPlan:  "premium",
			IsActive:          true,
			ContactPhone:      "+8801712345678",
			ContactEmail:      "info@pharmaciano.com",
		}
		if err := database.DB.Create(&organization).Error; err != nil {
			log.Printf("❌ Failed to create organization: %v", err)
			return
		}
		log.Println("✅ Created default organization")
	}

	// Create admin user
	hashedPassword, err := auth.HashPassword("admin123")
	if err != nil {
		log.Printf("❌ Password hashing error: %v", err)
		return
	}

	adminUser := models.User{
		Name:           "System Admin",
		Email:          "admin@pharmaciano.com",
		PasswordHash:   hashedPassword,
		RoleID:         adminRole.ID,
		OrganizationID: organization.ID,
	}

	if err := database.DB.Create(&adminUser).Error; err != nil {
		log.Printf("❌ Failed to create admin user: %v", err)
		return
	}

	log.Println("\n=========================================")
	log.Println("✅ ADMIN USER CREATED SUCCESSFULLY!")
	log.Println("=========================================")
	log.Printf("📧 Email: %s", adminUser.Email)
	log.Printf("🔑 Password: admin123")
	log.Printf("👤 Role: %s", adminRole.Name)
	log.Printf("🏢 Organization: %s", organization.Name)
	log.Println("=========================================")
	log.Println("⚠️  IMPORTANT: Change the default password!")
	log.Println("=========================================")
}
