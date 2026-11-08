package purchase

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPurchaseScopeValidationAndBranchAccess(t *testing.T) {
	branchA := uuid.New()
	branchB := uuid.New()
	orgID := uuid.New()

	tests := []struct {
		name  string
		scope PurchaseScope
		valid bool
	}{
		{"missing organization", PurchaseScope{OrgWide: true}, false},
		{"empty restricted scope", PurchaseScope{OrganizationID: orgID}, false},
		{"restricted scope", PurchaseScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{branchA}}, true},
		{"organization scope", PurchaseScope{OrganizationID: orgID, OrgWide: true}, true},
		{"empty branch", PurchaseScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{uuid.Nil}}, false},
		{"duplicate branches", PurchaseScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{branchA, branchA}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scope.validate()
			if (err == nil) != tt.valid {
				t.Fatalf("scope.validate() error = %v, want valid=%t", err, tt.valid)
			}
		})
	}

	restricted := PurchaseScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{branchA}}
	if !restricted.allowsBranch(branchA) || restricted.allowsBranch(branchB) {
		t.Fatal("restricted scope did not enforce its branch set")
	}
	orgWide := PurchaseScope{OrganizationID: orgID, OrgWide: true}
	if !orgWide.allowsBranch(branchB) {
		t.Fatal("organization-wide scope should allow branch selection")
	}
}

func TestScopedPurchaseOrderQueryUsesNonOverlappingPlaceholders(t *testing.T) {
	scope := PurchaseScope{
		OrganizationID: uuid.New(),
		BranchIDs:      []uuid.UUID{uuid.New()},
	}
	query, args := scopedPurchaseOrderQuery(scope, `SELECT * FROM purchase_orders
		WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL`)

	if !strings.Contains(query, "branch_id = ANY($2::uuid[])") ||
		!strings.Contains(query, "id = $3") {
		t.Fatalf("scoped query has incorrect placeholder positions: %s", query)
	}
	if len(args) != 2 || args[0] != scope.OrganizationID {
		t.Fatalf("scoped query args = %#v, want organization and branch IDs", args)
	}
}

func TestPurchaseOrderOrderByAllowListsSortFields(t *testing.T) {
	got := purchaseOrderOrderBy("-created_at,purchase_no")
	want := "ORDER BY created_at DESC, purchase_no ASC, id ASC"
	if got != want {
		t.Fatalf("purchaseOrderOrderBy() = %q, want %q", got, want)
	}

	got = purchaseOrderOrderBy("id; DROP TABLE purchase_orders")
	if got != "ORDER BY created_at DESC, id DESC" {
		t.Fatalf("unrecognized sort should use deterministic default, got %q", got)
	}
}

func TestEscapePurchaseLike(t *testing.T) {
	got := escapePurchaseLike(`a%_b\c`)
	if got != `a\%\_b\\c` {
		t.Fatalf("escapePurchaseLike() = %q", got)
	}
}
