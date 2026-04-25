package repository

import (
	"context"
	"time"

	"backend/internal/database"
	"backend/internal/models"

	"gorm.io/gorm"
)

type UserRepo struct{}

func (r *UserRepo) FindByID(ctx context.Context, id string) (*models.User, error) {
	var user models.User
	err := database.DB.WithContext(ctx).
		Preload("Role").
		First(&user, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &user, err
}

func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	err := database.DB.WithContext(ctx).Preload("Role").Where("email = ?", email).First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &user, err
}

func (r *UserRepo) UpdateLoginTime(ctx context.Context, userID string) error {
	now := time.Now()
	return database.DB.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).Update("last_login_at", now).Error
}
