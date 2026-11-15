package sale

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// Service exposes Sale reads and quote calculation. Mutating workflows remain
// unavailable until their shared-transaction dependencies and schema are ready.
type Service struct {
	repo    *Repository
	pricing *PricingService
}

func NewService(repo *Repository, pricing *PricingService) *Service {
	return &Service{repo: repo, pricing: pricing}
}

type SaleDetail struct {
	Sale        *Sale
	Items       []SaleItem
	Allocations []SaleStockAllocation
	Tenders     []SaleTender
	Invoice     *SaleInvoice
}

func (s *Service) GetSale(ctx context.Context, scope SaleScope, id uuid.UUID) (*SaleDetail, error) {
	if s == nil || s.repo == nil {
		return nil, errs.Internal(fmt.Errorf("sale repository is not configured"))
	}
	if err := scope.validate(); err != nil {
		return nil, errs.Validation(err.Error())
	}
	if id == uuid.Nil {
		return nil, errs.Validation("sale id is required")
	}

	sale, items, err := s.repo.FindSaleWithItems(ctx, scope, id)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("sale")
		}
		return nil, errs.DatabaseError(err)
	}

	detail := &SaleDetail{Sale: sale, Items: items}
	detail.Allocations, err = s.repo.ListSaleStockAllocations(ctx, scope.OrganizationID, id)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	detail.Tenders, err = s.repo.ListSaleTenders(ctx, scope.OrganizationID, sale.BranchID, id)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	detail.Invoice, err = s.repo.FindSaleInvoice(ctx, scope.OrganizationID, id)
	if err != nil && !errors.Is(err, db.ErrNoRows) {
		return nil, errs.DatabaseError(err)
	}
	return detail, nil
}

func (s *Service) ListSales(ctx context.Context, scope SaleScope, query ListSalesQuery) ([]Sale, int64, bool, error) {
	if s == nil || s.repo == nil {
		return nil, 0, false, errs.Internal(fmt.Errorf("sale repository is not configured"))
	}
	if err := scope.validate(); err != nil {
		return nil, 0, false, errs.Validation(err.Error())
	}
	sales, total, hasMore, err := s.repo.ListSales(ctx, scope, query)
	if err != nil {
		return nil, 0, false, errs.DatabaseError(err)
	}
	return sales, total, hasMore, nil
}

func (s *Service) CalculateQuote(ctx context.Context, scope SaleScope, request *CalculateRequest) (*CalculateResponse, error) {
	if s == nil || s.pricing == nil {
		return nil, errs.Internal(fmt.Errorf("sale pricing service is not configured"))
	}
	if request == nil {
		return nil, errs.Validation("calculate request is required")
	}
	if err := scope.validate(); err != nil {
		return nil, errs.Validation(err.Error())
	}
	return s.pricing.CalculateQuote(ctx, scope, request)
}
