package purchase

import (
	"context"
	"testing"

	appctx "backend/internal/common/context"

	"github.com/google/uuid"
)

func TestPurchaseScopeFromContextUsesEffectiveBranchAssignments(t *testing.T) {
	organizationID := uuid.New()
	actorID := uuid.New()
	assignedBranch := uuid.New()
	otherBranch := uuid.New()
	ctx := appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID:    actorID,
		OrgID:     organizationID,
		BranchIDs: []uuid.UUID{assignedBranch},
	})

	scope, gotActorID, err := purchaseScopeFromContext(ctx)
	if err != nil {
		t.Fatalf("purchaseScopeFromContext() error = %v", err)
	}
	if gotActorID != actorID || scope.OrganizationID != organizationID {
		t.Fatalf("scope identity = (%v, %v), want actor %v and organization %v",
			gotActorID, scope.OrganizationID, actorID, organizationID)
	}
	if scope.OrgWide || !scope.allowsBranch(assignedBranch) || scope.allowsBranch(otherBranch) {
		t.Fatalf("scope does not enforce effective branch assignments: %+v", scope)
	}
}

func TestValidatePurchaseDraftResolutionPreservesRequestedLineInputs(t *testing.T) {
	request := CreatePurchaseOrderRequest{
		BranchID:   uuid.New(),
		SupplierID: uuid.New(),
		Lines: []CreatePurchaseOrderLineRequest{{
			MedicineID:      uuid.New(),
			UnitID:          uuid.New(),
			OrderedQuantity: 4,
			UnitPriceMinor:  125,
			DiscountRateBPS: 250,
			TaxRateBPS:      500,
		}},
	}
	resolved := PurchaseDraftResolution{
		PurchaseNo:   "PO-123",
		CurrencyCode: "BDT",
		Lines: []PurchaseOrderLine{{
			MedicineID:                request.Lines[0].MedicineID,
			UnitID:                    request.Lines[0].UnitID,
			OrderedQuantity:           request.Lines[0].OrderedQuantity,
			UnitPriceMinor:            request.Lines[0].UnitPriceMinor,
			DiscountRate:              request.Lines[0].DiscountRateBPS,
			TaxRate:                   request.Lines[0].TaxRateBPS,
			UnitConversionNumerator:   1,
			UnitConversionDenominator: 1,
			ProductNameSnapshot:       "resolved catalog name",
			UnitNameSnapshot:          "resolved unit",
		}},
	}

	if err := validatePurchaseDraftResolution(request, resolved); err != nil {
		t.Fatalf("valid resolution rejected: %v", err)
	}
	resolved.Lines[0].UnitPriceMinor++
	if err := validatePurchaseDraftResolution(request, resolved); err == nil {
		t.Fatal("resolution that changed the requested unit price was accepted")
	}
}
