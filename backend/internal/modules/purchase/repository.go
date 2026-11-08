package purchase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository persists Purchase-owned data only. It expects the normalized
// purchase_orders schema described in PURCHASE_CONTRACT.md; the legacy
// 000008_purchase migration does not provide this schema.
type Repository struct {
	db *db.DB
}

func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// PurchaseScope is built from trusted authentication/tenant context, never
// directly from request parameters. Empty BranchIDs means organization-wide
// only when OrgWide is true.
type PurchaseScope struct {
	OrganizationID uuid.UUID
	BranchIDs      []uuid.UUID
	OrgWide        bool
}

func (s PurchaseScope) validate() error {
	if s.OrganizationID == uuid.Nil {
		return fmt.Errorf("purchase scope requires an organization")
	}
	if !s.OrgWide && len(s.BranchIDs) == 0 {
		return fmt.Errorf("purchase scope has no accessible branches")
	}
	seen := make(map[uuid.UUID]struct{}, len(s.BranchIDs))
	for _, id := range s.BranchIDs {
		if id == uuid.Nil {
			return fmt.Errorf("purchase scope contains an empty branch")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("purchase scope contains a duplicate branch")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s PurchaseScope) allowsBranch(id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	if s.OrgWide {
		return true
	}
	for _, allowed := range s.BranchIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

const purchaseOrderColumns = `id, organization_id, branch_id, supplier_id,
	purchase_no, reference_no, order_status, receipt_status, invoice_status,
	closure_status, currency_code, policy_snapshot, requested_by, submitted_by,
	submitted_at, approved_by, approved_at, rejected_by, rejected_at,
	rejection_reason, expected_delivery_at, ordered_by, ordered_at, cancelled_by,
	cancelled_at, cancellation_reason, closed_by, closed_at, notes, version,
	created_at, updated_at, deleted_at`

func scanPurchaseOrder(row pgx.Row) (*PurchaseOrder, error) {
	var order PurchaseOrder
	err := row.Scan(
		&order.ID, &order.OrganizationID, &order.BranchID, &order.SupplierID,
		&order.PurchaseNo, &order.ReferenceNo, &order.OrderStatus,
		&order.ReceiptStatus, &order.InvoiceStatus, &order.ClosureStatus,
		&order.CurrencyCode, &order.PolicySnapshot, &order.RequestedBy,
		&order.SubmittedBy, &order.SubmittedAt, &order.ApprovedBy,
		&order.ApprovedAt, &order.RejectedBy, &order.RejectedAt,
		&order.RejectionReason, &order.ExpectedDeliveryAt, &order.OrderedBy,
		&order.OrderedAt, &order.CancelledBy, &order.CancelledAt,
		&order.CancellationReason, &order.ClosedBy, &order.ClosedAt,
		&order.Notes, &order.Version, &order.CreatedAt, &order.UpdatedAt,
		&order.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &order, nil
}

const purchaseOrderLineColumns = `id, organization_id, purchase_order_id, line_no,
	medicine_id, variant_id, unit_id, unit_conversion_numerator,
	unit_conversion_denominator, ordered_quantity, accepted_quantity,
	unit_price_minor, discount_rate_bps, discount_amount_minor, tax_rate_bps,
	tax_amount_minor, line_total_minor, product_name_snapshot, sku_snapshot,
	unit_name_snapshot, tax_policy_snapshot, expected_delivery_at, version,
	created_at, updated_at, deleted_at`

func scanPurchaseOrderLine(row pgx.Row) (*PurchaseOrderLine, error) {
	var line PurchaseOrderLine
	err := row.Scan(
		&line.ID, &line.OrganizationID, &line.PurchaseOrderID, &line.LineNo,
		&line.MedicineID, &line.VariantID, &line.UnitID,
		&line.UnitConversionNumerator, &line.UnitConversionDenominator,
		&line.OrderedQuantity, &line.AcceptedQuantity, &line.UnitPriceMinor,
		&line.DiscountRate, &line.DiscountAmount, &line.TaxRate, &line.TaxAmount,
		&line.LineTotal, &line.ProductNameSnapshot, &line.SKU,
		&line.UnitNameSnapshot, &line.TaxPolicySnapshot, &line.ExpectedDeliveryAt,
		&line.Version, &line.CreatedAt, &line.UpdatedAt, &line.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &line, nil
}

// CreateDraft inserts the header, immutable creation transition, and lines as
// one transaction. If called within a service transaction, db.WithTx uses a
// savepoint and does not commit the outer transaction.
func (r *Repository) CreateDraft(
	ctx context.Context,
	scope PurchaseScope,
	order *PurchaseOrder,
	lines []PurchaseOrderLine,
	operationID uuid.UUID,
) (*PurchaseOrder, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if order == nil || order.OrganizationID != scope.OrganizationID ||
		!scope.allowsBranch(order.BranchID) {
		return nil, fmt.Errorf("purchase order is outside the authorized scope")
	}
	if operationID == uuid.Nil || order.RequestedBy == uuid.Nil ||
		strings.TrimSpace(order.PurchaseNo) == "" || strings.TrimSpace(order.CurrencyCode) == "" {
		return nil, fmt.Errorf("draft requires purchase number, currency, requester, and operation identity")
	}
	if len(lines) == 0 || len(lines) > maxPurchaseLines {
		return nil, fmt.Errorf("draft must contain between 1 and %d lines", maxPurchaseLines)
	}
	draftLines := append([]PurchaseOrderLine(nil), lines...)
	if order.OrderStatus != "" && order.OrderStatus != PurchaseOrderStatusDraft {
		return nil, fmt.Errorf("new purchase order must be a draft")
	}

	created := new(PurchaseOrder)
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const insertOrder = `INSERT INTO purchase_orders (
			organization_id, branch_id, supplier_id, purchase_no, reference_no,
			order_status, receipt_status, invoice_status, closure_status,
			currency_code, policy_snapshot, requested_by, expected_delivery_at,
			notes, version
		) VALUES ($1,$2,$3,$4,$5,'draft','not_received','not_invoiced','open',
			$6,$7,$8,$9,$10,1)
		RETURNING ` + purchaseOrderColumns
		row := r.db.FromCtx(txCtx).QueryRow(txCtx, insertOrder,
			order.OrganizationID, order.BranchID, order.SupplierID, order.PurchaseNo,
			order.ReferenceNo, order.CurrencyCode, order.PolicySnapshot,
			order.RequestedBy, order.ExpectedDeliveryAt, order.Notes,
		)
		var err error
		created, err = scanPurchaseOrder(row)
		if err != nil {
			return err
		}

		for i := range draftLines {
			if err := validateDraftLine(draftLines[i], scope.OrganizationID, created.ID); err != nil {
				return fmt.Errorf("lines[%d]: %w", i, err)
			}
			draftLines[i].LineNo = int32(i + 1)
		}
		if err := r.insertDraftLines(txCtx, scope.OrganizationID, created.ID, draftLines); err != nil {
			return err
		}
		return r.insertTransition(txCtx, PurchaseTransition{
			OrganizationID:  scope.OrganizationID,
			BranchID:        created.BranchID,
			PurchaseOrderID: created.ID,
			OperationID:     operationID,
			Dimension:       "order",
			ToState:         string(PurchaseOrderStatusDraft),
			Action:          PurchaseTransitionActionCreated,
			ActorID:         order.RequestedBy,
		})
	})
	if err != nil {
		return nil, err
	}
	return r.FindByID(ctx, scope, created.ID)
}

func validateDraftLine(line PurchaseOrderLine, organizationID, orderID uuid.UUID) error {
	if (line.OrganizationID != uuid.Nil && line.OrganizationID != organizationID) ||
		(line.PurchaseOrderID != uuid.Nil && line.PurchaseOrderID != orderID) {
		return fmt.Errorf("line organization or order does not match its parent")
	}
	if line.MedicineID == uuid.Nil || line.UnitID == uuid.Nil ||
		line.OrderedQuantity <= 0 || line.UnitPriceMinor < 0 ||
		line.UnitConversionNumerator <= 0 || line.UnitConversionDenominator <= 0 ||
		line.DiscountRate < 0 || line.DiscountRate > 10000 ||
		line.TaxRate < 0 || line.TaxRate > 10000 ||
		line.DiscountAmount < 0 || line.TaxAmount < 0 || line.LineTotal < 0 ||
		strings.TrimSpace(line.ProductNameSnapshot) == "" ||
		strings.TrimSpace(line.UnitNameSnapshot) == "" {
		return fmt.Errorf("invalid identity, conversion, quantity, price, rates, or snapshots")
	}
	if line.VariantID != nil && *line.VariantID == uuid.Nil {
		return fmt.Errorf("variant id must not be empty")
	}
	return nil
}

func (r *Repository) insertDraftLines(ctx context.Context, organizationID, orderID uuid.UUID, lines []PurchaseOrderLine) error {
	const insertLine = `INSERT INTO purchase_order_lines (
		organization_id, purchase_order_id, line_no, medicine_id, variant_id,
		unit_id, unit_conversion_numerator, unit_conversion_denominator,
		ordered_quantity, accepted_quantity, unit_price_minor, discount_rate_bps,
		discount_amount_minor, tax_rate_bps, tax_amount_minor, line_total_minor,
		product_name_snapshot, sku_snapshot, unit_name_snapshot,
		tax_policy_snapshot, expected_delivery_at, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,0,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,1)`

	batch := &pgx.Batch{}
	for _, line := range lines {
		batch.Queue(insertLine,
			organizationID, orderID, line.LineNo, line.MedicineID, line.VariantID,
			line.UnitID, line.UnitConversionNumerator, line.UnitConversionDenominator,
			line.OrderedQuantity, line.UnitPriceMinor, line.DiscountRate,
			line.DiscountAmount, line.TaxRate, line.TaxAmount, line.LineTotal,
			line.ProductNameSnapshot, line.SKU, line.UnitNameSnapshot,
			line.TaxPolicySnapshot, line.ExpectedDeliveryAt,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	for range lines {
		if _, err := results.Exec(); err != nil {
			if closeErr := results.Close(); closeErr != nil {
				return errors.Join(err, closeErr)
			}
			return err
		}
	}
	return results.Close()
}

// UpdateDraft replaces caller-selected draft fields and optionally replaces
// all lines. expectedVersion must be the version last read by the caller.
func (r *Repository) UpdateDraft(
	ctx context.Context,
	scope PurchaseScope,
	order *PurchaseOrder,
	expectedVersion int64,
	lines *[]PurchaseOrderLine,
	operationID uuid.UUID,
	actorID uuid.UUID,
) (*PurchaseOrder, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if order == nil || order.ID == uuid.Nil ||
		order.OrganizationID != scope.OrganizationID || !scope.allowsBranch(order.BranchID) {
		return nil, fmt.Errorf("purchase order is outside the authorized scope")
	}
	if expectedVersion < 1 || operationID == uuid.Nil || actorID == uuid.Nil {
		return nil, fmt.Errorf("version, operation identity, and actor are required")
	}
	if lines != nil && (len(*lines) == 0 || len(*lines) > maxPurchaseLines) {
		return nil, fmt.Errorf("draft must contain between 1 and %d lines", maxPurchaseLines)
	}
	var replacementLines []PurchaseOrderLine
	if lines != nil {
		replacementLines = append([]PurchaseOrderLine(nil), (*lines)...)
	}

	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const updateOrder = `UPDATE purchase_orders SET
			reference_no = $4, expected_delivery_at = $5, notes = $6,
			version = version + 1
		WHERE id = $1 AND organization_id = $2 AND branch_id = $3
			AND order_status = 'draft' AND deleted_at IS NULL AND version = $7
		RETURNING ` + purchaseOrderColumns
		updated, err := scanPurchaseOrder(r.db.FromCtx(txCtx).QueryRow(txCtx, updateOrder,
			order.ID, scope.OrganizationID, order.BranchID, order.ReferenceNo,
			order.ExpectedDeliveryAt, order.Notes, expectedVersion,
		))
		if errors.Is(err, db.ErrNoRows) {
			if _, findErr := r.FindByID(txCtx, scope, order.ID); findErr != nil {
				return findErr
			}
			return ErrPurchaseOrderConflict
		}
		if err != nil {
			return err
		}

		if lines != nil {
			for i := range replacementLines {
				if err := validateDraftLine(replacementLines[i], scope.OrganizationID, updated.ID); err != nil {
					return fmt.Errorf("lines[%d]: %w", i, err)
				}
				replacementLines[i].LineNo = int32(i + 1)
			}
			if _, err := r.db.FromCtx(txCtx).Exec(txCtx,
				`DELETE FROM purchase_order_lines
				 WHERE organization_id = $1 AND purchase_order_id = $2 AND deleted_at IS NULL`,
				scope.OrganizationID, updated.ID,
			); err != nil {
				return err
			}
			if err := r.insertDraftLines(txCtx, scope.OrganizationID, updated.ID, replacementLines); err != nil {
				return err
			}
		}

		from := string(PurchaseOrderStatusDraft)
		return r.insertTransition(txCtx, PurchaseTransition{
			OrganizationID:  scope.OrganizationID,
			BranchID:        updated.BranchID,
			PurchaseOrderID: updated.ID,
			OperationID:     operationID,
			Dimension:       "order",
			FromState:       &from,
			ToState:         string(PurchaseOrderStatusDraft),
			Action:          PurchaseTransitionActionUpdated,
			ActorID:         actorID,
		})
	})
	if err != nil {
		return nil, err
	}
	return r.FindByID(ctx, scope, order.ID)
}

var ErrPurchaseOrderConflict = errors.New("purchase order is not a mutable draft or its version is stale")

func (r *Repository) insertTransition(ctx context.Context, transition PurchaseTransition) error {
	const q = `INSERT INTO purchase_transitions (
		organization_id, branch_id, purchase_order_id, operation_id, dimension,
		from_state, to_state, action, actor_id, reason, policy_version, occurred_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		transition.OrganizationID, transition.BranchID, transition.PurchaseOrderID,
		transition.OperationID, transition.Dimension, transition.FromState,
		transition.ToState, transition.Action, transition.ActorID,
		transition.Reason, transition.PolicyVersion,
	)
	return err
}

func (r *Repository) FindByID(ctx context.Context, scope PurchaseScope, id uuid.UUID) (*PurchaseOrder, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("purchase order id is required")
	}
	q, args := scopedPurchaseOrderQuery(scope, `SELECT `+purchaseOrderColumns+`
		FROM purchase_orders
		WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL`)
	order, err := scanPurchaseOrder(r.db.FromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
	return order, err
}

func (r *Repository) FindByIDWithLines(ctx context.Context, scope PurchaseScope, id uuid.UUID) (*PurchaseOrder, []PurchaseOrderLine, error) {
	opts := db.DefaultTxOptions
	opts.Isolation = pgx.RepeatableRead
	opts.AccessMode = pgx.ReadOnly

	var order *PurchaseOrder
	var lines []PurchaseOrderLine
	err := r.db.WithTxOptions(ctx, opts, func(readCtx context.Context) error {
		var err error
		order, err = r.FindByID(readCtx, scope, id)
		if err != nil {
			return err
		}
		lines, err = r.findLines(readCtx, scope.OrganizationID, order.ID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return order, lines, nil
}

// FindByIDForUpdate must be called within the transaction that performs the
// associated mutation. It locks the tenant- and branch-scoped header row.
func (r *Repository) FindByIDForUpdate(ctx context.Context, scope PurchaseScope, id uuid.UUID) (*PurchaseOrder, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("purchase order id is required")
	}
	q, args := scopedPurchaseOrderQuery(scope, `SELECT `+purchaseOrderColumns+`
		FROM purchase_orders
		WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL
		FOR UPDATE`)
	return scanPurchaseOrder(r.db.FromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) findLines(ctx context.Context, organizationID, orderID uuid.UUID) ([]PurchaseOrderLine, error) {
	rows, err := r.db.FromCtx(ctx).Query(ctx,
		`SELECT `+purchaseOrderLineColumns+`
		 FROM purchase_order_lines
		 WHERE organization_id = $1 AND purchase_order_id = $2 AND deleted_at IS NULL
		 ORDER BY line_no ASC, id ASC`,
		organizationID, orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := make([]PurchaseOrderLine, 0)
	for rows.Next() {
		line, err := scanPurchaseOrderLine(rows)
		if err != nil {
			return nil, err
		}
		lines = append(lines, *line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

var purchaseOrderSortColumns = map[string]struct{}{
	"purchase_no": {}, "created_at": {}, "updated_at": {},
	"expected_delivery_at": {}, "order_status": {},
}

// List returns only order headers; detail lines are loaded by FindByID to
// avoid an N+1 query for the list response.
func (r *Repository) List(
	ctx context.Context,
	scope PurchaseScope,
	filter ListPurchaseOrdersQuery,
) ([]PurchaseOrder, int64, error) {
	if err := scope.validate(); err != nil {
		return nil, 0, err
	}
	if err := filter.NormalizeAndValidate(); err != nil {
		return nil, 0, err
	}
	if filter.BranchID != nil && !scope.allowsBranch(*filter.BranchID) {
		return []PurchaseOrder{}, 0, nil
	}

	where := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := []any{scope.OrganizationID}
	if !scope.OrgWide {
		args = append(args, scope.BranchIDs)
		where = append(where, "branch_id = ANY($"+strconv.Itoa(len(args))+"::uuid[])")
	}
	add := func(column string, value any) {
		args = append(args, value)
		where = append(where, column+" = $"+strconv.Itoa(len(args)))
	}
	if filter.BranchID != nil {
		add("branch_id", *filter.BranchID)
	}
	if filter.SupplierID != nil {
		add("supplier_id", *filter.SupplierID)
	}
	if filter.OrderStatus != nil {
		add("order_status", *filter.OrderStatus)
	}
	if filter.ReceiptStatus != nil {
		add("receipt_status", *filter.ReceiptStatus)
	}
	if filter.InvoiceStatus != nil {
		add("invoice_status", *filter.InvoiceStatus)
	}
	if filter.ClosureStatus != nil {
		add("closure_status", *filter.ClosureStatus)
	}
	if filter.From != nil {
		args = append(args, *filter.From)
		where = append(where, "created_at >= $"+strconv.Itoa(len(args)))
	}
	if filter.To != nil {
		args = append(args, *filter.To)
		where = append(where, "created_at < $"+strconv.Itoa(len(args)))
	}
	if filter.Q != nil && strings.TrimSpace(*filter.Q) != "" {
		args = append(args, "%"+escapePurchaseLike(strings.TrimSpace(*filter.Q))+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(purchase_no ILIKE $"+n+` ESCAPE '\' OR reference_no ILIKE $`+n+` ESCAPE '\')`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM purchase_orders WHERE `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []PurchaseOrder{}, 0, nil
	}

	orderBy := purchaseOrderOrderBy(filter.Offset.Sort)
	listArgs := append(append([]any(nil), args...), filter.Offset.Limit, filter.Offset.OffsetSQL())
	limitPos := len(listArgs) - 1
	offsetPos := len(listArgs)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+purchaseOrderColumns+` FROM purchase_orders WHERE `+whereSQL+`
		`+orderBy+` LIMIT $`+strconv.Itoa(limitPos)+` OFFSET $`+strconv.Itoa(offsetPos),
		listArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	orders := make([]PurchaseOrder, 0, filter.Offset.Limit)
	for rows.Next() {
		order, err := scanPurchaseOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		orders = append(orders, *order)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func scopedPurchaseOrderQuery(scope PurchaseScope, query string) (string, []any) {
	if scope.OrgWide {
		return query, []any{scope.OrganizationID}
	}
	query = strings.Replace(query, " AND deleted_at IS NULL", " AND deleted_at IS NULL AND branch_id = ANY($2::uuid[])", 1)
	query = strings.Replace(query, "id = $2", "id = $3", 1)
	return query, []any{scope.OrganizationID, scope.BranchIDs}
}

func purchaseOrderOrderBy(sort string) string {
	fields, err := pagination.ParseSort(sort, purchaseOrderSortColumns)
	if err != nil || len(fields) == 0 {
		return "ORDER BY created_at DESC, id DESC"
	}
	parts := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		direction := "ASC"
		if field.Desc {
			direction = "DESC"
		}
		parts = append(parts, field.Field+" "+direction)
	}
	parts = append(parts, "id ASC")
	return "ORDER BY " + strings.Join(parts, ", ")
}

func escapePurchaseLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
