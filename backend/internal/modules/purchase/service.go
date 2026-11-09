package purchase

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// PurchaseDraftResolution contains facts that must be resolved from
// organization-owned configuration and catalog data, not accepted from the
// request. Implementations must use the supplied scope and transaction context.
type PurchaseDraftResolution struct {
	PurchaseNo     string
	CurrencyCode   string
	PolicySnapshot common.JSONMap
	Lines          []PurchaseOrderLine
}

// PurchaseDraftResolver resolves currency, purchase numbering, product/unit
// snapshots, conversion factors, tax policy, and calculated line amounts.
// Implementations should return errs.AppError values for expected domain
// failures and must not open or commit their own transaction.
type PurchaseDraftResolver interface {
	ResolvePurchaseDraft(
		ctx context.Context,
		scope PurchaseScope,
		request CreatePurchaseOrderRequest,
	) (PurchaseDraftResolution, error)
}

// Service coordinates the Purchase order draft use cases. Audit and durable
// idempotency adapters are not present in the current module wiring. The
// repository stores operationID in transition history but does not implement
// replay-result semantics, so mutations must not be exposed until those
// shared capabilities are wired.
type Service struct {
	repo     *Repository
	db       *db.DB
	resolver PurchaseDraftResolver
}

func NewService(repo *Repository, database *db.DB, resolver PurchaseDraftResolver) *Service {
	return &Service{repo: repo, db: database, resolver: resolver}
}

// Create creates a draft order. operationID must come from the trusted shared
// operation-identity layer, not from request JSON.
func (s *Service) Create(
	ctx context.Context,
	req *CreatePurchaseOrderRequest,
	operationID uuid.UUID,
) (*PurchaseOrderResponse, error) {
	if req == nil {
		return nil, errs.Validation("purchase order request is required")
	}
	if err := req.Validate(); err != nil {
		return nil, errs.Validation(err.Error())
	}
	if operationID == uuid.Nil {
		return nil, errs.Validation("purchase operation identity is required")
	}
	if s == nil || s.repo == nil || s.db == nil || s.resolver == nil {
		return nil, errs.DatabaseError(fmt.Errorf("purchase draft dependencies are not configured"))
	}

	scope, actorID, err := purchaseScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if !scope.allowsBranch(req.BranchID) {
		return nil, errs.Forbidden("branch is outside the authorized purchase scope")
	}

	request := normalizeCreatePurchaseOrderRequest(*req)
	var createdID uuid.UUID
	err = s.db.WithTx(ctx, func(txCtx context.Context) error {
		resolution, err := s.resolver.ResolvePurchaseDraft(txCtx, scope, request)
		if err != nil {
			return mapPurchaseDependencyError(err)
		}
		if err := validatePurchaseDraftResolution(request, resolution); err != nil {
			return err
		}

		order := &PurchaseOrder{
			OrganizationID:     scope.OrganizationID,
			BranchID:           request.BranchID,
			SupplierID:         request.SupplierID,
			PurchaseNo:         strings.TrimSpace(resolution.PurchaseNo),
			ReferenceNo:        clearBlankString(request.ReferenceNo),
			CurrencyCode:       strings.ToUpper(strings.TrimSpace(resolution.CurrencyCode)),
			PolicySnapshot:     resolution.PolicySnapshot,
			RequestedBy:        actorID,
			ExpectedDeliveryAt: request.ExpectedDeliveryAt,
			Notes:              clearBlankString(request.Notes),
		}
		created, err := s.repo.CreateDraft(
			txCtx, scope, order, resolution.Lines, operationID,
		)
		if err != nil {
			return mapPurchaseWriteError(err)
		}
		createdID = created.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, createdID)
}

// Get returns an order and its lines, with both organization and branch scope
// enforced by the repository query.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*PurchaseOrderResponse, error) {
	if s == nil || s.repo == nil {
		return nil, errs.DatabaseError(fmt.Errorf("purchase repository is not configured"))
	}
	if id == uuid.Nil {
		return nil, errs.Validation("purchase order id is required")
	}
	scope, _, err := purchaseScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	order, lines, err := s.repo.FindByIDWithLines(ctx, scope, id)
	if err != nil {
		return nil, mapPurchaseReadError(err)
	}
	return purchaseOrderResponse(order, lines), nil
}

// List returns scoped purchase-order headers. Lines are intentionally omitted
// from list results to avoid loading children for every row.
func (s *Service) List(
	ctx context.Context,
	query *ListPurchaseOrdersQuery,
) ([]PurchaseOrderResponse, pagination.Meta, error) {
	if s == nil || s.repo == nil {
		return nil, pagination.Meta{}, errs.DatabaseError(fmt.Errorf("purchase repository is not configured"))
	}
	if query == nil {
		return nil, pagination.Meta{}, errs.Validation("purchase order query is required")
	}
	if err := query.NormalizeAndValidate(); err != nil {
		return nil, pagination.Meta{}, errs.Validation(err.Error())
	}
	scope, _, err := purchaseScopeFromContext(ctx)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	orders, total, err := s.repo.List(ctx, scope, *query)
	if err != nil {
		return nil, pagination.Meta{}, mapPurchaseReadError(err)
	}
	responses := make([]PurchaseOrderResponse, len(orders))
	for i := range orders {
		responses[i] = *purchaseOrderResponse(&orders[i], nil)
	}
	return responses, pagination.BuildOffsetMeta(query.Offset, total), nil
}

// Update changes only caller-editable fields on a draft. Version is required
// for optimistic concurrency; line replacements are re-resolved server-side.
func (s *Service) Update(
	ctx context.Context,
	id uuid.UUID,
	req *UpdatePurchaseOrderRequest,
	operationID uuid.UUID,
) (*PurchaseOrderResponse, error) {
	if req == nil {
		return nil, errs.Validation("purchase order update request is required")
	}
	if id == uuid.Nil {
		return nil, errs.Validation("purchase order id is required")
	}
	if req.Version < 1 {
		return nil, errs.Validation("purchase order version must be positive")
	}
	if operationID == uuid.Nil {
		return nil, errs.Validation("purchase operation identity is required")
	}
	if s == nil || s.repo == nil || s.db == nil {
		return nil, errs.DatabaseError(fmt.Errorf("purchase service dependencies are not configured"))
	}
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	scope, actorID, err := purchaseScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var updatedID uuid.UUID
	err = s.db.WithTx(ctx, func(txCtx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(txCtx, scope, id)
		if err != nil {
			return mapPurchaseReadError(err)
		}
		updated := *current
		if req.ReferenceNo != nil {
			updated.ReferenceNo = clearBlankString(req.ReferenceNo)
		}
		if req.ExpectedDeliveryAt != nil {
			updated.ExpectedDeliveryAt = req.ExpectedDeliveryAt
		}
		if req.Notes != nil {
			updated.Notes = clearBlankString(req.Notes)
		}

		var lines *[]PurchaseOrderLine
		if req.Lines != nil {
			if s.resolver == nil {
				return errs.DatabaseError(fmt.Errorf("purchase draft resolver is not configured"))
			}
			draftRequest := CreatePurchaseOrderRequest{
				BranchID:   current.BranchID,
				SupplierID: current.SupplierID,
				Lines:      append([]CreatePurchaseOrderLineRequest(nil), (*req.Lines)...),
			}
			if err := draftRequest.Validate(); err != nil {
				return errs.Validation(err.Error())
			}
			resolution, err := s.resolver.ResolvePurchaseDraft(txCtx, scope, draftRequest)
			if err != nil {
				return mapPurchaseDependencyError(err)
			}
			if err := validatePurchaseDraftResolution(draftRequest, resolution); err != nil {
				return err
			}
			replacement := append([]PurchaseOrderLine(nil), resolution.Lines...)
			lines = &replacement
		}

		saved, err := s.repo.UpdateDraft(
			txCtx, scope, &updated, req.Version, lines, operationID, actorID,
		)
		if err != nil {
			return mapPurchaseWriteError(err)
		}
		updatedID = saved.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, updatedID)
}

func purchaseScopeFromContext(ctx context.Context) (PurchaseScope, uuid.UUID, error) {
	orgID := appctx.OrgID(ctx)
	actorID := appctx.UserID(ctx)
	if orgID == uuid.Nil || actorID == uuid.Nil {
		return PurchaseScope{}, uuid.Nil, errs.Unauthenticated()
	}

	branchIDs, orgWide := appctx.BranchScope(ctx)
	scope := PurchaseScope{
		OrganizationID: orgID,
		BranchIDs:      branchIDs,
		OrgWide:        orgWide,
	}
	if err := scope.validate(); err != nil {
		return PurchaseScope{}, uuid.Nil, errs.Forbidden("no accessible purchase branches")
	}
	return scope, actorID, nil
}

func validatePurchaseDraftResolution(
	request CreatePurchaseOrderRequest,
	resolution PurchaseDraftResolution,
) error {
	if strings.TrimSpace(resolution.PurchaseNo) == "" ||
		strings.TrimSpace(resolution.CurrencyCode) == "" {
		return errs.Validation("purchase number and organization currency must be resolved")
	}
	if len(resolution.Lines) != len(request.Lines) {
		return errs.Validation("resolved purchase lines do not match the request")
	}
	for i, requested := range request.Lines {
		resolved := resolution.Lines[i]
		if resolved.MedicineID != requested.MedicineID ||
			!sameOptionalUUID(resolved.VariantID, requested.VariantID) ||
			resolved.UnitID != requested.UnitID ||
			resolved.OrderedQuantity != requested.OrderedQuantity ||
			resolved.UnitPriceMinor != requested.UnitPriceMinor ||
			resolved.DiscountRate != requested.DiscountRateBPS ||
			resolved.TaxRate != requested.TaxRateBPS ||
			!sameOptionalTime(resolved.ExpectedDeliveryAt, requested.ExpectedDeliveryAt) {
			return errs.Validation(fmt.Sprintf("resolved purchase line %d does not match the request", i))
		}
	}
	return nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func normalizeCreatePurchaseOrderRequest(req CreatePurchaseOrderRequest) CreatePurchaseOrderRequest {
	req.ReferenceNo = trimOptionalString(req.ReferenceNo)
	req.Notes = trimOptionalString(req.Notes)
	return req
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func clearBlankString(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func purchaseOrderResponse(order *PurchaseOrder, lines []PurchaseOrderLine) *PurchaseOrderResponse {
	if order == nil {
		return nil
	}
	response := &PurchaseOrderResponse{
		ID:                 order.ID,
		BranchID:           order.BranchID,
		SupplierID:         order.SupplierID,
		PurchaseNo:         order.PurchaseNo,
		ReferenceNo:        order.ReferenceNo,
		OrderStatus:        order.OrderStatus,
		ReceiptStatus:      order.ReceiptStatus,
		InvoiceStatus:      order.InvoiceStatus,
		ClosureStatus:      order.ClosureStatus,
		CurrencyCode:       order.CurrencyCode,
		RequestedBy:        order.RequestedBy,
		SubmittedBy:        order.SubmittedBy,
		SubmittedAt:        order.SubmittedAt,
		ApprovedBy:         order.ApprovedBy,
		ApprovedAt:         order.ApprovedAt,
		RejectedBy:         order.RejectedBy,
		RejectedAt:         order.RejectedAt,
		RejectionReason:    order.RejectionReason,
		ExpectedDeliveryAt: order.ExpectedDeliveryAt,
		OrderedAt:          order.OrderedAt,
		CancelledAt:        order.CancelledAt,
		ClosedAt:           order.ClosedAt,
		Notes:              order.Notes,
		Version:            order.Version,
		CreatedAt:          order.CreatedAt,
		UpdatedAt:          order.UpdatedAt,
	}
	if lines != nil {
		response.Lines = make([]PurchaseOrderLineResponse, len(lines))
		for i, line := range lines {
			response.Lines[i] = PurchaseOrderLineResponse{
				ID:                  line.ID,
				LineNo:              line.LineNo,
				MedicineID:          line.MedicineID,
				VariantID:           line.VariantID,
				UnitID:              line.UnitID,
				OrderedQuantity:     line.OrderedQuantity,
				AcceptedQuantity:    line.AcceptedQuantity,
				UnitPriceMinor:      line.UnitPriceMinor,
				DiscountAmountMinor: line.DiscountAmount,
				TaxAmountMinor:      line.TaxAmount,
				LineTotalMinor:      line.LineTotal,
				ProductName:         line.ProductNameSnapshot,
				SKU:                 line.SKU,
				UnitName:            line.UnitNameSnapshot,
				Version:             line.Version,
			}
		}
	}
	return response
}

func mapPurchaseReadError(err error) error {
	if stderrors.Is(err, db.ErrNoRows) {
		return errs.NotFound("purchase order")
	}
	return errs.DatabaseError(err)
}

func mapPurchaseWriteError(err error) error {
	switch {
	case stderrors.Is(err, ErrPurchaseOrderConflict):
		return errs.Conflict("purchase order is not a mutable draft or its version is stale")
	case stderrors.Is(err, db.ErrNoRows):
		return errs.NotFound("purchase order")
	case db.IsUniqueVilation(err):
		return errs.Conflict("purchase order number or operation identity already exists")
	case db.IsForeignKeyViolation(err):
		return errs.NotFound("purchase reference")
	default:
		return errs.DatabaseError(err)
	}
}

func mapPurchaseDependencyError(err error) error {
	var appErr *errs.AppError
	if stderrors.As(err, &appErr) {
		return err
	}
	return errs.DatabaseError(err)
}
