package purchase

import (
	"errors"
	"fmt"
)

var ErrInvalidPurchaseOrderTransition = errors.New("invalid purchase order transition")

// ValidatePurchaseOrderTransition enforces the order lifecycle independently
// of receipt, invoice, payment, and closure state.
func ValidatePurchaseOrderTransition(from, to PurchaseOrderStatus) error {
	if !validPurchaseOrderStatus(from) {
		return fmt.Errorf("%w: unknown source state %q", ErrInvalidPurchaseOrderTransition, from)
	}
	if !validPurchaseOrderStatus(to) {
		return fmt.Errorf("%w: unknown destination state %q", ErrInvalidPurchaseOrderTransition, to)
	}

	var allowed bool
	switch from {
	case PurchaseOrderStatusDraft:
		allowed = to == PurchaseOrderStatusPendingApproval || to == PurchaseOrderStatusCancelled
	case PurchaseOrderStatusPendingApproval:
		allowed = to == PurchaseOrderStatusApproved ||
			to == PurchaseOrderStatusRejected ||
			to == PurchaseOrderStatusCancelled
	case PurchaseOrderStatusApproved:
		allowed = to == PurchaseOrderStatusOrdered || to == PurchaseOrderStatusCancelled
	case PurchaseOrderStatusOrdered:
		allowed = to == PurchaseOrderStatusCancelled
	}
	if !allowed {
		return fmt.Errorf("%w: %q to %q", ErrInvalidPurchaseOrderTransition, from, to)
	}
	return nil
}

// DerivePurchaseReceiptStatus computes receipt progress from accepted posted
// quantities. It does not apply or authorize an over-receipt tolerance.
func DerivePurchaseReceiptStatus(ordered, accepted Quantity) (PurchaseReceiptStatus, error) {
	if ordered <= 0 {
		return "", fmt.Errorf("ordered quantity must be positive")
	}
	if accepted < 0 {
		return "", fmt.Errorf("accepted quantity must not be negative")
	}
	switch {
	case accepted == 0:
		return PurchaseReceiptStatusNotReceived, nil
	case accepted < ordered:
		return PurchaseReceiptStatusPartial, nil
	default:
		return PurchaseReceiptStatusFull, nil
	}
}
