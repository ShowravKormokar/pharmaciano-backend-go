package sale

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// InvoiceService reads immutable invoice facts. Number allocation and invoice
// issuance are intentionally not exposed before the legal numbering policy and
// a transaction-bound persistence path are approved.
type InvoiceService struct {
	repo *Repository
}

func NewInvoiceService(repo *Repository) *InvoiceService {
	return &InvoiceService{repo: repo}
}

func (s *InvoiceService) GetInvoice(ctx context.Context, scope SaleScope, saleID uuid.UUID) (*SaleInvoiceResponse, error) {
	if s == nil || s.repo == nil {
		return nil, errs.Internal(fmt.Errorf("sale repository is not configured"))
	}
	if err := scope.validate(); err != nil {
		return nil, errs.Validation(err.Error())
	}
	if saleID == uuid.Nil {
		return nil, errs.Validation("sale id is required")
	}
	if _, err := s.repo.FindSaleByID(ctx, scope, saleID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("sale")
		}
		return nil, errs.DatabaseError(err)
	}
	invoice, err := s.repo.FindSaleInvoice(ctx, scope.OrganizationID, saleID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("invoice")
		}
		return nil, errs.DatabaseError(err)
	}
	return &SaleInvoiceResponse{
		ID:              invoice.ID,
		Series:          invoice.Series,
		FiscalKey:       invoice.FiscalKey,
		InvoiceNo:       invoice.InvoiceNo,
		IssuedAt:        invoice.IssuedAt,
		CurrencyCode:    invoice.CurrencyCode,
		Snapshot:        invoice.Snapshot,
		SnapshotHash:    invoice.SnapshotHash,
		TemplateVersion: invoice.TemplateVersion,
	}, nil
}
