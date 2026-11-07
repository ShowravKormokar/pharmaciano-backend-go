package purchase

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

const maxPurchaseLines = 500

// CreatePurchaseOrderRequest contains only caller-editable draft data.
// Organization, actor, currency, totals, lifecycle states, and policy
// snapshots are resolved or calculated by the service.
type CreatePurchaseOrderRequest struct {
	BranchID           uuid.UUID                        `json:"branch_id" validate:"required"`
	SupplierID         uuid.UUID                        `json:"supplier_id" validate:"required"`
	ReferenceNo        *string                          `json:"reference_no" validate:"omitempty,max=120"`
	ExpectedDeliveryAt *time.Time                       `json:"expected_delivery_at" validate:"omitempty"`
	Notes              *string                          `json:"notes" validate:"omitempty,max=2000"`
	Lines              []CreatePurchaseOrderLineRequest `json:"lines" validate:"required,min=1,max=500,dive"`
}

// CreatePurchaseOrderLineRequest expresses price and quantity inputs only.
// Conversion factors, product/unit labels, tax snapshots, and extended
// amounts are resolved or calculated server-side.
type CreatePurchaseOrderLineRequest struct {
	MedicineID         uuid.UUID       `json:"medicine_id" validate:"required"`
	VariantID          *uuid.UUID      `json:"variant_id" validate:"omitempty"`
	UnitID             uuid.UUID       `json:"unit_id" validate:"required"`
	OrderedQuantity    Quantity        `json:"ordered_quantity" validate:"gt=0"`
	UnitPriceMinor     AmountMinor     `json:"unit_price_minor" validate:"gte=0"`
	DiscountRateBPS    RateBasisPoints `json:"discount_rate_bps" validate:"gte=0,lte=10000"`
	TaxRateBPS         RateBasisPoints `json:"tax_rate_bps" validate:"gte=0,lte=10000"`
	ExpectedDeliveryAt *time.Time      `json:"expected_delivery_at" validate:"omitempty"`
}

func (r CreatePurchaseOrderRequest) Validate() error {
	if r.BranchID == uuid.Nil || r.SupplierID == uuid.Nil {
		return fmt.Errorf("branch_id and supplier_id are required")
	}
	if len(r.Lines) == 0 || len(r.Lines) > maxPurchaseLines {
		return fmt.Errorf("purchase order must contain between 1 and %d lines", maxPurchaseLines)
	}
	for i, line := range r.Lines {
		if line.MedicineID == uuid.Nil || line.UnitID == uuid.Nil {
			return fmt.Errorf("lines[%d].medicine_id and unit_id are required", i)
		}
		if line.VariantID != nil && *line.VariantID == uuid.Nil {
			return fmt.Errorf("lines[%d].variant_id must not be empty", i)
		}
		if line.OrderedQuantity <= 0 || line.UnitPriceMinor < 0 {
			return fmt.Errorf("lines[%d] quantity must be positive and unit price nonnegative", i)
		}
		if line.DiscountRateBPS < 0 || line.DiscountRateBPS > 10000 ||
			line.TaxRateBPS < 0 || line.TaxRateBPS > 10000 {
			return fmt.Errorf("lines[%d] rates must be between 0 and 10000 basis points", i)
		}
	}
	return nil
}

// UpdatePurchaseOrderRequest replaces draft lines only when lines is supplied.
// Version is mandatory for optimistic concurrency control. A nil optional
// field means unchanged; an empty string clears reference_no or notes.
type UpdatePurchaseOrderRequest struct {
	Version            int64                             `json:"version" validate:"required,gt=0"`
	ReferenceNo        *string                           `json:"reference_no" validate:"omitempty,max=120"`
	ExpectedDeliveryAt *time.Time                        `json:"expected_delivery_at" validate:"omitempty"`
	Notes              *string                           `json:"notes" validate:"omitempty,max=2000"`
	Lines              *[]CreatePurchaseOrderLineRequest `json:"lines" validate:"omitempty,min=1,max=500,dive"`
}

func (r UpdatePurchaseOrderRequest) IsEmpty() bool {
	return r.ReferenceNo == nil &&
		r.ExpectedDeliveryAt == nil &&
		r.Notes == nil &&
		r.Lines == nil
}

type ListPurchaseOrdersQuery struct {
	pagination.Offset

	BranchID      *uuid.UUID             `form:"branch_id"`
	SupplierID    *uuid.UUID             `form:"supplier_id"`
	OrderStatus   *PurchaseOrderStatus   `form:"order_status"`
	ReceiptStatus *PurchaseReceiptStatus `form:"receipt_status"`
	InvoiceStatus *PurchaseInvoiceStatus `form:"invoice_status"`
	ClosureStatus *PurchaseClosureStatus `form:"closure_status"`
	From          *time.Time             `form:"from"`
	To            *time.Time             `form:"to"`
	Q             *string                `form:"q" validate:"omitempty,max=200"`
}

func (q *ListPurchaseOrdersQuery) NormalizeAndValidate() error {
	if q == nil {
		return fmt.Errorf("purchase query is required")
	}
	q.Offset.Normalize()
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		return fmt.Errorf("from must be earlier than to")
	}
	if q.Q != nil {
		term := strings.TrimSpace(*q.Q)
		if len(term) > pagination.MaxQueryLen {
			term = term[:pagination.MaxQueryLen]
		}
		q.Q = &term
	}
	if q.OrderStatus != nil && !validPurchaseOrderStatus(*q.OrderStatus) {
		return fmt.Errorf("invalid order_status")
	}
	if q.ReceiptStatus != nil && !validPurchaseReceiptStatus(*q.ReceiptStatus) {
		return fmt.Errorf("invalid receipt_status")
	}
	if q.InvoiceStatus != nil && !validPurchaseInvoiceStatus(*q.InvoiceStatus) {
		return fmt.Errorf("invalid invoice_status")
	}
	if q.ClosureStatus != nil && !validPurchaseClosureStatus(*q.ClosureStatus) {
		return fmt.Errorf("invalid closure_status")
	}
	return nil
}

type PurchaseReasonRequest struct {
	Reason string `json:"reason" validate:"required,not_blank,max=1000"`
}

type RecordPurchaseOrderRequest struct {
	SupplierReference *string `json:"supplier_reference" validate:"omitempty,max=120"`
	Notes             *string `json:"notes" validate:"omitempty,max=2000"`
}

type CancelPurchaseOrderRequest struct {
	Reason string `json:"reason" validate:"required,not_blank,max=1000"`
}

type ClosePurchaseOrderRequest struct {
	Reason  string `json:"reason" validate:"required,not_blank,max=1000"`
	Version int64  `json:"version" validate:"required,gt=0"`
}

type PostPurchaseReceiptRequest struct {
	WarehouseID       uuid.UUID                        `json:"warehouse_id" validate:"required"`
	DeliveryChallanNo *string                          `json:"delivery_challan_no" validate:"omitempty,max=120"`
	ReceivedAt        *time.Time                       `json:"received_at" validate:"omitempty"`
	Notes             *string                          `json:"notes" validate:"omitempty,max=2000"`
	Lines             []PostPurchaseReceiptLineRequest `json:"lines" validate:"required,min=1,max=500,dive"`
}

type PostPurchaseReceiptLineRequest struct {
	PurchaseOrderLineID      uuid.UUID   `json:"purchase_order_line_id" validate:"required"`
	DeliveredQuantity        Quantity    `json:"delivered_quantity" validate:"gt=0"`
	AcceptedQuantity         Quantity    `json:"accepted_quantity" validate:"gte=0"`
	RejectedQuantity         Quantity    `json:"rejected_quantity" validate:"gte=0"`
	RejectionReason          *string     `json:"rejection_reason" validate:"omitempty,max=1000"`
	RejectionDisposition     *string     `json:"rejection_disposition" validate:"omitempty,max=80"`
	BatchNumber              string      `json:"batch_number" validate:"required,not_blank,max=80"`
	ManufacturedAt           *time.Time  `json:"manufactured_at" validate:"omitempty"`
	ExpiryAt                 time.Time   `json:"expiry_at" validate:"required"`
	UnitAcquisitionCostMinor AmountMinor `json:"unit_acquisition_cost_minor" validate:"gte=0"`
	ValuationReference       *string     `json:"valuation_reference" validate:"omitempty,max=120"`
	EvidenceReferences       []string    `json:"evidence_references" validate:"omitempty,max=20,dive,required,not_blank,max=500"`
}

func (r PostPurchaseReceiptRequest) Validate() error {
	if r.WarehouseID == uuid.Nil {
		return fmt.Errorf("warehouse_id is required")
	}
	if len(r.Lines) == 0 || len(r.Lines) > maxPurchaseLines {
		return fmt.Errorf("receipt must contain between 1 and %d lines", maxPurchaseLines)
	}
	seen := make(map[uuid.UUID]struct{}, len(r.Lines))
	for i, line := range r.Lines {
		if line.PurchaseOrderLineID == uuid.Nil {
			return fmt.Errorf("lines[%d].purchase_order_line_id is required", i)
		}
		if _, ok := seen[line.PurchaseOrderLineID]; ok {
			return fmt.Errorf("purchase order line may appear only once in a receipt")
		}
		seen[line.PurchaseOrderLineID] = struct{}{}
		if line.DeliveredQuantity <= 0 ||
			line.AcceptedQuantity < 0 ||
			line.RejectedQuantity < 0 ||
			line.AcceptedQuantity > line.DeliveredQuantity ||
			line.RejectedQuantity > line.DeliveredQuantity ||
			line.AcceptedQuantity != line.DeliveredQuantity-line.RejectedQuantity {
			return fmt.Errorf("lines[%d] must have positive delivered quantity equal to accepted plus rejected quantity", i)
		}
		if line.AcceptedQuantity > 0 && line.ExpiryAt.IsZero() {
			return fmt.Errorf("lines[%d].expiry_at is required for accepted goods", i)
		}
		if line.AcceptedQuantity > 0 && strings.TrimSpace(line.BatchNumber) == "" {
			return fmt.Errorf("lines[%d].batch_number is required for accepted goods", i)
		}
		if line.ManufacturedAt != nil && !line.ExpiryAt.IsZero() && line.ManufacturedAt.After(line.ExpiryAt) {
			return fmt.Errorf("lines[%d].manufactured_at must not be after expiry_at", i)
		}
		if line.AcceptedQuantity > 0 && line.UnitAcquisitionCostMinor < 0 {
			return fmt.Errorf("lines[%d].unit_acquisition_cost_minor must not be negative", i)
		}
		if line.RejectedQuantity > 0 && (line.RejectionReason == nil || strings.TrimSpace(*line.RejectionReason) == "") {
			return fmt.Errorf("lines[%d].rejection_reason is required when goods are rejected", i)
		}
	}
	return nil
}

type CreatePurchaseInvoiceRequest struct {
	InvoiceNo          string                             `json:"invoice_no" validate:"required,not_blank,max=120"`
	InvoiceDate        time.Time                          `json:"invoice_date" validate:"required"`
	ReceivedAt         *time.Time                         `json:"received_at" validate:"omitempty"`
	DueAt              *time.Time                         `json:"due_at" validate:"omitempty"`
	FreightMinor       AmountMinor                        `json:"freight_minor" validate:"gte=0"`
	OtherChargesMinor  AmountMinor                        `json:"other_charges_minor" validate:"gte=0"`
	EvidenceReferences []string                           `json:"evidence_references" validate:"omitempty,max=20,dive,required,not_blank,max=500"`
	Lines              []CreatePurchaseInvoiceLineRequest `json:"lines" validate:"required,min=1,max=500,dive"`
}

func (r CreatePurchaseInvoiceRequest) Validate() error {
	if strings.TrimSpace(r.InvoiceNo) == "" || r.InvoiceDate.IsZero() {
		return fmt.Errorf("invoice_no and invoice_date are required")
	}
	if r.FreightMinor < 0 || r.OtherChargesMinor < 0 {
		return fmt.Errorf("freight and other charges must not be negative")
	}
	if len(r.Lines) == 0 || len(r.Lines) > maxPurchaseLines {
		return fmt.Errorf("invoice must contain between 1 and %d lines", maxPurchaseLines)
	}
	for i, line := range r.Lines {
		if strings.TrimSpace(line.Description) == "" || line.InvoicedQuantity <= 0 ||
			line.UnitPriceMinor < 0 || line.DiscountRateBPS < 0 ||
			line.DiscountRateBPS > 10000 || line.TaxRateBPS < 0 || line.TaxRateBPS > 10000 {
			return fmt.Errorf("invoice line %d has invalid description, quantity, price, or rate", i)
		}
		if err := validateInvoiceMatches(line); err != nil {
			return fmt.Errorf("invoice line %d: %w", i, err)
		}
		if line.PurchaseOrderLineID != nil && *line.PurchaseOrderLineID == uuid.Nil {
			return fmt.Errorf("invoice line %d has an empty purchase_order_line_id", i)
		}
	}
	return nil
}

func validateInvoiceMatches(line CreatePurchaseInvoiceLineRequest) error {
	seen := make(map[uuid.UUID]struct{}, len(line.Matches))
	var matched Quantity
	for _, match := range line.Matches {
		if match.PurchaseReceiptLineID == uuid.Nil || match.MatchedQuantity <= 0 {
			return fmt.Errorf("receipt match requires a receipt line and positive quantity")
		}
		if _, ok := seen[match.PurchaseReceiptLineID]; ok {
			return fmt.Errorf("receipt line may be matched only once per invoice line")
		}
		seen[match.PurchaseReceiptLineID] = struct{}{}
		if match.MatchedQuantity > line.InvoicedQuantity-matched {
			return fmt.Errorf("matched quantity exceeds invoiced quantity")
		}
		matched += match.MatchedQuantity
	}
	if matched != line.InvoicedQuantity {
		return fmt.Errorf("receipt match quantities must equal invoiced quantity")
	}
	return nil
}

type CreatePurchaseInvoiceLineRequest struct {
	PurchaseOrderLineID *uuid.UUID                          `json:"purchase_order_line_id" validate:"omitempty"`
	Description         string                              `json:"description" validate:"required,not_blank,max=500"`
	MedicineID          *uuid.UUID                          `json:"medicine_id" validate:"omitempty"`
	VariantID           *uuid.UUID                          `json:"variant_id" validate:"omitempty"`
	UnitID              *uuid.UUID                          `json:"unit_id" validate:"omitempty"`
	InvoicedQuantity    Quantity                            `json:"invoiced_quantity" validate:"gt=0"`
	UnitPriceMinor      AmountMinor                         `json:"unit_price_minor" validate:"gte=0"`
	DiscountRateBPS     RateBasisPoints                     `json:"discount_rate_bps" validate:"gte=0,lte=10000"`
	TaxRateBPS          RateBasisPoints                     `json:"tax_rate_bps" validate:"gte=0,lte=10000"`
	Matches             []CreatePurchaseInvoiceMatchRequest `json:"matches" validate:"required,min=1,max=500,dive"`
}

type CreatePurchaseInvoiceMatchRequest struct {
	PurchaseReceiptLineID uuid.UUID `json:"purchase_receipt_line_id" validate:"required"`
	MatchedQuantity       Quantity  `json:"matched_quantity" validate:"gt=0"`
}

type DisputePurchaseInvoiceRequest struct {
	Reason             string   `json:"reason" validate:"required,not_blank,max=1000"`
	EvidenceReferences []string `json:"evidence_references" validate:"required,min=1,max=20,dive,required,not_blank,max=500"`
}

type ReversePurchaseInvoiceRequest struct {
	Reason             string   `json:"reason" validate:"required,not_blank,max=1000"`
	EvidenceReferences []string `json:"evidence_references" validate:"required,min=1,max=20,dive,required,not_blank,max=500"`
}

type CreatePurchasePaymentRequest struct {
	AmountMinor       AmountMinor                              `json:"amount_minor" validate:"gt=0"`
	MethodCode        string                                   `json:"method_code" validate:"required,not_blank,max=32"`
	PaymentDate       time.Time                                `json:"payment_date" validate:"required"`
	ValueDate         *time.Time                               `json:"value_date" validate:"omitempty"`
	ExternalReference *string                                  `json:"external_reference" validate:"omitempty,max=120"`
	Notes             *string                                  `json:"notes" validate:"omitempty,max=2000"`
	Allocations       []CreatePurchasePaymentAllocationRequest `json:"allocations" validate:"required,min=1,max=100,dive"`
}

func (r CreatePurchasePaymentRequest) Validate() error {
	if r.AmountMinor <= 0 || strings.TrimSpace(r.MethodCode) == "" {
		return fmt.Errorf("positive amount_minor and method_code are required")
	}
	if len(r.Allocations) == 0 || len(r.Allocations) > 100 {
		return fmt.Errorf("payment must allocate to between 1 and 100 invoices")
	}
	seen := make(map[uuid.UUID]struct{}, len(r.Allocations))
	var allocated AmountMinor
	for _, allocation := range r.Allocations {
		if allocation.PurchaseInvoiceID == uuid.Nil || allocation.AmountMinor <= 0 {
			return fmt.Errorf("each allocation requires an invoice and positive amount")
		}
		if _, ok := seen[allocation.PurchaseInvoiceID]; ok {
			return fmt.Errorf("invoice may be allocated only once per payment")
		}
		seen[allocation.PurchaseInvoiceID] = struct{}{}
		if allocation.AmountMinor > r.AmountMinor-allocated {
			return fmt.Errorf("payment allocations exceed payment amount")
		}
		allocated += allocation.AmountMinor
	}
	if allocated != r.AmountMinor {
		return fmt.Errorf("payment allocations must equal payment amount")
	}
	return nil
}

type CreatePurchasePaymentAllocationRequest struct {
	PurchaseInvoiceID uuid.UUID   `json:"purchase_invoice_id" validate:"required"`
	AmountMinor       AmountMinor `json:"amount_minor" validate:"gt=0"`
}

type ReversePurchasePaymentRequest struct {
	Reason string `json:"reason" validate:"required,not_blank,max=1000"`
}

type CreatePurchaseReturnRequest struct {
	WarehouseID        uuid.UUID                         `json:"warehouse_id" validate:"required"`
	Reason             string                            `json:"reason" validate:"required,not_blank,max=1000"`
	EvidenceReferences []string                          `json:"evidence_references" validate:"required,min=1,max=20,dive,required,not_blank,max=500"`
	Lines              []CreatePurchaseReturnLineRequest `json:"lines" validate:"required,min=1,max=500,dive"`
}

type CreatePurchaseReturnLineRequest struct {
	PurchaseReceiptLineID uuid.UUID `json:"purchase_receipt_line_id" validate:"required"`
	ReturnQuantity        Quantity  `json:"return_quantity" validate:"gt=0"`
	Notes                 *string   `json:"notes" validate:"omitempty,max=1000"`
}

func (r CreatePurchaseReturnRequest) Validate() error {
	if r.WarehouseID == uuid.Nil || strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("warehouse_id and reason are required")
	}
	if len(r.Lines) == 0 || len(r.Lines) > maxPurchaseLines {
		return fmt.Errorf("return must contain between 1 and %d lines", maxPurchaseLines)
	}
	seen := make(map[uuid.UUID]struct{}, len(r.Lines))
	for _, line := range r.Lines {
		if line.PurchaseReceiptLineID == uuid.Nil || line.ReturnQuantity <= 0 {
			return fmt.Errorf("each return line requires a receipt line and positive quantity")
		}
		if _, ok := seen[line.PurchaseReceiptLineID]; ok {
			return fmt.Errorf("receipt line may appear only once in a return")
		}
		seen[line.PurchaseReceiptLineID] = struct{}{}
	}
	return nil
}

type ListPurchaseDocumentQuery struct {
	pagination.Offset
	From *time.Time `form:"from"`
	To   *time.Time `form:"to"`
}

func (q *ListPurchaseDocumentQuery) NormalizeAndValidate() error {
	if q == nil {
		return fmt.Errorf("purchase document query is required")
	}
	q.Offset.Normalize()
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		return fmt.Errorf("from must be earlier than to")
	}
	return nil
}

// Response types are intentionally separate from persistence models so
// tenant identifiers, operation internals, and future columns are not exposed
// accidentally.
type PurchaseOrderResponse struct {
	ID                 uuid.UUID                   `json:"id"`
	BranchID           uuid.UUID                   `json:"branch_id"`
	SupplierID         uuid.UUID                   `json:"supplier_id"`
	PurchaseNo         string                      `json:"purchase_no"`
	ReferenceNo        *string                     `json:"reference_no,omitempty"`
	OrderStatus        PurchaseOrderStatus         `json:"order_status"`
	ReceiptStatus      PurchaseReceiptStatus       `json:"receipt_status"`
	InvoiceStatus      PurchaseInvoiceStatus       `json:"invoice_status"`
	ClosureStatus      PurchaseClosureStatus       `json:"closure_status"`
	CurrencyCode       string                      `json:"currency_code"`
	RequestedBy        uuid.UUID                   `json:"requested_by"`
	SubmittedBy        *uuid.UUID                  `json:"submitted_by,omitempty"`
	SubmittedAt        *time.Time                  `json:"submitted_at,omitempty"`
	ApprovedBy         *uuid.UUID                  `json:"approved_by,omitempty"`
	ApprovedAt         *time.Time                  `json:"approved_at,omitempty"`
	RejectedBy         *uuid.UUID                  `json:"rejected_by,omitempty"`
	RejectedAt         *time.Time                  `json:"rejected_at,omitempty"`
	RejectionReason    *string                     `json:"rejection_reason,omitempty"`
	ExpectedDeliveryAt *time.Time                  `json:"expected_delivery_at,omitempty"`
	OrderedAt          *time.Time                  `json:"ordered_at,omitempty"`
	CancelledAt        *time.Time                  `json:"cancelled_at,omitempty"`
	ClosedAt           *time.Time                  `json:"closed_at,omitempty"`
	Notes              *string                     `json:"notes,omitempty"`
	Version            int64                       `json:"version"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	Lines              []PurchaseOrderLineResponse `json:"lines,omitempty"`
}

type PurchaseOrderLineResponse struct {
	ID                  uuid.UUID   `json:"id"`
	LineNo              int32       `json:"line_no"`
	MedicineID          uuid.UUID   `json:"medicine_id"`
	VariantID           *uuid.UUID  `json:"variant_id,omitempty"`
	UnitID              uuid.UUID   `json:"unit_id"`
	OrderedQuantity     Quantity    `json:"ordered_quantity"`
	AcceptedQuantity    Quantity    `json:"accepted_quantity"`
	UnitPriceMinor      AmountMinor `json:"unit_price_minor"`
	DiscountAmountMinor AmountMinor `json:"discount_amount_minor"`
	TaxAmountMinor      AmountMinor `json:"tax_amount_minor"`
	LineTotalMinor      AmountMinor `json:"line_total_minor"`
	ProductName         string      `json:"product_name"`
	SKU                 *string     `json:"sku,omitempty"`
	UnitName            string      `json:"unit_name"`
	Version             int64       `json:"version"`
}

type PurchaseApprovalResponse struct {
	ID                    uuid.UUID                `json:"id"`
	PurchaseOrderID       uuid.UUID                `json:"purchase_order_id"`
	Revision              int32                    `json:"revision"`
	Stage                 int16                    `json:"stage"`
	Decision              PurchaseApprovalDecision `json:"decision"`
	ActorID               uuid.UUID                `json:"actor_id"`
	Reason                *string                  `json:"reason,omitempty"`
	ApprovalPolicyVersion string                   `json:"approval_policy_version"`
	DecidedAt             time.Time                `json:"decided_at"`
}

type PurchaseReceiptResponse struct {
	ID                uuid.UUID                     `json:"id"`
	BranchID          uuid.UUID                     `json:"branch_id"`
	WarehouseID       uuid.UUID                     `json:"warehouse_id"`
	PurchaseOrderID   uuid.UUID                     `json:"purchase_order_id"`
	ReceiptNo         string                        `json:"receipt_no"`
	DeliveryChallanNo *string                       `json:"delivery_challan_no,omitempty"`
	State             PurchaseReceiptState          `json:"state"`
	ReceivedBy        uuid.UUID                     `json:"received_by"`
	ReceivedAt        time.Time                     `json:"received_at"`
	PostedAt          *time.Time                    `json:"posted_at,omitempty"`
	Notes             *string                       `json:"notes,omitempty"`
	Version           int64                         `json:"version"`
	Lines             []PurchaseReceiptLineResponse `json:"lines,omitempty"`
}

type PurchaseReceiptLineResponse struct {
	ID                       uuid.UUID   `json:"id"`
	PurchaseOrderLineID      uuid.UUID   `json:"purchase_order_line_id"`
	LineNo                   int32       `json:"line_no"`
	DeliveredQuantity        Quantity    `json:"delivered_quantity"`
	AcceptedQuantity         Quantity    `json:"accepted_quantity"`
	RejectedQuantity         Quantity    `json:"rejected_quantity"`
	BatchNumber              string      `json:"batch_number"`
	ExpiryAt                 time.Time   `json:"expiry_at"`
	UnitAcquisitionCostMinor AmountMinor `json:"unit_acquisition_cost_minor"`
	CurrencyCode             string      `json:"currency_code"`
}

type PurchaseInvoiceResponse struct {
	ID                 uuid.UUID                     `json:"id"`
	BranchID           uuid.UUID                     `json:"branch_id"`
	SupplierID         uuid.UUID                     `json:"supplier_id"`
	PurchaseOrderID    *uuid.UUID                    `json:"purchase_order_id,omitempty"`
	InvoiceNo          string                        `json:"invoice_no"`
	CurrencyCode       string                        `json:"currency_code"`
	InvoiceDate        time.Time                     `json:"invoice_date"`
	ReceivedAt         time.Time                     `json:"received_at"`
	DueAt              *time.Time                    `json:"due_at,omitempty"`
	SubtotalMinor      AmountMinor                   `json:"subtotal_minor"`
	DiscountMinor      AmountMinor                   `json:"discount_minor"`
	TaxMinor           AmountMinor                   `json:"tax_minor"`
	FreightMinor       AmountMinor                   `json:"freight_minor"`
	OtherChargesMinor  AmountMinor                   `json:"other_charges_minor"`
	TotalMinor         AmountMinor                   `json:"total_minor"`
	Status             PurchaseInvoiceState          `json:"status"`
	EvidenceReferences []string                      `json:"evidence_references,omitempty"`
	PostedAt           *time.Time                    `json:"posted_at,omitempty"`
	Version            int64                         `json:"version"`
	Lines              []PurchaseInvoiceLineResponse `json:"lines,omitempty"`
}

type PurchaseInvoiceLineResponse struct {
	ID                  uuid.UUID                      `json:"id"`
	LineNo              int32                          `json:"line_no"`
	Description         string                         `json:"description"`
	PurchaseOrderLineID *uuid.UUID                     `json:"purchase_order_line_id,omitempty"`
	InvoicedQuantity    Quantity                       `json:"invoiced_quantity"`
	UnitPriceMinor      AmountMinor                    `json:"unit_price_minor"`
	DiscountMinor       AmountMinor                    `json:"discount_minor"`
	TaxMinor            AmountMinor                    `json:"tax_minor"`
	LineTotalMinor      AmountMinor                    `json:"line_total_minor"`
	Matches             []PurchaseInvoiceMatchResponse `json:"matches,omitempty"`
}

type PurchaseInvoiceMatchResponse struct {
	ID                          uuid.UUID                `json:"id"`
	PurchaseOrderLineID         *uuid.UUID               `json:"purchase_order_line_id,omitempty"`
	PurchaseReceiptLineID       *uuid.UUID               `json:"purchase_receipt_line_id,omitempty"`
	MatchType                   PurchaseInvoiceMatchType `json:"match_type"`
	MatchedQuantity             Quantity                 `json:"matched_quantity"`
	MatchedAmountMinor          AmountMinor              `json:"matched_amount_minor"`
	ExceptionReason             *string                  `json:"exception_reason,omitempty"`
	ExceptionEvidenceReferences []string                 `json:"exception_evidence_references,omitempty"`
	ExceptionApprovedBy         *uuid.UUID               `json:"exception_approved_by,omitempty"`
	ExceptionApprovedAt         *time.Time               `json:"exception_approved_at,omitempty"`
}

type PurchasePaymentResponse struct {
	ID                uuid.UUID                           `json:"id"`
	BranchID          uuid.UUID                           `json:"branch_id"`
	SupplierID        uuid.UUID                           `json:"supplier_id"`
	PaymentNo         string                              `json:"payment_no"`
	CurrencyCode      string                              `json:"currency_code"`
	AmountMinor       AmountMinor                         `json:"amount_minor"`
	MethodCode        string                              `json:"method_code"`
	PaymentDate       time.Time                           `json:"payment_date"`
	ValueDate         *time.Time                          `json:"value_date,omitempty"`
	Status            PurchasePaymentStatus               `json:"status"`
	ExternalReference *string                             `json:"external_reference,omitempty"`
	PaidBy            uuid.UUID                           `json:"paid_by"`
	SettledAt         *time.Time                          `json:"settled_at,omitempty"`
	ReversalOfID      *uuid.UUID                          `json:"reversal_of_id,omitempty"`
	ReversedAt        *time.Time                          `json:"reversed_at,omitempty"`
	Version           int64                               `json:"version"`
	Allocations       []PurchasePaymentAllocationResponse `json:"allocations,omitempty"`
}

type PurchasePaymentAllocationResponse struct {
	PurchaseInvoiceID uuid.UUID   `json:"purchase_invoice_id"`
	AmountMinor       AmountMinor `json:"amount_minor"`
	CurrencyCode      string      `json:"currency_code"`
}

type PurchaseReturnResponse struct {
	ID                 uuid.UUID                    `json:"id"`
	BranchID           uuid.UUID                    `json:"branch_id"`
	WarehouseID        uuid.UUID                    `json:"warehouse_id"`
	SupplierID         uuid.UUID                    `json:"supplier_id"`
	PurchaseOrderID    *uuid.UUID                   `json:"purchase_order_id,omitempty"`
	ReturnNo           string                       `json:"return_no"`
	Status             PurchaseReturnStatus         `json:"status"`
	Reason             string                       `json:"reason"`
	EvidenceReferences []string                     `json:"evidence_references,omitempty"`
	RequestedBy        uuid.UUID                    `json:"requested_by"`
	ApprovedAt         *time.Time                   `json:"approved_at,omitempty"`
	DispatchedAt       *time.Time                   `json:"dispatched_at,omitempty"`
	PostedAt           *time.Time                   `json:"posted_at,omitempty"`
	Version            int64                        `json:"version"`
	Lines              []PurchaseReturnLineResponse `json:"lines,omitempty"`
}

type PurchaseReturnLineResponse struct {
	ID                    uuid.UUID   `json:"id"`
	PurchaseReceiptLineID uuid.UUID   `json:"purchase_receipt_line_id"`
	InventoryBatchID      uuid.UUID   `json:"inventory_batch_id"`
	LineNo                int32       `json:"line_no"`
	ReturnQuantity        Quantity    `json:"return_quantity"`
	AcquisitionCostMinor  AmountMinor `json:"acquisition_cost_minor"`
	CurrencyCode          string      `json:"currency_code"`
}

type PurchaseTransitionResponse struct {
	ID              uuid.UUID                `json:"id"`
	PurchaseOrderID uuid.UUID                `json:"purchase_order_id"`
	Dimension       string                   `json:"dimension"`
	FromState       *string                  `json:"from_state,omitempty"`
	ToState         string                   `json:"to_state"`
	Action          PurchaseTransitionAction `json:"action"`
	ActorID         uuid.UUID                `json:"actor_id"`
	Reason          *string                  `json:"reason,omitempty"`
	PolicyVersion   *string                  `json:"policy_version,omitempty"`
	OccurredAt      time.Time                `json:"occurred_at"`
}

func validPurchaseOrderStatus(status PurchaseOrderStatus) bool {
	switch status {
	case PurchaseOrderStatusDraft, PurchaseOrderStatusPendingApproval,
		PurchaseOrderStatusApproved, PurchaseOrderStatusOrdered,
		PurchaseOrderStatusRejected, PurchaseOrderStatusCancelled:
		return true
	default:
		return false
	}
}

func validPurchaseReceiptStatus(status PurchaseReceiptStatus) bool {
	switch status {
	case PurchaseReceiptStatusNotReceived, PurchaseReceiptStatusPartial, PurchaseReceiptStatusFull:
		return true
	default:
		return false
	}
}

func validPurchaseInvoiceStatus(status PurchaseInvoiceStatus) bool {
	switch status {
	case PurchaseInvoiceStatusNotInvoiced, PurchaseInvoiceStatusPartial,
		PurchaseInvoiceStatusFull, PurchaseInvoiceStatusDisputed:
		return true
	default:
		return false
	}
}

func validPurchaseClosureStatus(status PurchaseClosureStatus) bool {
	return status == PurchaseClosureStatusOpen || status == PurchaseClosureStatusClosed
}
