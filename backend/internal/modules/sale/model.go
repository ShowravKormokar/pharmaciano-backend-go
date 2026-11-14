// Package sale contains the persistence models for the Sale bounded context.
//
// Ownership rules:
//   - Sale owns sale documents, pricing snapshots, tenders, invoices, returns,
//     refunds, promotions, cash sessions, and command history.
//   - Inventory owns batches, stock balances, stock movements, reservations,
//     and valuation layers. Sale stores only references/results returned by
//     Inventory; it must never mutate Inventory-owned records directly.
//   - Ledger owns journals, account mappings, periods, and financial posting.
//
// Persisted models are not API DTOs. Handlers must use purpose-built DTOs,
// especially for provider and prescription data.
// Money is stored as int64 currency-minor units; quantity follows Inventory's
// indivisible int64 base-unit convention. The initial rollout is BDT-only.
// Embedding BaseModel does not permit deleting posted financial evidence.
//
// This is a domain persistence model file. Validation, state transitions,
// tenant scoping, authorization, transaction orchestration, and invariants
// belong in services/repositories/database constraints.

package sale

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// -----------------------------------------------------------------------------
// Scalar conventions
// -----------------------------------------------------------------------------

// RateBasisPoints stores a percentage multiplied by 100 (e.g. 12.50% = 1250).
// Monetary amounts and Inventory quantities use the shared int64 representation.
type RateBasisPoints int32

// -----------------------------------------------------------------------------
// Enumerations
// -----------------------------------------------------------------------------

type SaleType string

const (
	SaleTypeRetail    SaleType = "retail"
	SaleTypeWholesale SaleType = "wholesale"
	SaleTypeOnline    SaleType = "online"
)

type SaleStatus string

const (
	SaleStatusProcessing  SaleStatus = "processing"
	SaleStatusCompleted   SaleStatus = "completed"
	SaleStatusVoidPending SaleStatus = "void_pending"
	SaleStatusVoided      SaleStatus = "voided"
	SaleStatusReversed    SaleStatus = "reversed"
)

type SalePaymentStatus string

const (
	SalePaymentStatusUnpaid        SalePaymentStatus = "unpaid"
	SalePaymentStatusPartiallyPaid SalePaymentStatus = "partially_paid"
	SalePaymentStatusPaid          SalePaymentStatus = "paid"
)

type SaleInvoiceStatus string

const (
	SaleInvoiceStatusNotIssued SaleInvoiceStatus = "not_issued"
	SaleInvoiceStatusIssued    SaleInvoiceStatus = "issued"
	SaleInvoiceStatusVoided    SaleInvoiceStatus = "voided"
)

type PaymentIntentStatus string

const (
	PaymentIntentStatusCreated               PaymentIntentStatus = "created"
	PaymentIntentStatusSubmitted             PaymentIntentStatus = "submitted"
	PaymentIntentStatusPendingReconciliation PaymentIntentStatus = "pending_reconciliation"
	PaymentIntentStatusSettled               PaymentIntentStatus = "settled"
	PaymentIntentStatusFailed                PaymentIntentStatus = "failed"
	PaymentIntentStatusExpired               PaymentIntentStatus = "expired"
	PaymentIntentStatusCancelled             PaymentIntentStatus = "cancelled"
	PaymentIntentStatusReversalPending       PaymentIntentStatus = "reversal_pending"
	PaymentIntentStatusReversed              PaymentIntentStatus = "reversed"
)

type TenderMethod string

const (
	TenderMethodCash        TenderMethod = "cash"
	TenderMethodCard        TenderMethod = "card"
	TenderMethodMobile      TenderMethod = "mobile"
	TenderMethodBank        TenderMethod = "bank"
	TenderMethodStoreCredit TenderMethod = "store_credit"
	TenderMethodProvider    TenderMethod = "provider"
	TenderMethodOther       TenderMethod = "other"
)

type SaleReturnStatus string

const (
	SaleReturnStatusRequested SaleReturnStatus = "requested"
	SaleReturnStatusApproved  SaleReturnStatus = "approved"
	SaleReturnStatusRejected  SaleReturnStatus = "rejected"
	SaleReturnStatusPosted    SaleReturnStatus = "posted"
	SaleReturnStatusCancelled SaleReturnStatus = "cancelled"
)

type ReturnDisposition string

const (
	ReturnDispositionQuarantine ReturnDisposition = "quarantine"
	ReturnDispositionInspection ReturnDisposition = "inspection"
	ReturnDispositionRestock    ReturnDisposition = "restock"
	ReturnDispositionDispose    ReturnDisposition = "dispose"
)

type RefundStatus string

const (
	RefundStatusPending               RefundStatus = "pending"
	RefundStatusSubmitted             RefundStatus = "submitted"
	RefundStatusPendingReconciliation RefundStatus = "pending_reconciliation"
	RefundStatusSettled               RefundStatus = "settled"
	RefundStatusFailed                RefundStatus = "failed"
	RefundStatusReversed              RefundStatus = "reversed"
)

type CashSessionStatus string

const (
	CashSessionStatusOpen    CashSessionStatus = "open"
	CashSessionStatusClosing CashSessionStatus = "closing"
	CashSessionStatusClosed  CashSessionStatus = "closed"
)

type CashMovementType string

const (
	CashMovementTypeOpening    CashMovementType = "opening"
	CashMovementTypeSaleTender CashMovementType = "sale_tender"
	CashMovementTypeRefund     CashMovementType = "refund"
	CashMovementTypeCashIn     CashMovementType = "cash_in"
	CashMovementTypeCashOut    CashMovementType = "cash_out"
	CashMovementTypeClosing    CashMovementType = "closing"
	CashMovementTypeAdjustment CashMovementType = "adjustment"
)

type PrescriptionDecision string

const (
	PrescriptionDecisionNotRequired PrescriptionDecision = "not_required"
	PrescriptionDecisionPending     PrescriptionDecision = "pending"
	PrescriptionDecisionApproved    PrescriptionDecision = "approved"
	PrescriptionDecisionRejected    PrescriptionDecision = "rejected"
)

type SaleOperationStatus string

const (
	SaleOperationStatusInProgress SaleOperationStatus = "in_progress"
	SaleOperationStatusSucceeded  SaleOperationStatus = "succeeded"
	SaleOperationStatusFailed     SaleOperationStatus = "failed"
)

type SaleOperationType string

const (
	SaleOperationCheckout SaleOperationType = "checkout"
	SaleOperationReturn   SaleOperationType = "return"
	SaleOperationRefund   SaleOperationType = "refund"
	SaleOperationVoid     SaleOperationType = "void"
	SaleOperationPayment  SaleOperationType = "payment"
)

type SaleTransitionDimension string

const (
	SaleTransitionDimensionSale        SaleTransitionDimension = "sale"
	SaleTransitionDimensionPayment     SaleTransitionDimension = "payment"
	SaleTransitionDimensionInvoice     SaleTransitionDimension = "invoice"
	SaleTransitionDimensionReturn      SaleTransitionDimension = "return"
	SaleTransitionDimensionRefund      SaleTransitionDimension = "refund"
	SaleTransitionDimensionCashSession SaleTransitionDimension = "cash_session"
)

// -----------------------------------------------------------------------------
// POS cart (draft only; not a stock reservation or guaranteed price)
// -----------------------------------------------------------------------------

type SaleCart struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	OwnerUserID    uuid.UUID `db:"owner_user_id" json:"owner_user_id"`

	// Content is a bounded, validated draft representation. It is not the
	// authoritative checkout calculation.
	Content   common.JSONMap `db:"content" json:"content"`
	ExpiresAt time.Time      `db:"expires_at" json:"expires_at"`
	Version   int64          `db:"version" json:"version"`
}

// -----------------------------------------------------------------------------
// Sale aggregate and immutable sale-line snapshots
// -----------------------------------------------------------------------------

type Sale struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID  `db:"branch_id" json:"branch_id"`
	WarehouseID    uuid.UUID  `db:"warehouse_id" json:"warehouse_id"`
	CashierID      uuid.UUID  `db:"cashier_id" json:"cashier_id"`
	CustomerID     *uuid.UUID `db:"customer_id" json:"customer_id,omitempty"`
	CashSessionID  *uuid.UUID `db:"cash_session_id" json:"cash_session_id,omitempty"`
	OperationID    uuid.UUID  `db:"operation_id" json:"operation_id"`

	SaleNo        string            `db:"sale_no" json:"sale_no"`
	SaleType      SaleType          `db:"sale_type" json:"sale_type"`
	Status        SaleStatus        `db:"status" json:"status"`
	PaymentStatus SalePaymentStatus `db:"payment_status" json:"payment_status"`
	InvoiceStatus SaleInvoiceStatus `db:"invoice_status" json:"invoice_status"`

	CurrencyCode string `db:"currency_code" json:"currency_code"`

	SubtotalMinor       int64 `db:"subtotal_minor" json:"subtotal_minor"`
	DiscountMinor       int64 `db:"discount_minor" json:"discount_minor"`
	CouponDiscountMinor int64 `db:"coupon_discount_minor" json:"coupon_discount_minor"`
	TaxMinor            int64 `db:"tax_minor" json:"tax_minor"`
	OtherChargesMinor   int64 `db:"other_charges_minor" json:"other_charges_minor"`
	TotalMinor          int64 `db:"total_minor" json:"total_minor"`

	// These are reconciled projections of immutable tender/refund facts.
	// They must not be independently edited by generic update endpoints.
	PaidMinor   int64 `db:"paid_minor" json:"paid_minor"`
	ChangeMinor int64 `db:"change_minor" json:"change_minor"`

	CouponID       *uuid.UUID `db:"coupon_id" json:"coupon_id,omitempty"`
	PrescriptionID *uuid.UUID `db:"prescription_id" json:"prescription_id,omitempty"`
	Notes          *string    `db:"notes" json:"notes,omitempty"`

	// Applied policy snapshots make historical totals reproducible.
	PricingSnapshot common.JSONMap `db:"pricing_snapshot" json:"pricing_snapshot"`
	TaxSnapshot     common.JSONMap `db:"tax_snapshot" json:"tax_snapshot"`
	PolicyVersion   string         `db:"policy_version" json:"policy_version"`

	SoldAt      time.Time  `db:"sold_at" json:"sold_at"`
	CompletedAt *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	VoidedAt    *time.Time `db:"voided_at" json:"voided_at,omitempty"`
	VoidedBy    *uuid.UUID `db:"voided_by" json:"voided_by,omitempty"`
	VoidReason  *string    `db:"void_reason" json:"void_reason,omitempty"`

	Version int64 `db:"version" json:"version"`
}

type SaleItem struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	SaleID         uuid.UUID `db:"sale_id" json:"sale_id"`
	LineNo         int32     `db:"line_no" json:"line_no"`

	MedicineID uuid.UUID  `db:"medicine_id" json:"medicine_id"`
	VariantID  *uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	UnitID     uuid.UUID  `db:"unit_id" json:"unit_id"`

	// Quantities are indivisible integers in the selected unit and Inventory's
	// canonical base unit. The conversion ratio must produce a whole base unit.
	EnteredQuantity           int64 `db:"entered_quantity" json:"entered_quantity"`
	BaseQuantity              int64 `db:"base_quantity" json:"base_quantity"`
	UnitConversionNumerator   int64 `db:"unit_conversion_numerator" json:"unit_conversion_numerator"`
	UnitConversionDenominator int64 `db:"unit_conversion_denominator" json:"unit_conversion_denominator"`

	UnitPriceMinor  int64           `db:"unit_price_minor" json:"unit_price_minor"`
	DiscountMinor   int64           `db:"discount_minor" json:"discount_minor"`
	TaxMinor        int64           `db:"tax_minor" json:"tax_minor"`
	LineTotalMinor  int64           `db:"line_total_minor" json:"line_total_minor"`
	DiscountRateBPS RateBasisPoints `db:"discount_rate_bps" json:"discount_rate_bps"`
	TaxRateBPS      RateBasisPoints `db:"tax_rate_bps" json:"tax_rate_bps"`

	// Snapshots are immutable after posting and must be bounded/versioned.
	ProductSnapshot common.JSONMap `db:"product_snapshot" json:"product_snapshot"`
	UnitSnapshot    common.JSONMap `db:"unit_snapshot" json:"unit_snapshot"`
	TaxSnapshot     common.JSONMap `db:"tax_snapshot" json:"tax_snapshot"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// SaleStockAllocation records which Inventory-owned batch/movement/cost
// allocation fulfilled a sale line. It does not own or mutate those records.
type SaleStockAllocation struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	SaleID         uuid.UUID `db:"sale_id" json:"sale_id"`
	SaleItemID     uuid.UUID `db:"sale_item_id" json:"sale_item_id"`
	WarehouseID    uuid.UUID `db:"warehouse_id" json:"warehouse_id"`
	BatchID        uuid.UUID `db:"batch_id" json:"batch_id"`

	InventoryMovementID       uuid.UUID  `db:"inventory_movement_id" json:"inventory_movement_id"`
	InventoryCostAllocationID uuid.UUID  `db:"inventory_cost_allocation_id" json:"inventory_cost_allocation_id"`
	CostLayerID               *uuid.UUID `db:"cost_layer_id" json:"cost_layer_id,omitempty"`
	BaseQuantity              int64      `db:"base_quantity" json:"base_quantity"`

	// Display-only historical snapshot; BatchID remains the owner reference.
	BatchNoSnapshot string     `db:"batch_no_snapshot" json:"batch_no_snapshot"`
	ExpiryDate      *time.Time `db:"expiry_date" json:"expiry_date,omitempty"`

	UnitCostMinor  int64     `db:"unit_cost_minor" json:"unit_cost_minor"`
	TotalCostMinor int64     `db:"total_cost_minor" json:"total_cost_minor"`
	CurrencyCode   string    `db:"currency_code" json:"currency_code"`
	ValuationRef   string    `db:"valuation_ref" json:"valuation_ref"`
	AllocatedAt    time.Time `db:"allocated_at" json:"allocated_at"`
}

// -----------------------------------------------------------------------------
// Remote payment intents, append-only tenders, and provider events
// -----------------------------------------------------------------------------

type SalePaymentIntent struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID  `db:"branch_id" json:"branch_id"`
	OperationID    uuid.UUID  `db:"operation_id" json:"operation_id"`
	SaleID         *uuid.UUID `db:"sale_id" json:"sale_id,omitempty"`

	ProviderCode           string  `db:"provider_code" json:"provider_code"`
	ProviderAccountID      string  `db:"provider_account_id" json:"-"`
	ProviderIdempotencyKey string  `db:"provider_idempotency_key" json:"-"`
	ExternalReference      *string `db:"external_reference" json:"-"`

	AmountMinor  int64               `db:"amount_minor" json:"amount_minor"`
	CurrencyCode string              `db:"currency_code" json:"currency_code"`
	Status       PaymentIntentStatus `db:"status" json:"status"`

	ExpiresAt      time.Time  `db:"expires_at" json:"expires_at"`
	SubmittedAt    *time.Time `db:"submitted_at" json:"submitted_at,omitempty"`
	SettledAt      *time.Time `db:"settled_at" json:"settled_at,omitempty"`
	FailedAt       *time.Time `db:"failed_at" json:"failed_at,omitempty"`
	FailureCode    *string    `db:"failure_code" json:"failure_code,omitempty"`
	FailureMessage *string    `db:"failure_message" json:"-"`
	Version        int64      `db:"version" json:"version"`
}

type SaleTender struct {
	common.BaseModel

	OrganizationID  uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID        uuid.UUID  `db:"branch_id" json:"branch_id"`
	SaleID          uuid.UUID  `db:"sale_id" json:"sale_id"`
	PaymentIntentID *uuid.UUID `db:"payment_intent_id" json:"payment_intent_id,omitempty"`
	TenderNo        string     `db:"tender_no" json:"tender_no"`

	Method        TenderMethod `db:"method" json:"method"`
	AmountMinor   int64        `db:"amount_minor" json:"amount_minor"`
	CurrencyCode  string       `db:"currency_code" json:"currency_code"`
	ReferenceNo   *string      `db:"reference_no" json:"-"`
	ProviderRef   *string      `db:"provider_ref" json:"-"`
	ReceivedAt    time.Time    `db:"received_at" json:"received_at"`
	ReceivedBy    uuid.UUID    `db:"received_by" json:"received_by"`
	SettlementRef *string      `db:"settlement_ref" json:"-"`
}

type SalePaymentEvent struct {
	common.BaseModel

	OrganizationID  uuid.UUID `db:"organization_id" json:"organization_id"`
	PaymentIntentID uuid.UUID `db:"payment_intent_id" json:"payment_intent_id"`

	ProviderCode      string `db:"provider_code" json:"provider_code"`
	ProviderAccountID string `db:"provider_account_id" json:"-"`
	ProviderEventID   string `db:"provider_event_id" json:"-"`
	EventType         string `db:"event_type" json:"event_type"`

	AmountMinor  int64     `db:"amount_minor" json:"amount_minor"`
	CurrencyCode string    `db:"currency_code" json:"currency_code"`
	OccurredAt   time.Time `db:"occurred_at" json:"occurred_at"`
	ReceivedAt   time.Time `db:"received_at" json:"received_at"`

	// Store sanitized, allow-listed provider evidence only. Never store secrets,
	// PAN/CVV, authentication tokens, or unbounded raw webhook bodies.
	EvidenceSnapshot common.JSONMap `db:"evidence_snapshot" json:"-"`
}

// -----------------------------------------------------------------------------
// Immutable invoice facts and PostgreSQL-backed invoice counter
// -----------------------------------------------------------------------------

type SaleInvoice struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	SaleID         uuid.UUID `db:"sale_id" json:"sale_id"`

	Series       string    `db:"series" json:"series"`
	FiscalKey    string    `db:"fiscal_key" json:"fiscal_key"`
	InvoiceNo    string    `db:"invoice_no" json:"invoice_no"`
	IssuedAt     time.Time `db:"issued_at" json:"issued_at"`
	CurrencyCode string    `db:"currency_code" json:"currency_code"`

	Snapshot        common.JSONMap `db:"snapshot" json:"snapshot"`
	SnapshotHash    string         `db:"snapshot_hash" json:"snapshot_hash"`
	TemplateVersion string         `db:"template_version" json:"template_version"`
}

type SaleInvoiceCounter struct {
	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	Series         string    `db:"series" json:"series"`
	FiscalKey      string    `db:"fiscal_key" json:"fiscal_key"`
	NextNumber     int64     `db:"next_number" json:"next_number"`
	UpdatedAt      time.Time `db:"updated_at" json:"updated_at"`
}

// -----------------------------------------------------------------------------
// Returns and refunds are separate documents and state machines
// -----------------------------------------------------------------------------

type SalesReturn struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	SaleID         uuid.UUID `db:"sale_id" json:"sale_id"`
	OperationID    uuid.UUID `db:"operation_id" json:"operation_id"`

	ReturnNo         string           `db:"return_no" json:"return_no"`
	Status           SaleReturnStatus `db:"status" json:"status"`
	Reason           string           `db:"reason" json:"reason"`
	CurrencyCode     string           `db:"currency_code" json:"currency_code"`
	CreditTotalMinor int64            `db:"credit_total_minor" json:"credit_total_minor"`
	PolicySnapshot   common.JSONMap   `db:"policy_snapshot" json:"policy_snapshot"`
	EvidenceRefs     []string         `db:"evidence_refs" json:"-"`

	RequestedBy    uuid.UUID  `db:"requested_by" json:"requested_by"`
	RequestedAt    time.Time  `db:"requested_at" json:"requested_at"`
	ApprovedBy     *uuid.UUID `db:"approved_by" json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	RejectedBy     *uuid.UUID `db:"rejected_by" json:"rejected_by,omitempty"`
	RejectedAt     *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectedReason *string    `db:"rejected_reason" json:"rejected_reason,omitempty"`
	PostedBy       *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt       *time.Time `db:"posted_at" json:"posted_at,omitempty"`
	Version        int64      `db:"version" json:"version"`
}

type SalesReturnItem struct {
	common.BaseModel

	OrganizationID    uuid.UUID `db:"organization_id" json:"organization_id"`
	SalesReturnID     uuid.UUID `db:"sales_return_id" json:"sales_return_id"`
	SaleItemID        uuid.UUID `db:"sale_item_id" json:"sale_item_id"`
	StockAllocationID uuid.UUID `db:"stock_allocation_id" json:"stock_allocation_id"`
	BatchID           uuid.UUID `db:"batch_id" json:"batch_id"`

	LineNo            int32             `db:"line_no" json:"line_no"`
	Quantity          int64             `db:"quantity" json:"quantity"`
	Disposition       ReturnDisposition `db:"disposition" json:"disposition"`
	DispositionReason *string           `db:"disposition_reason" json:"disposition_reason,omitempty"`

	CreditMinor         int64      `db:"credit_minor" json:"credit_minor"`
	OriginalCostMinor   int64      `db:"original_cost_minor" json:"original_cost_minor"`
	CurrencyCode        string     `db:"currency_code" json:"currency_code"`
	InventoryMovementID *uuid.UUID `db:"inventory_movement_id" json:"inventory_movement_id,omitempty"`
}

type SaleRefund struct {
	common.BaseModel

	OrganizationID   uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID         uuid.UUID  `db:"branch_id" json:"branch_id"`
	SaleID           uuid.UUID  `db:"sale_id" json:"sale_id"`
	SalesReturnID    *uuid.UUID `db:"sales_return_id" json:"sales_return_id,omitempty"`
	OriginalTenderID *uuid.UUID `db:"original_tender_id" json:"original_tender_id,omitempty"`
	OperationID      uuid.UUID  `db:"operation_id" json:"operation_id"`

	RefundNo         string       `db:"refund_no" json:"refund_no"`
	AmountMinor      int64        `db:"amount_minor" json:"amount_minor"`
	CurrencyCode     string       `db:"currency_code" json:"currency_code"`
	Method           TenderMethod `db:"method" json:"method"`
	Status           RefundStatus `db:"status" json:"status"`
	ProviderRef      *string      `db:"provider_ref" json:"-"`
	Reason           string       `db:"reason" json:"reason"`
	RequestedBy      uuid.UUID    `db:"requested_by" json:"requested_by"`
	RequestedAt      time.Time    `db:"requested_at" json:"requested_at"`
	SettledAt        *time.Time   `db:"settled_at" json:"settled_at,omitempty"`
	FailedAt         *time.Time   `db:"failed_at" json:"failed_at,omitempty"`
	FailureCode      *string      `db:"failure_code" json:"failure_code,omitempty"`
	LedgerPostingRef *string      `db:"ledger_posting_ref" json:"-"`
	Version          int64        `db:"version" json:"version"`
}

// -----------------------------------------------------------------------------
// Restricted prescription evidence: keep minimum necessary data
// -----------------------------------------------------------------------------

type SalePrescriptionEvidence struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	SaleID         uuid.UUID `db:"sale_id" json:"sale_id"`

	ReviewerID      uuid.UUID            `db:"reviewer_id" json:"reviewer_id"`
	ReviewedAt      time.Time            `db:"reviewed_at" json:"reviewed_at"`
	PrescriptionRef *string              `db:"prescription_ref" json:"-"`
	PrescriberRef   *string              `db:"prescriber_ref" json:"-"`
	EvidenceRefs    []string             `db:"evidence_refs" json:"-"`
	Decision        PrescriptionDecision `db:"decision" json:"decision"`
	PolicyVersion   string               `db:"policy_version" json:"policy_version"`
	DecisionReason  *string              `db:"decision_reason" json:"-"`
}

// -----------------------------------------------------------------------------
// Price books and effective-dated price versions
// -----------------------------------------------------------------------------

type SalePriceBook struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID `db:"branch_id" json:"branch_id,omitempty"`
	Code           string     `db:"code" json:"code"`
	Name           string     `db:"name" json:"name"`
	CurrencyCode   string     `db:"currency_code" json:"currency_code"`
	IsActive       bool       `db:"is_active" json:"is_active"`
	Version        int64      `db:"version" json:"version"`
}

type SalePriceVersion struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	PriceBookID    uuid.UUID  `db:"price_book_id" json:"price_book_id"`
	MedicineID     uuid.UUID  `db:"medicine_id" json:"medicine_id"`
	VariantID      *uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	UnitID         uuid.UUID  `db:"unit_id" json:"unit_id"`

	UnitPriceMinor int64      `db:"unit_price_minor" json:"unit_price_minor"`
	CurrencyCode   string     `db:"currency_code" json:"currency_code"`
	ValidFrom      time.Time  `db:"valid_from" json:"valid_from"`
	ValidTo        *time.Time `db:"valid_to" json:"valid_to,omitempty"`
	Version        int64      `db:"version" json:"version"`
}

// -----------------------------------------------------------------------------
// Promotions (coupon/offer policy represented as one typed promotion model)
// -----------------------------------------------------------------------------

type PromotionKind string

const (
	PromotionKindPercentOff PromotionKind = "percent_off"
	PromotionKindFlatOff    PromotionKind = "flat_off"
	PromotionKindBuyXGetY   PromotionKind = "buy_x_get_y"
	PromotionKindBundle     PromotionKind = "bundle"
)

type SalePromotion struct {
	common.BaseModel

	OrganizationID uuid.UUID      `db:"organization_id" json:"organization_id"`
	Code           string         `db:"code" json:"code"`
	Name           string         `db:"name" json:"name"`
	Description    *string        `db:"description" json:"description,omitempty"`
	Kind           PromotionKind  `db:"kind" json:"kind"`
	Config         common.JSONMap `db:"config" json:"config"`

	CurrencyCode      *string          `db:"currency_code" json:"currency_code,omitempty"`
	MinPurchaseMinor  *int64           `db:"min_purchase_minor" json:"min_purchase_minor,omitempty"`
	MaxDiscountMinor  *int64           `db:"max_discount_minor" json:"max_discount_minor,omitempty"`
	DiscountRateBPS   *RateBasisPoints `db:"discount_rate_bps" json:"discount_rate_bps,omitempty"`
	FlatDiscountMinor *int64           `db:"flat_discount_minor" json:"flat_discount_minor,omitempty"`

	ValidFrom        time.Time `db:"valid_from" json:"valid_from"`
	ValidTo          time.Time `db:"valid_to" json:"valid_to"`
	UsageLimit       *int64    `db:"usage_limit" json:"usage_limit,omitempty"`
	UsageCount       int64     `db:"usage_count" json:"usage_count"`
	PerCustomerLimit *int64    `db:"per_customer_limit" json:"per_customer_limit,omitempty"`
	IsActive         bool      `db:"is_active" json:"is_active"`
	Version          int64     `db:"version" json:"version"`
}

type SalePromotionRedemption struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	PromotionID    uuid.UUID  `db:"promotion_id" json:"promotion_id"`
	SaleID         uuid.UUID  `db:"sale_id" json:"sale_id"`
	CustomerID     *uuid.UUID `db:"customer_id" json:"customer_id,omitempty"`

	RedemptionCount int64     `db:"redemption_count" json:"redemption_count"`
	DiscountMinor   int64     `db:"discount_minor" json:"discount_minor"`
	CurrencyCode    string    `db:"currency_code" json:"currency_code"`
	RedeemedAt      time.Time `db:"redeemed_at" json:"redeemed_at"`
}

// -----------------------------------------------------------------------------
// Cashier/drawer sessions and append-only cash movements
// -----------------------------------------------------------------------------

type SaleCashSession struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID  `db:"branch_id" json:"branch_id"`
	CashierID      uuid.UUID  `db:"cashier_id" json:"cashier_id"`
	DrawerID       *uuid.UUID `db:"drawer_id" json:"drawer_id,omitempty"`

	SessionNo     string            `db:"session_no" json:"session_no"`
	Status        CashSessionStatus `db:"status" json:"status"`
	CurrencyCode  string            `db:"currency_code" json:"currency_code"`
	OpeningMinor  int64             `db:"opening_minor" json:"opening_minor"`
	ExpectedMinor int64             `db:"expected_minor" json:"expected_minor"`
	CountedMinor  *int64            `db:"counted_minor" json:"counted_minor,omitempty"`
	VarianceMinor *int64            `db:"variance_minor" json:"variance_minor,omitempty"`

	OpenedAt         time.Time  `db:"opened_at" json:"opened_at"`
	OpenedBy         uuid.UUID  `db:"opened_by" json:"opened_by"`
	ClosingStartedAt *time.Time `db:"closing_started_at" json:"closing_started_at,omitempty"`
	ClosedAt         *time.Time `db:"closed_at" json:"closed_at,omitempty"`
	ClosedBy         *uuid.UUID `db:"closed_by" json:"closed_by,omitempty"`
	CloseReason      *string    `db:"close_reason" json:"close_reason,omitempty"`
	Version          int64      `db:"version" json:"version"`
}

type SaleCashMovement struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id" json:"branch_id"`
	CashSessionID  uuid.UUID `db:"cash_session_id" json:"cash_session_id"`

	MovementType CashMovementType `db:"movement_type" json:"movement_type"`
	AmountMinor  int64            `db:"amount_minor" json:"amount_minor"`
	CurrencyCode string           `db:"currency_code" json:"currency_code"`
	SourceType   string           `db:"source_type" json:"source_type"`
	SourceID     uuid.UUID        `db:"source_id" json:"source_id"`
	OperationID  uuid.UUID        `db:"operation_id" json:"operation_id"`
	ActorID      uuid.UUID        `db:"actor_id" json:"actor_id"`
	OccurredAt   time.Time        `db:"occurred_at" json:"occurred_at"`
	Reason       *string          `db:"reason" json:"reason,omitempty"`
}

// -----------------------------------------------------------------------------
// Durable command idempotency and append-only transition history
// -----------------------------------------------------------------------------

type SaleOperation struct {
	common.BaseModel

	OrganizationID uuid.UUID         `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID         `db:"branch_id" json:"branch_id"`
	OperationID    uuid.UUID         `db:"operation_id" json:"operation_id"`
	OperationType  SaleOperationType `db:"operation_type" json:"operation_type"`

	// Persist a keyed digest of the Idempotency-Key, not the raw key.
	IdempotencyKeyHash string              `db:"idempotency_key_hash" json:"-"`
	RequestHash        string              `db:"request_hash" json:"-"`
	Status             SaleOperationStatus `db:"status" json:"status"`

	ResourceType *string        `db:"resource_type" json:"resource_type,omitempty"`
	ResourceID   *uuid.UUID     `db:"resource_id" json:"resource_id,omitempty"`
	ResponseCode *int           `db:"response_code" json:"response_code,omitempty"`
	ResponseBody common.JSONMap `db:"response_body" json:"-"`

	StartedAt   time.Time  `db:"started_at" json:"started_at"`
	CompletedAt *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	ExpiresAt   time.Time  `db:"expires_at" json:"expires_at"`
}

type SaleTransition struct {
	common.BaseModel

	OrganizationID uuid.UUID               `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID               `db:"branch_id" json:"branch_id"`
	AggregateType  string                  `db:"aggregate_type" json:"aggregate_type"`
	AggregateID    uuid.UUID               `db:"aggregate_id" json:"aggregate_id"`
	Dimension      SaleTransitionDimension `db:"dimension" json:"dimension"`
	FromState      *string                 `db:"from_state" json:"from_state,omitempty"`
	ToState        string                  `db:"to_state" json:"to_state"`
	Action         string                  `db:"action" json:"action"`
	OperationID    uuid.UUID               `db:"operation_id" json:"operation_id"`
	ActorID        uuid.UUID               `db:"actor_id" json:"actor_id"`
	Reason         *string                 `db:"reason" json:"reason,omitempty"`
	PolicyVersion  *string                 `db:"policy_version" json:"policy_version,omitempty"`
	OccurredAt     time.Time               `db:"occurred_at" json:"occurred_at"`
}
