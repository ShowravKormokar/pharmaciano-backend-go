package purchase

import (
	"errors"
	"testing"
)

func TestValidatePurchaseOrderTransition(t *testing.T) {
	tests := []struct {
		name string
		from PurchaseOrderStatus
		to   PurchaseOrderStatus
		ok   bool
	}{
		{"submit draft", PurchaseOrderStatusDraft, PurchaseOrderStatusPendingApproval, true},
		{"cancel draft", PurchaseOrderStatusDraft, PurchaseOrderStatusCancelled, true},
		{"approve submitted", PurchaseOrderStatusPendingApproval, PurchaseOrderStatusApproved, true},
		{"reject submitted", PurchaseOrderStatusPendingApproval, PurchaseOrderStatusRejected, true},
		{"withdraw submitted", PurchaseOrderStatusPendingApproval, PurchaseOrderStatusCancelled, true},
		{"order approved", PurchaseOrderStatusApproved, PurchaseOrderStatusOrdered, true},
		{"cancel approved remainder", PurchaseOrderStatusApproved, PurchaseOrderStatusCancelled, true},
		{"cancel ordered remainder", PurchaseOrderStatusOrdered, PurchaseOrderStatusCancelled, true},
		{"skip approval", PurchaseOrderStatusDraft, PurchaseOrderStatusApproved, false},
		{"order before approval", PurchaseOrderStatusPendingApproval, PurchaseOrderStatusOrdered, false},
		{"edit after ordering", PurchaseOrderStatusOrdered, PurchaseOrderStatusDraft, false},
		{"repeat state", PurchaseOrderStatusApproved, PurchaseOrderStatusApproved, false},
		{"reopen rejected", PurchaseOrderStatusRejected, PurchaseOrderStatusDraft, false},
		{"unknown source", PurchaseOrderStatus("unknown"), PurchaseOrderStatusApproved, false},
		{"unknown destination", PurchaseOrderStatusDraft, PurchaseOrderStatus("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePurchaseOrderTransition(tt.from, tt.to)
			if (err == nil) != tt.ok {
				t.Fatalf("ValidatePurchaseOrderTransition(%q, %q) error = %v, want success %t", tt.from, tt.to, err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidPurchaseOrderTransition) {
				t.Fatalf("error %v does not wrap ErrInvalidPurchaseOrderTransition", err)
			}
		})
	}
}

func TestDerivePurchaseReceiptStatus(t *testing.T) {
	tests := []struct {
		name     string
		ordered  Quantity
		accepted Quantity
		want     PurchaseReceiptStatus
		wantErr  bool
	}{
		{"none received", 10, 0, PurchaseReceiptStatusNotReceived, false},
		{"partial", 10, 9, PurchaseReceiptStatusPartial, false},
		{"fully received", 10, 10, PurchaseReceiptStatusFull, false},
		{"approved over-receipt", 10, 11, PurchaseReceiptStatusFull, false},
		{"zero ordered", 0, 0, "", true},
		{"negative ordered", -1, 0, "", true},
		{"negative accepted", 10, -1, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DerivePurchaseReceiptStatus(tt.ordered, tt.accepted)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DerivePurchaseReceiptStatus(%d, %d) error = %v, wantErr %t", tt.ordered, tt.accepted, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("DerivePurchaseReceiptStatus(%d, %d) = %q, want %q", tt.ordered, tt.accepted, got, tt.want)
			}
		})
	}
}
