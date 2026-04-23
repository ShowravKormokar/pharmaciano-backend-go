package handlers

import (
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/database"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetUsers returns all users with their Role, Organization, and Branch preloaded
func GetUsers(c *gin.Context) {
	var users []models.User
	if err := database.DB.Preload("Role").Preload("Organization").Preload("Branch").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
}

// CreateUserRequest includes all fields from models.User except those auto-generated
type CreateUserRequest struct {
	OrganizationID   uuid.UUID  `json:"organization_id" binding:"required"`
	BranchID         *uuid.UUID `json:"branch_id"` // optional
	RoleID           uuid.UUID  `json:"role_id" binding:"required"`
	Name             string     `json:"name" binding:"required"`
	Email            string     `json:"email" binding:"required,email"`
	Phone            string     `json:"phone"`
	Password         string     `json:"password" binding:"required,min=6"`
	Status           string     `json:"status"`       // default "active"
	JoiningDate      *time.Time `json:"joining_date"` // default now
	NID              string     `json:"nid"`
	PresentAddress   string     `json:"present_address"`
	PermanentAddress string     `json:"permanent_address"`
	EducationalBG    string     `json:"educational_background"`
}

func CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Hash password
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	// Set defaults
	status := req.Status
	if status == "" {
		status = "active"
	}
	joiningDate := req.JoiningDate
	if joiningDate == nil {
		now := time.Now()
		joiningDate = &now
	}

	user := models.User{
		OrganizationID:   req.OrganizationID,
		BranchID:         req.BranchID,
		RoleID:           req.RoleID,
		Name:             req.Name,
		Email:            req.Email,
		Phone:            req.Phone,
		PasswordHash:     hashedPassword,
		Status:           status,
		JoiningDate:      *joiningDate,
		NID:              req.NID,
		PresentAddress:   req.PresentAddress,
		PermanentAddress: req.PermanentAddress,
		EducationalBG:    req.EducationalBG,
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload with relations for response
	database.DB.Preload("Role").Preload("Organization").Preload("Branch").First(&user, user.ID)
	c.JSON(http.StatusCreated, user)
}

func GetUserByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	var user models.User
	if err := database.DB.Preload("Role").Preload("Organization").Preload("Branch").First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func UpdateUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	var user models.User
	if err := database.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If password is being updated, hash it
	if newPassword, ok := payload["password"].(string); ok {
		hashedPassword, err := auth.HashPassword(newPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		payload["password_hash"] = hashedPassword
		delete(payload, "password")
	}

	// Prevent updates to sensitive or primary fields
	delete(payload, "id")
	delete(payload, "created_at")
	delete(payload, "updated_at")
	delete(payload, "deleted_at")
	delete(payload, "organization_id") // organization cannot be changed after creation
	delete(payload, "email")           // email change should go through a separate verification flow; optional but safe to block
	delete(payload, "password_hash")   // already handled above

	// If branch_id is sent as null or valid UUID, allow it (but type assert carefully)
	if branchIDRaw, exists := payload["branch_id"]; exists {
		if branchIDRaw == nil {
			payload["branch_id"] = nil
		} else if branchIDStr, ok := branchIDRaw.(string); ok {
			if branchIDStr == "" {
				payload["branch_id"] = nil
			} else if parsed, err := uuid.Parse(branchIDStr); err == nil {
				payload["branch_id"] = parsed
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid branch_id UUID"})
				return
			}
		}
	}

	if err := database.DB.Model(&user).Updates(payload).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload updated user with relations
	database.DB.Preload("Role").Preload("Organization").Preload("Branch").First(&user, user.ID)
	c.JSON(http.StatusOK, user)
}

// DeleteUser performs soft delete (since User has DeletedAt gorm.DeletedAt)
func DeleteUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	result := database.DB.Delete(&models.User{}, id)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}
