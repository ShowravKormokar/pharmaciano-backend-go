// Package purchase contains the persistence models and domain vocabulary for
// procurement workflows. Cross-module stock and accounting records remain
// owned by Inventory and Ledger respectively.
package purchase

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// Quantity is expressed in the canonical base unit used by Medicine/Inventory.
// It is integral by contract. If the canonical stock unit supports fractions,
// replace this alias consistently with the shared fixed-precision quantity
// type before creating migrations. Never use float32/float64 for quantities.
type Quantity int64

// AmountMinor is an amount expressed in the currency's smallest accounting
// unit (for example, poisha for BDT). Currency exponent and rounding rules
// must come from the shared money/currency policy. Never use float for money.
type AmountMinor int64

// RateBasisPoints represents a percentage multiplied by 100.
// Example: 15.25% = 1525 basis points.
type RateBasisPoints int32

// -----------------------------------------------------------------------------
// Purchase order lifecycle
// -----------------------------------------------------------------------------

type PurchaseOrderStatus string

const (
	PurchaseOrderStatusDraft           PurchaseOrderStatus = "draft"
	PurchaseOrderStatusPendingApproval PurchaseOrderStatus = "pending_approval"
	PurchaseOrderStatusApproved        PurchaseOrderStatus = "approved"
	PurchaseOrderStatusOrdered         PurchaseOrderStatus = "ordered"
	PurchaseOrderStatusRejected        PurchaseOrderStatus = "rejected"
	PurchaseOrderStatusCancelled       PurchaseOrderStatus = "cancelled"
)

type PurchaseReceiptStatus string

const (
	PurchaseReceiptStatusNotReceived PurchaseReceiptStatus = "not_received"
	PurchaseReceiptStatusPartial     PurchaseReceiptStatus = "partially_received"
	PurchaseReceiptStatusFull        PurchaseReceiptStatus = "fully_received"
)

type PurchaseInvoiceStatus string

const (
	PurchaseInvoiceStatusNotInvoiced PurchaseInvoiceStatus = "not_invoiced"
	PurchaseInvoiceStatusPartial     PurchaseInvoiceStatus = "partially_invoiced"
	PurchaseInvoiceStatusFull        PurchaseInvoiceStatus = "fully_invoiced"
	PurchaseInvoiceStatusDisputed    PurchaseInvoiceStatus = "disputed"
)

type PurchaseClosureStatus string

const (
	PurchaseClosureStatusOpen   PurchaseClosureStatus = "open"
	PurchaseClosureStatusClosed PurchaseClosureStatus = "closed"
)

type PurchaseOrder struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	SupplierID     uuid.UUID `db:"supplier_id" json:"supplier_id"`

	PurchaseNo  string  `db:"purchase_no" json:"purchase_no"`
	ReferenceNo *string `db:"reference_no" json:"reference_no,omitempty"`

	OrderStatus    PurchaseOrderStatus   `db:"order_status" json:"order_status"`
	ReceiptStatus  PurchaseReceiptStatus `db:"receipt_status" json:"receipt_status"`
	InvoiceStatus  PurchaseInvoiceStatus `db:"invoice_status" json:"invoice_status"`
	ClosureStatus  PurchaseClosureStatus `db:"closure_status" json:"closure_status"`
	CurrencyCode   string                `db:"currency_code" json:"currency_code"`
	PolicySnapshot common.JSONMap        `db:"policy_snapshot" json:"policy_snapshot,omitempty"`

	RequestedBy     uuid.UUID  `db:"requested_by" json:"requested_by"`
	SubmittedBy     *uuid.UUID `db:"submitted_by" json:"submitted_by,omitempty"`
	SubmittedAt     *time.Time `db:"submitted_at" json:"submitted_at,omitempty"`
	ApprovedBy      *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt      *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	RejectedBy      *uuid.UUID `db:"rejected_by" json:"rejected_by,omitempty"`
	RejectedAt      *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectionReason *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`

	ExpectedDeliveryAt *time.Time `db:"expected_delivery_at" json:"expected_delivery_at,omitempty"`
	OrderedBy          *uuid.UUID `db:"ordered_by" json:"ordered_by,omitempty"`
	OrderedAt          *time.Time `db:"ordered_at" json:"ordered_at,omitempty"`
	CancelledBy        *uuid.UUID `db:"cancelled_by" json:"cancelled_by,omitempty"`
	CancelledAt        *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`
	CancellationReason *string    `db:"cancellation_reason" json:"cancellation_reason,omitempty"`
	ClosedBy           *uuid.UUID `db:"closed_by" json:"closed_by,omitempty"`
	ClosedAt           *time.Time `db:"closed_at" json:"closed_at,omitempty"`

	Notes   *string `db:"notes" json:"notes,omitempty"`
	Version int64   `db:"version" json:"version"`
}

type PurchaseOrderLine struct {
	common.BaseModel

	OrganizationID  uuid.UUID `db:"organization_id" json:"organization_id"`
	PurchaseOrderID uuid.UUID `db:"purchase_order_id" json:"purchase_order_id"`
	LineNo          int32     `db:"line_no" json:"line_no"`

	MedicineID uuid.UUID  `db:"medicine_id" json:"medicine_id"`
	VariantID  *uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	UnitID     uuid.UUID  `db:"unit_id" json:"unit_id"`

	// Conversion snapshot: BaseQuantity = OrderedQuantity * numerator / denominator.
	// Store and validate the rational conversion without floating-point arithmetic.
	UnitConversionNumerator   int64 `db:"unit_conversion_numerator" json:"unit_conversion_numerator"`
	UnitConversionDenominator int64 `db:"unit_conversion_denominator" json:"unit_conversion_denominator"`

	OrderedQuantity Quantity `db:"ordered_quantity" json:"ordered_quantity"`
	// This is a transactionally maintained read optimization only. Posted receipt
	// lines remain the source of truth and must be reconciled against this value.
	AcceptedQuantity Quantity `db:"accepted_quantity" json:"accepted_quantity"`

	UnitPriceMinor AmountMinor     `db:"unit_price_minor" json:"unit_price_minor"`
	DiscountRate   RateBasisPoints `db:"discount_rate_bps" json:"discount_rate_bps"`
	DiscountAmount AmountMinor     `db:"discount_amount_minor" json:"discount_amount_minor"`
	TaxRate        RateBasisPoints `db:"tax_rate_bps" json:"tax_rate_bps"`
	TaxAmount      AmountMinor     `db:"tax_amount_minor" json:"tax_amount_minor"`
	LineTotal      AmountMinor     `db:"line_total_minor" json:"line_total_minor"`

	ProductNameSnapshot string         `db:"product_name_snapshot" json:"product_name_snapshot"`
	SKU                 *string        `db:"sku_snapshot" json:"sku,omitempty"`
	UnitNameSnapshot    string         `db:"unit_name_snapshot" json:"unit_name_snapshot"`
	TaxPolicySnapshot   common.JSONMap `db:"tax_policy_snapshot" json:"tax_policy_snapshot,omitempty"`

	ExpectedDeliveryAt *time.Time `db:"expected_delivery_at" json:"expected_delivery_at,omitempty"`
	Version            int64      `db:"version" json:"version"`
}

// -----------------------------------------------------------------------------
// Approval evidence
// -----------------------------------------------------------------------------

type PurchaseApprovalDecision string

const (
	PurchaseApprovalDecisionApproved PurchaseApprovalDecision = "approved"
	PurchaseApprovalDecisionRejected PurchaseApprovalDecision = "rejected"
	PurchaseApprovalDecisionReturned PurchaseApprovalDecision = "returned"
)

type PurchaseApproval struct {
	common.BaseModel

	OrganizationID        uuid.UUID                `db:"organization_id" json:"organization_id"`
	BranchID              uuid.UUID                `db:"branch_id" json:"branch_id"`
	PurchaseOrderID       uuid.UUID                `db:"purchase_order_id" json:"purchase_order_id"`
	Revision              int32                    `db:"revision" json:"revision"`
	Stage                 int16                    `db:"stage" json:"stage"`
	Decision              PurchaseApprovalDecision `db:"decision" json:"decision"`
	ActorID               uuid.UUID                `db:"actor_id" json:"actor_id"`
	Reason                *string                  `db:"reason" json:"reason,omitempty"`
	ApprovalPolicyVersion string                   `db:"approval_policy_version" json:"approval_policy_version"`
	DecidedAt             time.Time                `db:"decided_at" json:"decided_at"`
}

// -----------------------------------------------------------------------------
// Goods receipts. Inventory owns the batch, balance, movement and cost layer.
// -----------------------------------------------------------------------------

type PurchaseReceiptState string

const (
	PurchaseReceiptStateDraft  PurchaseReceiptState = "draft"
	PurchaseReceiptStatePosted PurchaseReceiptState = "posted"
	PurchaseReceiptStateVoid   PurchaseReceiptState = "void"
)

type PurchaseReceipt struct {
	common.BaseModel

	OrganizationID  uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID        uuid.UUID `db:"branch_id" json:"branch_id"`
	WarehouseID     uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	PurchaseOrderID uuid.UUID `db:"purchase_order_id" json:"purchase_order_id"`

	ReceiptNo         string               `db:"receipt_no" json:"receipt_no"`
	OperationID       uuid.UUID            `db:"operation_id" json:"operation_id"`
	DeliveryChallanNo *string              `db:"delivery_challan_no" json:"delivery_challan_no,omitempty"`
	State             PurchaseReceiptState `db:"state" json:"state"`

	ReceivedBy uuid.UUID  `db:"received_by" json:"received_by"`
	ReceivedAt time.Time  `db:"received_at" json:"received_at"`
	PostedBy   *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt   *time.Time `db:"posted_at" json:"posted_at,omitempty"`

	Notes   *string `db:"notes" json:"notes,omitempty"`
	Version int64   `db:"version" json:"version"`
}

type PurchaseReceiptLine struct {
	common.BaseModel

	OrganizationID      uuid.UUID `db:"organization_id" json:"organization_id"`
	PurchaseReceiptID   uuid.UUID `db:"purchase_receipt_id" json:"purchase_receipt_id"`
	PurchaseOrderLineID uuid.UUID `db:"purchase_order_line_id" json:"purchase_order_line_id"`
	LineNo              int32     `db:"line_no" json:"line_no"`

	// Inventory-owned identity. Purchase stores the returned reference; it
	// must not create or mutate InventoryBatch directly.
	InventoryBatchID *uuid.UUID `db:"inventory_batch_id" json:"inventory_batch_id,omitempty"`

	DeliveredQuantity    Quantity `db:"delivered_quantity" json:"delivered_quantity"`
	AcceptedQuantity     Quantity `db:"accepted_quantity" json:"accepted_quantity"`
	RejectedQuantity     Quantity `db:"rejected_quantity" json:"rejected_quantity"`
	RejectionReason      *string  `db:"rejection_reason" json:"rejection_reason,omitempty"`
	RejectionDisposition *string  `db:"rejection_disposition" json:"rejection_disposition,omitempty"`

	// Supplier-provided lot data is evidence/input to Inventory's receipt port,
	// not a second authoritative batch record.
	BatchNumberSnapshot string     `db:"batch_number_snapshot" json:"batch_number_snapshot"`
	ManufacturedAt      *time.Time `db:"manufactured_at" json:"manufactured_at,omitempty"`
	ExpiryAt            time.Time  `db:"expiry_at" json:"expiry_at"`

	UnitAcquisitionCostMinor AmountMinor `db:"unit_acquisition_cost_minor" json:"unit_acquisition_cost_minor"`
	CurrencyCode             string      `db:"currency_code" json:"currency_code"`
	ValuationReference       *string     `db:"valuation_reference" json:"valuation_reference,omitempty"`

	InventoryMovementID  *uuid.UUID `db:"inventory_movement_id" json:"inventory_movement_id,omitempty"`
	InventoryCostLayerID *uuid.UUID `db:"inventory_cost_layer_id" json:"inventory_cost_layer_id,omitempty"`
	EvidenceReferences   []string   `db:"evidence_references" json:"evidence_references,omitempty"`
}

// -----------------------------------------------------------------------------
// Supplier invoices and three-way matching
// -----------------------------------------------------------------------------

type PurchaseInvoiceState string

const (
	PurchaseInvoiceStateDraft    PurchaseInvoiceState = "draft"
	PurchaseInvoiceStateMatched  PurchaseInvoiceState = "matched"
	PurchaseInvoiceStateDisputed PurchaseInvoiceState = "disputed"
	PurchaseInvoiceStatePosted   PurchaseInvoiceState = "posted"
	PurchaseInvoiceStateReversed PurchaseInvoiceState = "reversed"
)

type PurchaseInvoice struct {
	common.BaseModel

	OrganizationID  uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID        uuid.UUID  `db:"branch_id" json:"branch_id"`
	SupplierID      uuid.UUID  `db:"supplier_id" json:"supplier_id"`
	PurchaseOrderID *uuid.UUID `db:"purchase_order_id" json:"purchase_order_id,omitempty"`

	OperationID         uuid.UUID `db:"operation_id" json:"operation_id"`
	InvoiceNo           string    `db:"invoice_no" json:"invoice_no"`
	NormalizedInvoiceNo string    `db:"normalized_invoice_no" json:"normalized_invoice_no"`
	CurrencyCode        string    `db:"currency_code" json:"currency_code"`

	InvoiceDate time.Time  `db:"invoice_date" json:"invoice_date"`
	ReceivedAt  time.Time  `db:"received_at" json:"received_at"`
	DueAt       *time.Time `db:"due_at" json:"due_at,omitempty"`

	SubtotalMinor     AmountMinor `db:"subtotal_minor" json:"subtotal_minor"`
	DiscountMinor     AmountMinor `db:"discount_minor" json:"discount_minor"`
	TaxMinor          AmountMinor `db:"tax_minor" json:"tax_minor"`
	FreightMinor      AmountMinor `db:"freight_minor" json:"freight_minor"`
	OtherChargesMinor AmountMinor `db:"other_charges_minor" json:"other_charges_minor"`
	TotalMinor        AmountMinor `db:"total_minor" json:"total_minor"`

	Status             PurchaseInvoiceState `db:"status" json:"status"`
	EvidenceReferences []string             `db:"evidence_references" json:"evidence_references,omitempty"`
	LedgerJournalID    *uuid.UUID           `db:"ledger_journal_id" json:"ledger_journal_id,omitempty"`

	PostedBy       *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt       *time.Time `db:"posted_at" json:"posted_at,omitempty"`
	ReversedBy     *uuid.UUID `db:"reversed_by" json:"reversed_by,omitempty"`
	ReversedAt     *time.Time `db:"reversed_at" json:"reversed_at,omitempty"`
	ReversalReason *string    `db:"reversal_reason" json:"reversal_reason,omitempty"`
	Version        int64      `db:"version" json:"version"`
}

type PurchaseInvoiceLine struct {
	common.BaseModel

	OrganizationID      uuid.UUID  `db:"organization_id" json:"organization_id"`
	PurchaseInvoiceID   uuid.UUID  `db:"purchase_invoice_id" json:"purchase_invoice_id"`
	PurchaseOrderLineID *uuid.UUID `db:"purchase_order_line_id" json:"purchase_order_line_id,omitempty"`
	LineNo              int32      `db:"line_no" json:"line_no"`

	DescriptionSnapshot string     `db:"description_snapshot" json:"description_snapshot"`
	MedicineID          *uuid.UUID `db:"medicine_id" json:"medicine_id,omitempty"`
	VariantID           *uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	UnitID              *uuid.UUID `db:"unit_id" json:"unit_id,omitempty"`

	InvoicedQuantity  Quantity        `db:"invoiced_quantity" json:"invoiced_quantity"`
	UnitPriceMinor    AmountMinor     `db:"unit_price_minor" json:"unit_price_minor"`
	DiscountRate      RateBasisPoints `db:"discount_rate_bps" json:"discount_rate_bps"`
	DiscountMinor     AmountMinor     `db:"discount_minor" json:"discount_minor"`
	TaxRate           RateBasisPoints `db:"tax_rate_bps" json:"tax_rate_bps"`
	TaxMinor          AmountMinor     `db:"tax_minor" json:"tax_minor"`
	LineTotalMinor    AmountMinor     `db:"line_total_minor" json:"line_total_minor"`
	TaxPolicySnapshot common.JSONMap  `db:"tax_policy_snapshot" json:"tax_policy_snapshot,omitempty"`
}

type PurchaseInvoiceMatchType string

const (
	PurchaseInvoiceMatchTypeThreeWay PurchaseInvoiceMatchType = "three_way"
	PurchaseInvoiceMatchTypeTwoWay   PurchaseInvoiceMatchType = "two_way_exception"
	PurchaseInvoiceMatchTypeManual   PurchaseInvoiceMatchType = "manual_exception"
)

type PurchaseInvoiceMatch struct {
	common.BaseModel

	OrganizationID        uuid.UUID  `db:"organization_id" json:"organization_id"`
	PurchaseInvoiceLineID uuid.UUID  `db:"purchase_invoice_line_id" json:"purchase_invoice_line_id"`
	PurchaseOrderLineID   *uuid.UUID `db:"purchase_order_line_id" json:"purchase_order_line_id,omitempty"`
	PurchaseReceiptLineID *uuid.UUID `db:"purchase_receipt_line_id" json:"purchase_receipt_line_id,omitempty"`

	MatchType                   PurchaseInvoiceMatchType `db:"match_type" json:"match_type"`
	MatchedQuantity             Quantity                 `db:"matched_quantity" json:"matched_quantity"`
	MatchedAmountMinor          AmountMinor              `db:"matched_amount_minor" json:"matched_amount_minor"`
	ExceptionReason             *string                  `db:"exception_reason" json:"exception_reason,omitempty"`
	ExceptionEvidenceReferences []string                 `db:"exception_evidence_references" json:"exception_evidence_references,omitempty"`
	ExceptionApprovedBy         *uuid.UUID               `db:"exception_approved_by" json:"exception_approved_by,omitempty"`
	ExceptionApprovedAt         *time.Time               `db:"exception_approved_at" json:"exception_approved_at,omitempty"`
}

// -----------------------------------------------------------------------------
// Supplier payments and allocations
// -----------------------------------------------------------------------------

type PurchasePaymentStatus string

const (
	PurchasePaymentStatusPending               PurchasePaymentStatus = "pending"
	PurchasePaymentStatusSubmitted             PurchasePaymentStatus = "submitted"
	PurchasePaymentStatusPendingReconciliation PurchasePaymentStatus = "pending_reconciliation"
	PurchasePaymentStatusSettled               PurchasePaymentStatus = "settled"
	PurchasePaymentStatusFailed                PurchasePaymentStatus = "failed"
	PurchasePaymentStatusReversalPending       PurchasePaymentStatus = "reversal_pending"
	PurchasePaymentStatusReversed              PurchasePaymentStatus = "reversed"
)

type PurchasePayment struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	SupplierID     uuid.UUID `db:"supplier_id" json:"supplier_id"`
	OperationID    uuid.UUID `db:"operation_id" json:"operation_id"`

	PaymentNo           string      `db:"payment_no" json:"payment_no"`
	CurrencyCode        string      `db:"currency_code" json:"currency_code"`
	AmountMinor         AmountMinor `db:"amount_minor" json:"amount_minor"`
	MethodCode          string      `db:"method_code" json:"method_code"`
	SettlementAccountID *uuid.UUID  `db:"settlement_account_id" json:"settlement_account_id,omitempty"`

	PaymentDate            time.Time             `db:"payment_date" json:"payment_date"`
	ValueDate              *time.Time            `db:"value_date" json:"value_date,omitempty"`
	Status                 PurchasePaymentStatus `db:"status" json:"status"`
	ExternalReference      *string               `db:"external_reference" json:"external_reference,omitempty"`
	ProviderCode           *string               `db:"provider_code" json:"provider_code,omitempty"`
	ProviderEventID        *string               `db:"provider_event_id" json:"provider_event_id,omitempty"`
	ProviderIdempotencyKey *string               `db:"provider_idempotency_key" json:"provider_idempotency_key,omitempty"`

	PaidBy          uuid.UUID  `db:"paid_by" json:"paid_by"`
	SettledAt       *time.Time `db:"settled_at" json:"settled_at,omitempty"`
	LedgerJournalID *uuid.UUID `db:"ledger_journal_id" json:"ledger_journal_id,omitempty"`

	ReversalOfID   *uuid.UUID `db:"reversal_of_id" json:"reversal_of_id,omitempty"`
	ReversedBy     *uuid.UUID `db:"reversed_by" json:"reversed_by,omitempty"`
	ReversedAt     *time.Time `db:"reversed_at" json:"reversed_at,omitempty"`
	ReversalReason *string    `db:"reversal_reason" json:"reversal_reason,omitempty"`
	Notes          *string    `db:"notes" json:"notes,omitempty"`
	Version        int64      `db:"version" json:"version"`
}

type PurchasePaymentAllocation struct {
	common.BaseModel

	OrganizationID       uuid.UUID   `db:"organization_id" json:"organization_id"`
	PurchasePaymentID    uuid.UUID   `db:"purchase_payment_id" json:"purchase_payment_id"`
	PurchaseInvoiceID    uuid.UUID   `db:"purchase_invoice_id" json:"purchase_invoice_id"`
	OperationID          uuid.UUID   `db:"operation_id" json:"operation_id"`
	AllocatedAmountMinor AmountMinor `db:"allocated_amount_minor" json:"allocated_amount_minor"`
	CurrencyCode         string      `db:"currency_code" json:"currency_code"`
}

// -----------------------------------------------------------------------------
// Supplier returns
// -----------------------------------------------------------------------------

type PurchaseReturnStatus string

const (
	PurchaseReturnStatusDraft           PurchaseReturnStatus = "draft"
	PurchaseReturnStatusPendingApproval PurchaseReturnStatus = "pending_approval"
	PurchaseReturnStatusApproved        PurchaseReturnStatus = "approved"
	PurchaseReturnStatusDispatched      PurchaseReturnStatus = "dispatched"
	PurchaseReturnStatusPosted          PurchaseReturnStatus = "posted"
	PurchaseReturnStatusCancelled       PurchaseReturnStatus = "cancelled"
)

type PurchaseReturn struct {
	common.BaseModel

	OrganizationID  uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID        uuid.UUID  `db:"branch_id" json:"branch_id"`
	WarehouseID     uuid.UUID  `db:"warehouse_id" json:"warehouse_id"`
	SupplierID      uuid.UUID  `db:"supplier_id" json:"supplier_id"`
	PurchaseOrderID *uuid.UUID `db:"purchase_order_id" json:"purchase_order_id,omitempty"`

	ReturnNo           string               `db:"return_no" json:"return_no"`
	OperationID        uuid.UUID            `db:"operation_id" json:"operation_id"`
	Status             PurchaseReturnStatus `db:"status" json:"status"`
	Reason             string               `db:"reason" json:"reason"`
	EvidenceReferences []string             `db:"evidence_references" json:"evidence_references,omitempty"`

	RequestedBy  uuid.UUID  `db:"requested_by" json:"requested_by"`
	ApprovedBy   *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt   *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	DispatchedBy *uuid.UUID `db:"dispatched_by" json:"dispatched_by,omitempty"`
	DispatchedAt *time.Time `db:"dispatched_at" json:"dispatched_at,omitempty"`
	PostedBy     *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt     *time.Time `db:"posted_at" json:"posted_at,omitempty"`
	CancelledBy  *uuid.UUID `db:"cancelled_by" json:"cancelled_by,omitempty"`
	CancelledAt  *time.Time `db:"cancelled_at" json:"cancelled_at,omitempty"`

	LedgerJournalID *uuid.UUID `db:"ledger_journal_id" json:"ledger_journal_id,omitempty"`
	Version         int64      `db:"version" json:"version"`
}

type PurchaseReturnLine struct {
	common.BaseModel

	OrganizationID        uuid.UUID `db:"organization_id" json:"organization_id"`
	PurchaseReturnID      uuid.UUID `db:"purchase_return_id" json:"purchase_return_id"`
	PurchaseReceiptLineID uuid.UUID `db:"purchase_receipt_line_id" json:"purchase_receipt_line_id"`
	InventoryBatchID      uuid.UUID `db:"inventory_batch_id" json:"inventory_batch_id"`
	LineNo                int32     `db:"line_no" json:"line_no"`

	ReturnQuantity       Quantity    `db:"return_quantity" json:"return_quantity"`
	AcquisitionCostMinor AmountMinor `db:"acquisition_cost_minor" json:"acquisition_cost_minor"`
	CurrencyCode         string      `db:"currency_code" json:"currency_code"`
	InventoryMovementID  *uuid.UUID  `db:"inventory_movement_id" json:"inventory_movement_id,omitempty"`
	Notes                *string     `db:"notes" json:"notes,omitempty"`
}

// -----------------------------------------------------------------------------
// Append-only purchase state transition evidence
// -----------------------------------------------------------------------------

type PurchaseTransitionAction string

const (
	PurchaseTransitionActionCreated   PurchaseTransitionAction = "created"
	PurchaseTransitionActionUpdated   PurchaseTransitionAction = "updated"
	PurchaseTransitionActionSubmitted PurchaseTransitionAction = "submitted"
	PurchaseTransitionActionApproved  PurchaseTransitionAction = "approved"
	PurchaseTransitionActionRejected  PurchaseTransitionAction = "rejected"
	PurchaseTransitionActionOrdered   PurchaseTransitionAction = "ordered"
	PurchaseTransitionActionCancelled PurchaseTransitionAction = "cancelled"
	PurchaseTransitionActionClosed    PurchaseTransitionAction = "closed"
)

type PurchaseTransition struct {
	common.BaseModel

	OrganizationID  uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID        uuid.UUID `db:"branch_id" json:"branch_id"`
	PurchaseOrderID uuid.UUID `db:"purchase_order_id" json:"purchase_order_id"`
	OperationID     uuid.UUID `db:"operation_id" json:"operation_id"`

	Dimension     string                   `db:"dimension" json:"dimension"`
	FromState     *string                  `db:"from_state" json:"from_state,omitempty"`
	ToState       string                   `db:"to_state" json:"to_state"`
	Action        PurchaseTransitionAction `db:"action" json:"action"`
	ActorID       uuid.UUID                `db:"actor_id" json:"actor_id"`
	Reason        *string                  `db:"reason" json:"reason,omitempty"`
	PolicyVersion *string                  `db:"policy_version" json:"policy_version,omitempty"`
	OccurredAt    time.Time                `db:"occurred_at" json:"occurred_at"`
}
