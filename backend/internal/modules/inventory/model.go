package inventory

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// -----------------------------------------------------------------------------
// Quantity and monetary conventions
// -----------------------------------------------------------------------------

// Quantity represents an indivisible quantity in the Medicine module's
// canonical base unit.
//
// IMPORTANT:
//   - Never store package/strip/box quantities directly in inventory balances.
//   - Convert through the Medicine module's authoritative conversion rules.
//   - If fractional base units become necessary, migrate consistently to a
//     fixed-precision numeric representation. Do not use float64.
//
// Quantity is an alias so database drivers can scan integer values directly.
type Quantity = int64

// Monetary amounts use integer minor units and an ISO currency code.
// The currency's minor-unit exponent must follow the shared currency policy.
// Do not use float32 or float64 for financial values.

// -----------------------------------------------------------------------------
// Batch enums
// -----------------------------------------------------------------------------

type BatchExpiryPrecision string

const (
	BatchExpiryPrecisionDay   BatchExpiryPrecision = "day"
	BatchExpiryPrecisionMonth BatchExpiryPrecision = "month"
)

type BatchRecallStatus string

const (
	BatchRecallStatusClear    BatchRecallStatus = "clear"
	BatchRecallStatusRecalled BatchRecallStatus = "recalled"
)

type BatchStatus string

const (
	BatchStatusActive    BatchStatus = "active"
	BatchStatusInactive  BatchStatus = "inactive"
	BatchStatusExhausted BatchStatus = "exhausted"
	BatchStatusRecalled  BatchStatus = "recalled"
)

// -----------------------------------------------------------------------------
// Stock condition and movement enums
// -----------------------------------------------------------------------------

type StockCondition string

const (
	StockConditionSellable    StockCondition = "sellable"
	StockConditionQuarantined StockCondition = "quarantined"
	StockConditionDamaged     StockCondition = "damaged"
	StockConditionExpired     StockCondition = "expired"
	StockConditionRecalled    StockCondition = "recalled"
	StockConditionInTransit   StockCondition = "in_transit"
)

type StockMovementKind string

const (
	StockMovementReceipt            StockMovementKind = "receipt"
	StockMovementSaleConsumption    StockMovementKind = "sale_consumption"
	StockMovementSaleReturn         StockMovementKind = "sale_return"
	StockMovementPurchaseReturn     StockMovementKind = "purchase_return"
	StockMovementAdjustmentIncrease StockMovementKind = "adjustment_increase"
	StockMovementAdjustmentDecrease StockMovementKind = "adjustment_decrease"
	StockMovementDamage             StockMovementKind = "damage"
	StockMovementExpiryWriteOff     StockMovementKind = "expiry_write_off"
	StockMovementTransferDispatch   StockMovementKind = "transfer_dispatch"
	StockMovementTransferReceipt    StockMovementKind = "transfer_receipt"
	StockMovementTransferLoss       StockMovementKind = "transfer_loss"
	StockMovementOpeningBalance     StockMovementKind = "opening_balance"
	StockMovementCorrection         StockMovementKind = "correction"
	StockMovementReversal           StockMovementKind = "reversal"
)

type StockSourceModule string

const (
	StockSourceModulePurchase   StockSourceModule = "purchase"
	StockSourceModuleSale       StockSourceModule = "sale"
	StockSourceModuleInventory  StockSourceModule = "inventory"
	StockSourceModuleAdjustment StockSourceModule = "adjustment"
	StockSourceModuleTransfer   StockSourceModule = "transfer"
	StockSourceModuleStockCount StockSourceModule = "stock_count"
)

// -----------------------------------------------------------------------------
// Reservation enums
// -----------------------------------------------------------------------------

type ReservationStatus string

const (
	ReservationStatusActive    ReservationStatus = "active"
	ReservationStatusConsumed  ReservationStatus = "consumed"
	ReservationStatusReleased  ReservationStatus = "released"
	ReservationStatusExpired   ReservationStatus = "expired"
	ReservationStatusCancelled ReservationStatus = "cancelled"
)

type ReservationOwnerType string

const (
	ReservationOwnerSale     ReservationOwnerType = "sale"
	ReservationOwnerCheckout ReservationOwnerType = "checkout"
	ReservationOwnerOrder    ReservationOwnerType = "order"
)

// -----------------------------------------------------------------------------
// Cost enums
// -----------------------------------------------------------------------------

type CostLayerOrigin string

const (
	CostLayerOriginPurchase       CostLayerOrigin = "purchase"
	CostLayerOriginOpeningBalance CostLayerOrigin = "opening_balance"
	CostLayerOriginAdjustment     CostLayerOrigin = "adjustment"
	CostLayerOriginTransfer       CostLayerOrigin = "transfer"
)

type CostAllocationKind string

const (
	CostAllocationSale           CostAllocationKind = "sale"
	CostAllocationSaleReturn     CostAllocationKind = "sale_return"
	CostAllocationPurchaseReturn CostAllocationKind = "purchase_return"
	CostAllocationWriteOff       CostAllocationKind = "write_off"
	CostAllocationTransfer       CostAllocationKind = "transfer"
	CostAllocationAdjustment     CostAllocationKind = "adjustment"
)

// -----------------------------------------------------------------------------
// Adjustment enums
// -----------------------------------------------------------------------------

type AdjustmentKind string

const (
	AdjustmentKindCountVariance  AdjustmentKind = "count_variance"
	AdjustmentKindDamage         AdjustmentKind = "damage"
	AdjustmentKindExpiryWriteOff AdjustmentKind = "expiry_write_off"
	AdjustmentKindOpeningBalance AdjustmentKind = "opening_balance"
	AdjustmentKindCorrection     AdjustmentKind = "correction"
	AdjustmentKindOtherApproved  AdjustmentKind = "other_approved"
)

type AdjustmentStatus string

const (
	AdjustmentStatusDraft     AdjustmentStatus = "draft"
	AdjustmentStatusSubmitted AdjustmentStatus = "submitted"
	AdjustmentStatusApproved  AdjustmentStatus = "approved"
	AdjustmentStatusRejected  AdjustmentStatus = "rejected"
	AdjustmentStatusPosted    AdjustmentStatus = "posted"
	AdjustmentStatusCancelled AdjustmentStatus = "cancelled"
)

// -----------------------------------------------------------------------------
// Transfer enums
// -----------------------------------------------------------------------------

type TransferStatus string

const (
	TransferStatusDraft             TransferStatus = "draft"
	TransferStatusApproved          TransferStatus = "approved"
	TransferStatusDispatched        TransferStatus = "dispatched"
	TransferStatusPartiallyReceived TransferStatus = "partially_received"
	TransferStatusReceived          TransferStatus = "received"
	TransferStatusCancelled         TransferStatus = "cancelled"
)

type TransferLineStatus string

const (
	TransferLineStatusPending    TransferLineStatus = "pending"
	TransferLineStatusDispatched TransferLineStatus = "dispatched"
	TransferLineStatusPartial    TransferLineStatus = "partially_received"
	TransferLineStatusReceived   TransferLineStatus = "received"
	TransferLineStatusDisputed   TransferLineStatus = "disputed"
	TransferLineStatusResolved   TransferLineStatus = "resolved"
)

// -----------------------------------------------------------------------------
// Recall enums
// -----------------------------------------------------------------------------

type RecallStatus string

const (
	RecallStatusActive RecallStatus = "active"
	RecallStatusClosed RecallStatus = "closed"
)

// -----------------------------------------------------------------------------
// Stock count enums
// -----------------------------------------------------------------------------

type StockCountStatus string

const (
	StockCountStatusDraft     StockCountStatus = "draft"
	StockCountStatusCounting  StockCountStatus = "counting"
	StockCountStatusSubmitted StockCountStatus = "submitted"
	StockCountStatusApproved  StockCountStatus = "approved"
	StockCountStatusPosted    StockCountStatus = "posted"
	StockCountStatusCancelled StockCountStatus = "cancelled"
)

type StockCountCutoffStrategy string

const (
	StockCountCutoffVersionSnapshot StockCountCutoffStrategy = "version_snapshot"
	StockCountCutoffFreeze          StockCountCutoffStrategy = "freeze"
)

// -----------------------------------------------------------------------------
// Batch identity
// -----------------------------------------------------------------------------

// InventoryBatch represents organization-level medicine lot identity.
//
// It deliberately does not contain branch, warehouse, stock quantities,
// reservations or selling prices. Those belong to other inventory entities
// or their owning modules.
type InventoryBatch struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	VariantID      uuid.UUID `db:"variant_id" json:"variant_id"`

	// Lot identity.
	NormalizedLotNo string `db:"normalized_lot_no" json:"normalized_lot_no"`
	DisplayLotNo    string `db:"display_lot_no" json:"display_lot_no"`

	ManufacturerID *uuid.UUID `db:"manufacturer_id" json:"manufacturer_id,omitempty"`

	// Source date precision must be preserved. For month-only expiry, the
	// application must apply the approved pharmacy expiry interpretation.
	ManufacturedOn  *time.Time           `db:"manufactured_on" json:"manufactured_on,omitempty"`
	ExpiresOn       time.Time            `db:"expires_on" json:"expires_on"`
	ExpiryPrecision BatchExpiryPrecision `db:"expiry_precision" json:"expiry_precision"`

	RecallStatus BatchRecallStatus `db:"recall_status" json:"recall_status"`
	Status       BatchStatus       `db:"status" json:"status"`

	// Optional source metadata. Keep bounded and validate at the DTO/service
	// boundary. Do not use this field as a substitute for normalized columns.
	SourceMetadata common.JSONMap `db:"source_metadata" json:"source_metadata,omitempty"`
}

// -----------------------------------------------------------------------------
// Warehouse stock balance
// -----------------------------------------------------------------------------

// InventoryBalance is the current quantity projection for one batch in one
// warehouse and one stock condition.
//
// Unique key:
// (organization_id, warehouse_id, batch_id, condition)
//
// AvailableQuantity is intentionally not persisted. Derive it as:
// on_hand_quantity - reserved_quantity.
//
// All quantity changes must happen in the same transaction as the corresponding
// immutable StockMovement insert.
type InventoryBalance struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	BatchID        uuid.UUID `db:"batch_id" json:"batch_id"`

	Condition StockCondition `db:"condition" json:"condition"`

	OnHandQuantity   Quantity `db:"on_hand_quantity" json:"on_hand_quantity"`
	ReservedQuantity Quantity `db:"reserved_quantity" json:"reserved_quantity"`

	// Incremented on every successful balance mutation.
	// Used for optimistic concurrency and stock-count reconciliation.
	Version int64 `db:"version" json:"version"`
}

// AvailableQuantity returns the derived quantity available for allocation.
// Call only with a balance loaded from the authoritative database.
func (b InventoryBalance) AvailableQuantity() Quantity {
	return b.OnHandQuantity - b.ReservedQuantity
}

// -----------------------------------------------------------------------------
// Immutable stock movement journal
// -----------------------------------------------------------------------------

// StockMovement is an immutable record of a committed stock quantity change.
//
// Do not update or soft-delete posted movements. Corrections must append
// compensating movements referencing the original movement.
type StockMovement struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	BatchID        uuid.UUID `db:"batch_id" json:"batch_id"`

	Condition StockCondition    `db:"condition" json:"condition"`
	Kind      StockMovementKind `db:"movement_kind" json:"movement_kind"`

	// Signed quantity delta. Positive adds stock; negative removes stock.
	QuantityDelta Quantity `db:"quantity_delta" json:"quantity_delta"`

	// Snapshot after this movement has been applied.
	QuantityAfter  Quantity `db:"quantity_after" json:"quantity_after"`
	BalanceVersion int64    `db:"balance_version" json:"balance_version"`

	// Stable operation identity makes retries/replays safe.
	OperationID     uuid.UUID `db:"operation_id" json:"operation_id"`
	OperationLineID uuid.UUID `db:"operation_line_id" json:"operation_line_id"`
	LineNumber      int       `db:"line_number" json:"line_number"`

	SourceModule StockSourceModule `db:"source_module" json:"source_module"`
	SourceType   *string           `db:"source_type" json:"source_type,omitempty"`
	SourceID     *uuid.UUID        `db:"source_id" json:"source_id,omitempty"`

	// Optional value delta in currency minor units. Quantity movements that
	// affect valuation should carry the appropriate financial evidence.
	ValueDeltaMinor *int64  `db:"value_delta_minor" json:"value_delta_minor,omitempty"`
	CurrencyCode    *string `db:"currency_code" json:"currency_code,omitempty"`

	PerformedBy uuid.UUID `db:"performed_by" json:"performed_by"`
	OccurredAt  time.Time `db:"occurred_at" json:"occurred_at"`

	ReversalOfMovementID *uuid.UUID `db:"reversal_of_movement_id" json:"reversal_of_movement_id,omitempty"`
	Reason               *string    `db:"reason" json:"reason,omitempty"`
}

// -----------------------------------------------------------------------------
// Reservations
// -----------------------------------------------------------------------------

// InventoryReservation is a durable stock reservation owned by an operation
// such as a Sale checkout. It is not a Redis lock.
type InventoryReservation struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`

	// Unique within the organization. Used to make reservation creation
	// idempotent across HTTP retries and worker retries.
	OperationID uuid.UUID `db:"operation_id" json:"operation_id"`

	OwnerType ReservationOwnerType `db:"owner_type" json:"owner_type"`
	OwnerID   uuid.UUID            `db:"owner_id" json:"owner_id"`

	Status ReservationStatus `db:"status" json:"status"`

	ExpiresAt   time.Time  `db:"expires_at" json:"expires_at"`
	ConsumedAt  *time.Time `db:"consumed_at" json:"consumed_at,omitempty"`
	ReleasedAt  *time.Time `db:"released_at" json:"released_at,omitempty"`
	CancelledAt *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`

	CreatedBy uuid.UUID `db:"created_by" json:"created_by"`
	Version   int64     `db:"version" json:"version"`
}

// InventoryReservationLine records the batch-level quantity reserved by a
// reservation. Quantities are in canonical base units.
type InventoryReservationLine struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	ReservationID  uuid.UUID `db:"reservation_id" json:"reservation_id"`

	BalanceID  uuid.UUID `db:"balance_id" json:"balance_id"`
	BatchID    uuid.UUID `db:"batch_id" json:"batch_id"`
	LineNumber int       `db:"line_number" json:"line_number"`

	ReservedQuantity Quantity `db:"reserved_quantity" json:"reserved_quantity"`
	ConsumedQuantity Quantity `db:"consumed_quantity" json:"consumed_quantity"`
	ReleasedQuantity Quantity `db:"released_quantity" json:"released_quantity"`

	// Balance version observed when this line was reserved.
	BalanceVersion int64 `db:"balance_version" json:"balance_version"`
}

// RemainingQuantity returns the portion of this reservation line that has
// neither been consumed nor released.
func (l InventoryReservationLine) RemainingQuantity() Quantity {
	return l.ReservedQuantity - l.ConsumedQuantity - l.ReleasedQuantity
}

// -----------------------------------------------------------------------------
// Inventory valuation: cost layers and allocations
// -----------------------------------------------------------------------------

// InventoryCostLayer represents immutable acquisition-cost provenance for
// stock received into a specific warehouse and batch.
//
// The remaining quantity is a transactional projection. It must be protected
// by row locks/guarded updates and reconciled against allocations.
type InventoryCostLayer struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	BatchID        uuid.UUID `db:"batch_id" json:"batch_id"`

	Origin CostLayerOrigin `db:"origin" json:"origin"`

	OriginOperationID     uuid.UUID `db:"origin_operation_id" json:"origin_operation_id"`
	OriginOperationLineID uuid.UUID `db:"origin_operation_line_id" json:"origin_operation_line_id"`

	OriginalQuantity  Quantity `db:"original_quantity" json:"original_quantity"`
	RemainingQuantity Quantity `db:"remaining_quantity" json:"remaining_quantity"`

	// UnitCostMinor is the acquisition cost for one canonical base unit.
	// CurrencyCode and currency exponent follow the shared money policy.
	UnitCostMinor int64  `db:"unit_cost_minor" json:"unit_cost_minor"`
	CurrencyCode  string `db:"currency_code" json:"currency_code"`

	AcquiredAt time.Time `db:"acquired_at" json:"acquired_at"`
}

// InventoryCostAllocation is immutable evidence of how a stock consumption
// was valued against one or more cost layers.
type InventoryCostAllocation struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`

	CostLayerID uuid.UUID `db:"cost_layer_id" json:"cost_layer_id"`
	MovementID  uuid.UUID `db:"movement_id" json:"movement_id"`

	OperationID     uuid.UUID `db:"operation_id" json:"operation_id"`
	OperationLineID uuid.UUID `db:"operation_line_id" json:"operation_line_id"`

	Kind CostAllocationKind `db:"allocation_kind" json:"allocation_kind"`

	AllocatedQuantity Quantity `db:"allocated_quantity" json:"allocated_quantity"`

	// Total allocated value for this allocation, not a floating-point unit cost.
	AllocatedValueMinor int64  `db:"allocated_value_minor" json:"allocated_value_minor"`
	CurrencyCode        string `db:"currency_code" json:"currency_code"`

	AllocatedAt time.Time `db:"allocated_at" json:"allocated_at"`
}

// -----------------------------------------------------------------------------
// Stock adjustments
// -----------------------------------------------------------------------------

// InventoryAdjustment is the workflow aggregate for a controlled stock
// correction, write-off, opening balance or count variance.
type InventoryAdjustment struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`

	OperationID  uuid.UUID `db:"operation_id" json:"operation_id"`
	AdjustmentNo string    `db:"adjustment_no" json:"adjustment_no"`

	Kind   AdjustmentKind   `db:"adjustment_kind" json:"adjustment_kind"`
	Status AdjustmentStatus `db:"status" json:"status"`

	Reason      string  `db:"reason" json:"reason"`
	EvidenceRef *string `db:"evidence_ref" json:"evidence_ref,omitempty"`

	RequestedBy uuid.UUID `db:"requested_by" json:"requested_by"`
	RequestedAt time.Time `db:"requested_at" json:"requested_at"`

	ApprovedBy *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt *time.Time `db:"approved_at" json:"approved_at,omitempty"`

	RejectedBy   *uuid.UUID `db:"rejected_by" json:"rejected_by,omitempty"`
	RejectedAt   *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectReason *string    `db:"reject_reason" json:"reject_reason,omitempty"`

	PostedBy *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt *time.Time `db:"posted_at" json:"posted_at,omitempty"`

	CancelledBy *uuid.UUID `db:"cancelled_by" json:"cancelled_by,omitempty"`
	CancelledAt *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`

	// Optimistic concurrency for draft edits and workflow transitions.
	Version int64 `db:"version" json:"version"`
}

// InventoryAdjustmentLine contains one batch-level quantity adjustment.
// QuantityDelta must be non-zero. Positive adds stock; negative removes stock.
type InventoryAdjustmentLine struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	AdjustmentID   uuid.UUID `db:"adjustment_id" json:"adjustment_id"`

	LineNumber int `db:"line_number" json:"line_number"`

	BatchID   uuid.UUID      `db:"batch_id" json:"batch_id"`
	Condition StockCondition `db:"condition" json:"condition"`

	QuantityDelta Quantity `db:"quantity_delta" json:"quantity_delta"`

	// Used to reject stale adjustment lines if the balance changed after
	// the adjustment was prepared.
	ExpectedBalanceVersion int64 `db:"expected_balance_version" json:"expected_balance_version"`

	Reason      string  `db:"reason" json:"reason"`
	EvidenceRef *string `db:"evidence_ref" json:"evidence_ref,omitempty"`

	// Required when the adjustment changes inventory valuation.
	UnitCostMinor *int64  `db:"unit_cost_minor" json:"unit_cost_minor,omitempty"`
	CurrencyCode  *string `db:"currency_code" json:"currency_code,omitempty"`
}

// -----------------------------------------------------------------------------
// Warehouse transfers
// -----------------------------------------------------------------------------

// WarehouseTransfer moves stock between two warehouses belonging to the same
// organization. Dispatched stock must be represented as in-transit stock until
// destination receipt or approved loss resolution.
type WarehouseTransfer struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	TransferNo     string    `db:"transfer_no" json:"transfer_no"`
	OperationID    uuid.UUID `db:"operation_id" json:"operation_id"`

	FromBranchID    uuid.UUID `db:"from_branch_id" json:"from_branch_id"`
	ToBranchID      uuid.UUID `db:"to_branch_id" json:"to_branch_id"`
	FromWarehouseID uuid.UUID `db:"from_warehouse_id" json:"from_warehouse_id"`
	ToWarehouseID   uuid.UUID `db:"to_warehouse_id" json:"to_warehouse_id"`

	Status TransferStatus `db:"status" json:"status"`

	RequestedBy uuid.UUID `db:"requested_by" json:"requested_by"`
	RequestedAt time.Time `db:"requested_at" json:"requested_at"`

	ApprovedBy *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt *time.Time `db:"approved_at" json:"approved_at,omitempty"`

	DispatchedBy *uuid.UUID `db:"dispatched_by" json:"dispatched_by,omitempty"`
	DispatchedAt *time.Time `db:"dispatched_at" json:"dispatched_at,omitempty"`

	ReceivedBy *uuid.UUID `db:"received_by" json:"received_by,omitempty"`
	ReceivedAt *time.Time `db:"received_at" json:"received_at,omitempty"`

	CancelledBy *uuid.UUID `db:"cancelled_by" json:"cancelled_by,omitempty"`
	CancelledAt *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`

	Notes *string `db:"notes" json:"notes,omitempty"`

	Version int64 `db:"version" json:"version"`
}

// WarehouseTransferItem represents one batch line in a transfer.
//
// RequestedQuantity, DispatchedQuantity, ReceivedQuantity and LostQuantity
// must satisfy:
//
//	0 <= received + lost <= dispatched <= requested
//
// BatchNo and ExpiryDate are intentionally not duplicated. Resolve them
// through BatchID when displaying transfer details.
type WarehouseTransferItem struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	TransferID     uuid.UUID `db:"transfer_id" json:"transfer_id"`

	LineNumber int `db:"line_number" json:"line_number"`

	VariantID uuid.UUID `db:"variant_id" json:"variant_id"`
	BatchID   uuid.UUID `db:"batch_id" json:"batch_id"`

	RequestedQuantity  Quantity `db:"requested_quantity" json:"requested_quantity"`
	DispatchedQuantity Quantity `db:"dispatched_quantity" json:"dispatched_quantity"`
	ReceivedQuantity   Quantity `db:"received_quantity" json:"received_quantity"`
	LostQuantity       Quantity `db:"lost_quantity" json:"lost_quantity"`

	Status TransferLineStatus `db:"status" json:"status"`

	// Snapshot of the source cost provenance used to preserve valuation
	// across dispatch and destination receipt.
	CurrencyCode *string `db:"currency_code" json:"currency_code,omitempty"`

	Notes *string `db:"notes" json:"notes,omitempty"`
}

// -----------------------------------------------------------------------------
// Recalls
// -----------------------------------------------------------------------------

// InventoryRecall blocks allocation of affected stock. A recall does not
// delete stock or movements and does not automatically make stock sellable
// when closed.
type InventoryRecall struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`

	VariantID *uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	BatchID   *uuid.UUID `db:"batch_id" json:"batch_id,omitempty"`

	RecallNo string       `db:"recall_no" json:"recall_no"`
	Status   RecallStatus `db:"status" json:"status"`

	Reason      string  `db:"reason" json:"reason"`
	SourceRef   *string `db:"source_ref" json:"source_ref,omitempty"`
	EvidenceRef *string `db:"evidence_ref" json:"evidence_ref,omitempty"`

	EffectiveAt time.Time `db:"effective_at" json:"effective_at"`

	ActivatedBy uuid.UUID `db:"activated_by" json:"activated_by"`
	ActivatedAt time.Time `db:"activated_at" json:"activated_at"`

	ClosedBy    *uuid.UUID `db:"closed_by" json:"closed_by,omitempty"`
	ClosedAt    *time.Time `db:"closed_at" json:"closed_at,omitempty"`
	CloseReason *string    `db:"close_reason" json:"close_reason,omitempty"`

	Version int64 `db:"version" json:"version"`
}

// -----------------------------------------------------------------------------
// Stock counts
// -----------------------------------------------------------------------------

// InventoryStockCount represents a controlled physical stock-count session.
// Posting must reconcile intervening movements; it must never blindly replace
// live on-hand quantity with a stale observed quantity.
type InventoryStockCount struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`

	CountNo string `db:"count_no" json:"count_no"`

	Status         StockCountStatus         `db:"status" json:"status"`
	CutoffStrategy StockCountCutoffStrategy `db:"cutoff_strategy" json:"cutoff_strategy"`

	OpenedBy uuid.UUID `db:"opened_by" json:"opened_by"`
	OpenedAt time.Time `db:"opened_at" json:"opened_at"`

	SubmittedBy *uuid.UUID `db:"submitted_by" json:"submitted_by,omitempty"`
	SubmittedAt *time.Time `db:"submitted_at" json:"submitted_at,omitempty"`

	ApprovedBy *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt *time.Time `db:"approved_at" json:"approved_at,omitempty"`

	PostedBy *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt *time.Time `db:"posted_at" json:"posted_at,omitempty"`

	CancelledBy *uuid.UUID `db:"cancelled_by" json:"cancelled_by,omitempty"`
	CancelledAt *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`

	Notes *string `db:"notes" json:"notes,omitempty"`

	Version int64 `db:"version" json:"version"`
}

// InventoryStockCountLine captures the expected snapshot and physical
// observation for one batch/condition at the count's logical cutoff.
type InventoryStockCountLine struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	StockCountID   uuid.UUID `db:"stock_count_id" json:"stock_count_id"`

	LineNumber int `db:"line_number" json:"line_number"`

	BalanceID uuid.UUID `db:"balance_id" json:"balance_id"`
	BatchID   uuid.UUID `db:"batch_id" json:"batch_id"`

	Condition StockCondition `db:"condition" json:"condition"`

	ExpectedQuantity Quantity  `db:"expected_quantity" json:"expected_quantity"`
	ObservedQuantity *Quantity `db:"observed_quantity" json:"observed_quantity,omitempty"`

	// Snapshot version at count start. Reconciliation must account for all
	// movements committed after this version and before count cutoff.
	ExpectedBalanceVersion int64 `db:"expected_balance_version" json:"expected_balance_version"`

	ObservedBy *uuid.UUID `db:"observed_by" json:"observed_by,omitempty"`
	ObservedAt *time.Time `db:"observed_at" json:"observed_at,omitempty"`

	Variance Quantity `db:"variance" json:"variance"`

	Reason      *string `db:"reason" json:"reason,omitempty"`
	EvidenceRef *string `db:"evidence_ref" json:"evidence_ref,omitempty"`
}

// -----------------------------------------------------------------------------
// Reorder policies
// -----------------------------------------------------------------------------

// InventoryReorderPolicy defines replenishment thresholds for one variant at
// one warehouse. It is advisory and must not create a Purchase Order itself.
type InventoryReorderPolicy struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	VariantID      uuid.UUID `db:"variant_id" json:"variant_id"`

	MinimumQuantity Quantity `db:"minimum_quantity" json:"minimum_quantity"`
	TargetQuantity  Quantity `db:"target_quantity" json:"target_quantity"`
	SafetyQuantity  Quantity `db:"safety_quantity" json:"safety_quantity"`

	LeadTimeDays int `db:"lead_time_days" json:"lead_time_days"`

	IsActive      bool       `db:"is_active" json:"is_active"`
	EffectiveFrom time.Time  `db:"effective_from" json:"effective_from"`
	EffectiveTo   *time.Time `db:"effective_to" json:"effective_to,omitempty"`

	Version int64 `db:"version" json:"version"`
}
