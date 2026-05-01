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

	deviceFp := auth.GenerateDeviceFingerprint(c.ClientIP(), c.Request.UserAgent(), config.Cfg.JWT.Secret)
	resp, err := h.svc.Login(c.Request.Context(), req, deviceFp, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			response.Error(c, appErr.Code, appErr.Message, nil)
		} else {
			response.Error(c, http.StatusInternalServerError, "login failed", nil)
		}
		return
	}

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

	deviceFp := auth.GenerateDeviceFingerprint(c.ClientIP(), c.Request.UserAgent(), config.Cfg.JWT.Secret)

	result, err := h.svc.RefreshToken(c.Request.Context(), req.RefreshToken, deviceFp)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, err.Error(), nil)
		return
	}

	parts := strings.SplitN(result, "::", 2)
	if len(parts) != 2 {
		response.Error(c, http.StatusInternalServerError, "token processing error", nil)
		return
	}
	newAccess, newRefresh := parts[0], parts[1]

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
	tokenString := ""
	if cookie, err := c.Cookie("access_token"); err == nil {
		tokenString = cookie
	} else {
		tokenString = c.GetString("access_token")
	}

	if tokenString == "" {
		response.Error(c, http.StatusBadRequest, "no token to revoke", nil)
		return
	}

	if err := h.svc.Logout(c.Request.Context(), tokenString); err != nil {
		response.Error(c, http.StatusInternalServerError, "logout failed", nil)
		return
	}

	if config.Cfg.TokenStrategy == "cookie" {
		utils.ClearAuthCookies(c.Writer)
	}
	response.Success(c, http.StatusOK, "logged out successfully", nil)
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.svc.LogoutAll(c.Request.Context(), userID); err != nil {
		response.Error(c, http.StatusInternalServerError, "logout all failed", nil)
		return
	}
	utils.ClearAuthCookies(c.Writer)
	response.Success(c, http.StatusOK, "logged out from all devices", nil)
}

func (h *AuthHandler) ActiveSessions(c *gin.Context) {
	userID := c.GetString("user_id")
	sessions, err := h.svc.GetActiveSessions(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "could not fetch sessions", nil)
		return
	}
	response.Success(c, http.StatusOK, "active sessions", sessions)
}

func (h *AuthHandler) RevokeSession(c *gin.Context) {
	userID := c.GetString("user_id")
	sessionID := c.Param("session_id")
	if err := h.svc.RevokeSession(c.Request.Context(), userID, sessionID); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	response.Success(c, http.StatusOK, "session revoked", nil)
}

func (h *AuthHandler) LoginHistory(c *gin.Context) {
	userID := c.GetString("user_id")
	history, err := h.svc.GetLoginHistory(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "failed to get history", nil)
		return
	}
	response.Success(c, http.StatusOK, "login history", history)
}

func (h *AuthHandler) SecurityStatus(c *gin.Context) {
	userID := c.GetString("user_id")
	alerts, _ := h.svc.GetSecurityAlerts(c, userID)
	risk, _ := h.svc.GetRiskStatus(c, userID)
	response.Success(c, http.StatusOK, "security status", gin.H{
		"alerts": alerts,
		"risk":   risk,
	})
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
