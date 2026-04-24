package user

import (
	"context"

	"backend/internal/models"
)

type Repository interface {
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	UpdateLoginTime(ctx context.Context, userID string) error
	Create(ctx context.Context, user *models.User) error
}
