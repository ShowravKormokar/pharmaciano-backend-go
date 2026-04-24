package handlers

import (
	"net/http"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/dto"
	"backend/internal/errors"
	"backend/internal/models"
	"backend/internal/services"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AuthHandler struct {
	svc *services.AuthService
}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{
		svc: services.NewAuthService(),
	}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid request", err.Error())
		return
	}

	// Super_Admin check
	if req.Email == config.Cfg.Super.Email {
		if req.Password != config.Cfg.Super.Password {
			response.Error(c, http.StatusUnauthorized, "invalid credentials", nil)
			return
		}
		accessToken, exp, _ := auth.GenerateAccessToken(uuid.Nil, "Super_Admin", config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL)
		refreshToken, _ := auth.GenerateRefreshToken(uuid.Nil, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL)
		resp := dto.LoginResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    exp,
			User: dto.UserBrief{
				ID:    uuid.Nil.String(),
				Name:  "Super Admin",
				Email: config.Cfg.Super.Email,
				Role:  "Super_Admin",
			},
		}
		go logAudit(uuid.Nil, "LOGIN", "auth", c.ClientIP(), c.Request.UserAgent(), "super_admin_login")
		response.SuccessAuth(c, http.StatusOK, "login successful", resp)
		return
	}

	resp, err := h.svc.Login(c.Request.Context(), req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			response.Error(c, appErr.Code, appErr.Message, nil)
		} else {
			response.Error(c, http.StatusInternalServerError, "login failed", nil)
		}
		return
	}

	go logAudit(uuid.MustParse(resp.User.ID), "LOGIN", "auth", c.ClientIP(), c.Request.UserAgent(), "user_login")
	response.SuccessAuth(c, http.StatusOK, "login successful", resp)
}

func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req dto.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid request", err.Error())
		return
	}
	newAccess, err := h.svc.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "invalid refresh token", nil)
		return
	}
	response.Success(c, http.StatusOK, "token refreshed", gin.H{"access_token": newAccess})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	tokenString := c.GetString("access_token") // set by JWTAuth middleware
	if err := h.svc.Logout(c.Request.Context(), tokenString); err != nil {
		response.Error(c, http.StatusInternalServerError, "logout failed", nil)
		return
	}
	response.Success(c, http.StatusOK, "logged out successfully", nil)
}

func logAudit(userID uuid.UUID, action, module, ip, userAgent, details string) {
	audit := models.AuditLog{
		UserID:    userID,
		Action:    action,
		Module:    module,
		IP:        ip,
		UserAgent: userAgent,
		Details:   details,
	}
	database.DB.Create(&audit)
}
