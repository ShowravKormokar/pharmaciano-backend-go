package sale

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	"backend/pkg/pagination"
)

const (
	maxReasonLen     = 500
	maxSearchLen     = pagination.MaxQueryLen
	maxNotesLen      = 2000
	maxReturnReason  = 500
	maxVoidReason    = 500
	maxRefundReason  = 500
	maxEvidenceRefLen = 500
)

type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message,omitempty"`
}

func (e *FieldError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "validation error on field " + e.Field
}

func newFieldError(field, rule, message string) error {
	return &FieldError{Field: field, Rule: rule, Message: message}
}

type SaleItemRequest struct {
	MedicineID uuid.UUID `json:"medicine_id" validate:"required"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty" validate:"omitempty"`
	UnitID     uuid.UUID `json:"unit_id" validate:"required"`

	EnteredQuantity int64 `json:"entered_quantity" validate:"required,gt=0"`

	UnitPriceMinor  int64  `json:"unit_price_minor" validate:"required,gte=0"`
	DiscountMinor   int64  `json:"discount_minor" validate:"omitempty,gte=0"`
	DiscountRateBPS int32  `json:"discount_rate_bps" validate:"omitempty,gte=0,lte=10000"`
	TaxRateBPS      int32  `json:"tax_rate_bps" validate:"omitempty,gte=0,lte=10000"`

	CouponID *uuid.UUID `json:"coupon_id,omitempty" validate:"omitempty"`
}

func (r *SaleItemRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("item", "required", "sale item is required")}
	}
	var errs []error
	if r.MedicineID == uuid.Nil {
		errs = append(errs, newFieldError("medicine_id", "required", "medicine_id is required"))
	}
	if r.UnitID == uuid.Nil {
		errs = append(errs, newFieldError("unit_id", "required", "unit_id is required"))
	}
	if r.EnteredQuantity <= 0 {
		errs = append(errs, newFieldError("entered_quantity", "gt", "entered_quantity must be positive"))
	}
	if r.UnitPriceMinor < 0 {
		errs = append(errs, newFieldError("unit_price_minor", "gte", "unit_price_minor cannot be negative"))
	}
	if r.DiscountMinor < 0 {
		errs = append(errs, newFieldError("discount_minor", "gte", "discount_minor cannot be negative"))
	}
	if r.DiscountRateBPS < 0 || r.DiscountRateBPS > 10000 {
		errs = append(errs, newFieldError("discount_rate_bps", "range", "discount_rate_bps must be 0-10000"))
	}
	if r.TaxRateBPS < 0 || r.TaxRateBPS > 10000 {
		errs = append(errs, newFieldError("tax_rate_bps", "range", "tax_rate_bps must be 0-10000"))
	}
	return errs
}

type TenderRequest struct {
	Method       TenderMethod `json:"method" validate:"required,oneof=cash card mobile bank store_credit provider other"`
	AmountMinor  int64        `json:"amount_minor" validate:"required,gt=0"`
	CurrencyCode string       `json:"currency_code" validate:"required,len=3"`
	ReferenceNo  *string      `json:"reference_no,omitempty" validate:"omitempty,max=100"`
}

func (r *TenderRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("tender", "required", "tender is required")}
	}
	var errs []error
	if r.AmountMinor <= 0 {
		errs = append(errs, newFieldError("amount_minor", "gt", "amount_minor must be positive"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	return errs
}

type CheckoutRequest struct {
	SaleType      SaleType         `json:"sale_type" validate:"required,oneof=retail wholesale online"`
	WarehouseID   uuid.UUID        `json:"warehouse_id" validate:"required"`
	CustomerID    *uuid.UUID       `json:"customer_id,omitempty" validate:"omitempty"`
	CashSessionID *uuid.UUID       `json:"cash_session_id,omitempty" validate:"omitempty"`

	Items   []SaleItemRequest `json:"items" validate:"required,min=1,dive"`
	Tenders []TenderRequest   `json:"tenders" validate:"required,min=1,dive"`

	CouponID       *uuid.UUID `json:"coupon_id,omitempty" validate:"omitempty"`
	PrescriptionID *uuid.UUID `json:"prescription_id,omitempty" validate:"omitempty"`
	Notes          *string    `json:"notes,omitempty" validate:"omitempty,max=2000"`

	IdempotencyKey string `json:"-" validate:"required"`
}

func (r *CheckoutRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "checkout request is required")}
	}
	var errs []error

	if r.WarehouseID == uuid.Nil {
		errs = append(errs, newFieldError("warehouse_id", "required", "warehouse_id is required"))
	}

if len(r.Items) == 0 {
			errs = append(errs, newFieldError("items", "min", "at least one sale item is required"))
		} else {
			for i, item := range r.Items {
				if itemErrs := item.Validate(); len(itemErrs) > 0 {
					for _, e := range itemErrs {
						if fe, ok := e.(*FieldError); ok {
							errs = append(errs, newFieldError("items["+string(rune('0'+i))+"]."+fe.Field, fe.Rule, fe.Message))
						}
					}
				}
			}
		}

		if len(r.Tenders) == 0 {
			errs = append(errs, newFieldError("tenders", "min", "at least one tender is required"))
		} else {
			for i, tender := range r.Tenders {
				if tenderErrs := tender.Validate(); len(tenderErrs) > 0 {
					for _, e := range tenderErrs {
						if fe, ok := e.(*FieldError); ok {
							errs = append(errs, newFieldError("tenders["+string(rune('0'+i))+"]."+fe.Field, fe.Rule, fe.Message))
						}
					}
				}
			}
		}

	if r.Notes != nil && len(*r.Notes) > maxNotesLen {
		errs = append(errs, newFieldError("notes", "max", "notes exceeds maximum length"))
	}

	return errs
}

type CalculateRequest struct {
	SaleType      SaleType         `json:"sale_type" validate:"required,oneof=retail wholesale online"`
	WarehouseID   uuid.UUID        `json:"warehouse_id" validate:"required"`
	CustomerID    *uuid.UUID       `json:"customer_id,omitempty" validate:"omitempty"`

	Items     []SaleItemRequest `json:"items" validate:"required,min=1,dive"`
	CouponID  *uuid.UUID        `json:"coupon_id,omitempty" validate:"omitempty"`
	Notes     *string           `json:"notes,omitempty" validate:"omitempty,max=2000"`
}

func (r *CalculateRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "calculate request is required")}
	}
	var errs []error

	if r.WarehouseID == uuid.Nil {
		errs = append(errs, newFieldError("warehouse_id", "required", "warehouse_id is required"))
	}

if len(r.Items) == 0 {
			errs = append(errs, newFieldError("items", "min", "at least one item is required"))
		} else {
			for i, item := range r.Items {
				if itemErrs := item.Validate(); len(itemErrs) > 0 {
					for _, e := range itemErrs {
						if fe, ok := e.(*FieldError); ok {
							errs = append(errs, newFieldError("items["+string(rune('0'+i))+"]."+fe.Field, fe.Rule, fe.Message))
						}
					}
				}
			}
		}

	if r.Notes != nil && len(*r.Notes) > maxNotesLen {
		errs = append(errs, newFieldError("notes", "max", "notes exceeds maximum length"))
	}

	return errs
}

type LookupRequest struct {
	Barcode *string `json:"barcode,omitempty" validate:"omitempty,not_blank,max=60"`
	SKU     *string `json:"sku,omitempty" validate:"omitempty,not_blank,max=60"`
	Name    *string `json:"name,omitempty" validate:"omitempty,not_blank,max=200"`
}

func (r *LookupRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "lookup request is required")}
	}
	var errs []error

	hasBarcode := r.Barcode != nil && strings.TrimSpace(*r.Barcode) != ""
	hasSKU := r.SKU != nil && strings.TrimSpace(*r.SKU) != ""
	hasName := r.Name != nil && strings.TrimSpace(*r.Name) != ""

	count := 0
	if hasBarcode {
		count++
	}
	if hasSKU {
		count++
	}
	if hasName {
		count++
	}

	if count != 1 {
		errs = append(errs, newFieldError("lookup", "exclusive", "provide exactly one of barcode, sku, or name"))
	}

	return errs
}

type ApplyCouponRequest struct {
	Code     string     `json:"code" validate:"required,not_blank,max=50"`
	Subtotal int64      `json:"subtotal" validate:"required,gte=0"`
	CustomerID *uuid.UUID `json:"customer_id,omitempty" validate:"omitempty"`
}

func (r *ApplyCouponRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "apply coupon request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Code) == "" {
		errs = append(errs, newFieldError("code", "required", "coupon code is required"))
	}
	if r.Subtotal < 0 {
		errs = append(errs, newFieldError("subtotal", "gte", "subtotal cannot be negative"))
	}
	return errs
}

type CreateSaleRequest struct {
	SaleType      SaleType         `json:"sale_type" validate:"required,oneof=retail wholesale online"`
	WarehouseID   uuid.UUID        `json:"warehouse_id" validate:"required"`
	CustomerID    *uuid.UUID       `json:"customer_id,omitempty" validate:"omitempty"`
	CashSessionID *uuid.UUID       `json:"cash_session_id,omitempty" validate:"omitempty"`

	Items   []SaleItemRequest `json:"items" validate:"required,min=1,dive"`
	Tenders []TenderRequest   `json:"tenders" validate:"required,min=1,dive"`

	CouponID       *uuid.UUID `json:"coupon_id,omitempty" validate:"omitempty"`
	PrescriptionID *uuid.UUID `json:"prescription_id,omitempty" validate:"omitempty"`
	Notes          *string    `json:"notes,omitempty" validate:"omitempty,max=2000"`

	IdempotencyKey string `json:"-" validate:"required"`
}

func (r *CreateSaleRequest) Validate() []error {
	return (&CheckoutRequest{
		SaleType:      r.SaleType,
		WarehouseID:   r.WarehouseID,
		CustomerID:    r.CustomerID,
		CashSessionID: r.CashSessionID,
		Items:         r.Items,
		Tenders:       r.Tenders,
		CouponID:      r.CouponID,
		PrescriptionID: r.PrescriptionID,
		Notes:         r.Notes,
		IdempotencyKey: r.IdempotencyKey,
	}).Validate()
}

type SaleItemResponse struct {
	ID              uuid.UUID `json:"id"`
	LineNo          int32     `json:"line_no"`

	MedicineID uuid.UUID  `json:"medicine_id"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty"`
	UnitID     uuid.UUID  `json:"unit_id"`

	EnteredQuantity           int64 `json:"entered_quantity"`
	BaseQuantity              int64 `json:"base_quantity"`
	UnitConversionNumerator   int64 `json:"unit_conversion_numerator"`
	UnitConversionDenominator int64 `json:"unit_conversion_denominator"`

	UnitPriceMinor  int64  `json:"unit_price_minor"`
	DiscountMinor   int64  `json:"discount_minor"`
	TaxMinor        int64  `json:"tax_minor"`
	LineTotalMinor  int64  `json:"line_total_minor"`
	DiscountRateBPS int32  `json:"discount_rate_bps"`
	TaxRateBPS      int32  `json:"tax_rate_bps"`

	ProductSnapshot common.JSONMap `json:"product_snapshot"`
	UnitSnapshot    common.JSONMap `json:"unit_snapshot"`
	TaxSnapshot     common.JSONMap `json:"tax_snapshot"`

	CreatedAt time.Time `json:"created_at"`
}

type SaleTenderResponse struct {
	ID              uuid.UUID  `json:"id"`
	TenderNo        string     `json:"tender_no"`
	Method          TenderMethod `json:"method"`
	AmountMinor     int64      `json:"amount_minor"`
	CurrencyCode    string     `json:"currency_code"`
	ReferenceNo     *string    `json:"reference_no,omitempty"`
	ReceivedAt      time.Time  `json:"received_at"`
	ReceivedBy      uuid.UUID  `json:"received_by"`
	SettlementRef   *string    `json:"settlement_ref,omitempty"`
}

type SalePaymentIntentResponse struct {
	ID                  uuid.UUID          `json:"id"`
	ProviderCode        string             `json:"provider_code"`
	AmountMinor         int64              `json:"amount_minor"`
	CurrencyCode        string             `json:"currency_code"`
	Status              PaymentIntentStatus `json:"status"`
	ExternalReference   *string            `json:"external_reference,omitempty"`
	ExpiresAt           time.Time          `json:"expires_at"`
	SubmittedAt         *time.Time         `json:"submitted_at,omitempty"`
	SettledAt           *time.Time         `json:"settled_at,omitempty"`
	FailedAt            *time.Time         `json:"failed_at,omitempty"`
	FailureCode         *string            `json:"failure_code,omitempty"`
}

type SaleInvoiceResponse struct {
	ID             uuid.UUID `json:"id"`
	Series         string    `json:"series"`
	FiscalKey      string    `json:"fiscal_key"`
	InvoiceNo      string    `json:"invoice_no"`
	IssuedAt       time.Time `json:"issued_at"`
	CurrencyCode   string    `json:"currency_code"`
	Snapshot       common.JSONMap `json:"snapshot"`
	SnapshotHash   string    `json:"snapshot_hash"`
	TemplateVersion string   `json:"template_version"`
}

type SaleResponse struct {
	ID              uuid.UUID  `json:"id"`
	OrganizationID  uuid.UUID  `json:"organization_id"`
	BranchID        uuid.UUID  `json:"branch_id"`
	WarehouseID     uuid.UUID  `json:"warehouse_id"`
	CashierID       uuid.UUID  `json:"cashier_id"`
	CustomerID      *uuid.UUID `json:"customer_id,omitempty"`
	CashSessionID   *uuid.UUID `json:"cash_session_id,omitempty"`
	OperationID     uuid.UUID  `json:"operation_id"`

	SaleNo        string            `json:"sale_no"`
	SaleType      SaleType          `json:"sale_type"`
	Status        SaleStatus        `json:"status"`
	PaymentStatus SalePaymentStatus `json:"payment_status"`
	InvoiceStatus SaleInvoiceStatus `json:"invoice_status"`

	CurrencyCode string `json:"currency_code"`

	SubtotalMinor       int64 `json:"subtotal_minor"`
	DiscountMinor       int64 `json:"discount_minor"`
	CouponDiscountMinor int64 `json:"coupon_discount_minor"`
	TaxMinor            int64 `json:"tax_minor"`
	OtherChargesMinor   int64 `json:"other_charges_minor"`
	TotalMinor          int64 `json:"total_minor"`

	PaidMinor   int64 `json:"paid_minor"`
	ChangeMinor int64 `json:"change_minor"`

	CouponID       *uuid.UUID `json:"coupon_id,omitempty"`
	PrescriptionID *uuid.UUID `json:"prescription_id,omitempty"`
	Notes          *string    `json:"notes,omitempty"`

	PricingSnapshot common.JSONMap `json:"pricing_snapshot"`
	TaxSnapshot     common.JSONMap `json:"tax_snapshot"`
	PolicyVersion   string         `json:"policy_version"`

	Items    []SaleItemResponse    `json:"items,omitempty"`
	Tenders  []SaleTenderResponse  `json:"tenders,omitempty"`
	Intents  []SalePaymentIntentResponse `json:"intents,omitempty"`
	Invoice  *SaleInvoiceResponse  `json:"invoice,omitempty"`

	SoldAt      time.Time  `json:"sold_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	VoidedAt    *time.Time `json:"voided_at,omitempty"`
	VoidedBy    *uuid.UUID `json:"voided_by,omitempty"`
	VoidReason  *string    `json:"void_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SaleListItem struct {
	ID              uuid.UUID  `json:"id"`
	SaleNo          string     `json:"sale_no"`
	SaleType        SaleType   `json:"sale_type"`
	Status          SaleStatus `json:"status"`
	PaymentStatus   SalePaymentStatus `json:"payment_status"`
	InvoiceStatus   SaleInvoiceStatus `json:"invoice_status"`

	CustomerID      *uuid.UUID `json:"customer_id,omitempty"`
	CashierID       uuid.UUID  `json:"cashier_id"`
	CashSessionID   *uuid.UUID `json:"cash_session_id,omitempty"`

	CurrencyCode string `json:"currency_code"`
	TotalMinor   int64  `json:"total_minor"`
	PaidMinor    int64  `json:"paid_minor"`

	SoldAt      time.Time  `json:"sold_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type ListSalesQuery struct {
	pagination.CursorQuery

	BranchID      *uuid.UUID        `form:"branch_id"`
	CashierID     *uuid.UUID        `form:"cashier_id"`
	CustomerID    *uuid.UUID        `form:"customer_id"`
	Status        *SaleStatus       `form:"status"`
	PaymentStatus *SalePaymentStatus `form:"payment_status"`
	InvoiceStatus *SaleInvoiceStatus `form:"invoice_status"`
	FromDate      *time.Time        `form:"from"`
	ToDate        *time.Time        `form:"to"`
	Q             string            `form:"q"`
}

func (q *ListSalesQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{newFieldError("query", "required", "query is required")}
	}
	var errs []error

	q.CursorQuery.NormalizeWithMax(100)

	if q.Q != "" {
		q.Q = strings.TrimSpace(q.Q)
		if len(q.Q) > maxSearchLen {
			errs = append(errs, newFieldError("q", "max", "search term exceeds maximum length"))
		}
	}

	if q.FromDate != nil && q.ToDate != nil && !q.FromDate.Before(*q.ToDate) {
		errs = append(errs, newFieldError("from", "before", "from_date must be before to_date"))
	}

	return errs
}

type VoidSaleRequest struct {
	Reason string `json:"reason" validate:"required,not_blank,max=500"`
	IdempotencyKey string `json:"-" validate:"required"`
}

func (r *VoidSaleRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "void request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Reason) == "" {
		errs = append(errs, newFieldError("reason", "required", "void reason is required"))
	}
	if len(r.Reason) > maxVoidReason {
		errs = append(errs, newFieldError("reason", "max", "reason exceeds maximum length"))
	}
	return errs
}

type RefundSaleRequest struct {
	AmountMinor     int64        `json:"amount_minor" validate:"required,gt=0"`
	Method          TenderMethod `json:"method" validate:"required,oneof=cash card mobile bank store_credit provider other"`
	OriginalTenderID *uuid.UUID  `json:"original_tender_id,omitempty" validate:"omitempty"`
	Reason          string       `json:"reason" validate:"required,not_blank,max=500"`
	IdempotencyKey  string       `json:"-" validate:"required"`
}

func (r *RefundSaleRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "refund request is required")}
	}
	var errs []error
	if r.AmountMinor <= 0 {
		errs = append(errs, newFieldError("amount_minor", "gt", "amount_minor must be positive"))
	}
	if strings.TrimSpace(r.Reason) == "" {
		errs = append(errs, newFieldError("reason", "required", "refund reason is required"))
	}
	if len(r.Reason) > maxRefundReason {
		errs = append(errs, newFieldError("reason", "max", "reason exceeds maximum length"))
	}
	return errs
}

type CollectPaymentRequest struct {
	AmountMinor     int64        `json:"amount_minor" validate:"required,gt=0"`
	Method          TenderMethod `json:"method" validate:"required,oneof=cash card mobile bank store_credit provider other"`
	ReferenceNo     *string      `json:"reference_no,omitempty" validate:"omitempty,max=100"`
	Notes           *string      `json:"notes,omitempty" validate:"omitempty,max=1000"`
	IdempotencyKey  string       `json:"-" validate:"required"`
}

func (r *CollectPaymentRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "collect payment request is required")}
	}
	var errs []error
	if r.AmountMinor <= 0 {
		errs = append(errs, newFieldError("amount_minor", "gt", "amount_minor must be positive"))
	}
	if r.Notes != nil && len(*r.Notes) > 1000 {
		errs = append(errs, newFieldError("notes", "max", "notes exceeds maximum length"))
	}
	return errs
}

type CreateReturnRequest struct {
	SaleID       uuid.UUID `json:"sale_id" validate:"required"`
	Reason       string    `json:"reason" validate:"required,not_blank,max=500"`
	Items        []ReturnItemRequest `json:"items" validate:"required,min=1,dive"`
	IdempotencyKey string `json:"-" validate:"required"`
}

type ReturnItemRequest struct {
	SaleItemID        uuid.UUID `json:"sale_item_id" validate:"required"`
	StockAllocationID uuid.UUID `json:"stock_allocation_id" validate:"required"`
	BatchID           uuid.UUID `json:"batch_id" validate:"required"`
	Quantity          int64     `json:"quantity" validate:"required,gt=0"`
	Disposition       ReturnDisposition `json:"disposition" validate:"required,oneof=quarantine inspection restock dispose"`
	DispositionReason *string   `json:"disposition_reason,omitempty" validate:"omitempty,max=500"`
}

func (r *ReturnItemRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("item", "required", "return item is required")}
	}
	var errs []error
	if r.SaleItemID == uuid.Nil {
		errs = append(errs, newFieldError("sale_item_id", "required", "sale_item_id is required"))
	}
	if r.StockAllocationID == uuid.Nil {
		errs = append(errs, newFieldError("stock_allocation_id", "required", "stock_allocation_id is required"))
	}
	if r.BatchID == uuid.Nil {
		errs = append(errs, newFieldError("batch_id", "required", "batch_id is required"))
	}
	if r.Quantity <= 0 {
		errs = append(errs, newFieldError("quantity", "gt", "quantity must be positive"))
	}
	return errs
}

func (r *CreateReturnRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create return request is required")}
	}
	var errs []error
	if r.SaleID == uuid.Nil {
		errs = append(errs, newFieldError("sale_id", "required", "sale_id is required"))
	}
	if strings.TrimSpace(r.Reason) == "" {
		errs = append(errs, newFieldError("reason", "required", "return reason is required"))
	}
	if len(r.Reason) > maxReturnReason {
		errs = append(errs, newFieldError("reason", "max", "reason exceeds maximum length"))
	}
if len(r.Items) == 0 {
			errs = append(errs, newFieldError("items", "min", "at least one return item is required"))
		} else {
			for i, item := range r.Items {
				if itemErrs := item.Validate(); len(itemErrs) > 0 {
					for _, e := range itemErrs {
						if fe, ok := e.(*FieldError); ok {
							errs = append(errs, newFieldError("items["+string(rune('0'+i))+"]."+fe.Field, fe.Rule, fe.Message))
						}
					}
				}
			}
		}
	return errs
}

type ApproveReturnRequest struct {
	Items []ReturnApprovalItem `json:"items" validate:"required,min=1,dive"`
	IdempotencyKey string `json:"-" validate:"required"`
}

type ReturnApprovalItem struct {
	ReturnItemID uuid.UUID `json:"return_item_id" validate:"required"`
	Disposition  ReturnDisposition `json:"disposition" validate:"required,oneof=quarantine inspection restock dispose"`
	DispositionReason *string `json:"disposition_reason,omitempty" validate:"omitempty,max=500"`
}

func (r *ApproveReturnRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "approve return request is required")}
	}
	var errs []error
	if len(r.Items) == 0 {
		errs = append(errs, newFieldError("items", "min", "at least one approval item is required"))
	} else {
		for i, item := range r.Items {
			if item.ReturnItemID == uuid.Nil {
				errs = append(errs, newFieldError("items["+string(rune('0'+i))+"].return_item_id", "required", "return_item_id is required"))
			}
		}
	}
	return errs
}

type RejectReturnRequest struct {
	Reason string `json:"reason" validate:"required,not_blank,max=500"`
	IdempotencyKey string `json:"-" validate:"required"`
}

func (r *RejectReturnRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "reject return request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Reason) == "" {
		errs = append(errs, newFieldError("reason", "required", "rejection reason is required"))
	}
	if len(r.Reason) > maxReturnReason {
		errs = append(errs, newFieldError("reason", "max", "reason exceeds maximum length"))
	}
	return errs
}

type SalesReturnResponse struct {
	ID              uuid.UUID `json:"id"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	BranchID        uuid.UUID `json:"branch_id"`
	SaleID          uuid.UUID `json:"sale_id"`
	OperationID     uuid.UUID `json:"operation_id"`

	ReturnNo         string           `json:"return_no"`
	Status           SaleReturnStatus `json:"status"`
	Reason           string           `json:"reason"`
	CurrencyCode     string           `json:"currency_code"`
	CreditTotalMinor int64            `json:"credit_total_minor"`
	PolicySnapshot   common.JSONMap   `json:"policy_snapshot"`

	RequestedBy uuid.UUID  `json:"requested_by"`
	RequestedAt time.Time  `json:"requested_at"`
	ApprovedBy  *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	RejectedBy  *uuid.UUID `json:"rejected_by,omitempty"`
	RejectedAt  *time.Time `json:"rejected_at,omitempty"`
	RejectedReason *string `json:"rejected_reason,omitempty"`
	PostedBy    *uuid.UUID `json:"posted_by,omitempty"`
	PostedAt    *time.Time `json:"posted_at,omitempty"`

	Items []SalesReturnItemResponse `json:"items,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SalesReturnItemResponse struct {
	ID                 uuid.UUID `json:"id"`
	SalesReturnID      uuid.UUID `json:"sales_return_id"`
	SaleItemID         uuid.UUID `json:"sale_item_id"`
	StockAllocationID  uuid.UUID `json:"stock_allocation_id"`
	BatchID            uuid.UUID `json:"batch_id"`

	LineNo            int32             `json:"line_no"`
	Quantity          int64             `json:"quantity"`
	Disposition       ReturnDisposition `json:"disposition"`
	DispositionReason *string           `json:"disposition_reason,omitempty"`

	CreditMinor         int64      `json:"credit_minor"`
	OriginalCostMinor   int64      `json:"original_cost_minor"`
	CurrencyCode        string     `json:"currency_code"`
	InventoryMovementID *uuid.UUID `json:"inventory_movement_id,omitempty"`
}

type ListReturnsQuery struct {
	pagination.CursorQuery

	BranchID  *uuid.UUID        `form:"branch_id"`
	Status    *SaleReturnStatus `form:"status"`
	FromDate  *time.Time        `form:"from"`
	ToDate    *time.Time        `form:"to"`
	Q         string            `form:"q"`
}

func (q *ListReturnsQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{newFieldError("query", "required", "query is required")}
	}
	var errs []error

	q.CursorQuery.NormalizeWithMax(100)

	if q.Q != "" {
		q.Q = strings.TrimSpace(q.Q)
		if len(q.Q) > maxSearchLen {
			errs = append(errs, newFieldError("q", "max", "search term exceeds maximum length"))
		}
	}

	if q.FromDate != nil && q.ToDate != nil && !q.FromDate.Before(*q.ToDate) {
		errs = append(errs, newFieldError("from", "before", "from_date must be before to_date"))
	}

	return errs
}

type RefundResponse struct {
	ID                  uuid.UUID  `json:"id"`
	OrganizationID      uuid.UUID  `json:"organization_id"`
	BranchID            uuid.UUID  `json:"branch_id"`
	SaleID              uuid.UUID  `json:"sale_id"`
	SalesReturnID       *uuid.UUID `json:"sales_return_id,omitempty"`
	OriginalTenderID    *uuid.UUID `json:"original_tender_id,omitempty"`
	OperationID         uuid.UUID  `json:"operation_id"`

	RefundNo         string       `json:"refund_no"`
	AmountMinor      int64        `json:"amount_minor"`
	CurrencyCode     string       `json:"currency_code"`
	Method           TenderMethod `json:"method"`
	Status           RefundStatus `json:"status"`
	ProviderRef      *string      `json:"provider_ref,omitempty"`
	Reason           string       `json:"reason"`

	RequestedBy  uuid.UUID  `json:"requested_by"`
	RequestedAt  time.Time  `json:"requested_at"`
	SettledAt    *time.Time `json:"settled_at,omitempty"`
	FailedAt     *time.Time `json:"failed_at,omitempty"`
	FailureCode  *string    `json:"failure_code,omitempty"`
	LedgerPostingRef *string `json:"ledger_posting_ref,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateRefundRequest struct {
	SaleID           uuid.UUID  `json:"sale_id" validate:"required"`
	SalesReturnID    *uuid.UUID `json:"sales_return_id,omitempty" validate:"omitempty"`
	OriginalTenderID *uuid.UUID `json:"original_tender_id,omitempty" validate:"omitempty"`
	AmountMinor      int64      `json:"amount_minor" validate:"required,gt=0"`
	CurrencyCode     string     `json:"currency_code" validate:"required,len=3"`
	Method           TenderMethod `json:"method" validate:"required,oneof=cash card mobile bank store_credit provider other"`
	Reason           string     `json:"reason" validate:"required,not_blank,max=500"`
	IdempotencyKey   string     `json:"-" validate:"required"`
}

func (r *CreateRefundRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create refund request is required")}
	}
	var errs []error
	if r.SaleID == uuid.Nil {
		errs = append(errs, newFieldError("sale_id", "required", "sale_id is required"))
	}
	if r.AmountMinor <= 0 {
		errs = append(errs, newFieldError("amount_minor", "gt", "amount_minor must be positive"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	if strings.TrimSpace(r.Reason) == "" {
		errs = append(errs, newFieldError("reason", "required", "refund reason is required"))
	}
	if len(r.Reason) > maxRefundReason {
		errs = append(errs, newFieldError("reason", "max", "reason exceeds maximum length"))
	}
	return errs
}

type CashSessionResponse struct {
	ID              uuid.UUID `json:"id"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	BranchID        uuid.UUID `json:"branch_id"`
	CashierID       uuid.UUID `json:"cashier_id"`
	DrawerID        *uuid.UUID `json:"drawer_id,omitempty"`

	SessionNo     string            `json:"session_no"`
	Status        CashSessionStatus `json:"status"`
	CurrencyCode  string            `json:"currency_code"`
	OpeningMinor  int64             `json:"opening_minor"`
	ExpectedMinor int64             `json:"expected_minor"`
	CountedMinor  *int64            `json:"counted_minor,omitempty"`
	VarianceMinor *int64            `json:"variance_minor,omitempty"`

	OpenedAt         time.Time  `json:"opened_at"`
	OpenedBy         uuid.UUID  `json:"opened_by"`
	ClosingStartedAt *time.Time `json:"closing_started_at,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	ClosedBy         *uuid.UUID `json:"closed_by,omitempty"`
	CloseReason      *string    `json:"close_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OpenCashSessionRequest struct {
	DrawerID       *uuid.UUID `json:"drawer_id,omitempty" validate:"omitempty"`
	OpeningMinor   int64      `json:"opening_minor" validate:"required,gte=0"`
	CurrencyCode   string     `json:"currency_code" validate:"required,len=3"`
	IdempotencyKey string     `json:"-" validate:"required"`
}

func (r *OpenCashSessionRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "open cash session request is required")}
	}
	var errs []error
	if r.OpeningMinor < 0 {
		errs = append(errs, newFieldError("opening_minor", "gte", "opening_minor cannot be negative"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	return errs
}

type CloseCashSessionRequest struct {
	CountedMinor   int64  `json:"counted_minor" validate:"required,gte=0"`
	CloseReason    *string `json:"close_reason,omitempty" validate:"omitempty,max=500"`
	IdempotencyKey string `json:"-" validate:"required"`
}

func (r *CloseCashSessionRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "close cash session request is required")}
	}
	var errs []error
	if r.CountedMinor < 0 {
		errs = append(errs, newFieldError("counted_minor", "gte", "counted_minor cannot be negative"))
	}
	if r.CloseReason != nil && len(*r.CloseReason) > maxReasonLen {
		errs = append(errs, newFieldError("close_reason", "max", "close_reason exceeds maximum length"))
	}
	return errs
}

type CashMovementResponse struct {
	ID              uuid.UUID `json:"id"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	BranchID        uuid.UUID `json:"branch_id"`
	CashSessionID   uuid.UUID `json:"cash_session_id"`

	MovementType CashMovementType `json:"movement_type"`
	AmountMinor  int64            `json:"amount_minor"`
	CurrencyCode string           `json:"currency_code"`
	SourceType   string           `json:"source_type"`
	SourceID     uuid.UUID        `json:"source_id"`
	OperationID  uuid.UUID        `json:"operation_id"`
	ActorID      uuid.UUID        `json:"actor_id"`
	OccurredAt   time.Time        `json:"occurred_at"`
	Reason       *string          `json:"reason,omitempty"`
}

type PriceBookResponse struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	BranchID       *uuid.UUID `json:"branch_id,omitempty"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	CurrencyCode   string    `json:"currency_code"`
	IsActive       bool      `json:"is_active"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreatePriceBookRequest struct {
	BranchID     *uuid.UUID `json:"branch_id,omitempty" validate:"omitempty"`
	Code         string     `json:"code" validate:"required,not_blank,max=50"`
	Name         string     `json:"name" validate:"required,not_blank,max=200"`
	CurrencyCode string     `json:"currency_code" validate:"required,len=3"`
	IsActive     *bool      `json:"is_active,omitempty" validate:"omitempty"`
}

func (r *CreatePriceBookRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create price book request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Code) == "" {
		errs = append(errs, newFieldError("code", "required", "code is required"))
	}
	if strings.TrimSpace(r.Name) == "" {
		errs = append(errs, newFieldError("name", "required", "name is required"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	return errs
}

type UpdatePriceBookRequest struct {
	Name       *string `json:"name" validate:"omitempty,not_blank,max=200"`
	IsActive   *bool   `json:"is_active" validate:"omitempty"`
	Version    int64   `json:"version" validate:"required,gte=1"`
}

func (r *UpdatePriceBookRequest) IsEmpty() bool {
	return r.Name == nil && r.IsActive == nil
}

func (r *UpdatePriceBookRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "update price book request is required")}
	}
	var errs []error
	if r.Version < 1 {
		errs = append(errs, newFieldError("version", "gte", "version must be >= 1"))
	}
	return errs
}

type PriceVersionResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	PriceBookID    uuid.UUID  `json:"price_book_id"`
	MedicineID     uuid.UUID  `json:"medicine_id"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty"`
	UnitID         uuid.UUID  `json:"unit_id"`

	UnitPriceMinor int64     `json:"unit_price_minor"`
	CurrencyCode   string    `json:"currency_code"`
	ValidFrom      time.Time `json:"valid_from"`
	ValidTo        *time.Time `json:"valid_to,omitempty"`
	Version        int64     `json:"version"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreatePriceVersionRequest struct {
	PriceBookID    uuid.UUID  `json:"price_book_id" validate:"required"`
	MedicineID     uuid.UUID  `json:"medicine_id" validate:"required"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty" validate:"omitempty"`
	UnitID         uuid.UUID  `json:"unit_id" validate:"required"`

	UnitPriceMinor int64     `json:"unit_price_minor" validate:"required,gte=0"`
	CurrencyCode   string    `json:"currency_code" validate:"required,len=3"`
	ValidFrom      time.Time `json:"valid_from" validate:"required"`
	ValidTo        *time.Time `json:"valid_to,omitempty" validate:"omitempty"`
}

func (r *CreatePriceVersionRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create price version request is required")}
	}
	var errs []error
	if r.PriceBookID == uuid.Nil {
		errs = append(errs, newFieldError("price_book_id", "required", "price_book_id is required"))
	}
	if r.MedicineID == uuid.Nil {
		errs = append(errs, newFieldError("medicine_id", "required", "medicine_id is required"))
	}
	if r.UnitID == uuid.Nil {
		errs = append(errs, newFieldError("unit_id", "required", "unit_id is required"))
	}
	if r.UnitPriceMinor < 0 {
		errs = append(errs, newFieldError("unit_price_minor", "gte", "unit_price_minor cannot be negative"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	if r.ValidTo != nil && !r.ValidFrom.Before(*r.ValidTo) {
		errs = append(errs, newFieldError("valid_to", "after", "valid_to must be after valid_from"))
	}
	return errs
}

type PromotionResponse struct {
	ID                 uuid.UUID       `json:"id"`
	OrganizationID     uuid.UUID       `json:"organization_id"`
	Code               string          `json:"code"`
	Name               string          `json:"name"`
	Description        *string         `json:"description,omitempty"`
	Kind               PromotionKind   `json:"kind"`
	Config             common.JSONMap  `json:"config"`

	CurrencyCode      *string          `json:"currency_code,omitempty"`
	MinPurchaseMinor  *int64           `json:"min_purchase_minor,omitempty"`
	MaxDiscountMinor  *int64           `json:"max_discount_minor,omitempty"`
	DiscountRateBPS   *int32           `json:"discount_rate_bps,omitempty"`
	FlatDiscountMinor *int64           `json:"flat_discount_minor,omitempty"`

	ValidFrom        time.Time `json:"valid_from"`
	ValidTo          time.Time `json:"valid_to"`
	UsageLimit       *int64    `json:"usage_limit,omitempty"`
	UsageCount       int64     `json:"usage_count"`
	PerCustomerLimit *int64    `json:"per_customer_limit,omitempty"`
	IsActive         bool      `json:"is_active"`
	Version          int64     `json:"version"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreatePromotionRequest struct {
	Code           string         `json:"code" validate:"required,not_blank,max=50"`
	Name           string         `json:"name" validate:"required,not_blank,max=200"`
	Description    *string        `json:"description,omitempty" validate:"omitempty,max=1000"`
	Kind           PromotionKind  `json:"kind" validate:"required,oneof=percent_off flat_off buy_x_get_y bundle"`
	Config         common.JSONMap `json:"config" validate:"required"`

	CurrencyCode      *string        `json:"currency_code,omitempty" validate:"omitempty,len=3"`
	MinPurchaseMinor  *int64         `json:"min_purchase_minor,omitempty" validate:"omitempty,gte=0"`
	MaxDiscountMinor  *int64         `json:"max_discount_minor,omitempty" validate:"omitempty,gte=0"`
	DiscountRateBPS   *int32         `json:"discount_rate_bps,omitempty" validate:"omitempty,gte=0,lte=10000"`
	FlatDiscountMinor *int64         `json:"flat_discount_minor,omitempty" validate:"omitempty,gte=0"`

	ValidFrom        time.Time `json:"valid_from" validate:"required"`
	ValidTo          time.Time `json:"valid_to" validate:"required"`
	UsageLimit       *int64    `json:"usage_limit,omitempty" validate:"omitempty,gte=1"`
	PerCustomerLimit *int64    `json:"per_customer_limit,omitempty" validate:"omitempty,gte=1"`
	IsActive         *bool     `json:"is_active,omitempty" validate:"omitempty"`
}

func (r *CreatePromotionRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create promotion request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Code) == "" {
		errs = append(errs, newFieldError("code", "required", "code is required"))
	}
	if strings.TrimSpace(r.Name) == "" {
		errs = append(errs, newFieldError("name", "required", "name is required"))
	}
	if !r.ValidFrom.Before(r.ValidTo) {
		errs = append(errs, newFieldError("valid_to", "after", "valid_to must be after valid_from"))
	}
	if r.CurrencyCode != nil && len(*r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	if r.DiscountRateBPS != nil && (*r.DiscountRateBPS < 0 || *r.DiscountRateBPS > 10000) {
		errs = append(errs, newFieldError("discount_rate_bps", "range", "discount_rate_bps must be 0-10000"))
	}
	return errs
}

type UpdatePromotionRequest struct {
	Name              *string        `json:"name" validate:"omitempty,not_blank,max=200"`
	Description       *string        `json:"description,omitempty" validate:"omitempty,max=1000"`
	Config            common.JSONMap `json:"config,omitempty" validate:"omitempty"`
	CurrencyCode      *string        `json:"currency_code,omitempty" validate:"omitempty,len=3"`
	MinPurchaseMinor  *int64         `json:"min_purchase_minor,omitempty" validate:"omitempty,gte=0"`
	MaxDiscountMinor  *int64         `json:"max_discount_minor,omitempty" validate:"omitempty,gte=0"`
	DiscountRateBPS   *int32         `json:"discount_rate_bps,omitempty" validate:"omitempty,gte=0,lte=10000"`
	FlatDiscountMinor *int64         `json:"flat_discount_minor,omitempty" validate:"omitempty,gte=0"`
	ValidFrom         *time.Time     `json:"valid_from,omitempty" validate:"omitempty"`
	ValidTo           *time.Time     `json:"valid_to,omitempty" validate:"omitempty"`
	UsageLimit        *int64         `json:"usage_limit,omitempty" validate:"omitempty,gte=1"`
	PerCustomerLimit  *int64         `json:"per_customer_limit,omitempty" validate:"omitempty,gte=1"`
	IsActive          *bool          `json:"is_active,omitempty" validate:"omitempty"`
	Version           int64          `json:"version" validate:"required,gte=1"`
}

func (r *UpdatePromotionRequest) IsEmpty() bool {
	return r.Name == nil && r.Description == nil && r.Config == nil &&
		r.CurrencyCode == nil && r.MinPurchaseMinor == nil && r.MaxDiscountMinor == nil &&
		r.DiscountRateBPS == nil && r.FlatDiscountMinor == nil &&
		r.ValidFrom == nil && r.ValidTo == nil && r.UsageLimit == nil &&
		r.PerCustomerLimit == nil && r.IsActive == nil
}

func (r *UpdatePromotionRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "update promotion request is required")}
	}
	var errs []error
	if r.Version < 1 {
		errs = append(errs, newFieldError("version", "gte", "version must be >= 1"))
	}
	if r.ValidFrom != nil && r.ValidTo != nil && !r.ValidFrom.Before(*r.ValidTo) {
		errs = append(errs, newFieldError("valid_to", "after", "valid_to must be after valid_from"))
	}
	if r.CurrencyCode != nil && len(*r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	if r.DiscountRateBPS != nil && (*r.DiscountRateBPS < 0 || *r.DiscountRateBPS > 10000) {
		errs = append(errs, newFieldError("discount_rate_bps", "range", "discount_rate_bps must be 0-10000"))
	}
	return errs
}

type PromotionRedemptionResponse struct {
	ID               uuid.UUID  `json:"id"`
	OrganizationID   uuid.UUID  `json:"organization_id"`
	PromotionID      uuid.UUID  `json:"promotion_id"`
	SaleID           uuid.UUID  `json:"sale_id"`
	CustomerID       *uuid.UUID `json:"customer_id,omitempty"`

	RedemptionCount int64     `json:"redemption_count"`
	DiscountMinor   int64     `json:"discount_minor"`
	CurrencyCode    string    `json:"currency_code"`
	RedeemedAt      time.Time `json:"redeemed_at"`
}

type CalculateResponse struct {
	SubtotalMinor       int64              `json:"subtotal_minor"`
	DiscountMinor       int64              `json:"discount_minor"`
	CouponDiscountMinor int64              `json:"coupon_discount_minor"`
	TaxMinor            int64              `json:"tax_minor"`
	OtherChargesMinor   int64              `json:"other_charges_minor"`
	TotalMinor          int64              `json:"total_minor"`
	CurrencyCode        string             `json:"currency_code"`
	Items               []CalculateItemResponse `json:"items"`
	PolicyVersion       string             `json:"policy_version"`
	QuotedAt            time.Time          `json:"quoted_at"`
	ExpiresAt           *time.Time         `json:"expires_at,omitempty"`
}

type CalculateItemResponse struct {
	MedicineID    uuid.UUID  `json:"medicine_id"`
	VariantID     *uuid.UUID `json:"variant_id,omitempty"`
	UnitID        uuid.UUID  `json:"unit_id"`
	EnteredQuantity int64    `json:"entered_quantity"`
	BaseQuantity  int64      `json:"base_quantity"`

	UnitPriceMinor  int64  `json:"unit_price_minor"`
	DiscountMinor   int64  `json:"discount_minor"`
	TaxMinor        int64  `json:"tax_minor"`
	LineTotalMinor  int64  `json:"line_total_minor"`
	DiscountRateBPS int32  `json:"discount_rate_bps"`
	TaxRateBPS      int32  `json:"tax_rate_bps"`
}

type ApplyCouponResponse struct {
	Code               string  `json:"code"`
	DiscountMinor      int64   `json:"discount_minor"`
	CurrencyCode       string  `json:"currency_code"`
	DiscountRateBPS    int32   `json:"discount_rate_bps,omitempty"`
	FlatDiscountMinor  int64   `json:"flat_discount_minor,omitempty"`
	MaxDiscountMinor   int64   `json:"max_discount_minor,omitempty"`
	IsValid            bool    `json:"is_valid"`
	RejectionReason    *string `json:"rejection_reason,omitempty"`
}

type LookupResponse struct {
	MedicineID   uuid.UUID  `json:"medicine_id"`
	VariantID    *uuid.UUID `json:"variant_id,omitempty"`

	MedicineName string  `json:"medicine_name"`
	VariantName  *string `json:"variant_name,omitempty"`
	BrandName    *string `json:"brand_name,omitempty"`

	SKU          *string `json:"sku,omitempty"`
	Barcode      *string `json:"barcode,omitempty"`
	Strength     *string `json:"strength,omitempty"`
	PackSize     *string `json:"pack_size,omitempty"`
	UnitsPerPack *int    `json:"units_per_pack,omitempty"`

	RequiresPrescription bool `json:"requires_prescription"`
	IsControlled         bool `json:"is_controlled"`
	IsActive             bool `json:"is_active"`

	BatchInfo []BatchInfoResponse `json:"batch_info,omitempty"`
}

type BatchInfoResponse struct {
	BatchID        uuid.UUID `json:"batch_id"`
	LotNumber      string    `json:"lot_number"`
	ExpiryDate     string    `json:"expiry_date"`
	ExpiryPrecision string   `json:"expiry_precision"`
	SellableOnHand int64     `json:"sellable_on_hand_quantity"`
	AvailableForSale int64   `json:"available_quantity"`
	UnitCostMinor  int64     `json:"unit_cost_minor"`
	CurrencyCode   string    `json:"currency_code"`
}

type CheckoutResponse struct {
	Sale *SaleResponse `json:"sale"`
}

type VoidResponse struct {
	Sale *SaleResponse `json:"sale"`
}

type RefundSaleResponse struct {
	Sale   *SaleResponse   `json:"sale,omitempty"`
	Refund *RefundResponse `json:"refund,omitempty"`
}

type CollectPaymentResponse struct {
	Sale   *SaleResponse   `json:"sale,omitempty"`
	Tender *SaleTenderResponse `json:"tender,omitempty"`
}

type CreateReturnResponse struct {
	Return *SalesReturnResponse `json:"return"`
}

type ApproveReturnResponse struct {
	Return *SalesReturnResponse `json:"return"`
}

type RejectReturnResponse struct {
	Return *SalesReturnResponse `json:"return"`
}

type CreateRefundResponse struct {
	Refund *RefundResponse `json:"refund"`
}

type OpenCashSessionResponse struct {
	Session *CashSessionResponse `json:"session"`
}

type CloseCashSessionResponse struct {
	Session *CashSessionResponse `json:"session"`
}

type PrescriptionEvidenceResponse struct {
	ID              uuid.UUID             `json:"id"`
	OrganizationID  uuid.UUID             `json:"organization_id"`
	BranchID        uuid.UUID             `json:"branch_id"`
	SaleID          uuid.UUID             `json:"sale_id"`

	ReviewerID      uuid.UUID             `json:"reviewer_id"`
	ReviewedAt      time.Time             `json:"reviewed_at"`
	PrescriptionRef *string               `json:"prescription_ref,omitempty"`
	PrescriberRef   *string               `json:"prescriber_ref,omitempty"`
	EvidenceRefs    []string              `json:"evidence_refs,omitempty"`
	Decision        PrescriptionDecision  `json:"decision"`
	PolicyVersion   string                `json:"policy_version"`
	DecisionReason  *string               `json:"decision_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type CreatePrescriptionEvidenceRequest struct {
	SaleID          uuid.UUID            `json:"sale_id" validate:"required"`
	ReviewerID      uuid.UUID            `json:"reviewer_id" validate:"required"`
	PrescriptionRef *string              `json:"prescription_ref,omitempty" validate:"omitempty,max=500"`
	PrescriberRef   *string              `json:"prescriber_ref,omitempty" validate:"omitempty,max=500"`
	EvidenceRefs    []string             `json:"evidence_refs,omitempty" validate:"omitempty,dive,max=500"`
	Decision        PrescriptionDecision `json:"decision" validate:"required,oneof=not_required pending approved rejected"`
	PolicyVersion   string               `json:"policy_version" validate:"required,not_blank,max=50"`
	DecisionReason  *string              `json:"decision_reason,omitempty" validate:"omitempty,max=500"`
}

func (r *CreatePrescriptionEvidenceRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create prescription evidence request is required")}
	}
	var errs []error
	if r.SaleID == uuid.Nil {
		errs = append(errs, newFieldError("sale_id", "required", "sale_id is required"))
	}
	if r.ReviewerID == uuid.Nil {
		errs = append(errs, newFieldError("reviewer_id", "required", "reviewer_id is required"))
	}
	if strings.TrimSpace(r.PolicyVersion) == "" {
		errs = append(errs, newFieldError("policy_version", "required", "policy_version is required"))
	}
	for i, ref := range r.EvidenceRefs {
		if len(ref) > maxEvidenceRefLen {
			errs = append(errs, newFieldError("evidence_refs["+string(rune('0'+i))+"]", "max", "evidence reference exceeds maximum length"))
		}
	}
	return errs
}

type ExportSalesRequest struct {
	Purpose string         `json:"purpose" validate:"required,min=3,max=255"`
	Format  string         `json:"format" validate:"required,oneof=csv xlsx"`
	Filters map[string]any `json:"filters"`
	Fields  []string       `json:"fields" validate:"omitempty,dive,max=50"`
}

func (r *ExportSalesRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "export request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.Purpose) == "" {
		errs = append(errs, newFieldError("purpose", "required", "purpose is required"))
	}
	if r.Format != "csv" && r.Format != "xlsx" {
		errs = append(errs, newFieldError("format", "oneof", "format must be csv or xlsx"))
	}
	return errs
}

type ExportJobResponse struct {
	JobID     uuid.UUID `json:"job_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ExportJobStatusResponse struct {
	JobID       uuid.UUID  `json:"job_id"`
	Status      string     `json:"status"`
	RowCount    *int64     `json:"row_count,omitempty"`
	FileSize    *int64     `json:"file_size,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

type SaleOperationResponse struct {
	ID                  uuid.UUID         `json:"id"`
	OrganizationID      uuid.UUID         `json:"organization_id"`
	BranchID            uuid.UUID         `json:"branch_id"`
	OperationID         uuid.UUID         `json:"operation_id"`
	OperationType       SaleOperationType `json:"operation_type"`
	IdempotencyKeyHash  string            `json:"idempotency_key_hash"`
	RequestHash         string            `json:"request_hash"`
	Status              SaleOperationStatus `json:"status"`
	ResourceType        *string           `json:"resource_type,omitempty"`
	ResourceID          *uuid.UUID        `json:"resource_id,omitempty"`
	ResponseCode        *int              `json:"response_code,omitempty"`
	StartedAt           time.Time         `json:"started_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
	ExpiresAt           time.Time         `json:"expires_at"`
}

type SaleTransitionResponse struct {
	ID             uuid.UUID               `json:"id"`
	OrganizationID uuid.UUID               `json:"organization_id"`
	BranchID       uuid.UUID               `json:"branch_id"`
	AggregateType  string                  `json:"aggregate_type"`
	AggregateID    uuid.UUID               `json:"aggregate_id"`
	Dimension      SaleTransitionDimension `json:"dimension"`
	FromState      *string                 `json:"from_state,omitempty"`
	ToState        string                  `json:"to_state"`
	Action         string                  `json:"action"`
	OperationID    uuid.UUID               `json:"operation_id"`
	ActorID        uuid.UUID               `json:"actor_id"`
	Reason         *string                 `json:"reason,omitempty"`
	PolicyVersion  *string                 `json:"policy_version,omitempty"`
	OccurredAt     time.Time               `json:"occurred_at"`
}

type SaleStockAllocationResponse struct {
	ID                       uuid.UUID  `json:"id"`
	OrganizationID           uuid.UUID  `json:"organization_id"`
	SaleID                   uuid.UUID  `json:"sale_id"`
	SaleItemID               uuid.UUID  `json:"sale_item_id"`
	WarehouseID              uuid.UUID  `json:"warehouse_id"`
	BatchID                  uuid.UUID  `json:"batch_id"`
	InventoryMovementID      uuid.UUID  `json:"inventory_movement_id"`
	InventoryCostAllocationID uuid.UUID `json:"inventory_cost_allocation_id"`
	CostLayerID              *uuid.UUID `json:"cost_layer_id,omitempty"`
	BaseQuantity             int64      `json:"base_quantity"`

	BatchNoSnapshot string     `json:"batch_no_snapshot"`
	ExpiryDate      *time.Time `json:"expiry_date,omitempty"`

	UnitCostMinor   int64  `json:"unit_cost_minor"`
	TotalCostMinor  int64  `json:"total_cost_minor"`
	CurrencyCode    string `json:"currency_code"`
	ValuationRef    string `json:"valuation_ref"`
	AllocatedAt     time.Time `json:"allocated_at"`
}

type PaymentEventResponse struct {
	ID                  uuid.UUID `json:"id"`
	OrganizationID      uuid.UUID `json:"organization_id"`
	PaymentIntentID     uuid.UUID `json:"payment_intent_id"`
	ProviderCode        string    `json:"provider_code"`
	ProviderAccountID   string    `json:"provider_account_id"`
	ProviderEventID     string    `json:"provider_event_id"`
	EventType           string    `json:"event_type"`
	AmountMinor         int64     `json:"amount_minor"`
	CurrencyCode        string    `json:"currency_code"`
	OccurredAt          time.Time `json:"occurred_at"`
	ReceivedAt          time.Time `json:"received_at"`
}

type SaleCartResponse struct {
	ID            uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	BranchID       uuid.UUID `json:"branch_id"`
	OwnerUserID    uuid.UUID `json:"owner_user_id"`

	Content   common.JSONMap `json:"content"`
	ExpiresAt time.Time      `json:"expires_at"`
	Version   int64          `json:"version"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateCartRequest struct {
	Content   common.JSONMap `json:"content" validate:"required"`
	ExpiresAt time.Time      `json:"expires_at" validate:"required"`
}

func (r *CreateCartRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "create cart request is required")}
	}
	var errs []error
	if r.Content == nil {
		errs = append(errs, newFieldError("content", "required", "cart content is required"))
	}
	if r.ExpiresAt.IsZero() {
		errs = append(errs, newFieldError("expires_at", "required", "expires_at is required"))
	}
	return errs
}

type UpdateCartRequest struct {
	Content   *common.JSONMap `json:"content,omitempty" validate:"omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty" validate:"omitempty"`
	Version   int64           `json:"version" validate:"required,gte=1"`
}

func (r *UpdateCartRequest) IsEmpty() bool {
	return r.Content == nil && r.ExpiresAt == nil
}

func (r *UpdateCartRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "update cart request is required")}
	}
	var errs []error
	if r.Version < 1 {
		errs = append(errs, newFieldError("version", "gte", "version must be >= 1"))
	}
	return errs
}

type InvoiceCounterResponse struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	BranchID       uuid.UUID `json:"branch_id"`
	Series         string    `json:"series"`
	FiscalKey      string    `json:"fiscal_key"`
	NextNumber     int64     `json:"next_number"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SalePaymentIntentRequest struct {
	ProviderCode           string `json:"provider_code" validate:"required,not_blank,max=50"`
	ProviderAccountID      string `json:"provider_account_id" validate:"required,not_blank,max=100"`
	ProviderIdempotencyKey string `json:"provider_idempotency_key" validate:"required,not_blank,max=100"`
	ExternalReference      *string `json:"external_reference,omitempty" validate:"omitempty,max=200"`
	AmountMinor            int64  `json:"amount_minor" validate:"required,gt=0"`
	CurrencyCode           string `json:"currency_code" validate:"required,len=3"`
	ExpiresAt              time.Time `json:"expires_at" validate:"required"`
	IdempotencyKey         string `json:"-" validate:"required"`
}

func (r *SalePaymentIntentRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "payment intent request is required")}
	}
	var errs []error
	if strings.TrimSpace(r.ProviderCode) == "" {
		errs = append(errs, newFieldError("provider_code", "required", "provider_code is required"))
	}
	if strings.TrimSpace(r.ProviderAccountID) == "" {
		errs = append(errs, newFieldError("provider_account_id", "required", "provider_account_id is required"))
	}
	if strings.TrimSpace(r.ProviderIdempotencyKey) == "" {
		errs = append(errs, newFieldError("provider_idempotency_key", "required", "provider_idempotency_key is required"))
	}
	if r.AmountMinor <= 0 {
		errs = append(errs, newFieldError("amount_minor", "gt", "amount_minor must be positive"))
	}
	if len(r.CurrencyCode) != 3 {
		errs = append(errs, newFieldError("currency_code", "len", "currency_code must be 3 characters"))
	}
	if r.ExpiresAt.IsZero() {
		errs = append(errs, newFieldError("expires_at", "required", "expires_at is required"))
	}
	return errs
}