package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/dto"
	"backend/internal/errors"
	"backend/internal/models"
	"backend/internal/repository"

	"github.com/google/uuid"
	"github.com/mssola/useragent"
)

type AuthService struct {
	userRepo repository.UserRepo
}

func NewAuthService() *AuthService {
	return &AuthService{userRepo: repository.UserRepo{}}
}

func (s *AuthService) Login(ctx context.Context, req dto.LoginRequest, deviceFingerprint string, ip, userAgent string) (*dto.LoginResponse, error) {
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil || user == nil {
		return nil, errors.ErrInvalidCredentials
	}

	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		return nil, errors.NewAppError(http.StatusLocked, "account temporarily locked due to multiple failed attempts", nil)
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		go s.handleFailedLogin(context.Background(), user)
		return nil, errors.ErrInvalidCredentials
	}

	if user.Status != "active" {
		return nil, errors.ErrInactiveAccount
	}

	go s.handleSuccessfulLogin(context.Background(), user)

	role := user.Role.Name
	if user.Email == config.Cfg.Super.Email {
		role = "Super_Admin"
	}

	sessionID := uuid.New().String()

	// Device naming using useragent package
	ua := useragent.New(userAgent)
	browser, _ := ua.Browser() // e.g. "Chrome", "Firefox"
	deviceName := fmt.Sprintf("%s on %s", browser, ua.OS())

	location := getGeoLocation(ip)

	session := models.Session{
		ID:         sessionID,
		UserID:     user.ID.String(),
		DeviceName: deviceName,
		DeviceFp:   deviceFingerprint,
		IP:         ip,
		Location:   location,
		UserAgent:  userAgent,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Duration(config.Cfg.JWT.RefreshTTL) * time.Minute),
	}

	sessionJSON, _ := json.Marshal(session)
	cache.RDB.Set(ctx, cache.SessionKey(sessionID), sessionJSON, time.Until(session.ExpiresAt))
	cache.RDB.SAdd(ctx, cache.UserSessionsKey(user.ID.String()), sessionID)

	s.checkIPAnomaly(ctx, user.ID.String(), ip)

	accessToken, exp, err := auth.GenerateAccessToken(user.ID, role, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL, deviceFingerprint, sessionID)
	if err != nil {
		return nil, errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	refreshToken, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL, deviceFingerprint, sessionID)
	if err != nil {
		return nil, errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	go s.userRepo.UpdateLoginTime(context.Background(), user.ID.String())

	return &dto.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    exp,
		User: dto.UserBrief{
			ID:    user.ID.String(),
			Name:  user.Name,
			Email: user.Email,
			Role:  role,
		},
	}, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string, deviceFingerprint string) (string, error) {
	claims, err := auth.ValidateToken(refreshToken, config.Cfg.JWT.Secret)
	if err != nil {
		return "", errors.ErrTokenValidation
	}

	if claims.DeviceFingerprint != "" && claims.DeviceFingerprint != deviceFingerprint {
		return "", errors.NewAppError(http.StatusUnauthorized, "device mismatch", nil)
	}

	// Check if session exists (it might have been revoked)
	if claims.SessionID != "" {
		exists, _ := cache.RDB.Exists(ctx, cache.SessionKey(claims.SessionID)).Result()
		if exists == 0 {
			return "", errors.NewAppError(http.StatusUnauthorized, "session expired or revoked", nil)
		}
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return "", errors.ErrTokenValidation
	}

	user, err := s.userRepo.FindByID(ctx, userID.String())
	if err != nil || user == nil {
		return "", errors.ErrInvalidCredentials
	}
	if user.Status != "active" {
		return "", errors.ErrInactiveAccount
	}

	role := user.Role.Name
	if user.Email == config.Cfg.Super.Email {
		role = "Super_Admin"
	}

	newAccess, _, err := auth.GenerateAccessToken(user.ID, role, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL, deviceFingerprint, claims.SessionID)
	if err != nil {
		return "", errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	// Rotate refresh token
	newRefreshToken, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL, deviceFingerprint, claims.SessionID)
	if err != nil {
		return "", errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	// Blacklist old refresh token
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining > 0 {
		key := cache.TokenBlacklistKey(claims.ID)
		cache.RDB.Set(ctx, key, "true", remaining)
	}

	// Extend session TTL (sliding session)
	if claims.SessionID != "" {
		cache.RDB.Expire(ctx, cache.SessionKey(claims.SessionID), time.Duration(config.Cfg.JWT.RefreshTTL)*time.Minute)
	}

	return newAccess + "::" + newRefreshToken, nil
}

func (s *AuthService) Logout(ctx context.Context, tokenString string) error {
	claims, err := auth.ValidateToken(tokenString, config.Cfg.JWT.Secret)
	if err != nil {
		return err
	}

	// Revoke current session
	if claims.SessionID != "" {
		cache.RDB.Del(ctx, cache.SessionKey(claims.SessionID))
		cache.RDB.SRem(ctx, cache.UserSessionsKey(claims.UserID.String()), claims.SessionID)
	}

	// Blacklist access token
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining > 0 {
		key := cache.TokenBlacklistKey(claims.ID)
		return cache.RDB.Set(ctx, key, "true", remaining).Err()
	}
	return nil
}

func (s *AuthService) LogoutAll(ctx context.Context, userID string) error {
	sessions, _ := cache.RDB.SMembers(ctx, cache.UserSessionsKey(userID)).Result()
	for _, sid := range sessions {
		cache.RDB.Del(ctx, cache.SessionKey(sid))
	}
	cache.RDB.Del(ctx, cache.UserSessionsKey(userID))
	return nil
}

func (s *AuthService) GetLoginHistory(ctx context.Context, userID string) ([]string, error) {
	key := fmt.Sprintf("login_history:%s", userID)
	return cache.RDB.LRange(ctx, key, 0, -1).Result()
}

// internal helper: check IP anomaly and log warning
func (s *AuthService) checkIPAnomaly(ctx context.Context, userID, currentIP string) {
	lastIP, err := cache.RDB.Get(ctx, "last_ip:"+userID).Result()
	if err == nil && lastIP != "" && lastIP != currentIP {
		// Could trigger alert, for now just log (Zap logger available)
		// logger.Log.Warn("IP change detected",
		// 	zap.String("user_id", userID),
		// 	zap.String("old_ip", lastIP),
		// 	zap.String("new_ip", currentIP))
	}
	// Update last IP
	cache.RDB.Set(ctx, "last_ip:"+userID, currentIP, 24*time.Hour)

	// Add login history entry
	entry := fmt.Sprintf("%s - IP: %s - TS: %s", time.Now().Format(time.RFC3339), currentIP, time.Now().String())
	key := fmt.Sprintf("login_history:%s", userID)
	cache.RDB.LPush(ctx, key, entry)
	cache.RDB.LTrim(ctx, key, 0, 9) // keep last 10
}

func (s *AuthService) handleFailedLogin(ctx context.Context, user *models.User) {
	newAttempts := user.FailedAttempts + 1
	updates := map[string]interface{}{"failed_attempts": newAttempts}
	if newAttempts >= 5 {
		lockTime := time.Now().Add(15 * time.Minute)
		updates["locked_until"] = lockTime
	}
	database.DB.Model(user).Updates(updates)
}

func (s *AuthService) handleSuccessfulLogin(ctx context.Context, user *models.User) {
	database.DB.Model(user).Updates(map[string]interface{}{
		"failed_attempts": 0,
		"locked_until":    nil,
	})
}

// Simple geo-location using ip-api.com (free, no key)
func getGeoLocation(ip string) string {
	if ip == "" || ip == "::1" || ip == "127.0.0.1" {
		return "localhost"
	}
	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=city,country", ip)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()
	var result struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.City == "" {
		return "unknown"
	}
	return fmt.Sprintf("%s, %s", result.City, result.Country)
}
