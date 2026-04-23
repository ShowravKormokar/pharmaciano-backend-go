package sale

import (
	"context"
	"time"
)

// Sale represents the core sale domain entity
type Sale struct {
	ID          string
	CustomerID  string
	TotalAmount float64
	Status      string
	Items       []*SaleItem
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SaleItem represents a sale item
type SaleItem struct {
	ID         string
	SaleID     string
	MedicineID string
	Quantity   int
	Price      float64
	Subtotal   float64
}

// ISaleRepository defines the interface for sale data access
type ISaleRepository interface {
	Create(ctx context.Context, sale *Sale) error
	FindByID(ctx context.Context, id string) (*Sale, error)
	Update(ctx context.Context, sale *Sale) error
	Delete(ctx context.Context, id string) error
	ListByCustomer(ctx context.Context, customerID string, limit, offset int) ([]*Sale, error)
	List(ctx context.Context, limit, offset int) ([]*Sale, error)
}

// ISaleService defines the interface for sale business logic
type ISaleService interface {
	CreateSale(ctx context.Context, sale *Sale) error
	GetSale(ctx context.Context, id string) (*Sale, error)
	UpdateSale(ctx context.Context, sale *Sale) error
	DeleteSale(ctx context.Context, id string) error
	ListSales(ctx context.Context, limit, offset int) ([]*Sale, error)
}
