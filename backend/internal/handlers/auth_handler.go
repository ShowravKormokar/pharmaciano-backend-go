package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/dto"
	"backend/internal/errors"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/utils"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AuthHandler struct {
	svc *services.AuthService
}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{svc: services.NewAuthService()}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid request", err.Error())
		return
	}

	// Compute device fingerprint for this login attempt
	deviceFp := auth.GenerateDeviceFingerprint(c.ClientIP(), c.Request.UserAgent(), config.Cfg.JWT.Secret)

	resp, err := h.svc.Login(c.Request.Context(), req, deviceFp)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			response.Error(c, appErr.Code, appErr.Message, nil)
		} else {
			response.Error(c, http.StatusInternalServerError, "login failed", nil)
		}
		return
	}

	// If we want to use HTTP-only secure cookies (for web frontend)
	if config.Cfg.TokenStrategy == "cookie" {
		utils.SetAuthCookies(c.Writer, resp.AccessToken, resp.RefreshToken)

		resp.AccessToken = ""
		resp.RefreshToken = ""
	}

	go logAudit(uuid.MustParse(resp.User.ID), "LOGIN", "auth", c.ClientIP(), c.Request.UserAgent(), "login")
	response.SuccessAuth(c, http.StatusOK, "login successful", resp)
}

func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req dto.RefreshRequest
	// support body or cookie
	if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, "invalid request", err.Error())
			return
		}
	} else {
		cookie, err := c.Cookie("refresh_token")
		if err != nil {
			response.Error(c, http.StatusBadRequest, "refresh token missing", nil)
			return
		}
		req.RefreshToken = cookie
	}

	// Compute device fingerprint (must match the one embedded in the old refresh token)
	deviceFp := auth.GenerateDeviceFingerprint(c.ClientIP(), c.Request.UserAgent(), config.Cfg.JWT.Secret)

	result, err := h.svc.RefreshToken(c.Request.Context(), req.RefreshToken, deviceFp)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "invalid refresh token", nil)
		return
	}

	parts := strings.SplitN(result, "::", 2)
	if len(parts) != 2 {
		response.Error(c, http.StatusInternalServerError, "token processing error", nil)
		return
	}
	newAccess, newRefresh := parts[0], parts[1]

	// If using cookies, set new access/refresh cookies
	if config.Cfg.TokenStrategy == "cookie" {
		utils.SetAuthCookies(c.Writer, newAccess, newRefresh)
		response.Success(c, http.StatusOK, "token refreshed", nil)
		return
	}

	response.Success(c, http.StatusOK, "token refreshed", gin.H{
		"access_token":  newAccess,
		"refresh_token": newRefresh,
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	// Try to get token from cookie first, then from header
	tokenString := ""
	if cookie, err := c.Cookie("access_token"); err == nil {
		tokenString = cookie
	} else {
		tokenString = c.GetString("access_token") // set by JWTAuth middleware
	}

	if tokenString == "" {
		response.Error(c, http.StatusBadRequest, "no token to revoke", nil)
		return
	}

	if err := h.svc.Logout(c.Request.Context(), tokenString); err != nil {
		response.Error(c, http.StatusInternalServerError, "logout failed", nil)
		return
	}

	// Clear cookies if using cookie strategy
	if config.Cfg.TokenStrategy == "cookie" {
		utils.ClearAuthCookies(c.Writer)
	}

	response.Success(c, http.StatusOK, "logged out successfully", nil)
}

func logAudit(userID uuid.UUID, action, module, ip, userAgent, details string) {
	event := map[string]interface{}{
		"event":   action,
		"status":  "SUCCESS",
		"method":  "password",
		"ip":      ip,
		"device":  userAgent,
		"details": details,
	}
	jsonDetails, _ := json.Marshal(event)
	audit := models.AuditLog{
		UserID:    userID,
		Action:    action,
		Module:    module,
		IP:        ip,
		UserAgent: userAgent,
		Details:   string(jsonDetails),
	}
	database.DB.Create(&audit)
}
