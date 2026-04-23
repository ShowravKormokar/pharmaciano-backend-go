package customer

import (
	"context"
	"time"
)

// Customer represents the core customer domain entity
type Customer struct {
	ID        string
	Name      string
	Email     string
	Phone     string
	Address   string
	City      string
	State     string
	ZipCode   string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ICustomerRepository defines the interface for customer data access
type ICustomerRepository interface {
	Create(ctx context.Context, customer *Customer) error
	FindByID(ctx context.Context, id string) (*Customer, error)
	FindByEmail(ctx context.Context, email string) (*Customer, error)
	Update(ctx context.Context, customer *Customer) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Customer, error)
}

// ICustomerService defines the interface for customer business logic
type ICustomerService interface {
	RegisterCustomer(ctx context.Context, customer *Customer) error
	GetCustomer(ctx context.Context, id string) (*Customer, error)
	UpdateCustomer(ctx context.Context, customer *Customer) error
	DeleteCustomer(ctx context.Context, id string) error
	ListCustomers(ctx context.Context, limit, offset int) ([]*Customer, error)
	SearchCustomers(ctx context.Context, query string) ([]*Customer, error)
}
