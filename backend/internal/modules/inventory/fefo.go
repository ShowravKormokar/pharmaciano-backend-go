package inventory

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidFEFOQuery      = errors.New("invalid FEFO query")
	ErrInsufficientFEFOStock = errors.New("insufficient eligible stock")
	ErrInvalidFEFOCandidate  = errors.New("invalid FEFO candidate")
)

// FEFOQuery describes a request in canonical base units and resolved scope.
// BusinessDate must already be computed using the branch's configured timezone.
type FEFOQuery struct {
	OrganizationID uuid.UUID
	BranchID       uuid.UUID
	WarehouseID    uuid.UUID
	VariantID      uuid.UUID
	BusinessDate   time.Time
	Quantity       Quantity
}

// FEFOCandidate joins a batch identity to one warehouse/condition balance.
type FEFOCandidate struct {
	Batch   InventoryBatch
	Balance InventoryBalance
}

// FEFOAllocation identifies the balance and quantity to consume from a batch.
type FEFOAllocation struct {
	BatchID   uuid.UUID
	BalanceID uuid.UUID
	Quantity  Quantity
}

// AllocateFEFO selects eligible balances by earliest expiry, with batch ID as
// a stable tie-breaker. It does not lock or mutate stock; callers must reload
// and validate the selected balances under PostgreSQL row locks before writing.
func AllocateFEFO(query FEFOQuery, candidates []FEFOCandidate) ([]FEFOAllocation, error) {
	if query.OrganizationID == uuid.Nil || query.BranchID == uuid.Nil ||
		query.WarehouseID == uuid.Nil || query.VariantID == uuid.Nil ||
		query.BusinessDate.IsZero() || query.Quantity <= 0 {
		return nil, ErrInvalidFEFOQuery
	}

	eligible := make([]FEFOCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		balance := candidate.Balance
		batch := candidate.Batch
		if batch.ID == uuid.Nil || balance.ID == uuid.Nil {
			return nil, ErrInvalidFEFOCandidate
		}
		if balance.OrganizationID != query.OrganizationID ||
			balance.BranchID != query.BranchID || balance.WarehouseID != query.WarehouseID ||
			balance.BatchID != batch.ID || batch.OrganizationID != query.OrganizationID ||
			batch.VariantID != query.VariantID {
			continue
		}
		if balance.OnHandQuantity < 0 || balance.ReservedQuantity < 0 ||
			balance.ReservedQuantity > balance.OnHandQuantity {
			return nil, ErrInvalidFEFOCandidate
		}
		if balance.Condition != StockConditionSellable ||
			batch.Status != BatchStatusActive || batch.RecallStatus != BatchRecallStatusClear ||
			batch.ExpiryPrecision != BatchExpiryPrecisionDay ||
			dateBefore(batch.ExpiresOn, query.BusinessDate) || balance.AvailableQuantity() <= 0 {
			continue
		}
		eligible = append(eligible, candidate)
	}

	sort.Slice(eligible, func(i, j int) bool {
		left, right := eligible[i].Batch, eligible[j].Batch
		if !sameCalendarDate(left.ExpiresOn, right.ExpiresOn) {
			return dateBefore(left.ExpiresOn, right.ExpiresOn)
		}
		return left.ID.String() < right.ID.String()
	})

	remaining := query.Quantity
	allocations := make([]FEFOAllocation, 0, len(eligible))
	for _, candidate := range eligible {
		available := candidate.Balance.AvailableQuantity()
		allocated := available
		if allocated > remaining {
			allocated = remaining
		}
		allocations = append(allocations, FEFOAllocation{
			BatchID:   candidate.Batch.ID,
			BalanceID: candidate.Balance.ID,
			Quantity:  allocated,
		})
		remaining -= allocated
		if remaining == 0 {
			return allocations, nil
		}
	}
	return nil, ErrInsufficientFEFOStock
}

func dateBefore(left, right time.Time) bool {
	leftYear, leftMonth, leftDay := left.Date()
	rightYear, rightMonth, rightDay := right.Date()
	if leftYear != rightYear {
		return leftYear < rightYear
	}
	if leftMonth != rightMonth {
		return leftMonth < rightMonth
	}
	return leftDay < rightDay
}

func sameCalendarDate(left, right time.Time) bool {
	return !dateBefore(left, right) && !dateBefore(right, left)
}
