package purchase

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/validator"
)

func TestPostPurchaseReceiptRequestValidate(t *testing.T) {
	now := time.Now().UTC()
	line := PostPurchaseReceiptLineRequest{
		PurchaseOrderLineID:      uuid.New(),
		DeliveredQuantity:        5,
		AcceptedQuantity:         3,
		RejectedQuantity:         2,
		RejectionReason:          stringPointer("damaged packaging"),
		BatchNumber:              "LOT-2026-01",
		ExpiryAt:                 now.AddDate(1, 0, 0),
		UnitAcquisitionCostMinor: 100,
	}
	request := PostPurchaseReceiptRequest{
		WarehouseID: uuid.New(),
		Lines:       []PostPurchaseReceiptLineRequest{line},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}

	request.Lines[0].AcceptedQuantity = 4
	if err := request.Validate(); err == nil {
		t.Fatal("receipt with inconsistent delivered/accepted/rejected quantities was accepted")
	}
}

func TestPostPurchaseReceiptRejectsDuplicatePOLine(t *testing.T) {
	line := PostPurchaseReceiptLineRequest{
		PurchaseOrderLineID: uuid.New(),
		DeliveredQuantity:   1,
		AcceptedQuantity:    1,
		BatchNumber:         "LOT-1",
		ExpiryAt:            time.Now().UTC().AddDate(1, 0, 0),
	}
	request := PostPurchaseReceiptRequest{
		WarehouseID: uuid.New(),
		Lines:       []PostPurchaseReceiptLineRequest{line, line},
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "only once") {
		t.Fatalf("expected duplicate-line validation error, got %v", err)
	}
}

func TestCreatePurchasePaymentRequestValidate(t *testing.T) {
	invoiceID := uuid.New()
	request := CreatePurchasePaymentRequest{
		AmountMinor: 300,
		MethodCode:  "bank_transfer",
		PaymentDate: time.Now().UTC(),
		Allocations: []CreatePurchasePaymentAllocationRequest{
			{PurchaseInvoiceID: invoiceID, AmountMinor: 100},
			{PurchaseInvoiceID: uuid.New(), AmountMinor: 200},
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid payment allocations rejected: %v", err)
	}

	request.Allocations[1].AmountMinor = 201
	if err := request.Validate(); err == nil {
		t.Fatal("over-allocated payment was accepted")
	}
}

func TestCreatePurchaseInvoiceRequestValidate(t *testing.T) {
	receiptLineID := uuid.New()
	request := CreatePurchaseInvoiceRequest{
		InvoiceNo:   "INV-001",
		InvoiceDate: time.Now().UTC(),
		Lines: []CreatePurchaseInvoiceLineRequest{{
			Description:      "Medicine",
			InvoicedQuantity: 5,
			Matches: []CreatePurchaseInvoiceMatchRequest{{
				PurchaseReceiptLineID: receiptLineID,
				MatchedQuantity:       5,
			}},
		}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid invoice rejected: %v", err)
	}

	request.Lines[0].Matches[0].MatchedQuantity = 6
	if err := request.Validate(); err == nil {
		t.Fatal("invoice match exceeding invoiced quantity was accepted")
	}
}

func TestCreatePurchaseReturnRequestValidate(t *testing.T) {
	receiptLineID := uuid.New()
	request := CreatePurchaseReturnRequest{
		WarehouseID:        uuid.New(),
		Reason:             "supplier recall",
		EvidenceReferences: []string{"evidence://return/1"},
		Lines: []CreatePurchaseReturnLineRequest{{
			PurchaseReceiptLineID: receiptLineID,
			ReturnQuantity:        2,
		}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid return rejected: %v", err)
	}
	request.Lines = append(request.Lines, request.Lines[0])
	if err := request.Validate(); err == nil {
		t.Fatal("duplicate return line was accepted")
	}
}

func TestListPurchaseOrdersQueryNormalizeAndValidate(t *testing.T) {
	q := ListPurchaseOrdersQuery{}
	q.Offset.Page = 0
	q.Offset.Limit = 1000
	if err := q.NormalizeAndValidate(); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
	if q.Offset.Page != 1 || q.Offset.Limit > 100 {
		t.Fatalf("pagination was not normalized: page=%d limit=%d", q.Offset.Page, q.Offset.Limit)
	}

	q.OrderStatus = statusPointer(PurchaseOrderStatus("unrecognized"))
	if err := q.NormalizeAndValidate(); err == nil {
		t.Fatal("invalid order status was accepted")
	}
}

func TestUpdatePurchaseOrderRequestIsEmpty(t *testing.T) {
	if !(UpdatePurchaseOrderRequest{}).IsEmpty() {
		t.Fatal("zero update request should be empty")
	}
	if (UpdatePurchaseOrderRequest{Notes: stringPointer("")}).IsEmpty() {
		t.Fatal("explicit empty notes must represent a change")
	}
}

func TestPurchaseRequestValidationTags(t *testing.T) {
	v, err := validator.New()
	if err != nil {
		t.Fatalf("create shared validator: %v", err)
	}

	now := time.Now().UTC()
	request := CreatePurchaseOrderRequest{
		BranchID:   uuid.New(),
		SupplierID: uuid.New(),
		Lines: []CreatePurchaseOrderLineRequest{{
			MedicineID:      uuid.New(),
			UnitID:          uuid.New(),
			OrderedQuantity: 1,
			UnitPriceMinor:  100,
		}},
	}
	if err := v.Struct(request); err != nil {
		t.Fatalf("valid purchase order failed struct validation: %v", err)
	}

	receipt := PostPurchaseReceiptRequest{
		WarehouseID: uuid.New(),
		Lines: []PostPurchaseReceiptLineRequest{{
			PurchaseOrderLineID:      uuid.New(),
			DeliveredQuantity:        1,
			AcceptedQuantity:         1,
			BatchNumber:              "LOT-1",
			ExpiryAt:                 now.AddDate(1, 0, 0),
			UnitAcquisitionCostMinor: 100,
		}},
	}
	if err := v.Struct(receipt); err != nil {
		t.Fatalf("valid receipt failed struct validation: %v", err)
	}
}

func stringPointer(value string) *string { return &value }

func statusPointer(value PurchaseOrderStatus) *PurchaseOrderStatus { return &value }
