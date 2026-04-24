package user

import (
	"backend/internal/errors"
	"context"
	"net/http"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/dto"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Authenticate performs login for normal users (not Super_Admin)
func (s *Service) Authenticate(ctx context.Context, req dto.LoginRequest) (*dto.LoginResponse, error) {
	user, err := s.repo.FindByEmail(ctx, req.Email)
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

	// Update last login (non-blocking)
	go s.repo.UpdateLoginTime(context.Background(), user.ID.String())

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
