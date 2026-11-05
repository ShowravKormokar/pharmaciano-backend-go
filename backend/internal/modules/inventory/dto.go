package inventory

import (
	"strings"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// ListBatchesQuery contains only client-controlled filters. Tenant identity
// and effective branch scope are resolved from the authenticated context.
type ListBatchesQuery struct {
	pagination.CursorQuery
	BranchID    *uuid.UUID      `form:"branch_id"`
	WarehouseID *uuid.UUID      `form:"warehouse_id"`
	VariantID   *uuid.UUID      `form:"variant_id"`
	Status      *BatchStatus    `form:"status"`
	Condition   *StockCondition `form:"condition"`
	Query       string          `form:"q"`
}

func (q *ListBatchesQuery) NormalizeAndValidate() error {
	if q == nil {
		return ErrInvalidBatchQuery
	}
	q.CursorQuery.NormalizeWithMax(100)
	q.Query = strings.TrimSpace(q.Query)
	if len(q.Query) > 80 || q.Sort != "" {
		return ErrInvalidBatchQuery
	}
	if q.Status != nil && !validBatchStatus(*q.Status) {
		return ErrInvalidBatchQuery
	}
	if q.Condition != nil && !validStockCondition(*q.Condition) {
		return ErrInvalidBatchQuery
	}
	if q.BranchID != nil && *q.BranchID == uuid.Nil ||
		q.WarehouseID != nil && *q.WarehouseID == uuid.Nil ||
		q.VariantID != nil && *q.VariantID == uuid.Nil {
		return ErrInvalidBatchQuery
	}
	return nil
}

// BatchListItem is a safe read projection; it deliberately excludes cost data.
type BatchListItem struct {
	ID                uuid.UUID            `json:"id"`
	VariantID         uuid.UUID            `json:"variant_id"`
	LotNumber         string               `json:"lot_number"`
	ExpiryDate        string               `json:"expiry_date"`
	ExpiryPrecision   BatchExpiryPrecision `json:"expiry_precision"`
	RecallStatus      BatchRecallStatus    `json:"recall_status"`
	Status            BatchStatus          `json:"status"`
	SellableOnHand    Quantity             `json:"sellable_on_hand_quantity"`
	ActiveReserved    Quantity             `json:"active_reserved_quantity"`
	AvailableForSale  Quantity             `json:"available_quantity"`
	NonSellableOnHand Quantity             `json:"non_sellable_on_hand_quantity"`
}

// BatchListResponse is a bounded cursor page.
type BatchListResponse struct {
	Items      []BatchListItem `json:"items"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type batchListFilter struct {
	BranchID    *uuid.UUID
	BranchScope []uuid.UUID
	WarehouseID *uuid.UUID
	VariantID   *uuid.UUID
	Status      *BatchStatus
	Condition   *StockCondition
	Query       string
}

func validBatchStatus(status BatchStatus) bool {
	switch status {
	case BatchStatusActive, BatchStatusInactive, BatchStatusExhausted, BatchStatusRecalled:
		return true
	default:
		return false
	}
}

func validStockCondition(condition StockCondition) bool {
	switch condition {
	case StockConditionSellable, StockConditionQuarantined, StockConditionDamaged,
		StockConditionExpired, StockConditionRecalled, StockConditionInTransit:
		return true
	default:
		return false
	}
}
