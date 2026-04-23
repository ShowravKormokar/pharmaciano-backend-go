package user

import "time"

// User represents the core user domain entity
type User struct {
	ID           string
	Email        string
	PasswordHash string
	FirstName    string
	LastName     string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateUserRequest represents the request to create a new user
type CreateUserRequest struct {
	Email     string
	Password  string
	FirstName string
	LastName  string
}

// UpdateUserRequest represents the request to update a user
type UpdateUserRequest struct {
	Email     string
	FirstName string
	LastName  string
}
