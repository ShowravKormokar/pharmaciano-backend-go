package dto

import "time"

// SaleResponse represents the sale response DTO
type SaleResponse struct {
	ID          string        `json:"id"`
	CustomerID  string        `json:"customer_id"`
	TotalAmount float64       `json:"total_amount"`
	Status      string        `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	SaleItems   []SaleItemDTO `json:"sale_items,omitempty"`
}

// SaleItemDTO represents a sale item DTO
type SaleItemDTO struct {
	ID         string  `json:"id"`
	MedicineID string  `json:"medicine_id"`
	Quantity   int     `json:"quantity"`
	Price      float64 `json:"price"`
	Subtotal   float64 `json:"subtotal"`
}

// CreateSaleRequest represents the create sale request DTO
type CreateSaleRequest struct {
	CustomerID string            `json:"customer_id" binding:"required"`
	Items      []SaleItemRequest `json:"items" binding:"required,dive"`
}

// SaleItemRequest represents a sale item request DTO
type SaleItemRequest struct {
	MedicineID string  `json:"medicine_id" binding:"required"`
	Quantity   int     `json:"quantity" binding:"required,gt=0"`
	Price      float64 `json:"price" binding:"required,gt=0"`
}
