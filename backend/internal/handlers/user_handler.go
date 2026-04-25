package handlers

import (
	"net/http"

	"backend/internal/cache"
	"backend/internal/database"
	"backend/internal/dto"
	"backend/internal/models"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func GetMe(c *gin.Context) {
	userID := c.GetString("user_id")
	var user models.User
	err := database.DB.
		Preload("Organization").
		Preload("Branch").
		Preload("Role").
		First(&user, "id = ?", userID).Error
	if err != nil {
		response.Error(c, http.StatusNotFound, "user not found", nil)
		return
	}

	// Load permissions from cache or DB
	perms, err := cache.GetRolePermissions(c.Request.Context(), user.Role.Name)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "failed to load permissions", nil)
		return
	}

	// Build response DTO
	resp := buildUserProfile(&user, perms)
	response.Success(c, http.StatusOK, "user fetched", resp)
}

func buildUserProfile(u *models.User, perms []dto.PermissionBrief) *dto.UserProfileResponse {
	resp := &dto.UserProfileResponse{
		ID:          u.ID.String(),
		Name:        u.Name,
		Email:       u.Email,
		Phone:       u.Phone,
		Status:      u.Status,
		JoiningDate: u.JoiningDate,
		LastLoginAt: u.LastLoginAt,
	}
	if u.Organization.ID != uuid.Nil {
		resp.Organization = &dto.OrganizationBrief{
			ID:      u.Organization.ID.String(),
			Name:    u.Organization.Name,
			City:    u.Organization.City,
			Country: u.Organization.Country,
		}
	}
	if u.Branch != nil && u.Branch.ID != uuid.Nil {
		resp.Branch = &dto.BranchBrief{
			ID:      u.Branch.ID.String(),
			Name:    u.Branch.Name,
			Address: u.Branch.Address,
		}
	}
	resp.Role = &dto.RoleWithPermissionsBrief{
		ID:          u.Role.ID.String(),
		Name:        u.Role.Name,
		Description: u.Role.Description,
		Permissions: perms,
	}
	return resp
}
