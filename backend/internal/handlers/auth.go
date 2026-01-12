package handlers

import (
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func Me(c *gin.Context) {
	c.JSON(200, gin.H{
		"user_id": c.GetUint("user_id"),
		"role":    c.GetString("role"),
	})
}

func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := database.DB.Preload("Role").
		Where("email = ?", req.Email).
		First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	accessToken, _ := auth.GenerateAccessToken(
		user.ID,
		user.Role.Name,
		config.Cfg.JWT.JWTSecret,
		time.Minute*time.Duration(config.Cfg.JWT.JWTAccessTTL),
	)

	refreshToken, _ := auth.GenerateRefreshToken(
		user.ID,
		config.Cfg.JWT.JWTSecret,
		time.Hour*time.Duration(config.Cfg.JWT.JWTRefreshTTL),
	)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}
