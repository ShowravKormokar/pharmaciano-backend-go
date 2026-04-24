package services

import (
	"context"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/dto"
	"backend/internal/errors"
	"backend/internal/repository"

	"github.com/google/uuid"
)

type AuthService struct {
	userRepo repository.UserRepo
}

func NewAuthService() *AuthService {
	return &AuthService{userRepo: repository.UserRepo{}}
}

func (s *AuthService) Login(ctx context.Context, req dto.LoginRequest) (*dto.LoginResponse, error) {
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil || user == nil {
		return nil, errors.ErrInvalidCredentials
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		return nil, errors.ErrInvalidCredentials
	}
	if user.Status != "active" {
		return nil, errors.ErrInactiveAccount
	}

	accessToken, exp, err := auth.GenerateAccessToken(user.ID, user.Role.Name, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL)
	if err != nil {
		return nil, errors.NewAppError(http.StatusInternalServerError, "token generation failed", err)
	}
	refreshToken, err := auth.GenerateRefreshToken(user.ID, config.Cfg.JWT.Secret, config.Cfg.JWT.RefreshTTL)
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
			Role:  user.Role.Name,
		},
	}, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (string, error) {
	claims, err := auth.ValidateToken(refreshToken, config.Cfg.JWT.Secret)
	if err != nil {
		return "", errors.ErrTokenValidation
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return "", errors.ErrTokenValidation
	}
	newAccess, _, err := auth.GenerateAccessToken(userID, claims.Role, config.Cfg.JWT.Secret, config.Cfg.JWT.AccessTTL)
	return newAccess, err
}

func (s *AuthService) Logout(ctx context.Context, tokenString string) error {
	claims, err := auth.ValidateToken(tokenString, config.Cfg.JWT.Secret)
	if err != nil {
		return err
	}
	// Blacklist the token's JTI until its expiry
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		key := cache.TokenBlacklistKey(claims.ID)
		cache.RDB.Set(ctx, key, "true", ttl)
	}
	return nil
}
