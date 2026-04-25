package services

import (
	"context"
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
)

type AuthService struct {
	userRepo repository.UserRepo
}

func NewAuthService() *AuthService {
	return &AuthService{userRepo: repository.UserRepo{}}
}

func (s *AuthService) Login(ctx context.Context, req dto.LoginRequest, deviceFingerprint string) (*dto.LoginResponse, error) {
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil || user == nil {
		return nil, errors.ErrInvalidCredentials
	}

	// Check if account is locked
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

	accessToken, exp, err := auth.GenerateAccessToken(user.ID, role, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL, deviceFingerprint)
	if err != nil {
		return nil, errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}
	refreshToken, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL, deviceFingerprint)
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

	// Device binding check – refresh token must match current device
	if claims.DeviceFingerprint != "" && claims.DeviceFingerprint != deviceFingerprint {
		return "", errors.NewAppError(http.StatusUnauthorized, "device mismatch", nil)
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

	newAccess, _, err := auth.GenerateAccessToken(user.ID, role, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL, deviceFingerprint)
	if err != nil {
		return "", errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	newRefreshToken, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL, deviceFingerprint)
	if err != nil {
		return "", errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}

	// Blacklist the old refresh token
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining > 0 {
		key := cache.TokenBlacklistKey(claims.ID)
		cache.RDB.Set(ctx, key, "true", remaining)
	}

	return newAccess + "::" + newRefreshToken, nil
}

func (s *AuthService) Logout(ctx context.Context, tokenString string) error {
	claims, err := auth.ValidateToken(tokenString, config.Cfg.JWT.Secret)
	if err != nil {
		return err
	}
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining > 0 {
		key := cache.TokenBlacklistKey(claims.ID)
		return cache.RDB.Set(ctx, key, "true", remaining).Err()
	}
	return nil
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
