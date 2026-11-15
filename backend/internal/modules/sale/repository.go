package sale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"backend/internal/common"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var (
	ErrNotFound               = errors.New("sale: not found")
	ErrDuplicateOperation     = errors.New("sale: duplicate operation")
	ErrVersionConflict        = errors.New("sale: version conflict")
	ErrInvalidStateTransition = errors.New("sale: invalid state transition")
	ErrInsufficientStock      = errors.New("sale: insufficient stock")
	ErrInvoiceNumberExhausted = errors.New("sale: invoice number exhausted")
)

type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

const saleColumns = `id, organization_id, branch_id, warehouse_id, cashier_id, customer_id, cash_session_id, operation_id,
	sale_no, sale_type, status, payment_status, invoice_status, currency_code,
	subtotal_minor, discount_minor, coupon_discount_minor, tax_minor, other_charges_minor, total_minor,
	paid_minor, change_minor, coupon_id, prescription_id, notes,
	pricing_snapshot, tax_snapshot, policy_version,
	sold_at, completed_at, voided_at, voided_by, void_reason,
	version, created_at, updated_at, deleted_at`

func scanSale(row pgx.Row) (*Sale, error) {
	var s Sale
	err := row.Scan(
		&s.ID, &s.OrganizationID, &s.BranchID, &s.WarehouseID, &s.CashierID, &s.CustomerID, &s.CashSessionID, &s.OperationID,
		&s.SaleNo, &s.SaleType, &s.Status, &s.PaymentStatus, &s.InvoiceStatus, &s.CurrencyCode,
		&s.SubtotalMinor, &s.DiscountMinor, &s.CouponDiscountMinor, &s.TaxMinor, &s.OtherChargesMinor, &s.TotalMinor,
		&s.PaidMinor, &s.ChangeMinor, &s.CouponID, &s.PrescriptionID, &s.Notes,
		&s.PricingSnapshot, &s.TaxSnapshot, &s.PolicyVersion,
		&s.SoldAt, &s.CompletedAt, &s.VoidedAt, &s.VoidedBy, &s.VoidReason,
		&s.Version, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

const saleItemColumns = `id, organization_id, sale_id, line_no,
	medicine_id, variant_id, unit_id,
	entered_quantity, base_quantity, unit_conversion_numerator, unit_conversion_denominator,
	unit_price_minor, discount_minor, tax_minor, line_total_minor, discount_rate_bps, tax_rate_bps,
	product_snapshot, unit_snapshot, tax_snapshot,
	created_at`

func scanSaleItem(row pgx.Row) (*SaleItem, error) {
	var i SaleItem
	err := row.Scan(
		&i.ID, &i.OrganizationID, &i.SaleID, &i.LineNo,
		&i.MedicineID, &i.VariantID, &i.UnitID,
		&i.EnteredQuantity, &i.BaseQuantity, &i.UnitConversionNumerator, &i.UnitConversionDenominator,
		&i.UnitPriceMinor, &i.DiscountMinor, &i.TaxMinor, &i.LineTotalMinor, &i.DiscountRateBPS, &i.TaxRateBPS,
		&i.ProductSnapshot, &i.UnitSnapshot, &i.TaxSnapshot,
		&i.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

const saleStockAllocationColumns = `id, organization_id, sale_id, sale_item_id, warehouse_id, batch_id,
	inventory_movement_id, inventory_cost_allocation_id, cost_layer_id, base_quantity,
	batch_no_snapshot, expiry_date, unit_cost_minor, total_cost_minor, currency_code, valuation_ref, allocated_at`

func scanSaleStockAllocation(row pgx.Row) (*SaleStockAllocation, error) {
	var a SaleStockAllocation
	err := row.Scan(
		&a.ID, &a.OrganizationID, &a.SaleID, &a.SaleItemID, &a.WarehouseID, &a.BatchID,
		&a.InventoryMovementID, &a.InventoryCostAllocationID, &a.CostLayerID, &a.BaseQuantity,
		&a.BatchNoSnapshot, &a.ExpiryDate, &a.UnitCostMinor, &a.TotalCostMinor, &a.CurrencyCode, &a.ValuationRef, &a.AllocatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

const saleTenderColumns = `id, organization_id, branch_id, sale_id, payment_intent_id, tender_no,
	method, amount_minor, currency_code, reference_no, provider_ref, received_at, received_by, settlement_ref`

func scanSaleTender(row pgx.Row) (*SaleTender, error) {
	var t SaleTender
	err := row.Scan(
		&t.ID, &t.OrganizationID, &t.BranchID, &t.SaleID, &t.PaymentIntentID, &t.TenderNo,
		&t.Method, &t.AmountMinor, &t.CurrencyCode, &t.ReferenceNo, &t.ProviderRef, &t.ReceivedAt, &t.ReceivedBy, &t.SettlementRef,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

const salePaymentIntentColumns = `id, organization_id, branch_id, operation_id, sale_id,
	provider_code, provider_account_id, provider_idempotency_key, external_reference,
	amount_minor, currency_code, status, expires_at, submitted_at, settled_at, failed_at, failure_code, failure_message, version`

func scanSalePaymentIntent(row pgx.Row) (*SalePaymentIntent, error) {
	var p SalePaymentIntent
	err := row.Scan(
		&p.ID, &p.OrganizationID, &p.BranchID, &p.OperationID, &p.SaleID,
		&p.ProviderCode, &p.ProviderAccountID, &p.ProviderIdempotencyKey, &p.ExternalReference,
		&p.AmountMinor, &p.CurrencyCode, &p.Status, &p.ExpiresAt, &p.SubmittedAt, &p.SettledAt, &p.FailedAt, &p.FailureCode, &p.FailureMessage, &p.Version,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const saleInvoiceColumns = `id, organization_id, branch_id, sale_id, series, fiscal_key, invoice_no, issued_at, currency_code, snapshot, snapshot_hash, template_version`

func scanSaleInvoice(row pgx.Row) (*SaleInvoice, error) {
	var inv SaleInvoice
	err := row.Scan(
		&inv.ID, &inv.OrganizationID, &inv.BranchID, &inv.SaleID,
		&inv.Series, &inv.FiscalKey, &inv.InvoiceNo, &inv.IssuedAt, &inv.CurrencyCode,
		&inv.Snapshot, &inv.SnapshotHash, &inv.TemplateVersion,
	)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

const saleInvoiceCounterColumns = `organization_id, branch_id, series, fiscal_key, next_number, updated_at`

func scanSaleInvoiceCounter(row pgx.Row) (*SaleInvoiceCounter, error) {
	var c SaleInvoiceCounter
	err := row.Scan(
		&c.OrganizationID, &c.BranchID, &c.Series, &c.FiscalKey, &c.NextNumber, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const salesReturnColumns = `id, organization_id, branch_id, sale_id, operation_id, return_no, status, reason, currency_code, credit_total_minor, policy_snapshot, evidence_refs,
	requested_by, requested_at, approved_by, approved_at, rejected_by, rejected_at, rejected_reason, posted_by, posted_at, version`

func scanSalesReturn(row pgx.Row) (*SalesReturn, error) {
	var r SalesReturn
	err := row.Scan(
		&r.ID, &r.OrganizationID, &r.BranchID, &r.SaleID, &r.OperationID, &r.ReturnNo, &r.Status, &r.Reason, &r.CurrencyCode, &r.CreditTotalMinor, &r.PolicySnapshot, &r.EvidenceRefs,
		&r.RequestedBy, &r.RequestedAt, &r.ApprovedBy, &r.ApprovedAt, &r.RejectedBy, &r.RejectedAt, &r.RejectedReason, &r.PostedBy, &r.PostedAt, &r.Version,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

const salesReturnItemColumns = `id, organization_id, sales_return_id, sale_item_id, stock_allocation_id, batch_id, line_no, quantity, disposition, disposition_reason, credit_minor, original_cost_minor, currency_code, inventory_movement_id`

func scanSalesReturnItem(row pgx.Row) (*SalesReturnItem, error) {
	var i SalesReturnItem
	err := row.Scan(
		&i.ID, &i.OrganizationID, &i.SalesReturnID, &i.SaleItemID, &i.StockAllocationID, &i.BatchID, &i.LineNo, &i.Quantity, &i.Disposition, &i.DispositionReason, &i.CreditMinor, &i.OriginalCostMinor, &i.CurrencyCode, &i.InventoryMovementID,
	)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

const saleRefundColumns = `id, organization_id, branch_id, sale_id, sales_return_id, original_tender_id, operation_id, refund_no, amount_minor, currency_code, method, status, provider_ref, reason, requested_by, requested_at, settled_at, failed_at, failure_code, ledger_posting_ref, version`

func scanSaleRefund(row pgx.Row) (*SaleRefund, error) {
	var r SaleRefund
	err := row.Scan(
		&r.ID, &r.OrganizationID, &r.BranchID, &r.SaleID, &r.SalesReturnID, &r.OriginalTenderID, &r.OperationID, &r.RefundNo, &r.AmountMinor, &r.CurrencyCode, &r.Method, &r.Status, &r.ProviderRef, &r.Reason, &r.RequestedBy, &r.RequestedAt, &r.SettledAt, &r.FailedAt, &r.FailureCode, &r.LedgerPostingRef, &r.Version,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

const salePrescriptionEvidenceColumns = `id, organization_id, branch_id, sale_id, reviewer_id, reviewed_at, prescription_ref, prescriber_ref, evidence_refs, decision, policy_version, decision_reason`

func scanSalePrescriptionEvidence(row pgx.Row) (*SalePrescriptionEvidence, error) {
	var e SalePrescriptionEvidence
	err := row.Scan(
		&e.ID, &e.OrganizationID, &e.BranchID, &e.SaleID, &e.ReviewerID, &e.ReviewedAt, &e.PrescriptionRef, &e.PrescriberRef, &e.EvidenceRefs, &e.Decision, &e.PolicyVersion, &e.DecisionReason,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

const salePriceBookColumns = `id, organization_id, branch_id, code, name, currency_code, is_active, version`

func scanSalePriceBook(row pgx.Row) (*SalePriceBook, error) {
	var p SalePriceBook
	err := row.Scan(
		&p.ID, &p.OrganizationID, &p.BranchID, &p.Code, &p.Name, &p.CurrencyCode, &p.IsActive, &p.Version,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const salePriceVersionColumns = `id, organization_id, price_book_id, medicine_id, variant_id, unit_id, unit_price_minor, currency_code, valid_from, valid_to, version`

func scanSalePriceVersion(row pgx.Row) (*SalePriceVersion, error) {
	var v SalePriceVersion
	err := row.Scan(
		&v.ID, &v.OrganizationID, &v.PriceBookID, &v.MedicineID, &v.VariantID, &v.UnitID,
		&v.UnitPriceMinor, &v.CurrencyCode, &v.ValidFrom, &v.ValidTo, &v.Version,
	)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

const salePromotionColumns = `id, organization_id, code, name, description, kind, config, currency_code, min_purchase_minor, max_discount_minor, discount_rate_bps, flat_discount_minor, valid_from, valid_to, usage_limit, usage_count, per_customer_limit, is_active, version`

func scanSalePromotion(row pgx.Row) (*SalePromotion, error) {
	var p SalePromotion
	err := row.Scan(
		&p.ID, &p.OrganizationID, &p.Code, &p.Name, &p.Description, &p.Kind, &p.Config,
		&p.CurrencyCode, &p.MinPurchaseMinor, &p.MaxDiscountMinor, &p.DiscountRateBPS, &p.FlatDiscountMinor,
		&p.ValidFrom, &p.ValidTo, &p.UsageLimit, &p.UsageCount, &p.PerCustomerLimit, &p.IsActive, &p.Version,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const salePromotionRedemptionColumns = `id, organization_id, promotion_id, sale_id, customer_id, redemption_count, discount_minor, currency_code, redeemed_at`

func scanSalePromotionRedemption(row pgx.Row) (*SalePromotionRedemption, error) {
	var r SalePromotionRedemption
	err := row.Scan(
		&r.ID, &r.OrganizationID, &r.PromotionID, &r.SaleID, &r.CustomerID, &r.RedemptionCount, &r.DiscountMinor, &r.CurrencyCode, &r.RedeemedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

const saleCashSessionColumns = `id, organization_id, branch_id, cashier_id, drawer_id, session_no, status, currency_code, opening_minor, expected_minor, counted_minor, variance_minor, opened_at, opened_by, closing_started_at, closed_at, closed_by, close_reason, version`

func scanSaleCashSession(row pgx.Row) (*SaleCashSession, error) {
	var s SaleCashSession
	err := row.Scan(
		&s.ID, &s.OrganizationID, &s.BranchID, &s.CashierID, &s.DrawerID, &s.SessionNo, &s.Status, &s.CurrencyCode, &s.OpeningMinor, &s.ExpectedMinor, &s.CountedMinor, &s.VarianceMinor, &s.OpenedAt, &s.OpenedBy, &s.ClosingStartedAt, &s.ClosedAt, &s.ClosedBy, &s.CloseReason, &s.Version,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

const saleCashMovementColumns = `id, organization_id, branch_id, cash_session_id, movement_type, amount_minor, currency_code, source_type, source_id, operation_id, actor_id, occurred_at, reason`

func scanSaleCashMovement(row pgx.Row) (*SaleCashMovement, error) {
	var m SaleCashMovement
	err := row.Scan(
		&m.ID, &m.OrganizationID, &m.BranchID, &m.CashSessionID, &m.MovementType, &m.AmountMinor, &m.CurrencyCode, &m.SourceType, &m.SourceID, &m.OperationID, &m.ActorID, &m.OccurredAt, &m.Reason,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

const saleOperationColumns = `id, organization_id, branch_id, operation_id, operation_type, idempotency_key_hash, request_hash, status, resource_type, resource_id, response_code, response_body, started_at, completed_at, expires_at`

func scanSaleOperation(row pgx.Row) (*SaleOperation, error) {
	var o SaleOperation
	err := row.Scan(
		&o.ID, &o.OrganizationID, &o.BranchID, &o.OperationID, &o.OperationType, &o.IdempotencyKeyHash, &o.RequestHash, &o.Status, &o.ResourceType, &o.ResourceID, &o.ResponseCode, &o.ResponseBody, &o.StartedAt, &o.CompletedAt, &o.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

const saleTransitionColumns = `id, organization_id, branch_id, aggregate_type, aggregate_id, dimension, from_state, to_state, action, operation_id, actor_id, reason, policy_version, occurred_at`

func scanSaleTransition(row pgx.Row) (*SaleTransition, error) {
	var t SaleTransition
	err := row.Scan(
		&t.ID, &t.OrganizationID, &t.BranchID, &t.AggregateType, &t.AggregateID, &t.Dimension, &t.FromState, &t.ToState, &t.Action, &t.OperationID, &t.ActorID, &t.Reason, &t.PolicyVersion, &t.OccurredAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

const saleCartColumns = `id, organization_id, branch_id, owner_user_id, content, expires_at, version`

func scanSaleCart(row pgx.Row) (*SaleCart, error) {
	var c SaleCart
	err := row.Scan(
		&c.ID, &c.OrganizationID, &c.BranchID, &c.OwnerUserID, &c.Content, &c.ExpiresAt, &c.Version,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const salePaymentEventColumns = `id, organization_id, payment_intent_id, provider_code, provider_account_id, provider_event_id, event_type, amount_minor, currency_code, occurred_at, received_at, evidence_snapshot`

func scanSalePaymentEvent(row pgx.Row) (*SalePaymentEvent, error) {
	var e SalePaymentEvent
	err := row.Scan(
		&e.ID, &e.OrganizationID, &e.PaymentIntentID, &e.ProviderCode, &e.ProviderAccountID, &e.ProviderEventID, &e.EventType, &e.AmountMinor, &e.CurrencyCode, &e.OccurredAt, &e.ReceivedAt, &e.EvidenceSnapshot,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

type SaleScope struct {
	OrganizationID uuid.UUID
	BranchIDs      []uuid.UUID
	OrgWide        bool
}

func (s SaleScope) validate() error {
	if s.OrganizationID == uuid.Nil {
		return fmt.Errorf("sale scope requires an organization")
	}
	if !s.OrgWide && len(s.BranchIDs) == 0 {
		return fmt.Errorf("sale scope has no accessible branches")
	}
	seen := make(map[uuid.UUID]struct{}, len(s.BranchIDs))
	for _, id := range s.BranchIDs {
		if id == uuid.Nil {
			return fmt.Errorf("sale scope contains an empty branch")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("sale scope contains a duplicate branch")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s SaleScope) allowsBranch(id uuid.UUID) bool {
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

func scopedSaleQuery(scope SaleScope, query string) (string, []any) {
	if scope.OrgWide {
		return query, []any{scope.OrganizationID}
	}
	query = strings.Replace(query, " AND deleted_at IS NULL", " AND deleted_at IS NULL AND branch_id = ANY($2::uuid[])", 1)
	query = strings.Replace(query, "id = $2", "id = $3", 1)
	query = strings.Replace(query, "sale_id = $2", "sale_id = $3", 1)
	query = strings.Replace(query, "cash_session_id = $2", "cash_session_id = $3", 1)
	return query, []any{scope.OrganizationID, scope.BranchIDs}
}

func (r *Repository) CreateSaleWithTx(
	ctx context.Context,
	sale *Sale,
	items []SaleItem,
	allocations []SaleStockAllocation,
	tenders []SaleTender,
	intents []SalePaymentIntent,
	invoice *SaleInvoice,
	operation *SaleOperation,
	transitions []SaleTransition,
) (*Sale, error) {
	if sale == nil {
		return nil, fmt.Errorf("sale is required")
	}
	if sale.OrganizationID == uuid.Nil || sale.BranchID == uuid.Nil {
		return nil, fmt.Errorf("sale organization and branch are required")
	}
	if sale.OperationID == uuid.Nil || sale.CashierID == uuid.Nil || sale.WarehouseID == uuid.Nil || sale.SaleNo == "" {
		return nil, fmt.Errorf("sale requires operation_id, cashier_id, warehouse_id, and sale_no")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("sale must have at least one item")
	}
	if len(tenders) == 0 {
		return nil, fmt.Errorf("sale must have at least one tender")
	}

	var created *Sale
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const insertSale = `INSERT INTO sales (
			organization_id, branch_id, warehouse_id, cashier_id, customer_id, cash_session_id, operation_id,
			sale_no, sale_type, status, payment_status, invoice_status, currency_code,
			subtotal_minor, discount_minor, coupon_discount_minor, tax_minor, other_charges_minor, total_minor,
			paid_minor, change_minor, coupon_id, prescription_id, notes,
			pricing_snapshot, tax_snapshot, policy_version,
			sold_at, version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,1)
		RETURNING ` + saleColumns

		row := r.db.FromCtx(txCtx).QueryRow(txCtx, insertSale,
			sale.OrganizationID, sale.BranchID, sale.WarehouseID, sale.CashierID, sale.CustomerID, sale.CashSessionID, sale.OperationID,
			sale.SaleNo, sale.SaleType, sale.Status, sale.PaymentStatus, sale.InvoiceStatus, sale.CurrencyCode,
			sale.SubtotalMinor, sale.DiscountMinor, sale.CouponDiscountMinor, sale.TaxMinor, sale.OtherChargesMinor, sale.TotalMinor,
			sale.PaidMinor, sale.ChangeMinor, sale.CouponID, sale.PrescriptionID, sale.Notes,
			sale.PricingSnapshot, sale.TaxSnapshot, sale.PolicyVersion,
			sale.SoldAt,
		)
		var err error
		created, err = scanSale(row)
		if err != nil {
			return err
		}

		for i := range items {
			items[i].SaleID = created.ID
			items[i].OrganizationID = created.OrganizationID
			items[i].LineNo = int32(i + 1)
		}
		if err := r.insertSaleItems(txCtx, created.OrganizationID, created.ID, items); err != nil {
			return err
		}

		if len(allocations) > 0 {
			if err := r.insertSaleStockAllocations(txCtx, created.OrganizationID, created.ID, allocations); err != nil {
				return err
			}
		}

		if len(tenders) > 0 {
			if err := r.insertSaleTenders(txCtx, created.OrganizationID, created.BranchID, created.ID, tenders); err != nil {
				return err
			}
		}

		if len(intents) > 0 {
			if err := r.insertSalePaymentIntents(txCtx, created.OrganizationID, created.BranchID, created.ID, intents); err != nil {
				return err
			}
		}

		if invoice != nil {
			invoice.SaleID = created.ID
			invoice.OrganizationID = created.OrganizationID
			invoice.BranchID = created.BranchID
			if err := r.insertSaleInvoice(txCtx, invoice); err != nil {
				return err
			}
		}

		if operation != nil {
			operation.OrganizationID = created.OrganizationID
			operation.BranchID = created.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}

		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "operation_id") {
				return nil, ErrDuplicateOperation
			}
			if strings.Contains(pgErr.ConstraintName, "sale_no") {
				return nil, fmt.Errorf("sale_no already exists")
			}
		}
		return nil, err
	}
	return created, nil
}

func (r *Repository) insertSaleItems(ctx context.Context, orgID, saleID uuid.UUID, items []SaleItem) error {
	const insertItem = `INSERT INTO sale_items (
		organization_id, sale_id, line_no, medicine_id, variant_id, unit_id,
		entered_quantity, base_quantity, unit_conversion_numerator, unit_conversion_denominator,
		unit_price_minor, discount_minor, tax_minor, line_total_minor, discount_rate_bps, tax_rate_bps,
		product_snapshot, unit_snapshot, tax_snapshot, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,now())`

	batch := &pgx.Batch{}
	for _, item := range items {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now()
		}
		batch.Queue(insertItem,
			item.OrganizationID, item.SaleID, item.LineNo, item.MedicineID, item.VariantID, item.UnitID,
			item.EnteredQuantity, item.BaseQuantity, item.UnitConversionNumerator, item.UnitConversionDenominator,
			item.UnitPriceMinor, item.DiscountMinor, item.TaxMinor, item.LineTotalMinor, item.DiscountRateBPS, item.TaxRateBPS,
			item.ProductSnapshot, item.UnitSnapshot, item.TaxSnapshot,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range items {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) insertSaleStockAllocations(ctx context.Context, orgID, saleID uuid.UUID, allocations []SaleStockAllocation) error {
	const insertAlloc = `INSERT INTO sale_stock_allocations (
		organization_id, sale_id, sale_item_id, warehouse_id, batch_id,
		inventory_movement_id, inventory_cost_allocation_id, cost_layer_id, base_quantity,
		batch_no_snapshot, expiry_date, unit_cost_minor, total_cost_minor, currency_code, valuation_ref, allocated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now())`

	batch := &pgx.Batch{}
	for _, alloc := range allocations {
		if alloc.AllocatedAt.IsZero() {
			alloc.AllocatedAt = time.Now()
		}
		batch.Queue(insertAlloc,
			alloc.OrganizationID, alloc.SaleID, alloc.SaleItemID, alloc.WarehouseID, alloc.BatchID,
			alloc.InventoryMovementID, alloc.InventoryCostAllocationID, alloc.CostLayerID, alloc.BaseQuantity,
			alloc.BatchNoSnapshot, alloc.ExpiryDate, alloc.UnitCostMinor, alloc.TotalCostMinor, alloc.CurrencyCode, alloc.ValuationRef,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range allocations {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) insertSaleTenders(ctx context.Context, orgID, branchID, saleID uuid.UUID, tenders []SaleTender) error {
	const insertTender = `INSERT INTO sale_tenders (
		organization_id, branch_id, sale_id, payment_intent_id, tender_no,
		method, amount_minor, currency_code, reference_no, provider_ref, received_at, received_by, settlement_ref
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`

	batch := &pgx.Batch{}
	for _, tender := range tenders {
		batch.Queue(insertTender,
			orgID, branchID, saleID, tender.PaymentIntentID, tender.TenderNo,
			tender.Method, tender.AmountMinor, tender.CurrencyCode, tender.ReferenceNo, tender.ProviderRef, tender.ReceivedAt, tender.ReceivedBy, tender.SettlementRef,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range tenders {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) insertSalePaymentIntents(ctx context.Context, orgID, branchID, saleID uuid.UUID, intents []SalePaymentIntent) error {
	const insertIntent = `INSERT INTO sale_payment_intents (
		organization_id, branch_id, operation_id, sale_id,
		provider_code, provider_account_id, provider_idempotency_key, external_reference,
		amount_minor, currency_code, status, expires_at, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1)`

	batch := &pgx.Batch{}
	for _, intent := range intents {
		if intent.Version == 0 {
			intent.Version = 1
		}
		batch.Queue(insertIntent,
			orgID, branchID, intent.OperationID, saleID,
			intent.ProviderCode, intent.ProviderAccountID, intent.ProviderIdempotencyKey, intent.ExternalReference,
			intent.AmountMinor, intent.CurrencyCode, intent.Status, intent.ExpiresAt,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range intents {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) insertSaleInvoice(ctx context.Context, inv *SaleInvoice) error {
	const insertInv = `INSERT INTO sale_invoices (
		organization_id, branch_id, sale_id, series, fiscal_key, invoice_no, issued_at, currency_code, snapshot, snapshot_hash, template_version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

	_, err := r.db.FromCtx(ctx).Exec(ctx, insertInv,
		inv.OrganizationID, inv.BranchID, inv.SaleID, inv.Series, inv.FiscalKey, inv.InvoiceNo, inv.IssuedAt, inv.CurrencyCode, inv.Snapshot, inv.SnapshotHash, inv.TemplateVersion,
	)
	return err
}

func (r *Repository) insertSaleOperation(ctx context.Context, op *SaleOperation) error {
	const insertOp = `INSERT INTO sale_operations (
		organization_id, branch_id, operation_id, operation_type, idempotency_key_hash, request_hash, status, resource_type, resource_id, response_code, response_body, started_at, expires_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`

	_, err := r.db.FromCtx(ctx).Exec(ctx, insertOp,
		op.OrganizationID, op.BranchID, op.OperationID, op.OperationType, op.IdempotencyKeyHash, op.RequestHash, op.Status, op.ResourceType, op.ResourceID, op.ResponseCode, op.ResponseBody, op.StartedAt, op.ExpiresAt,
	)
	return err
}

func (r *Repository) insertSaleTransitions(ctx context.Context, transitions []SaleTransition) error {
	const insertTrans = `INSERT INTO sale_transitions (
		organization_id, branch_id, aggregate_type, aggregate_id, dimension, from_state, to_state, action, operation_id, actor_id, reason, policy_version, occurred_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())`

	batch := &pgx.Batch{}
	for _, t := range transitions {
		batch.Queue(insertTrans,
			t.OrganizationID, t.BranchID, t.AggregateType, t.AggregateID, t.Dimension, t.FromState, t.ToState, t.Action, t.OperationID, t.ActorID, t.Reason, t.PolicyVersion,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range transitions {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) FindSaleByID(ctx context.Context, scope SaleScope, id uuid.UUID) (*Sale, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("sale id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+saleColumns+` FROM sales WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL`)
	return scanSale(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) FindSaleByIDForUpdate(ctx context.Context, scope SaleScope, id uuid.UUID) (*Sale, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("sale id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+saleColumns+` FROM sales WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL FOR UPDATE`)
	return scanSale(r.db.FromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) FindSaleByOperationID(ctx context.Context, orgID uuid.UUID, operationID uuid.UUID) (*Sale, error) {
	if orgID == uuid.Nil || operationID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and operation_id are required")
	}
	const q = `SELECT ` + saleColumns + ` FROM sales WHERE operation_id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanSale(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, operationID, orgID))
}

func (r *Repository) FindSaleByNo(ctx context.Context, scope SaleScope, saleNo string) (*Sale, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(saleNo) == "" {
		return nil, fmt.Errorf("sale_no is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+saleColumns+` FROM sales WHERE sale_no = $2 AND organization_id = $1 AND deleted_at IS NULL`)
	return scanSale(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, append(args, saleNo)...))
}

func (r *Repository) FindSaleWithItems(ctx context.Context, scope SaleScope, id uuid.UUID) (*Sale, []SaleItem, error) {
	sale, err := r.FindSaleByID(ctx, scope, id)
	if err != nil {
		return nil, nil, err
	}
	items, err := r.ListSaleItems(ctx, sale.OrganizationID, id)
	return sale, items, err
}

func (r *Repository) ListSaleItems(ctx context.Context, orgID, saleID uuid.UUID) ([]SaleItem, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleItemColumns+` FROM sale_items WHERE organization_id = $1 AND sale_id = $2 AND deleted_at IS NULL ORDER BY line_no ASC`,
		orgID, saleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SaleItem
	for rows.Next() {
		item, err := scanSaleItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *Repository) ListSaleStockAllocations(ctx context.Context, orgID, saleID uuid.UUID) ([]SaleStockAllocation, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleStockAllocationColumns+` FROM sale_stock_allocations WHERE organization_id = $1 AND sale_id = $2 ORDER BY id ASC`,
		orgID, saleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var allocs []SaleStockAllocation
	for rows.Next() {
		a, err := scanSaleStockAllocation(rows)
		if err != nil {
			return nil, err
		}
		allocs = append(allocs, *a)
	}
	return allocs, rows.Err()
}

func (r *Repository) ListSaleTenders(ctx context.Context, orgID, branchID, saleID uuid.UUID) ([]SaleTender, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleTenderColumns+` FROM sale_tenders WHERE organization_id = $1 AND branch_id = $2 AND sale_id = $3 ORDER BY id ASC`,
		orgID, branchID, saleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenders []SaleTender
	for rows.Next() {
		t, err := scanSaleTender(rows)
		if err != nil {
			return nil, err
		}
		tenders = append(tenders, *t)
	}
	return tenders, rows.Err()
}

func (r *Repository) ListSalePaymentIntents(ctx context.Context, orgID, branchID, saleID uuid.UUID) ([]SalePaymentIntent, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+salePaymentIntentColumns+` FROM sale_payment_intents WHERE organization_id = $1 AND branch_id = $2 AND sale_id = $3 ORDER BY id ASC`,
		orgID, branchID, saleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []SalePaymentIntent
	for rows.Next() {
		i, err := scanSalePaymentIntent(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, *i)
	}
	return intents, rows.Err()
}

func (r *Repository) FindSaleInvoice(ctx context.Context, orgID, saleID uuid.UUID) (*SaleInvoice, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	const q = `SELECT ` + saleInvoiceColumns + ` FROM sale_invoices WHERE organization_id = $1 AND sale_id = $2`
	return scanSaleInvoice(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, saleID))
}

func (r *Repository) ListSales(ctx context.Context, scope SaleScope, filter ListSalesQuery) ([]Sale, int64, bool, error) {
	if err := scope.validate(); err != nil {
		return nil, 0, false, err
	}
	if errs := filter.NormalizeAndValidate(); len(errs) > 0 {
		return nil, 0, false, errs[0]
	}
	if filter.BranchID != nil && !scope.allowsBranch(*filter.BranchID) {
		return []Sale{}, 0, false, nil
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
	if filter.CashierID != nil {
		add("cashier_id", *filter.CashierID)
	}
	if filter.CustomerID != nil {
		add("customer_id", *filter.CustomerID)
	}
	if filter.Status != nil {
		add("status", *filter.Status)
	}
	if filter.PaymentStatus != nil {
		add("payment_status", *filter.PaymentStatus)
	}
	if filter.InvoiceStatus != nil {
		add("invoice_status", *filter.InvoiceStatus)
	}
	if filter.FromDate != nil {
		args = append(args, *filter.FromDate)
		where = append(where, "sold_at >= $"+strconv.Itoa(len(args)))
	}
	if filter.ToDate != nil {
		args = append(args, *filter.ToDate)
		where = append(where, "sold_at < $"+strconv.Itoa(len(args)))
	}
	if filter.Q != "" {
		args = append(args, "%"+escapeLike(strings.TrimSpace(filter.Q))+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(sale_no ILIKE $"+n+` ESCAPE '\')`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM sales WHERE `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, false, err
	}
	if total == 0 {
		return []Sale{}, 0, false, nil
	}

	sortKey := saleListSortKey(scope, filter)
	var cursor *pagination.Cursor
	if filter.Cursor != "" {
		decoded, err := r.cursorCodec.Decode(filter.Cursor)
		if err != nil || decoded.ValidateSortKey(sortKey) != nil || decoded.CreatedAt.IsZero() {
			return nil, 0, false, pagination.ErrInvalidCursor
		}
		cursor = &decoded
	}

	listArgs := args[:]
	if cursor != nil {
		listArgs = append(listArgs, cursor.CreatedAt, cursor.ID)
		whereSQL += " AND (sold_at, id) < ($" + strconv.Itoa(len(listArgs)-1) + ", $" + strconv.Itoa(len(listArgs)) + ")"
	}
	listArgs = append(listArgs, filter.Limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(listArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleColumns+` FROM sales WHERE `+whereSQL+`
		ORDER BY sold_at DESC, id DESC
		LIMIT `+limitPlaceholder,
		listArgs...,
	)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()

	sales := make([]Sale, 0, filter.Limit)
	for rows.Next() {
		sale, err := scanSale(rows)
		if err != nil {
			return nil, 0, false, err
		}
		if len(sales) == filter.Limit {
			return sales, total, true, nil
		}
		sales = append(sales, *sale)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}

	return sales, total, false, nil
}

func saleListSortKey(scope SaleScope, filter ListSalesQuery) string {
	scopeBytes := []byte{}
	if !scope.OrgWide {
		for _, id := range scope.BranchIDs {
			scopeBytes = append(scopeBytes, id.String()...)
		}
	}
	payload := struct {
		OrgID       string
		BranchID    *uuid.UUID
		BranchScope []uuid.UUID
		CashierID   *uuid.UUID
		CustomerID  *uuid.UUID
		Status      *SaleStatus
		PayStatus   *SalePaymentStatus
		InvStatus   *SaleInvoiceStatus
		FromDate    *time.Time
		ToDate      *time.Time
		Q           string
	}{scope.OrganizationID.String(), filter.BranchID, scope.BranchIDs, filter.CashierID, filter.CustomerID, filter.Status, filter.PaymentStatus, filter.InvoiceStatus, filter.FromDate, filter.ToDate, filter.Q}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "sale-list-v1:" + hex.EncodeToString(sum[:])
}

func (r *Repository) UpdateSaleStatus(ctx context.Context, scope SaleScope, id uuid.UUID, expectedVersion int64, status SaleStatus, paymentStatus *SalePaymentStatus, invoiceStatus *SaleInvoiceStatus, completedAt, voidedAt *time.Time, voidedBy *uuid.UUID, voidReason *string, operation *SaleOperation, transitions []SaleTransition) (*Sale, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("id and expected_version are required")
	}

	var updated *Sale
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		sets := []string{"status = $4", "version = version + 1", "updated_at = now()"}
		args := []any{id, scope.OrganizationID, expectedVersion, status}
		n := 5

		if paymentStatus != nil {
			sets = append(sets, "payment_status = $"+strconv.Itoa(n))
			args = append(args, *paymentStatus)
			n++
		}
		if invoiceStatus != nil {
			sets = append(sets, "invoice_status = $"+strconv.Itoa(n))
			args = append(args, *invoiceStatus)
			n++
		}
		if completedAt != nil {
			sets = append(sets, "completed_at = $"+strconv.Itoa(n))
			args = append(args, *completedAt)
			n++
		}
		if voidedAt != nil {
			sets = append(sets, "voided_at = $"+strconv.Itoa(n), "voided_by = $"+strconv.Itoa(n+1), "void_reason = $"+strconv.Itoa(n+2))
			args = append(args, *voidedAt, voidedBy, voidReason)
			n += 3
		}

		q := `UPDATE sales SET ` + strings.Join(sets, ", ") +
			` WHERE id = $1 AND organization_id = $2 AND version = $3 AND deleted_at IS NULL RETURNING ` + saleColumns

		sale, err := scanSale(r.db.FromCtx(txCtx).QueryRow(txCtx, q, args...))
		if errors.Is(err, db.ErrNoRows) {
			if _, findErr := r.FindSaleByID(txCtx, scope, id); findErr != nil {
				return findErr
			}
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		updated = sale

		if operation != nil {
			operation.OrganizationID = scope.OrganizationID
			operation.BranchID = updated.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}
		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (r *Repository) FindSaleOperation(ctx context.Context, orgID uuid.UUID, operationID uuid.UUID) (*SaleOperation, error) {
	if orgID == uuid.Nil || operationID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and operation_id are required")
	}
	const q = `SELECT ` + saleOperationColumns + ` FROM sale_operations WHERE organization_id = $1 AND operation_id = $2`
	return scanSaleOperation(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, operationID))
}

func (r *Repository) FindSaleOperationForUpdate(ctx context.Context, orgID uuid.UUID, operationID uuid.UUID) (*SaleOperation, error) {
	if orgID == uuid.Nil || operationID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and operation_id are required")
	}
	const q = `SELECT ` + saleOperationColumns + ` FROM sale_operations WHERE organization_id = $1 AND operation_id = $2 FOR UPDATE`
	return scanSaleOperation(r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, operationID))
}

func (r *Repository) CompleteSaleOperation(ctx context.Context, orgID, operationID uuid.UUID, expectedVersion int64, status SaleOperationStatus, resourceType *string, resourceID *uuid.UUID, responseCode *int, responseBody common.JSONMap) error {
	if orgID == uuid.Nil || operationID == uuid.Nil || expectedVersion < 1 {
		return fmt.Errorf("organization_id, operation_id, and expected_version are required")
	}
	const q = `UPDATE sale_operations SET status = $3, resource_type = $4, resource_id = $5, response_code = $6, response_body = $7, completed_at = now(), version = version + 1
		WHERE organization_id = $1 AND operation_id = $2 AND version = $8`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, orgID, operationID, status, resourceType, resourceID, responseCode, responseBody, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return nil
}

func (r *Repository) ListSaleTransitions(ctx context.Context, orgID uuid.UUID, aggregateType string, aggregateID uuid.UUID, dimensions []SaleTransitionDimension) ([]SaleTransition, error) {
	if orgID == uuid.Nil || aggregateID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and aggregate_id are required")
	}
	args := []any{orgID, aggregateType, aggregateID}
	where := "WHERE organization_id = $1 AND aggregate_type = $2 AND aggregate_id = $3"
	if len(dimensions) > 0 {
		dimStrs := make([]string, len(dimensions))
		for i, d := range dimensions {
			args = append(args, d)
			dimStrs[i] = "$" + strconv.Itoa(len(args))
		}
		where += " AND dimension IN (" + strings.Join(dimStrs, ",") + ")"
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleTransitionColumns+` FROM sale_transitions `+where+` ORDER BY occurred_at ASC, id ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var transitions []SaleTransition
	for rows.Next() {
		t, err := scanSaleTransition(rows)
		if err != nil {
			return nil, err
		}
		transitions = append(transitions, *t)
	}
	return transitions, rows.Err()
}

func (r *Repository) FindPaymentIntentByID(ctx context.Context, orgID, branchID, id uuid.UUID) (*SalePaymentIntent, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + salePaymentIntentColumns + ` FROM sale_payment_intents WHERE id = $1 AND organization_id = $2 AND branch_id = $3`
	return scanSalePaymentIntent(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID, branchID))
}

func (r *Repository) FindPaymentIntentForUpdate(ctx context.Context, orgID, branchID, id uuid.UUID) (*SalePaymentIntent, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + salePaymentIntentColumns + ` FROM sale_payment_intents WHERE id = $1 AND organization_id = $2 AND branch_id = $3 FOR UPDATE`
	return scanSalePaymentIntent(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, branchID))
}

func (r *Repository) UpdatePaymentIntentStatus(ctx context.Context, orgID, branchID, id uuid.UUID, expectedVersion int64, status PaymentIntentStatus, submittedAt, settledAt, failedAt *time.Time, failureCode, failureMessage *string) (*SalePaymentIntent, error) {
	if orgID == uuid.Nil || id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("organization_id, id, and expected_version are required")
	}
	const q = `UPDATE sale_payment_intents SET status = $4, submitted_at = $5, settled_at = $6, failed_at = $7, failure_code = $8, failure_message = $9, version = version + 1
		WHERE id = $1 AND organization_id = $2 AND branch_id = $3 AND version = $10 RETURNING ` + salePaymentIntentColumns
	return scanSalePaymentIntent(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, branchID, status, submittedAt, settledAt, failedAt, failureCode, failureMessage, expectedVersion))
}

func (r *Repository) InsertPaymentEvent(ctx context.Context, event *SalePaymentEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now()
	}
	const q = `INSERT INTO sale_payment_events (
		id, organization_id, payment_intent_id, provider_code, provider_account_id, provider_event_id, event_type, amount_minor, currency_code, occurred_at, received_at, evidence_snapshot
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	ON CONFLICT (organization_id, provider_code, provider_account_id, provider_event_id) DO NOTHING`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		event.ID, event.OrganizationID, event.PaymentIntentID, event.ProviderCode, event.ProviderAccountID, event.ProviderEventID,
		event.EventType, event.AmountMinor, event.CurrencyCode, event.OccurredAt, event.ReceivedAt, event.EvidenceSnapshot,
	)
	return err
}

func (r *Repository) FindPaymentEventByProviderEventID(ctx context.Context, orgID uuid.UUID, providerCode, providerAccountID, providerEventID string) (*SalePaymentEvent, error) {
	if orgID == uuid.Nil || providerCode == "" || providerAccountID == "" || providerEventID == "" {
		return nil, fmt.Errorf("required fields missing")
	}
	const q = `SELECT ` + salePaymentEventColumns + ` FROM sale_payment_events WHERE organization_id = $1 AND provider_code = $2 AND provider_account_id = $3 AND provider_event_id = $4`
	return scanSalePaymentEvent(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, providerCode, providerAccountID, providerEventID))
}

func (r *Repository) GetNextInvoiceNumber(ctx context.Context, orgID, branchID uuid.UUID, series, fiscalKey string) (int64, error) {
	if orgID == uuid.Nil || branchID == uuid.Nil || series == "" || fiscalKey == "" {
		return 0, fmt.Errorf("organization_id, branch_id, series, and fiscal_key are required")
	}
	const q = `SELECT next_number FROM sale_invoice_counters WHERE organization_id = $1 AND branch_id = $2 AND series = $3 AND fiscal_key = $4 FOR UPDATE`
	var nextNum int64
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, branchID, series, fiscalKey).Scan(&nextNum)
	if errors.Is(err, db.ErrNoRows) {
		return 0, ErrInvoiceNumberExhausted
	}
	return nextNum, err
}

func (r *Repository) IncrementInvoiceCounter(ctx context.Context, orgID, branchID uuid.UUID, series, fiscalKey string, expectedNext int64) (int64, error) {
	if orgID == uuid.Nil || branchID == uuid.Nil || series == "" || fiscalKey == "" {
		return 0, fmt.Errorf("required fields missing")
	}
	const q = `UPDATE sale_invoice_counters SET next_number = next_number + 1, updated_at = now()
		WHERE organization_id = $1 AND branch_id = $2 AND series = $3 AND fiscal_key = $4 AND next_number = $5
		RETURNING next_number`
	var nextNum int64
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, branchID, series, fiscalKey, expectedNext).Scan(&nextNum)
	if err != nil {
		return 0, err
	}
	return nextNum, nil
}

func (r *Repository) UpsertInvoiceCounter(ctx context.Context, orgID, branchID uuid.UUID, series, fiscalKey string, startNumber int64) error {
	if orgID == uuid.Nil || branchID == uuid.Nil || series == "" || fiscalKey == "" {
		return fmt.Errorf("required fields missing")
	}
	const q = `INSERT INTO sale_invoice_counters (organization_id, branch_id, series, fiscal_key, next_number, updated_at)
		VALUES ($1,$2,$3,$4,$5,now())
		ON CONFLICT (organization_id, branch_id, series, fiscal_key) DO NOTHING`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, orgID, branchID, series, fiscalKey, startNumber)
	return err
}

func (r *Repository) CreateSalesReturnWithTx(
	ctx context.Context,
	ret *SalesReturn,
	items []SalesReturnItem,
	operation *SaleOperation,
	transitions []SaleTransition,
) (*SalesReturn, error) {
	if ret.OrganizationID == uuid.Nil || ret.BranchID == uuid.Nil || ret.SaleID == uuid.Nil || ret.OperationID == uuid.Nil || ret.ReturnNo == "" || ret.RequestedBy == uuid.Nil {
		return nil, fmt.Errorf("required fields missing")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("return must have at least one item")
	}

	var created *SalesReturn
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const insertReturn = `INSERT INTO sales_returns (
			organization_id, branch_id, sale_id, operation_id, return_no, status, reason, currency_code, credit_total_minor, policy_snapshot, evidence_refs,
			requested_by, requested_at, version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),1)
		RETURNING ` + salesReturnColumns
		row := r.db.FromCtx(txCtx).QueryRow(txCtx, insertReturn,
			ret.OrganizationID, ret.BranchID, ret.SaleID, ret.OperationID, ret.ReturnNo, ret.Status, ret.Reason, ret.CurrencyCode, ret.CreditTotalMinor, ret.PolicySnapshot, ret.EvidenceRefs,
			ret.RequestedBy,
		)
		var err error
		created, err = scanSalesReturn(row)
		if err != nil {
			return err
		}

		for i := range items {
			items[i].SalesReturnID = created.ID
			items[i].OrganizationID = created.OrganizationID
			items[i].LineNo = int32(i + 1)
		}
		if err := r.insertSalesReturnItems(txCtx, created.OrganizationID, created.ID, items); err != nil {
			return err
		}

		if operation != nil {
			operation.OrganizationID = created.OrganizationID
			operation.BranchID = created.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}
		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "operation_id") {
				return nil, ErrDuplicateOperation
			}
			if strings.Contains(pgErr.ConstraintName, "return_no") {
				return nil, fmt.Errorf("return_no already exists")
			}
		}
		return nil, err
	}
	return created, nil
}

func (r *Repository) insertSalesReturnItems(ctx context.Context, orgID, returnID uuid.UUID, items []SalesReturnItem) error {
	const insertItem = `INSERT INTO sales_return_items (
		organization_id, sales_return_id, sale_item_id, stock_allocation_id, batch_id, line_no, quantity, disposition, disposition_reason, credit_minor, original_cost_minor, currency_code, inventory_movement_id
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`

	batch := &pgx.Batch{}
	for _, item := range items {
		batch.Queue(insertItem,
			item.OrganizationID, item.SalesReturnID, item.SaleItemID, item.StockAllocationID, item.BatchID, item.LineNo, item.Quantity, item.Disposition, item.DispositionReason, item.CreditMinor, item.OriginalCostMinor, item.CurrencyCode, item.InventoryMovementID,
		)
	}
	results := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer results.Close()
	for range items {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) FindSalesReturnByID(ctx context.Context, scope SaleScope, id uuid.UUID) (*SalesReturn, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("return id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+salesReturnColumns+` FROM sales_returns WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL`)
	return scanSalesReturn(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) FindSalesReturnByIDForUpdate(ctx context.Context, scope SaleScope, id uuid.UUID) (*SalesReturn, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("return id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+salesReturnColumns+` FROM sales_returns WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL FOR UPDATE`)
	return scanSalesReturn(r.db.FromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) ListSalesReturnItems(ctx context.Context, orgID, returnID uuid.UUID) ([]SalesReturnItem, error) {
	if orgID == uuid.Nil || returnID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and return_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+salesReturnItemColumns+` FROM sales_return_items WHERE organization_id = $1 AND sales_return_id = $2 ORDER BY line_no ASC`,
		orgID, returnID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SalesReturnItem
	for rows.Next() {
		item, err := scanSalesReturnItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *Repository) UpdateSalesReturnStatus(ctx context.Context, scope SaleScope, id uuid.UUID, expectedVersion int64, status SaleReturnStatus, approvedBy, rejectedBy, postedBy *uuid.UUID, approvedAt, rejectedAt, postedAt *time.Time, rejectedReason *string, creditTotalMinor *int64, operation *SaleOperation, transitions []SaleTransition) (*SalesReturn, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("id and expected_version are required")
	}

	var updated *SalesReturn
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		sets := []string{"status = $4", "version = version + 1"}
		args := []any{id, scope.OrganizationID, expectedVersion, status}
		n := 5

		if approvedBy != nil {
			sets = append(sets, "approved_by = $"+strconv.Itoa(n), "approved_at = $"+strconv.Itoa(n+1))
			args = append(args, *approvedBy, approvedAt)
			n += 2
		}
		if rejectedBy != nil {
			sets = append(sets, "rejected_by = $"+strconv.Itoa(n), "rejected_at = $"+strconv.Itoa(n+1), "rejected_reason = $"+strconv.Itoa(n+2))
			args = append(args, *rejectedBy, rejectedAt, rejectedReason)
			n += 3
		}
		if postedBy != nil {
			sets = append(sets, "posted_by = $"+strconv.Itoa(n), "posted_at = $"+strconv.Itoa(n+1))
			args = append(args, *postedBy, postedAt)
			n += 2
		}
		if creditTotalMinor != nil {
			sets = append(sets, "credit_total_minor = $"+strconv.Itoa(n))
			args = append(args, *creditTotalMinor)
			n++
		}

		q := `UPDATE sales_returns SET ` + strings.Join(sets, ", ") +
			` WHERE id = $1 AND organization_id = $2 AND version = $3 AND deleted_at IS NULL RETURNING ` + salesReturnColumns

		ret, err := scanSalesReturn(r.db.FromCtx(txCtx).QueryRow(txCtx, q, args...))
		if errors.Is(err, db.ErrNoRows) {
			if _, findErr := r.FindSalesReturnByID(txCtx, scope, id); findErr != nil {
				return findErr
			}
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		updated = ret

		if operation != nil {
			operation.OrganizationID = scope.OrganizationID
			operation.BranchID = updated.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}
		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (r *Repository) ListSalesReturns(ctx context.Context, scope SaleScope, filter ListReturnsQuery) ([]SalesReturn, int64, error) {
	if err := scope.validate(); err != nil {
		return nil, 0, err
	}
	if errs := filter.NormalizeAndValidate(); len(errs) > 0 {
		return nil, 0, errs[0]
	}
	if filter.BranchID != nil && !scope.allowsBranch(*filter.BranchID) {
		return []SalesReturn{}, 0, nil
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
	if filter.Status != nil {
		add("status", *filter.Status)
	}
	if filter.FromDate != nil {
		args = append(args, *filter.FromDate)
		where = append(where, "requested_at >= $"+strconv.Itoa(len(args)))
	}
	if filter.ToDate != nil {
		args = append(args, *filter.ToDate)
		where = append(where, "requested_at < $"+strconv.Itoa(len(args)))
	}
	if filter.Q != "" {
		args = append(args, "%"+escapeLike(strings.TrimSpace(filter.Q))+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(return_no ILIKE $"+n+` ESCAPE '\')`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM sales_returns WHERE `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []SalesReturn{}, 0, nil
	}

	sortKey := salesReturnListSortKey(scope, filter)
	var cursor *pagination.Cursor
	if filter.Cursor != "" {
		decoded, err := r.cursorCodec.Decode(filter.Cursor)
		if err != nil || decoded.ValidateSortKey(sortKey) != nil || decoded.CreatedAt.IsZero() {
			return nil, 0, pagination.ErrInvalidCursor
		}
		cursor = &decoded
	}

	listArgs := args[:]
	if cursor != nil {
		listArgs = append(listArgs, cursor.CreatedAt, cursor.ID)
		whereSQL += " AND (requested_at, id) < ($" + strconv.Itoa(len(listArgs)-1) + ", $" + strconv.Itoa(len(listArgs)) + ")"
	}
	listArgs = append(listArgs, filter.Limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(listArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+salesReturnColumns+` FROM sales_returns WHERE `+whereSQL+`
		ORDER BY requested_at DESC, id DESC
		LIMIT `+limitPlaceholder,
		listArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	returns := make([]SalesReturn, 0, filter.Limit)
	for rows.Next() {
		ret, err := scanSalesReturn(rows)
		if err != nil {
			return nil, 0, err
		}
		if len(returns) == filter.Limit {
			return returns, total, nil
		}
		returns = append(returns, *ret)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return returns, total, nil
}

func salesReturnListSortKey(scope SaleScope, filter ListReturnsQuery) string {
	payload := struct {
		OrgID       string
		BranchID    *uuid.UUID
		BranchScope []uuid.UUID
		Status      *SaleReturnStatus
		FromDate    *time.Time
		ToDate      *time.Time
		Q           string
	}{scope.OrganizationID.String(), filter.BranchID, scope.BranchIDs, filter.Status, filter.FromDate, filter.ToDate, filter.Q}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "sales-return-list-v1:" + hex.EncodeToString(sum[:])
}

func (r *Repository) CreateSaleRefundWithTx(
	ctx context.Context,
	ref *SaleRefund,
	operation *SaleOperation,
	transitions []SaleTransition,
) (*SaleRefund, error) {
	if ref.OrganizationID == uuid.Nil || ref.BranchID == uuid.Nil || ref.SaleID == uuid.Nil || ref.OperationID == uuid.Nil || ref.RefundNo == "" || ref.RequestedBy == uuid.Nil {
		return nil, fmt.Errorf("required fields missing")
	}

	var created *SaleRefund
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		const insertRefund = `INSERT INTO sale_refunds (
			organization_id, branch_id, sale_id, sales_return_id, original_tender_id, operation_id, refund_no, amount_minor, currency_code, method, status, provider_ref, reason, requested_by, requested_at, version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now(),1)
		RETURNING ` + saleRefundColumns
		row := r.db.FromCtx(txCtx).QueryRow(txCtx, insertRefund,
			ref.OrganizationID, ref.BranchID, ref.SaleID, ref.SalesReturnID, ref.OriginalTenderID, ref.OperationID, ref.RefundNo, ref.AmountMinor, ref.CurrencyCode, ref.Method, ref.Status, ref.ProviderRef, ref.Reason, ref.RequestedBy,
		)
		var err error
		created, err = scanSaleRefund(row)
		if err != nil {
			return err
		}

		if operation != nil {
			operation.OrganizationID = created.OrganizationID
			operation.BranchID = created.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}
		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "operation_id") {
				return nil, ErrDuplicateOperation
			}
			if strings.Contains(pgErr.ConstraintName, "refund_no") {
				return nil, fmt.Errorf("refund_no already exists")
			}
		}
		return nil, err
	}
	return created, nil
}

func (r *Repository) FindSaleRefundByID(ctx context.Context, scope SaleScope, id uuid.UUID) (*SaleRefund, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("refund id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+saleRefundColumns+` FROM sale_refunds WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL`)
	return scanSaleRefund(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) FindSaleRefundByIDForUpdate(ctx context.Context, scope SaleScope, id uuid.UUID) (*SaleRefund, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("refund id is required")
	}
	q, args := scopedSaleQuery(scope, `SELECT `+saleRefundColumns+` FROM sale_refunds WHERE id = $2 AND organization_id = $1 AND deleted_at IS NULL FOR UPDATE`)
	return scanSaleRefund(r.db.FromCtx(ctx).QueryRow(ctx, q, append(args, id)...))
}

func (r *Repository) UpdateSaleRefundStatus(ctx context.Context, scope SaleScope, id uuid.UUID, expectedVersion int64, status RefundStatus, settledAt, failedAt *time.Time, failureCode, providerRef, ledgerPostingRef *string, operation *SaleOperation, transitions []SaleTransition) (*SaleRefund, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("id and expected_version are required")
	}

	var updated *SaleRefund
	err := r.db.WithTx(ctx, func(txCtx context.Context) error {
		sets := []string{"status = $4", "version = version + 1"}
		args := []any{id, scope.OrganizationID, expectedVersion, status}
		n := 5

		if settledAt != nil {
			sets = append(sets, "settled_at = $"+strconv.Itoa(n))
			args = append(args, *settledAt)
			n++
		}
		if failedAt != nil {
			sets = append(sets, "failed_at = $"+strconv.Itoa(n), "failure_code = $"+strconv.Itoa(n+1))
			args = append(args, *failedAt, failureCode)
			n += 2
		}
		if providerRef != nil {
			sets = append(sets, "provider_ref = $"+strconv.Itoa(n))
			args = append(args, *providerRef)
			n++
		}
		if ledgerPostingRef != nil {
			sets = append(sets, "ledger_posting_ref = $"+strconv.Itoa(n))
			args = append(args, *ledgerPostingRef)
			n++
		}

		q := `UPDATE sale_refunds SET ` + strings.Join(sets, ", ") +
			` WHERE id = $1 AND organization_id = $2 AND version = $3 AND deleted_at IS NULL RETURNING ` + saleRefundColumns

		ref, err := scanSaleRefund(r.db.FromCtx(txCtx).QueryRow(txCtx, q, args...))
		if errors.Is(err, db.ErrNoRows) {
			if _, findErr := r.FindSaleRefundByID(txCtx, scope, id); findErr != nil {
				return findErr
			}
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		updated = ref

		if operation != nil {
			operation.OrganizationID = scope.OrganizationID
			operation.BranchID = updated.BranchID
			if err := r.insertSaleOperation(txCtx, operation); err != nil {
				return err
			}
		}
		if len(transitions) > 0 {
			if err := r.insertSaleTransitions(txCtx, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (r *Repository) InsertPrescriptionEvidence(ctx context.Context, ev *SalePrescriptionEvidence) error {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.New()
	}
	const q = `INSERT INTO sale_prescription_evidence (
		id, organization_id, branch_id, sale_id, reviewer_id, reviewed_at, prescription_ref, prescriber_ref, evidence_refs, decision, policy_version, decision_reason
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		ev.ID, ev.OrganizationID, ev.BranchID, ev.SaleID, ev.ReviewerID, ev.ReviewedAt, ev.PrescriptionRef, ev.PrescriberRef, ev.EvidenceRefs, ev.Decision, ev.PolicyVersion, ev.DecisionReason,
	)
	return err
}

func (r *Repository) FindPrescriptionEvidence(ctx context.Context, orgID, saleID uuid.UUID) (*SalePrescriptionEvidence, error) {
	if orgID == uuid.Nil || saleID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and sale_id are required")
	}
	const q = `SELECT ` + salePrescriptionEvidenceColumns + ` FROM sale_prescription_evidence WHERE organization_id = $1 AND sale_id = $2`
	return scanSalePrescriptionEvidence(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, saleID))
}

func (r *Repository) FindPriceBookByID(ctx context.Context, orgID, id uuid.UUID) (*SalePriceBook, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + salePriceBookColumns + ` FROM sale_price_books WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanSalePriceBook(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindPriceBookByCode(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID, code string) (*SalePriceBook, error) {
	if orgID == uuid.Nil || code == "" {
		return nil, fmt.Errorf("organization_id and code are required")
	}
	args := []any{orgID, code}
	where := "WHERE organization_id = $1 AND code = $2 AND deleted_at IS NULL"
	if branchID != nil {
		args = append(args, *branchID)
		where += " AND (branch_id = $3 OR branch_id IS NULL)"
	} else {
		where += " AND branch_id IS NULL"
	}
	const base = `SELECT ` + salePriceBookColumns + ` FROM sale_price_books `
	return scanSalePriceBook(r.db.ReadFromCtx(ctx).QueryRow(ctx, base+where+` ORDER BY (branch_id IS NOT NULL) DESC LIMIT 1`, args...))
}

func (r *Repository) ListPriceBooks(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID, isActive *bool, limit, offset int) ([]SalePriceBook, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization_id is required")
	}
	args := []any{orgID}
	where := "WHERE organization_id = $1 AND deleted_at IS NULL"
	n := 2
	if branchID != nil {
		args = append(args, *branchID)
		where += " AND (branch_id = $" + strconv.Itoa(n) + " OR branch_id IS NULL)"
		n++
	} else {
		where += " AND branch_id IS NULL"
	}
	if isActive != nil {
		args = append(args, *isActive)
		where += " AND is_active = $" + strconv.Itoa(n)
		n++
	}
	args = append(args, limit, offset)
	q := `SELECT ` + salePriceBookColumns + ` FROM sale_price_books ` + where + ` ORDER BY code ASC LIMIT $` + strconv.Itoa(n) + ` OFFSET $` + strconv.Itoa(n+1)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []SalePriceBook
	for rows.Next() {
		b, err := scanSalePriceBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, *b)
	}
	return books, rows.Err()
}

func (r *Repository) CreatePriceBook(ctx context.Context, book *SalePriceBook) (*SalePriceBook, error) {
	if book.ID == uuid.Nil {
		book.ID = uuid.New()
	}
	if book.Version == 0 {
		book.Version = 1
	}
	if book.CreatedAt.IsZero() {
		book.CreatedAt = time.Now()
	}
	book.UpdatedAt = book.CreatedAt
	const q = `INSERT INTO sale_price_books (
		id, organization_id, branch_id, code, name, currency_code, is_active, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	RETURNING ` + salePriceBookColumns
	return scanSalePriceBook(r.db.FromCtx(ctx).QueryRow(ctx, q,
		book.ID, book.OrganizationID, book.BranchID, book.Code, book.Name, book.CurrencyCode, book.IsActive, book.Version,
	))
}

func (r *Repository) UpdatePriceBook(ctx context.Context, book *SalePriceBook, expectedVersion int64) (*SalePriceBook, error) {
	if book.ID == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("id and expected_version are required")
	}
	book.UpdatedAt = time.Now()
	const q = `UPDATE sale_price_books SET name = $3, is_active = $4, version = version + 1, updated_at = $5
		WHERE id = $1 AND organization_id = $2 AND version = $6 AND deleted_at IS NULL RETURNING ` + salePriceBookColumns
	return scanSalePriceBook(r.db.FromCtx(ctx).QueryRow(ctx, q,
		book.ID, book.OrganizationID, book.Name, book.IsActive, book.UpdatedAt, expectedVersion,
	))
}

func (r *Repository) FindPriceVersion(ctx context.Context, orgID, bookID, medicineID, variantID *uuid.UUID, unitID uuid.UUID, at time.Time) (*SalePriceVersion, error) {
	if orgID == nil || *orgID == uuid.Nil || unitID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and unit_id are required")
	}
	args := []any{orgID, unitID, at}
	where := "WHERE organization_id = $1 AND unit_id = $2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3) AND deleted_at IS NULL"
	n := 4
	if bookID != nil {
		args = append(args, *bookID)
		where += " AND price_book_id = $" + strconv.Itoa(n)
		n++
	}
	if medicineID != nil {
		args = append(args, *medicineID)
		where += " AND medicine_id = $" + strconv.Itoa(n)
		n++
	}
	if variantID != nil {
		args = append(args, *variantID)
		where += " AND (variant_id = $" + strconv.Itoa(n) + " OR variant_id IS NULL)"
		n++
	}
	const base = `SELECT ` + salePriceVersionColumns + ` FROM sale_price_versions `
	return scanSalePriceVersion(r.db.ReadFromCtx(ctx).QueryRow(ctx, base+where+` ORDER BY valid_from DESC LIMIT 1`, args...))
}

func (r *Repository) CreatePriceVersion(ctx context.Context, v *SalePriceVersion) (*SalePriceVersion, error) {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	if v.Version == 0 {
		v.Version = 1
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	v.UpdatedAt = v.CreatedAt
	const q = `INSERT INTO sale_price_versions (
		id, organization_id, price_book_id, medicine_id, variant_id, unit_id, unit_price_minor, currency_code, valid_from, valid_to, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	RETURNING ` + salePriceVersionColumns
	return scanSalePriceVersion(r.db.FromCtx(ctx).QueryRow(ctx, q,
		v.ID, v.OrganizationID, v.PriceBookID, v.MedicineID, v.VariantID, v.UnitID, v.UnitPriceMinor, v.CurrencyCode, v.ValidFrom, v.ValidTo, v.Version,
	))
}

func (r *Repository) FindPromotionByID(ctx context.Context, orgID, id uuid.UUID) (*SalePromotion, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + salePromotionColumns + ` FROM sale_promotions WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanSalePromotion(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindPromotionByCode(ctx context.Context, orgID uuid.UUID, code string) (*SalePromotion, error) {
	if orgID == uuid.Nil || code == "" {
		return nil, fmt.Errorf("organization_id and code are required")
	}
	const q = `SELECT ` + salePromotionColumns + ` FROM sale_promotions WHERE organization_id = $1 AND code = $2 AND deleted_at IS NULL`
	return scanSalePromotion(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, code))
}

func (r *Repository) ListActivePromotions(ctx context.Context, orgID uuid.UUID, at time.Time, currencyCode *string, limit int) ([]SalePromotion, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization_id is required")
	}
	args := []any{orgID, at}
	where := "WHERE organization_id = $1 AND is_active = TRUE AND deleted_at IS NULL AND valid_from <= $2 AND (valid_to IS NULL OR valid_to > $2)"
	n := 3
	if currencyCode != nil {
		args = append(args, *currencyCode)
		where += " AND (currency_code = $" + strconv.Itoa(n) + " OR currency_code IS NULL)"
		n++
	}
	args = append(args, limit)
	q := `SELECT ` + salePromotionColumns + ` FROM sale_promotions ` + where + ` ORDER BY valid_from DESC LIMIT $` + strconv.Itoa(n)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var promos []SalePromotion
	for rows.Next() {
		p, err := scanSalePromotion(rows)
		if err != nil {
			return nil, err
		}
		promos = append(promos, *p)
	}
	return promos, rows.Err()
}

func (r *Repository) CreatePromotion(ctx context.Context, p *SalePromotion) (*SalePromotion, error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = p.CreatedAt
	const q = `INSERT INTO sale_promotions (
		id, organization_id, code, name, description, kind, config, currency_code, min_purchase_minor, max_discount_minor, discount_rate_bps, flat_discount_minor, valid_from, valid_to, usage_limit, usage_count, per_customer_limit, is_active, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	RETURNING ` + salePromotionColumns
	return scanSalePromotion(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.ID, p.OrganizationID, p.Code, p.Name, p.Description, p.Kind, p.Config,
		p.CurrencyCode, p.MinPurchaseMinor, p.MaxDiscountMinor, p.DiscountRateBPS, p.FlatDiscountMinor,
		p.ValidFrom, p.ValidTo, p.UsageLimit, p.UsageCount, p.PerCustomerLimit, p.IsActive, p.Version,
	))
}

func (r *Repository) UpdatePromotion(ctx context.Context, p *SalePromotion, expectedVersion int64) (*SalePromotion, error) {
	if p.ID == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("id and expected_version are required")
	}
	p.UpdatedAt = time.Now()
	const q = `UPDATE sale_promotions SET
		name = $3, description = $4, config = $5, currency_code = $6, min_purchase_minor = $7, max_discount_minor = $8,
		discount_rate_bps = $9, flat_discount_minor = $10, valid_from = $11, valid_to = $12, usage_limit = $13,
		per_customer_limit = $14, is_active = $15, version = version + 1, updated_at = $16
		WHERE id = $1 AND organization_id = $2 AND version = $17 AND deleted_at IS NULL RETURNING ` + salePromotionColumns
	return scanSalePromotion(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.ID, p.OrganizationID, p.Name, p.Description, p.Config, p.CurrencyCode, p.MinPurchaseMinor, p.MaxDiscountMinor,
		p.DiscountRateBPS, p.FlatDiscountMinor, p.ValidFrom, p.ValidTo, p.UsageLimit, p.PerCustomerLimit, p.IsActive, p.UpdatedAt, expectedVersion,
	))
}

func (r *Repository) IncrementPromotionUsage(ctx context.Context, orgID, promoID uuid.UUID, expectedVersion int64) error {
	if orgID == uuid.Nil || promoID == uuid.Nil || expectedVersion < 1 {
		return fmt.Errorf("required fields missing")
	}
	const q = `UPDATE sale_promotions SET usage_count = usage_count + 1, version = version + 1, updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND version = $3`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, promoID, orgID, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return nil
}

func (r *Repository) CreatePromotionRedemption(ctx context.Context, redemption *SalePromotionRedemption) error {
	if redemption.ID == uuid.Nil {
		redemption.ID = uuid.New()
	}
	if redemption.RedeemedAt.IsZero() {
		redemption.RedeemedAt = time.Now()
	}
	const q = `INSERT INTO sale_promotion_redemptions (
		id, organization_id, promotion_id, sale_id, customer_id, redemption_count, discount_minor, currency_code, redeemed_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		redemption.ID, redemption.OrganizationID, redemption.PromotionID, redemption.SaleID, redemption.CustomerID, redemption.RedemptionCount, redemption.DiscountMinor, redemption.CurrencyCode, redemption.RedeemedAt,
	)
	return err
}

func (r *Repository) CheckPromotionRedemptionLimit(ctx context.Context, orgID, promoID, customerID uuid.UUID, limit int64) (int64, error) {
	if orgID == uuid.Nil || promoID == uuid.Nil {
		return 0, fmt.Errorf("organization_id and promotion_id are required")
	}
	const q = `SELECT COALESCE(SUM(redemption_count), 0) FROM sale_promotion_redemptions WHERE organization_id = $1 AND promotion_id = $2 AND customer_id = $3`
	var count int64
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, promoID, customerID).Scan(&count)
	return count, err
}

func (r *Repository) OpenCashSession(ctx context.Context, session *SaleCashSession) (*SaleCashSession, error) {
	if session.OrganizationID == uuid.Nil || session.BranchID == uuid.Nil || session.CashierID == uuid.Nil || session.SessionNo == "" {
		return nil, fmt.Errorf("required fields missing")
	}
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	if session.Version == 0 {
		session.Version = 1
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	session.UpdatedAt = session.CreatedAt
	const q = `INSERT INTO sale_cash_sessions (
		id, organization_id, branch_id, cashier_id, drawer_id, session_no, status, currency_code, opening_minor, expected_minor, opened_at, opened_by, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	RETURNING ` + saleCashSessionColumns
	return scanSaleCashSession(r.db.FromCtx(ctx).QueryRow(ctx, q,
		session.ID, session.OrganizationID, session.BranchID, session.CashierID, session.DrawerID, session.SessionNo, session.Status, session.CurrencyCode, session.OpeningMinor, session.ExpectedMinor, session.OpenedAt, session.OpenedBy, session.Version,
	))
}

func (r *Repository) FindActiveCashSession(ctx context.Context, orgID, branchID, cashierID uuid.UUID) (*SaleCashSession, error) {
	if orgID == uuid.Nil || branchID == uuid.Nil || cashierID == uuid.Nil {
		return nil, fmt.Errorf("organization_id, branch_id, and cashier_id are required")
	}
	const q = `SELECT ` + saleCashSessionColumns + ` FROM sale_cash_sessions WHERE organization_id = $1 AND branch_id = $2 AND cashier_id = $3 AND status = 'open'`
	return scanSaleCashSession(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, branchID, cashierID))
}

func (r *Repository) FindCashSessionByID(ctx context.Context, orgID, id uuid.UUID) (*SaleCashSession, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + saleCashSessionColumns + ` FROM sale_cash_sessions WHERE id = $1 AND organization_id = $2`
	return scanSaleCashSession(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindCashSessionForUpdate(ctx context.Context, orgID, id uuid.UUID) (*SaleCashSession, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + saleCashSessionColumns + ` FROM sale_cash_sessions WHERE id = $1 AND organization_id = $2 FOR UPDATE`
	return scanSaleCashSession(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) UpdateCashSessionStatus(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, status CashSessionStatus, countedMinor *int64, varianceMinor *int64, closedAt, closingStartedAt *time.Time, closedBy *uuid.UUID, closeReason *string) (*SaleCashSession, error) {
	if orgID == uuid.Nil || id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("required fields missing")
	}
	sets := []string{"status = $4", "version = version + 1", "updated_at = now()"}
	args := []any{id, orgID, expectedVersion, status}
	n := 5

	if countedMinor != nil {
		sets = append(sets, "counted_minor = $"+strconv.Itoa(n), "variance_minor = $"+strconv.Itoa(n+1))
		args = append(args, *countedMinor, varianceMinor)
		n += 2
	}
	if closingStartedAt != nil {
		sets = append(sets, "closing_started_at = $"+strconv.Itoa(n))
		args = append(args, *closingStartedAt)
		n++
	}
	if closedAt != nil {
		sets = append(sets, "closed_at = $"+strconv.Itoa(n), "closed_by = $"+strconv.Itoa(n+1), "close_reason = $"+strconv.Itoa(n+2))
		args = append(args, *closedAt, closedBy, closeReason)
		n += 3
	}

	q := `UPDATE sale_cash_sessions SET ` + strings.Join(sets, ", ") +
		` WHERE id = $1 AND organization_id = $2 AND version = $3 RETURNING ` + saleCashSessionColumns
	return scanSaleCashSession(r.db.FromCtx(ctx).QueryRow(ctx, q, args...))
}

func (r *Repository) InsertCashMovement(ctx context.Context, m *SaleCashMovement) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	if m.OccurredAt.IsZero() {
		m.OccurredAt = time.Now()
	}
	const q = `INSERT INTO sale_cash_movements (
		id, organization_id, branch_id, cash_session_id, movement_type, amount_minor, currency_code, source_type, source_id, operation_id, actor_id, occurred_at, reason
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		m.ID, m.OrganizationID, m.BranchID, m.CashSessionID, m.MovementType, m.AmountMinor, m.CurrencyCode, m.SourceType, m.SourceID, m.OperationID, m.ActorID, m.OccurredAt, m.Reason,
	)
	return err
}

func (r *Repository) ListCashMovements(ctx context.Context, orgID, sessionID uuid.UUID) ([]SaleCashMovement, error) {
	if orgID == uuid.Nil || sessionID == uuid.Nil {
		return nil, fmt.Errorf("organization_id and session_id are required")
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		`SELECT `+saleCashMovementColumns+` FROM sale_cash_movements WHERE organization_id = $1 AND cash_session_id = $2 ORDER BY occurred_at ASC, id ASC`,
		orgID, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var moves []SaleCashMovement
	for rows.Next() {
		m, err := scanSaleCashMovement(rows)
		if err != nil {
			return nil, err
		}
		moves = append(moves, *m)
	}
	return moves, rows.Err()
}

func (r *Repository) CreateCart(ctx context.Context, cart *SaleCart) (*SaleCart, error) {
	if cart.OrganizationID == uuid.Nil || cart.BranchID == uuid.Nil || cart.OwnerUserID == uuid.Nil || cart.Content == nil || cart.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("required fields missing")
	}
	if cart.ID == uuid.Nil {
		cart.ID = uuid.New()
	}
	if cart.Version == 0 {
		cart.Version = 1
	}
	if cart.CreatedAt.IsZero() {
		cart.CreatedAt = time.Now()
	}
	cart.UpdatedAt = cart.CreatedAt
	const q = `INSERT INTO sale_carts (
		id, organization_id, branch_id, owner_user_id, content, expires_at, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7)
	RETURNING ` + saleCartColumns
	return scanSaleCart(r.db.FromCtx(ctx).QueryRow(ctx, q,
		cart.ID, cart.OrganizationID, cart.BranchID, cart.OwnerUserID, cart.Content, cart.ExpiresAt, cart.Version,
	))
}

func (r *Repository) FindCartByID(ctx context.Context, orgID, id uuid.UUID) (*SaleCart, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("organization_id and id are required")
	}
	const q = `SELECT ` + saleCartColumns + ` FROM sale_carts WHERE id = $1 AND organization_id = $2 AND expires_at > now()`
	return scanSaleCart(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) UpdateCart(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, content *common.JSONMap, expiresAt *time.Time) (*SaleCart, error) {
	if orgID == uuid.Nil || id == uuid.Nil || expectedVersion < 1 {
		return nil, fmt.Errorf("required fields missing")
	}
	sets := []string{"version = version + 1"}
	args := []any{id, orgID, expectedVersion}
	n := 4

	if content != nil {
		sets = append(sets, "content = $"+strconv.Itoa(n))
		args = append(args, *content)
		n++
	}
	if expiresAt != nil {
		sets = append(sets, "expires_at = $"+strconv.Itoa(n))
		args = append(args, *expiresAt)
		n++
	}

	q := `UPDATE sale_carts SET ` + strings.Join(sets, ", ") +
		` WHERE id = $1 AND organization_id = $2 AND version = $3 RETURNING ` + saleCartColumns
	return scanSaleCart(r.db.FromCtx(ctx).QueryRow(ctx, q, args...))
}

func (r *Repository) DeleteExpiredCarts(ctx context.Context, orgID uuid.UUID) (int64, error) {
	if orgID == uuid.Nil {
		return 0, fmt.Errorf("organization_id is required")
	}
	const q = `DELETE FROM sale_carts WHERE organization_id = $1 AND expires_at <= now()`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, orgID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

const saleRefundItemColumns = `id, organization_id, refund_id, sale_item_id, quantity, refund_amount_minor, restocked, restock_condition, original_cost_minor, currency_code, inventory_movement_id, version`

func scanSaleRefundItem(row pgx.Row) (*SaleRefundItem, error) {
	var ri SaleRefundItem
	err := row.Scan(
		&ri.ID, &ri.OrganizationID, &ri.RefundID, &ri.SaleLineID, &ri.Quantity, &ri.RefundAmountMinor, &ri.Restocked, &ri.RestockCondition, &ri.OriginalCostMinor, &ri.CurrencyCode, &ri.InventoryMovementID, &ri.Version,
	)
	if err != nil {
		return nil, err
	}
	return &ri, nil
}

func (r *Repository) GetSale(ctx context.Context, id uuid.UUID) (*Sale, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("id is required")
	}
	const q = `SELECT ` + saleColumns + ` FROM sales WHERE id = $1 AND deleted_at IS NULL`
	return scanSale(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id))
}

func (r *Repository) GetSaleTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Sale, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("id is required")
	}
	const q = `SELECT ` + saleColumns + ` FROM sales WHERE id = $1 AND deleted_at IS NULL`
	return scanSale(tx.QueryRow(ctx, q, id))
}

func (r *Repository) GetSaleByIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, key string) (*Sale, error) {
	if orgID == uuid.Nil || key == "" {
		return nil, fmt.Errorf("orgID and key required")
	}
	const q = `SELECT ` + saleColumns + ` FROM sales WHERE organization_id = $1 AND idempotency_key = $2 AND deleted_at IS NULL`
	return scanSale(tx.QueryRow(ctx, q, orgID, key))
}

func (r *Repository) GetSaleLines(ctx context.Context, saleID uuid.UUID) ([]*SaleItem, error) {
	if saleID == uuid.Nil {
		return nil, fmt.Errorf("saleID is required")
	}
	const q = `SELECT ` + saleItemColumns + ` FROM sale_items WHERE sale_id = $1 AND deleted_at IS NULL ORDER BY line_no`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*SaleItem
	for rows.Next() {
		item, err := scanSaleItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetSaleLinesTx(ctx context.Context, tx pgx.Tx, saleID uuid.UUID) ([]*SaleItem, error) {
	if saleID == uuid.Nil {
		return nil, fmt.Errorf("saleID is required")
	}
	const q = `SELECT ` + saleItemColumns + ` FROM sale_items WHERE sale_id = $1 AND deleted_at IS NULL ORDER BY line_no`
	rows, err := tx.Query(ctx, q, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*SaleItem
	for rows.Next() {
		item, err := scanSaleItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetSaleTenders(ctx context.Context, saleID uuid.UUID) ([]*SaleTender, error) {
	if saleID == uuid.Nil {
		return nil, fmt.Errorf("saleID is required")
	}
	const q = `SELECT ` + saleTenderColumns + ` FROM sale_tenders WHERE sale_id = $1 AND deleted_at IS NULL ORDER BY tender_no`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenders []*SaleTender
	for rows.Next() {
		tender, err := scanSaleTender(rows)
		if err != nil {
			return nil, err
		}
		tenders = append(tenders, tender)
	}
	return tenders, rows.Err()
}

func (r *Repository) GetSaleTendersTx(ctx context.Context, tx pgx.Tx, saleID uuid.UUID) ([]*SaleTender, error) {
	if saleID == uuid.Nil {
		return nil, fmt.Errorf("saleID is required")
	}
	const q = `SELECT ` + saleTenderColumns + ` FROM sale_tenders WHERE sale_id = $1 AND deleted_at IS NULL ORDER BY tender_no`
	rows, err := tx.Query(ctx, q, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenders []*SaleTender
	for rows.Next() {
		tender, err := scanSaleTender(rows)
		if err != nil {
			return nil, err
		}
		tenders = append(tenders, tender)
	}
	return tenders, rows.Err()
}

func (r *Repository) CreateSaleTx(ctx context.Context, tx pgx.Tx, sale *Sale) error {
	if sale == nil {
		return fmt.Errorf("sale is required")
	}
	const q = `INSERT INTO sales (
		organization_id, branch_id, warehouse_id, cashier_id, customer_id, cash_session_id, operation_id,
		sale_no, sale_type, status, payment_status, invoice_status, currency_code,
		subtotal_minor, discount_minor, coupon_discount_minor, tax_minor, other_charges_minor, total_minor,
		paid_minor, change_minor, coupon_id, prescription_id, notes,
		pricing_snapshot, tax_snapshot, policy_version,
		sold_at, completed_at, voided_at, voided_by, void_reason,
		version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34)
	RETURNING ` + saleColumns

	return tx.QueryRow(ctx, q,
		sale.OrganizationID, sale.BranchID, sale.WarehouseID, sale.CashierID, sale.CustomerID, sale.CashSessionID, sale.OperationID,
		sale.SaleNo, sale.SaleType, sale.Status, sale.PaymentStatus, sale.InvoiceStatus, sale.CurrencyCode,
		sale.SubtotalMinor, sale.DiscountMinor, sale.CouponDiscountMinor, sale.TaxMinor, sale.OtherChargesMinor, sale.TotalMinor,
		sale.PaidMinor, sale.ChangeMinor, sale.CouponID, sale.PrescriptionID, sale.Notes,
		sale.PricingSnapshot, sale.TaxSnapshot, sale.PolicyVersion,
		sale.SoldAt, sale.CompletedAt, sale.VoidedAt, sale.VoidedBy, sale.VoidReason,
		sale.Version,
	).Scan(&sale.ID, &sale.CreatedAt, &sale.UpdatedAt)
}

func (r *Repository) CreateSaleLineTx(ctx context.Context, tx pgx.Tx, line *SaleItem) error {
	if line == nil {
		return fmt.Errorf("line is required")
	}
	const q = `INSERT INTO sale_items (
		sale_id, line_no, medicine_id, variant_id, unit_id,
		entered_quantity, base_quantity, unit_conversion_numerator, unit_conversion_denominator,
		unit_price_minor, discount_minor, tax_minor, line_total_minor, discount_rate_bps, tax_rate_bps,
		product_snapshot, unit_snapshot, tax_snapshot, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,1)
	RETURNING ` + saleItemColumns

	return tx.QueryRow(ctx, q,
		line.SaleID, line.LineNo, line.MedicineID, line.VariantID, line.UnitID,
		line.EnteredQuantity, line.BaseQuantity, line.UnitConversionNumerator, line.UnitConversionDenominator,
		line.UnitPriceMinor, line.DiscountMinor, line.TaxMinor, line.LineTotalMinor, line.DiscountRateBPS, line.TaxRateBPS,
		line.ProductSnapshot, line.UnitSnapshot, line.TaxSnapshot,
	).Scan(&line.ID, &line.CreatedAt)
}

func (r *Repository) CreateSaleTenderTx(ctx context.Context, tx pgx.Tx, tender *SaleTender) error {
	if tender == nil {
		return fmt.Errorf("tender is required")
	}
	const q = `INSERT INTO sale_tenders (
		organization_id, branch_id, sale_id, payment_intent_id, tender_no,
		method, amount_minor, currency_code, reference_no, provider_ref, received_at, received_by, settlement_ref
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	RETURNING ` + saleTenderColumns

	return tx.QueryRow(ctx, q,
		tender.OrganizationID, tender.BranchID, tender.SaleID, tender.PaymentIntentID, tender.TenderNo,
		tender.Method, tender.AmountMinor, tender.CurrencyCode, tender.ReferenceNo, tender.ProviderRef, tender.ReceivedAt, tender.ReceivedBy, tender.SettlementRef,
	).Scan(&tender.ID)
}

func (r *Repository) UpdateSaleTx(ctx context.Context, tx pgx.Tx, sale *Sale) error {
	if sale == nil {
		return fmt.Errorf("sale is required")
	}
	const q = `UPDATE sales SET
		branch_id = $2, warehouse_id = $3, cashier_id = $4, customer_id = $5, cash_session_id = $6, operation_id = $7,
		sale_no = $8, sale_type = $9, status = $10, payment_status = $11, invoice_status = $12, currency_code = $13,
		subtotal_minor = $14, discount_minor = $15, coupon_discount_minor = $16, tax_minor = $17, other_charges_minor = $18, total_minor = $19,
		paid_minor = $20, change_minor = $21, coupon_id = $22, prescription_id = $23, notes = $24,
		pricing_snapshot = $25, tax_snapshot = $26, policy_version = $27,
		sold_at = $28, completed_at = $29, voided_at = $30, voided_by = $31, void_reason = $32,
		version = version + 1
	WHERE id = $1 AND version = $33
	RETURNING ` + saleColumns

	return tx.QueryRow(ctx, q,
		sale.ID, sale.BranchID, sale.WarehouseID, sale.CashierID, sale.CustomerID, sale.CashSessionID, sale.OperationID,
		sale.SaleNo, sale.SaleType, sale.Status, sale.PaymentStatus, sale.InvoiceStatus, sale.CurrencyCode,
		sale.SubtotalMinor, sale.DiscountMinor, sale.CouponDiscountMinor, sale.TaxMinor, sale.OtherChargesMinor, sale.TotalMinor,
		sale.PaidMinor, sale.ChangeMinor, sale.CouponID, sale.PrescriptionID, sale.Notes,
		sale.PricingSnapshot, sale.TaxSnapshot, sale.PolicyVersion,
		sale.SoldAt, sale.CompletedAt, sale.VoidedAt, sale.VoidedBy, sale.VoidReason,
		sale.Version,
	).Scan(&sale.ID, &sale.BranchID, &sale.WarehouseID, &sale.CashierID, &sale.CustomerID, &sale.CashSessionID, &sale.OperationID,
		&sale.SaleNo, &sale.SaleType, &sale.Status, &sale.PaymentStatus, &sale.InvoiceStatus, &sale.CurrencyCode,
		&sale.SubtotalMinor, &sale.DiscountMinor, &sale.CouponDiscountMinor, &sale.TaxMinor, &sale.OtherChargesMinor, &sale.TotalMinor,
		&sale.PaidMinor, &sale.ChangeMinor, &sale.CouponID, &sale.PrescriptionID, &sale.Notes,
		&sale.PricingSnapshot, &sale.TaxSnapshot, &sale.PolicyVersion,
		&sale.SoldAt, &sale.CompletedAt, &sale.VoidedAt, &sale.VoidedBy, &sale.VoidReason,
		&sale.Version)
}

func (r *Repository) UpdateSaleInvoiceSnapshot(ctx context.Context, saleID uuid.UUID, snapshot common.JSONMap) error {
	if saleID == uuid.Nil {
		return fmt.Errorf("saleID is required")
	}
	const q = `UPDATE sales SET pricing_snapshot = $2, version = version + 1 WHERE id = $1`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, saleID, snapshot)
	return err
}

func (r *Repository) UpdateCashSessionSaleTx(ctx context.Context, tx pgx.Tx, sessionID, saleID uuid.UUID, amountMinor int64) error {
	if sessionID == uuid.Nil || saleID == uuid.Nil {
		return fmt.Errorf("sessionID and saleID required")
	}
	const q = `UPDATE sale_cash_sessions SET expected_minor = expected_minor + $3, version = version + 1 WHERE id = $1 AND sale_id = $2`
	_, err := tx.Exec(ctx, q, sessionID, saleID, amountMinor)
	return err
}

func (r *Repository) UpdateCashSessionVoidTx(ctx context.Context, tx pgx.Tx, sessionID, saleID uuid.UUID, amountMinor int64) error {
	if sessionID == uuid.Nil || saleID == uuid.Nil {
		return fmt.Errorf("sessionID and saleID required")
	}
	const q = `UPDATE sale_cash_sessions SET expected_minor = expected_minor - $3, version = version + 1 WHERE id = $1 AND sale_id = $2`
	_, err := tx.Exec(ctx, q, sessionID, saleID, amountMinor)
	return err
}

func (r *Repository) UpdateCashSessionRefundTx(ctx context.Context, tx pgx.Tx, sessionID, saleID uuid.UUID, amountMinor int64) error {
	if sessionID == uuid.Nil || saleID == uuid.Nil {
		return fmt.Errorf("sessionID and saleID required")
	}
	const q = `UPDATE sale_cash_sessions SET expected_minor = expected_minor - $3, version = version + 1 WHERE id = $1 AND sale_id = $2`
	_, err := tx.Exec(ctx, q, sessionID, saleID, amountMinor)
	return err
}

func (r *Repository) SetIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, key string, saleID uuid.UUID) error {
	if orgID == uuid.Nil || key == "" || saleID == uuid.Nil {
		return fmt.Errorf("orgID, key, and saleID required")
	}
	const q = `UPDATE sales SET idempotency_key = $3 WHERE organization_id = $1 AND id = $2`
	_, err := tx.Exec(ctx, q, orgID, saleID, key)
	return err
}

func (r *Repository) GetSaleRefunds(ctx context.Context, saleID uuid.UUID) ([]*SaleRefund, error) {
	if saleID == uuid.Nil {
		return nil, fmt.Errorf("saleID is required")
	}
	const q = `SELECT ` + saleRefundColumns + ` FROM sale_refunds WHERE sale_id = $1 AND deleted_at IS NULL ORDER BY requested_at`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refunds []*SaleRefund
	for rows.Next() {
		refund, err := scanSaleRefund(rows)
		if err != nil {
			return nil, err
		}
		refunds = append(refunds, refund)
	}
	return refunds, rows.Err()
}

func (r *Repository) CreateSaleRefundTx(ctx context.Context, tx pgx.Tx, refund *SaleRefund) error {
	if refund == nil {
		return fmt.Errorf("refund is required")
	}
	const q = `INSERT INTO sale_refunds (
		organization_id, branch_id, sale_id, sales_return_id, original_tender_id, operation_id,
		refund_no, amount_minor, currency_code, method, status, provider_ref, reason, requested_by, requested_at, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1)
	RETURNING ` + saleRefundColumns

	return tx.QueryRow(ctx, q,
		refund.OrganizationID, refund.BranchID, refund.SaleID, refund.SalesReturnID, refund.OriginalTenderID, refund.OperationID,
		refund.RefundNo, refund.AmountMinor, refund.CurrencyCode, refund.Method, refund.Status, refund.ProviderRef, refund.Reason, refund.RequestedBy, refund.RequestedAt,
	).Scan(&refund.ID, &refund.CreatedAt, &refund.UpdatedAt)
}

func (r *Repository) UpdateSaleRefundTx(ctx context.Context, tx pgx.Tx, refund *SaleRefund) error {
	if refund == nil {
		return fmt.Errorf("refund is required")
	}
	const q = `UPDATE sale_refunds SET
		amount_minor = $2, status = $3, provider_ref = $4, reason = $5, settled_at = $6, failed_at = $7, failure_code = $8, ledger_posting_ref = $9, version = version + 1
	WHERE id = $1 AND version = $10
	RETURNING ` + saleRefundColumns

	return tx.QueryRow(ctx, q,
		refund.ID, refund.AmountMinor, refund.Status, refund.ProviderRef, refund.Reason, refund.SettledAt, refund.FailedAt, refund.FailureCode, refund.LedgerPostingRef, refund.Version,
	).Scan(&refund.ID, &refund.OrganizationID, &refund.BranchID, &refund.SaleID, &refund.SalesReturnID, &refund.OriginalTenderID, &refund.OperationID,
		&refund.RefundNo, &refund.AmountMinor, &refund.CurrencyCode, &refund.Method, &refund.Status, &refund.ProviderRef, &refund.Reason, &refund.RequestedBy, &refund.RequestedAt, &refund.SettledAt, &refund.FailedAt, &refund.FailureCode, &refund.LedgerPostingRef, &refund.Version)
}

func (r *Repository) CreateSaleRefundLineTx(ctx context.Context, tx pgx.Tx, line *SaleRefundItem) error {
	if line == nil {
		return fmt.Errorf("line is required")
	}
	const q = `INSERT INTO sale_refund_items (
		refund_id, sale_item_id, quantity, refund_amount_minor, restocked, restock_condition, original_cost_minor, currency_code, inventory_movement_id, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1)
	RETURNING ` + saleRefundItemColumns

	return tx.QueryRow(ctx, q,
		line.RefundID, line.SaleLineID, line.Quantity, line.RefundAmountMinor, line.Restocked, line.RestockCondition, line.OriginalCostMinor, line.CurrencyCode, line.InventoryMovementID,
	).Scan(&line.ID, &line.OrganizationID, &line.RefundID, &line.SaleLineID, &line.Quantity, &line.RefundAmountMinor, &line.Restocked, &line.RestockCondition, &line.OriginalCostMinor, &line.CurrencyCode, &line.InventoryMovementID, &line.Version)
}

func (r *Repository) GetOpenCashSession(ctx context.Context, orgID, registerID uuid.UUID) (*SaleCashSession, error) {
	if orgID == uuid.Nil || registerID == uuid.Nil {
		return nil, fmt.Errorf("orgID and registerID required")
	}
	const q = `SELECT ` + saleCashSessionColumns + ` FROM sale_cash_sessions WHERE organization_id = $1 AND cashier_id = $2 AND status = 'open' AND deleted_at IS NULL`
	return scanSaleCashSession(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, registerID))
}

func (r *Repository) GenerateInvoiceNumberTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, branchID *uuid.UUID, series string) (string, int64, error) {
	if orgID == uuid.Nil || series == "" {
		return "", 0, fmt.Errorf("orgID and series required")
	}
	var seq int64
	err := tx.QueryRow(ctx, `
		UPDATE sale.invoice_sequences
		SET current_sequence = current_sequence + 1,
		    updated_at = now()
		WHERE organization_id = $1 AND branch_id IS NOT DISTINCT FROM $2 AND series = $3
		RETURNING current_sequence
	`, orgID, branchID, series).Scan(&seq)

	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			_, err = tx.Exec(ctx, `
				INSERT INTO sale.invoice_sequences (organization_id, branch_id, series, current_sequence)
				VALUES ($1, $2, $3, 1)
			`, orgID, branchID, series)
			if err != nil {
				return "", 0, err
			}
			seq = 1
		} else {
			return "", 0, err
		}
	}

	invNum := fmt.Sprintf("%s-%08d", series, seq)
	return invNum, seq, nil
}

func (r *Repository) ExportSales(ctx context.Context, scope SaleScope, req *ExportSalesRequest) ([]*Sale, error) {
	return nil, fmt.Errorf("sale exports require the asynchronous export and authorization path")
}

func (r *Repository) LookupProducts(ctx context.Context, scope SaleScope, query string, limit int) ([]ProductLookupResult, error) {
	return nil, fmt.Errorf("product lookup must use the Medicine and Inventory owner-module adapters")
}

func (r *Repository) GetCustomer(ctx context.Context, orgID, id uuid.UUID) (*Customer, error) {
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("orgID and id required")
	}
	const q = `SELECT id, organization_id, code, name, phone, email, address, vat_number, loyalty_tier, loyalty_points, credit_limit_minor, credit_used_minor, is_active, created_at, updated_at, version FROM masterdata.customers WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	row := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID)
	return scanCustomer(row)
}

func (r *Repository) GetOrganization(ctx context.Context, orgID uuid.UUID) (*Organization, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("orgID required")
	}
	const q = `SELECT id, name, vat_number, address FROM masterdata.organizations WHERE id = $1 AND deleted_at IS NULL`
	row := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID)
	var org Organization
	err := row.Scan(&org.ID, &org.Name, &org.VATNumber, &org.Address)
	return &org, err
}

func (r *Repository) GetBranch(ctx context.Context, orgID, branchID uuid.UUID) (*Branch, error) {
	if orgID == uuid.Nil || branchID == uuid.Nil {
		return nil, fmt.Errorf("orgID and branchID required")
	}
	const q = `SELECT id, name, address FROM masterdata.branches WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	row := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, branchID, orgID)
	var branch Branch
	err := row.Scan(&branch.ID, &branch.Name, &branch.Address)
	return &branch, err
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type ProductLookupResult struct {
	MedicineID  uuid.UUID
	Name        string
	SKU         string
	Barcode     string
	VariantID   *uuid.UUID
	VariantName string
	UnitID      uuid.UUID
	UnitName    string
	PriceMinor  int64
	StockQty    int64
}

type Organization struct {
	ID        uuid.UUID
	Name      string
	VATNumber *string
	Address   *string
}

type Branch struct {
	ID      uuid.UUID
	Name    string
	Address *string
}

type Customer struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	Code             string
	Name             string
	Phone            *string
	Email            *string
	Address          *string
	VATNumber        *string
	LoyaltyTier      *string
	LoyaltyPoints    int64
	CreditLimitMinor int64
	CreditUsedMinor  int64
	IsActive         bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Version          int64
}

func scanCustomer(row pgx.Row) (*Customer, error) {
	var c Customer
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Code, &c.Name, &c.Phone, &c.Email, &c.Address, &c.VATNumber, &c.LoyaltyTier, &c.LoyaltyPoints, &c.CreditLimitMinor, &c.CreditUsedMinor, &c.IsActive, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
