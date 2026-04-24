package handlers

import (
	"net/http"

	"backend/internal/database"
	"backend/internal/models"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
)

func GetMe(c *gin.Context) {
	userID := c.GetString("user_id")

	var user models.User
	// Preload Organization, Branch, Role, and Role's Permissions
	if err := database.DB.
		Preload("Organization").
		Preload("Branch").
		Preload("Role.Permissions").
		First(&user, "id = ?", userID).Error; err != nil {
		response.Error(c, http.StatusNotFound, "user not found", nil)
		return
	}

	response.Success(c, http.StatusOK, "user fetched", user)
}
