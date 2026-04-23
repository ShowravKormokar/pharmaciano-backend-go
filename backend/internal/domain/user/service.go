package user

import "context"

// IUserService defines the interface for user business logic
type IUserService interface {
	Register(ctx context.Context, req *CreateUserRequest) (*User, error)
	GetUser(ctx context.Context, id string) (*User, error)
	UpdateUser(ctx context.Context, id string, req *UpdateUserRequest) (*User, error)
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, limit, offset int) ([]*User, error)
}
