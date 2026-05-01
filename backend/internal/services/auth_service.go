package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/dto"
	"backend/internal/errors"
	"backend/internal/models"
	"backend/internal/repository"
	"backend/internal/security"

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
		return nil, errors.NewAppError(http.StatusLocked, "account temporarily locked", nil)
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

	// Device naming
	ua := useragent.New(userAgent)
	browser, _ := ua.Browser()
	deviceName := fmt.Sprintf("%s on %s", browser, ua.OS())

	location := getGeoLocation(ip)

	// Optional geo‑blocking (example)
	if strings.Contains(location, "Unknown") {
		return nil, errors.NewAppError(http.StatusForbidden, "login blocked from unknown region", nil)
	}

	session := models.Session{
		ID:         sessionID,
		UserID:     user.ID.String(),
		DeviceName: deviceName,
		DeviceFp:   deviceFingerprint,
		IP:         ip,
		Location:   location,
		UserAgent:  userAgent,
		LastSeen:   time.Now(),
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Duration(config.Cfg.JWT.RefreshTTL) * time.Minute),
	}

	sessionJSON, _ := json.Marshal(session)
	if err := cache.RDB.Set(ctx, cache.SessionKey(sessionID), sessionJSON, time.Until(session.ExpiresAt)).Err(); err != nil {
		return nil, errors.NewAppError(http.StatusInternalServerError, "failed to store session", err)
	}
	cache.RDB.SAdd(ctx, cache.UserSessionsKey(user.ID.String()), sessionID)

	// IP anomaly detection
	s.checkIPAnomaly(ctx, user.ID.String(), ip)

	// Risk calculation (example)
	riskInput := security.RiskInput{
		IPChanged: false, // would need historical data
		NewDevice: false, // can be checked by comparing fingerprint
	}
	if score := security.CalculateRisk(riskInput); score > 70 {
		return nil, errors.NewAppError(http.StatusForbidden, "high risk login blocked", nil)
	}

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

	// 🔥 Refresh token reuse detection
	used, _ := cache.RDB.Exists(ctx, cache.RefreshUsedKey(claims.ID)).Result()
	if used > 0 {
		go s.handleTokenReuseAttack(ctx, claims.UserID.String(), claims.SessionID)
		return "", errors.NewAppError(http.StatusUnauthorized, "token reuse detected", nil)
	}

	if claims.DeviceFingerprint != deviceFingerprint {
		return "", errors.NewAppError(http.StatusUnauthorized, "device mismatch", nil)
	}

	// Session existence check
	if claims.SessionID != "" {
		exists, _ := cache.RDB.Exists(ctx, cache.SessionKey(claims.SessionID)).Result()
		if exists == 0 {
			return "", errors.NewAppError(http.StatusUnauthorized, "session expired or revoked", nil)
		}
	}

	userID, _ := uuid.Parse(claims.Subject)
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

	newRefresh, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL, deviceFingerprint, claims.SessionID)
	if err != nil {
		return "", errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	// Mark old refresh token as used (reuse detection)
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		cache.RDB.Set(ctx, cache.RefreshUsedKey(claims.ID), "1", ttl)
		// Also blacklist as before (defense-in-depth)
		cache.RDB.Set(ctx, cache.TokenBlacklistKey(claims.ID), "1", ttl)
	}

	// Extend session TTL
	if claims.SessionID != "" {
		cache.RDB.Expire(ctx, cache.SessionKey(claims.SessionID), time.Duration(config.Cfg.JWT.RefreshTTL)*time.Minute)
	}

	return newAccess + "::" + newRefresh, nil
}

func (s *AuthService) Logout(ctx context.Context, tokenString string) error {
	claims, err := auth.ValidateToken(tokenString, config.Cfg.JWT.Secret)
	if err != nil {
		return err
	}

	if claims.SessionID != "" {
		cache.RDB.Del(ctx, cache.SessionKey(claims.SessionID))
		cache.RDB.SRem(ctx, cache.UserSessionsKey(claims.UserID.String()), claims.SessionID)
	}

	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		return cache.RDB.Set(ctx, cache.TokenBlacklistKey(claims.ID), "1", ttl).Err()
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

func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID string) error {
	data, err := cache.RDB.Get(ctx, cache.SessionKey(sessionID)).Result()
	if err != nil {
		return errors.NewAppError(http.StatusNotFound, "session not found", nil)
	}
	var sess models.Session
	json.Unmarshal([]byte(data), &sess)
	if sess.UserID != userID {
		return errors.NewAppError(http.StatusForbidden, "not allowed", nil)
	}
	cache.RDB.Del(ctx, cache.SessionKey(sessionID))
	cache.RDB.SRem(ctx, cache.UserSessionsKey(userID), sessionID)
	return nil
}

func (s *AuthService) GetActiveSessions(ctx context.Context, userID string) ([]models.Session, error) {
	sessionIDs, _ := cache.RDB.SMembers(ctx, cache.UserSessionsKey(userID)).Result()
	var sessions []models.Session
	for _, sid := range sessionIDs {
		data, err := cache.RDB.Get(ctx, cache.SessionKey(sid)).Result()
		if err != nil {
			continue
		}
		var sess models.Session
		if json.Unmarshal([]byte(data), &sess) == nil {
			sessions = append(sessions, sess)
		}
	}
	return sessions, nil
}

type LoginHistoryEntry struct {
	Timestamp string `json:"timestamp"`
	IP        string `json:"ip"`
	Device    string `json:"device"`
	Location  string `json:"location"`
}

func (s *AuthService) GetLoginHistory(ctx context.Context, userID string) ([]LoginHistoryEntry, error) {
	raw, _ := cache.RDB.LRange(ctx, "login_history:"+userID, 0, -1).Result()
	var history []LoginHistoryEntry
	for _, item := range raw {
		var entry LoginHistoryEntry
		if json.Unmarshal([]byte(item), &entry) == nil {
			history = append(history, entry)
		}
	}
	return history, nil
}

func (s *AuthService) GetSecurityAlerts(ctx context.Context, userID string) (map[string]string, error) {
	alerts := make(map[string]string)
	val, _ := cache.RDB.Get(ctx, "security_alert:"+userID).Result()
	if val != "" {
		alerts["alert"] = val
	}
	return alerts, nil
}

func (s *AuthService) GetRiskStatus(ctx context.Context, userID string) (string, error) {
	return cache.RDB.Get(ctx, "risk:"+userID).Result()
}

// ---------- Helpers ----------

func (s *AuthService) handleTokenReuseAttack(ctx context.Context, userID, sessionID string) {
	_ = s.LogoutAll(ctx, userID)
	cache.RDB.Set(ctx, "security_alert:"+userID, "refresh_reuse_attack", time.Hour*24)
}

func (s *AuthService) checkIPAnomaly(ctx context.Context, userID, currentIP string) {
	lastIP, _ := cache.RDB.Get(ctx, "last_ip:"+userID).Result()
	if lastIP != "" && lastIP != currentIP {
		cache.RDB.Set(ctx, "risk:"+userID, "ip_changed", time.Hour)
	}
	cache.RDB.Set(ctx, "last_ip:"+userID, currentIP, 24*time.Hour)

	entry := LoginHistoryEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		IP:        currentIP,
		Location:  getGeoLocation(currentIP),
	}
	data, _ := json.Marshal(entry)
	cache.RDB.LPush(ctx, "login_history:"+userID, data)
	cache.RDB.LTrim(ctx, "login_history:"+userID, 0, 9)
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
