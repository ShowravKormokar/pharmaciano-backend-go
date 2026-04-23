package inventory

import (
	"context"
	"time"
)

// Inventory represents the core inventory domain entity
type Inventory struct {
	ID             string
	MedicineID     string
	WarehouseID    string
	Quantity       int
	ReorderLevel   int
	ReorderQty     int
	LastStockLevel int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// InventoryBatch represents a batch of medicines
type InventoryBatch struct {
	ID              string
	MedicineID      string
	BatchNumber     string
	Quantity        int
	ExpiryDate      time.Time
	ManufactureDate time.Time
	CreatedAt       time.Time
}

// IInventoryRepository defines the interface for inventory data access
type IInventoryRepository interface {
	Create(ctx context.Context, inventory *Inventory) error
	FindByID(ctx context.Context, id string) (*Inventory, error)
	FindByMedicineAndWarehouse(ctx context.Context, medicineID, warehouseID string) (*Inventory, error)
	Update(ctx context.Context, inventory *Inventory) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Inventory, error)
	GetLowStockItems(ctx context.Context) ([]*Inventory, error)
}

// IInventoryService defines the interface for inventory business logic
type IInventoryService interface {
	AddStock(ctx context.Context, medicineID, warehouseID string, quantity int) error
	RemoveStock(ctx context.Context, medicineID, warehouseID string, quantity int) error
	GetInventory(ctx context.Context, id string) (*Inventory, error)
	ListInventory(ctx context.Context, limit, offset int) ([]*Inventory, error)
	CheckLowStock(ctx context.Context) ([]*Inventory, error)
}
