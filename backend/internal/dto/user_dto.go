package dto

import "time"

// UserResponse represents the user response DTO
type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateUserRequest represents the create user request DTO
type CreateUserRequest struct {
	Email     string `json:"email" binding:"required,email"`
	Password  string `json:"password" binding:"required,min=6"`
	FirstName string `json:"first_name" binding:"required"`
	LastName  string `json:"last_name" binding:"required"`
}

// UpdateUserRequest represents the update user request DTO
type UpdateUserRequest struct {
	Email     string `json:"email" binding:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// LoginRequest represents the login request DTO
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse represents the login response DTO
type LoginResponse struct {
	User        *UserResponse `json:"user"`
	AccessToken string        `json:"access_token"`
	ExpiresAt   time.Time     `json:"expires_at"`
}
