package handlers

import (
	"net/http"

	"backend/internal/auth"
	"backend/internal/config"

	"github.com/gin-gonic/gin"
)

func Login(c *gin.Context) {
	// demo user (later DB)
	userID := uint(1)
	role := "admin"

	token, err := auth.GenerateAccessToken(
		userID,
		role,
		config.Cfg.JWT.Secret,
		config.Cfg.JWT.AccessTTL,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to generate token",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
	})
}
