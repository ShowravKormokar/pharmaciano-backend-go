package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAllocateFEFOOrdersEligibleStockAndExcludesBlockedStock(t *testing.T) {
	orgID, branchID, warehouseID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	firstBatchID, laterBatchID, blockedBatchID := uuid.New(), uuid.New(), uuid.New()
	query := FEFOQuery{
		OrganizationID: orgID,
		BranchID:       branchID,
		WarehouseID:    warehouseID,
		VariantID:      variantID,
		BusinessDate:   time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC),
		Quantity:       6,
	}
	date := func(day int) time.Time { return time.Date(2026, time.December, day, 0, 0, 0, 0, time.UTC) }
	candidate := func(batchID uuid.UUID, expires time.Time, onHand, reserved Quantity, condition StockCondition, recall BatchRecallStatus) FEFOCandidate {
		return FEFOCandidate{
			Batch: InventoryBatch{
				OrganizationID:  orgID,
				VariantID:       variantID,
				ExpiresOn:       expires,
				ExpiryPrecision: BatchExpiryPrecisionDay,
				Status:          BatchStatusActive,
				RecallStatus:    recall,
			},
			Balance: InventoryBalance{
				OrganizationID:   orgID,
				BranchID:         branchID,
				WarehouseID:      warehouseID,
				BatchID:          batchID,
				Condition:        condition,
				OnHandQuantity:   onHand,
				ReservedQuantity: reserved,
			},
		}
	}
	first := candidate(firstBatchID, date(10), 4, 1, StockConditionSellable, BatchRecallStatusClear)
	first.Batch.ID = firstBatchID
	first.Balance.ID = uuid.New()
	later := candidate(laterBatchID, date(20), 8, 0, StockConditionSellable, BatchRecallStatusClear)
	later.Batch.ID = laterBatchID
	later.Balance.ID = uuid.New()
	blocked := candidate(blockedBatchID, date(6), 20, 0, StockConditionSellable, BatchRecallStatusRecalled)
	blocked.Batch.ID = blockedBatchID
	blocked.Balance.ID = uuid.New()
	monthOnly := candidate(uuid.New(), date(8), 20, 0, StockConditionSellable, BatchRecallStatusClear)
	monthOnly.Batch.ExpiryPrecision = BatchExpiryPrecisionMonth
	monthOnly.Batch.ID = monthOnly.Balance.BatchID
	monthOnly.Balance.ID = uuid.New()

	got, err := AllocateFEFO(query, []FEFOCandidate{later, blocked, monthOnly, first})
	if err != nil {
		t.Fatalf("AllocateFEFO() error = %v", err)
	}
	if len(got) != 2 || got[0].BatchID != firstBatchID || got[0].Quantity != 3 || got[1].BatchID != laterBatchID || got[1].Quantity != 3 {
		t.Fatalf("AllocateFEFO() = %#v, want 3 from earliest and 3 from next", got)
	}
}

func TestAllocateFEFORejectsMalformedCandidate(t *testing.T) {
	orgID, branchID, warehouseID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	query := FEFOQuery{
		OrganizationID: orgID,
		BranchID:       branchID,
		WarehouseID:    warehouseID,
		VariantID:      variantID,
		BusinessDate:   time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC),
		Quantity:       1,
	}
	invalid := FEFOCandidate{
		Batch: InventoryBatch{OrganizationID: orgID, VariantID: variantID},
		Balance: InventoryBalance{
			OrganizationID: orgID, BranchID: branchID, WarehouseID: warehouseID,
			Condition: StockConditionSellable, OnHandQuantity: 1,
		},
	}
	if _, err := AllocateFEFO(query, []FEFOCandidate{invalid}); !errors.Is(err, ErrInvalidFEFOCandidate) {
		t.Fatalf("AllocateFEFO() error = %v, want invalid candidate", err)
	}
}

func TestAllocateFEFORejectsInsufficientEligibleStock(t *testing.T) {
	query := FEFOQuery{
		OrganizationID: uuid.New(),
		BranchID:       uuid.New(),
		WarehouseID:    uuid.New(),
		VariantID:      uuid.New(),
		BusinessDate:   time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC),
		Quantity:       2,
	}
	allocations, err := AllocateFEFO(query, nil)
	if !errors.Is(err, ErrInsufficientFEFOStock) {
		t.Fatalf("AllocateFEFO() error = %v, want insufficient stock", err)
	}
	if allocations != nil {
		t.Fatalf("AllocateFEFO() allocations = %#v, want nil on failure", allocations)
	}
}

func TestBatchListSortKeyNormalizesBranchScopeOrder(t *testing.T) {
	orgID, first, second := uuid.New(), uuid.New(), uuid.New()
	left := batchListFilter{BranchScope: []uuid.UUID{first, second}}
	right := batchListFilter{BranchScope: []uuid.UUID{second, first}}
	if batchListSortKey(orgID, left) != batchListSortKey(orgID, right) {
		t.Fatal("equivalent branch scopes produced different cursor sort keys")
	}
}

func TestEscapeLikePrefixTreatsWildcardsLiterally(t *testing.T) {
	got := escapeLikePrefix(`Lot%_\A`)
	want := `lot\%\_\\a`
	if got != want {
		t.Fatalf("escapeLikePrefix() = %q, want %q", got, want)
	}
}
