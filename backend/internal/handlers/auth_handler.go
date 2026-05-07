package handlers

import (
	"encoding/json"
	"fmt"
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
	"github.com/mssola/useragent"
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

	// DEBUG: [auth_handler.go] Login - start
	// fmt.Printf("[auth_handler.go] Login: email=%s, ip=%s\n", req.Email, utils.GetClientIP(c))

	deviceFp := auth.GenerateDeviceFingerprint(utils.GetClientIP(c), c.Request.UserAgent(), config.Cfg.JWT.Secret)
	resp, err := h.svc.Login(c.Request.Context(), req, deviceFp, utils.GetClientIP(c), c.Request.UserAgent())
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			// DEBUG: [auth_handler.go] Login - app error
			// fmt.Printf("[auth_handler.go] Login: error - %s (code %d)\n", appErr.Message, appErr.Code)
			response.Error(c, appErr.Code, appErr.Message, nil)
		} else {
			// DEBUG: [auth_handler.go] Login - internal error
			// fmt.Printf("[auth_handler.go] Login: internal error - %v\n", err)
			response.Error(c, http.StatusInternalServerError, "login failed", nil)
		}
		return
	}

	// DEBUG: [auth_handler.go] Login - success, tokens generated
	// fmt.Printf("[auth_handler.go] Login: success - userID=%s, role=%s, accessToken len=%d, refreshToken len=%d\n", resp.User.ID, resp.User.Role, len(resp.AccessToken), len(resp.RefreshToken))

	if config.Cfg.TokenStrategy == "cookie" {
		utils.SetAuthCookies(c.Writer, resp.AccessToken, resp.RefreshToken)
		// DEBUG: [auth_handler.go] Login - cookies set
		// fmt.Printf("[auth_handler.go] Login: set httpOnly cookies (access+refresh)\n")
		resp.AccessToken = ""
		resp.RefreshToken = ""
	}

	go logAudit(uuid.MustParse(resp.User.ID), "LOGIN", "auth", utils.GetClientIP(c), c.Request.UserAgent(), "login")
	response.SuccessAuth(c, http.StatusOK, "login successful", resp)
}

func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req dto.RefreshRequest

	// FIRST try cookie
	cookie, err := c.Cookie("refresh_token")
	if err == nil && cookie != "" {
		req.RefreshToken = cookie
		// fmt.Printf("[auth_handler.go] RefreshToken: token from cookie\n")
	} else {
		// fallback to JSON body
		if err := c.ShouldBindJSON(&req); err != nil {
			// fmt.Printf("[auth_handler.go] RefreshToken: no cookie and invalid body\n")
			response.Error(c, http.StatusBadRequest, "refresh token missing", nil)
			return
		}
	}

	// validate token exists
	if req.RefreshToken == "" {
		// fmt.Printf("[auth_handler.go] RefreshToken: empty refresh token\n")
		response.Error(c, http.StatusBadRequest, "refresh token missing", nil)
		return
	}

	// fmt.Printf("[auth_handler.go] RefreshToken: token received len=%d\n", len(req.RefreshToken))

	deviceFp := auth.GenerateDeviceFingerprint(
		utils.GetClientIP(c),
		c.Request.UserAgent(),
		config.Cfg.JWT.Secret,
	)

	result, err := h.svc.RefreshToken(
		c.Request.Context(),
		req.RefreshToken,
		deviceFp,
	)

	if err != nil {
		// fmt.Printf("[auth_handler.go] RefreshToken: error=%v\n", err)

		// clear bad cookies
		utils.ClearAuthCookies(c.Writer)

		response.Error(c, http.StatusUnauthorized, err.Error(), nil)
		return
	}

	parts := strings.SplitN(result, "::", 2)
	if len(parts) != 2 {
		response.Error(c, http.StatusInternalServerError, "token processing error", nil)
		return
	}

	newAccess := parts[0]
	newRefresh := parts[1]

	// set fresh cookies
	utils.SetAuthCookies(c.Writer, newAccess, newRefresh)

	// fmt.Printf("[auth_handler.go] RefreshToken: cookies updated\n")

	response.Success(c, http.StatusOK, "token refreshed", nil)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	tokenString := ""
	if cookie, err := c.Cookie("access_token"); err == nil {
		tokenString = cookie
	} else {
		tokenString = c.GetString("access_token")
	}

	// DEBUG: [auth_handler.go] Logout - start
	// fmt.Printf("[auth_handler.go] Logout: token len=%d\n", len(tokenString))

	if tokenString == "" {
		// DEBUG: [auth_handler.go] Logout - no token
		// fmt.Printf("[auth_handler.go] Logout: no token to revoke\n")
		response.Error(c, http.StatusBadRequest, "no token to revoke", nil)
		return
	}

	if err := h.svc.Logout(c.Request.Context(), tokenString); err != nil {
		// DEBUG: [auth_handler.go] Logout - error
		// fmt.Printf("[auth_handler.go] Logout: error - %v\n", err)
		response.Error(c, http.StatusInternalServerError, "logout failed", nil)
		return
	}

	if config.Cfg.TokenStrategy == "cookie" {
		utils.ClearAuthCookies(c.Writer)
		// DEBUG: [auth_handler.go] Logout - cookies cleared
		// fmt.Printf("[auth_handler.go] Logout: cleared cookies\n")
	}
	response.Success(c, http.StatusOK, "logged out successfully", nil)
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID := c.GetString("user_id")

	// DEBUG: [auth_handler.go] LogoutAll - start
	// fmt.Printf("[auth_handler.go] LogoutAll: userID=%s\n", userID)

	if err := h.svc.LogoutAll(c.Request.Context(), userID); err != nil {
		// DEBUG: [auth_handler.go] LogoutAll - error
		// fmt.Printf("[auth_handler.go] LogoutAll: error - %v\n", err)
		response.Error(c, http.StatusInternalServerError, "logout all failed", nil)
		return
	}
	utils.ClearAuthCookies(c.Writer)
	response.Success(c, http.StatusOK, "logged out from all devices", nil)
}

func (h *AuthHandler) ActiveSessions(c *gin.Context) {
	userID := c.GetString("user_id")

	// DEBUG: [auth_handler.go] ActiveSessions
	// fmt.Printf("[auth_handler.go] ActiveSessions: userID=%s\n", userID)

	sessions, err := h.svc.GetActiveSessions(c.Request.Context(), userID)
	if err != nil {
		// DEBUG: [auth_handler.go] ActiveSessions - error
		// fmt.Printf("[auth_handler.go] ActiveSessions: error - %v\n", err)
		response.Error(c, http.StatusInternalServerError, "could not fetch sessions", nil)
		return
	}

	// DEBUG: [auth_handler.go] ActiveSessions - result
	// fmt.Printf("[auth_handler.go] ActiveSessions: %d sessions returned\n", len(sessions))

	response.Success(c, http.StatusOK, "active sessions", sessions)
}

func (h *AuthHandler) RevokeSession(c *gin.Context) {
	userID := c.GetString("user_id")
	sessionID := c.Param("session_id")

	// DEBUG: [auth_handler.go] RevokeSession
	// fmt.Printf("[auth_handler.go] RevokeSession: userID=%s, sessionID=%s\n", userID, sessionID)

	if err := h.svc.RevokeSession(c.Request.Context(), userID, sessionID); err != nil {
		// DEBUG: [auth_handler.go] RevokeSession - error
		// fmt.Printf("[auth_handler.go] RevokeSession: error - %v\n", err)
		response.Error(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	response.Success(c, http.StatusOK, "session revoked", nil)
}

func (h *AuthHandler) LoginHistory(c *gin.Context) {
	userID := c.GetString("user_id")

	// DEBUG: [auth_handler.go] LoginHistory
	// fmt.Printf("[auth_handler.go] LoginHistory: userID=%s\n", userID)

	history, err := h.svc.GetLoginHistory(c.Request.Context(), userID)
	if err != nil {
		// DEBUG: [auth_handler.go] LoginHistory - error
		// fmt.Printf("[auth_handler.go] LoginHistory: error - %v\n", err)
		response.Error(c, http.StatusInternalServerError, "failed to get history", nil)
		return
	}

	// DEBUG: [auth_handler.go] LoginHistory - result
	// fmt.Printf("[auth_handler.go] LoginHistory: %d entries\n", len(history))

	response.Success(c, http.StatusOK, "login history", history)
}

func (h *AuthHandler) SecurityStatus(c *gin.Context) {
	userID := c.GetString("user_id")

	// DEBUG: [auth_handler.go] SecurityStatus
	// fmt.Printf("[auth_handler.go] SecurityStatus: userID=%s\n", userID)

	alerts, _ := h.svc.GetSecurityAlerts(c, userID)
	risk, _ := h.svc.GetRiskStatus(c, userID)

	// DEBUG: [auth_handler.go] SecurityStatus - data
	// fmt.Printf("[auth_handler.go] SecurityStatus: risk=%s, alerts=%v\n", risk, alerts)

	response.Success(c, http.StatusOK, "security status", gin.H{
		"alerts": alerts,
		"risk":   risk,
	})
}

func logAudit(userID uuid.UUID, action, module, ip, userAgent, details string) {

	ua := useragent.New(userAgent)

	browser, _ := ua.Browser()

	device := fmt.Sprintf(
		"%s on %s",
		browser,
		ua.OS(),
	)

	location := utils.GetGeoLocation(ip)

	event := map[string]interface{}{
		"event":    action,
		"status":   "SUCCESS",
		"method":   "password",
		"ip":       ip,
		"browser":  browser,
		"os":       ua.OS(),
		"device":   device,
		"location": location,
		"details":  details,
	}

	jsonDetails, _ := json.Marshal(event)

	audit := models.AuditLog{
		UserID:    userID,
		Action:    action,
		Module:    module,
		IP:        ip,
		Browser:   browser,
		OS:        ua.OS(),
		Device:    device,
		Location:  location,
		UserAgent: userAgent,
		Details:   string(jsonDetails),
	}

	database.DB.Create(&audit)
}
