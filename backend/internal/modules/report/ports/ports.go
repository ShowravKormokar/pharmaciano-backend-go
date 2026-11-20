// Package ports defines the narrow, owner-supplied read ports Report depends
// on. Report never imports source repositories; the composition root supplies
// concrete implementations. See REPORT_CONTRACT §2.1.
//
// For the MVP, a sales-backed adapter (salesStore) is provided here using the
// verified `sales`/`sale_items` schema (migration 000009). It is a placeholder
// contract and must be replaced by an owner-approved Sale read port before the
// sales report is promoted past its release gate.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"backend/internal/platform/db"
)

// SalesLine is a flattened sale line suitable for a sales register export.
// Each row is one item line of a finalized sale.
type SalesLine struct {
	SaleID       uuid.UUID
	InvoiceNo    string
	SoldAt       time.Time
	BranchID     uuid.UUID
	WarehouseID  *uuid.UUID
	CashierID    uuid.UUID
	CustomerID   *uuid.UUID
	Status       string
	MedicineID   uuid.UUID
	VariantID    *uuid.UUID
	BatchID      uuid.UUID
	BatchNo      string
	Quantity     int
	UnitPriceMin int64
	CostPriceMin int64
	LineTotalMin int64
}

// SalesReader is the source contract for sales register data. Implementations
// must enforce tenant scope in SQL and return only committed, finalized facts.
type SalesReader interface {
	// SalesRegister returns finalized sale lines within [from, to) for the
	// tenant-scoped branch set, ordered by sold_at desc, id desc, bounded by
	// limit (caller passes maxRows+1 to detect truncation).
	SalesRegister(ctx context.Context, orgID uuid.UUID, branchIDs []uuid.UUID, from, to time.Time, limit int) ([]SalesLine, error)
}

// salesStore is the MVP SalesReader backed by the verified sales schema. It is
// a placeholder; the owning Sale module must supply the approved read port.
type salesStore struct {
	db *db.DB
}

// NewSalesStore builds the MVP sales read adapter over the sales schema.
func NewSalesStore(database *db.DB) SalesReader {
	return &salesStore{db: database}
}

const salesRegisterQuery = `
SELECT s.id, s.invoice_no, s.sold_at, s.branch_id, s.warehouse_id,
       s.cashier_id, s.customer_id, s.status,
       i.medicine_id, i.variant_id, i.batch_id, i.batch_no,
       i.quantity, i.unit_price, i.cost_price, i.line_total
FROM sales AS s
JOIN sale_items AS i ON i.sale_id = s.id AND i.deleted_at IS NULL
WHERE s.organization_id = $1
  AND s.deleted_at IS NULL
  AND s.status = 'completed'
  AND s.sold_at >= $2
  AND s.sold_at < $3
  AND ($4::uuid[] IS NULL OR s.branch_id = ANY($4::uuid[]))
ORDER BY s.sold_at DESC, s.id DESC
LIMIT $5`

func (s *salesStore) SalesRegister(ctx context.Context, orgID uuid.UUID, branchIDs []uuid.UUID, from, to time.Time, limit int) ([]SalesLine, error) {
	var branchIDsArg any
	if len(branchIDs) == 0 {
		branchIDsArg = nil
	} else {
		branchIDsArg = branchIDs
	}
	rows, err := s.db.FromCtx(ctx).Query(ctx, salesRegisterQuery,
		orgID, from, to, branchIDsArg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SalesLine
	for rows.Next() {
		var l SalesLine
		var warehouseID, customerID, variantID pgtype.UUID
		err := rows.Scan(
			&l.SaleID, &l.InvoiceNo, &l.SoldAt, &l.BranchID, &warehouseID,
			&l.CashierID, &customerID, &l.Status,
			&l.MedicineID, &variantID, &l.BatchID, &l.BatchNo,
			&l.Quantity, &l.UnitPriceMin, &l.CostPriceMin, &l.LineTotalMin,
		)
		if err != nil {
			return nil, err
		}
		if warehouseID.Valid {
			id := uuid.UUID(warehouseID.Bytes)
			l.WarehouseID = &id
		}
		if customerID.Valid {
			id := uuid.UUID(customerID.Bytes)
			l.CustomerID = &id
		}
		if variantID.Valid {
			id := uuid.UUID(variantID.Bytes)
			l.VariantID = &id
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Compile-time interface check.
var _ SalesReader = (*salesStore)(nil)
