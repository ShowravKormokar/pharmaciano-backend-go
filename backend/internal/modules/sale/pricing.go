package sale

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	errs "backend/internal/errors"
	"backend/internal/modules/masterdata"
	"backend/internal/modules/medicine"
	"backend/internal/platform/db"
)

// PricingResolver resolves pricing from price books, promotions, coupons, and tax rules.
type PricingResolver interface {
	ResolvePrice(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID, priceBookID uuid.UUID, medicineID, variantID, unitID uuid.UUID, at time.Time) (*ResolvedPrice, error)
	ResolveTax(ctx context.Context, orgID uuid.UUID, taxRateID uuid.UUID, at time.Time) (*ResolvedTax, error)
	ResolvePromotions(ctx context.Context, orgID uuid.UUID, currencyCode string, at time.Time, subtotalMinor int64, customerID *uuid.UUID, items []PricingItem) ([]AppliedPromotion, int64, error)
	ResolveCoupon(ctx context.Context, orgID uuid.UUID, code string, subtotalMinor int64, currencyCode string, customerID *uuid.UUID, at time.Time) (*AppliedCoupon, error)
}

type ResolvedPrice struct {
	UnitPriceMinor int64
	CurrencyCode   string
	PriceVersion   *SalePriceVersion
	Snapshot       common.JSONMap
}

type ResolvedTax struct {
	RateBPS   int32
	TaxRateID uuid.UUID
	Snapshot  common.JSONMap
}

type PricingItem struct {
	MedicineID     uuid.UUID
	VariantID      *uuid.UUID
	UnitID         uuid.UUID
	Quantity       int64
	UnitPriceMinor int64
	LineTotalMinor int64
}

type AppliedPromotion struct {
	PromotionID   uuid.UUID
	Kind          PromotionKind
	DiscountMinor int64
	Description   string
	Snapshot      common.JSONMap
}

type AppliedCoupon struct {
	CouponID        uuid.UUID
	DiscountMinor   int64
	DiscountRateBPS int32
	Snapshot        common.JSONMap
}

type PricingService struct {
	repo           *Repository
	db             *db.DB
	medicineRepo   PricingMedicineRepository
	masterDataRepo PricingMasterDataRepository
	taxPolicy      TaxCalculationPolicy
}

type PricingMedicineRepository interface {
	GetMedicine(ctx context.Context, orgID, id uuid.UUID) (*medicine.Medicine, error)
	GetMedicineVariant(ctx context.Context, orgID, id uuid.UUID) (*medicine.MedicineVariant, error)
	GetUnit(ctx context.Context, orgID, id uuid.UUID) (*masterdata.UnitType, error)
}

type PricingMasterDataRepository interface {
	GetTaxRate(ctx context.Context, id uuid.UUID) (*masterdata.TaxRate, error)
}

type TaxCalculationPolicy interface {
	CalculateTax(baseMinor int64, rateBPS int32) (int64, error)
	Version() string
}

func NewPricingService(repo *Repository, database *db.DB, medRepo PricingMedicineRepository, mdRepo PricingMasterDataRepository, taxPolicy TaxCalculationPolicy) *PricingService {
	return &PricingService{
		repo:           repo,
		db:             database,
		medicineRepo:   medRepo,
		masterDataRepo: mdRepo,
		taxPolicy:      taxPolicy,
	}
}

func (s *PricingService) CalculateQuote(ctx context.Context, scope SaleScope, req *CalculateRequest) (*CalculateResponse, error) {
	if req == nil {
		return nil, errs.Validation("calculate request is required")
	}
	if validationErrors := req.Validate(); len(validationErrors) > 0 {
		return nil, errs.Validation(validationErrors[0].Error())
	}
	if err := scope.validate(); err != nil {
		return nil, errs.Validation(err.Error())
	}
	if s.repo == nil || s.medicineRepo == nil || s.masterDataRepo == nil || s.taxPolicy == nil {
		return nil, errs.Business(errs.CodePriceInvalid, "sale pricing policies and owner-module adapters are not configured")
	}

	now := time.Now().UTC()
	currency := "BDT"

	var items []CalculateItemResponse
	var subtotalMinor, discountMinor, couponDiscountMinor, taxMinor, otherChargesMinor, totalMinor int64

	branchID, err := singleBranchScope(scope)
	if err != nil {
		return nil, errs.Validation(err.Error())
	}
	priceBook, err := s.resolveEffectivePriceBook(ctx, scope.OrganizationID, branchID, req.SaleType)
	if err != nil {
		return nil, err
	}

	for i, itemReq := range req.Items {
		med, err := s.medicineRepo.GetMedicine(ctx, scope.OrganizationID, itemReq.MedicineID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeNotFound, "medicine not found")
		}
		if med == nil || med.DeletedAt != nil {
			return nil, errs.NotFound("medicine")
		}

		var variant *medicine.MedicineVariant
		if itemReq.VariantID != nil {
			variant, err = s.medicineRepo.GetMedicineVariant(ctx, scope.OrganizationID, *itemReq.VariantID)
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeNotFound, "variant not found")
			}
			if variant == nil || variant.DeletedAt != nil {
				return nil, errs.NotFound("variant")
			}
		}

		unit, err := s.medicineRepo.GetUnit(ctx, scope.OrganizationID, itemReq.UnitID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeNotFound, "unit not found")
		}
		if unit == nil || unit.DeletedAt != nil {
			return nil, errs.NotFound("unit")
		}

		resolvedPrice, err := s.resolveItemPrice(ctx, scope.OrganizationID, priceBook.ID, med.ID, itemReq.VariantID, itemReq.UnitID, now)
		if err != nil {
			return nil, err
		}

		conversionNum := int64(1)
		baseQty := itemReq.EnteredQuantity
		var ok bool
		if variant != nil && variant.UnitsPerPack != nil && *variant.UnitsPerPack > 0 {
			conversionNum = int64(*variant.UnitsPerPack)
			baseQty, ok = checkedMultiply(itemReq.EnteredQuantity, conversionNum)
			if !ok {
				return nil, errs.Validation(fmt.Sprintf("item %d: base quantity exceeds supported range", i+1))
			}
		} else if med.UnitsPerPack != nil && *med.UnitsPerPack > 0 {
			conversionNum = int64(*med.UnitsPerPack)
			baseQty, ok = checkedMultiply(itemReq.EnteredQuantity, conversionNum)
			if !ok {
				return nil, errs.Validation(fmt.Sprintf("item %d: base quantity exceeds supported range", i+1))
			}
		}

		if baseQty <= 0 {
			return nil, errs.Validation(fmt.Sprintf("item %d: base quantity must be positive", i+1))
		}

		lineSubtotal, ok := checkedMultiply(resolvedPrice.UnitPriceMinor, baseQty)
		if !ok {
			return nil, errs.Validation(fmt.Sprintf("item %d: line subtotal exceeds supported range", i+1))
		}
		subtotalMinor, ok = checkedAdd(subtotalMinor, lineSubtotal)
		if !ok {
			return nil, errs.Validation("quote subtotal exceeds supported range")
		}

		discountRate := int32(0)
		discountAmt := int64(0)

		if med.TaxRateID == nil {
			return nil, errs.Business(errs.CodePriceInvalid, "tax classification is required to calculate a sale quote")
		}
		taxRate := int32(0)
		if med.TaxRateID != nil {
			taxInfo, err := s.resolveTaxForMedicine(ctx, scope.OrganizationID, *med.TaxRateID, now)
			if err != nil {
				return nil, err
			}
			if taxInfo == nil {
				return nil, errs.Business(errs.CodePriceInvalid, "tax rate is unavailable")
			}
			taxRate = taxInfo.RateBPS
		}
		lineTax, err := s.taxPolicy.CalculateTax(lineSubtotal-discountAmt, taxRate)
		if err != nil {
			return nil, errs.Business(errs.CodePriceInvalid, "tax calculation policy rejected the quote")
		}
		discountMinor += discountAmt
		taxMinor, ok = checkedAdd(taxMinor, lineTax)
		if !ok {
			return nil, errs.Validation("quote tax exceeds supported range")
		}

		lineTotal, ok := checkedAdd(lineSubtotal-discountAmt, lineTax)
		if !ok {
			return nil, errs.Validation(fmt.Sprintf("item %d: line total exceeds supported range", i+1))
		}
		totalMinor, ok = checkedAdd(totalMinor, lineTotal)
		if !ok {
			return nil, errs.Validation("quote total exceeds supported range")
		}

		items = append(items, CalculateItemResponse{
			MedicineID:      med.ID,
			VariantID:       itemReq.VariantID,
			UnitID:          itemReq.UnitID,
			EnteredQuantity: itemReq.EnteredQuantity,
			BaseQuantity:    baseQty,
			UnitPriceMinor:  resolvedPrice.UnitPriceMinor,
			DiscountMinor:   discountAmt,
			TaxMinor:        lineTax,
			LineTotalMinor:  lineTotal,
			DiscountRateBPS: discountRate,
			TaxRateBPS:      taxRate,
		})
	}

	if req.CouponID != nil {
		coupon, err := s.resolveCouponByID(ctx, scope.OrganizationID, *req.CouponID, subtotalMinor, currency, req.CustomerID, now)
		if err != nil {
			return nil, err
		}
		if coupon != nil {
			couponDiscountMinor = coupon.DiscountMinor
			if couponDiscountMinor > totalMinor {
				return nil, errs.Business(errs.CodePriceInvalid, "coupon discount exceeds quote total")
			}
			totalMinor -= couponDiscountMinor
		}
	}

	quotedAt := now
	expiresAt := quotedAt.Add(15 * time.Minute)

	return &CalculateResponse{
		SubtotalMinor:       subtotalMinor,
		DiscountMinor:       discountMinor,
		CouponDiscountMinor: couponDiscountMinor,
		TaxMinor:            taxMinor,
		OtherChargesMinor:   otherChargesMinor,
		TotalMinor:          totalMinor,
		CurrencyCode:        currency,
		Items:               items,
		PolicyVersion:       fmt.Sprintf("tax-%s/price-book-%s-%d", s.taxPolicy.Version(), priceBook.Code, priceBook.Version),
		QuotedAt:            quotedAt,
		ExpiresAt:           &expiresAt,
	}, nil
}

func (s *PricingService) ApplyCoupon(ctx context.Context, orgID uuid.UUID, req *ApplyCouponRequest) (*ApplyCouponResponse, error) {
	if req == nil {
		return nil, errs.Validation("apply coupon request is required")
	}
	if validationErrors := req.Validate(); len(validationErrors) > 0 {
		return nil, errs.Validation(validationErrors[0].Error())
	}
	if orgID == uuid.Nil {
		return nil, errs.Validation("organization context is required")
	}

	now := time.Now().UTC()
	currency := "BDT"

	coupon, err := s.repo.FindPromotionByCode(ctx, orgID, req.Code)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if coupon == nil {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon not found"),
		}, nil
	}
	if coupon.DeletedAt != nil {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon is archived"),
		}, nil
	}
	if !coupon.IsActive {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon is inactive"),
		}, nil
	}
	if now.Before(coupon.ValidFrom) || now.After(coupon.ValidTo) {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon is not valid for current date"),
		}, nil
	}
	if coupon.CurrencyCode != nil && *coupon.CurrencyCode != currency {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon currency mismatch"),
		}, nil
	}
	if coupon.MinPurchaseMinor != nil && req.Subtotal < *coupon.MinPurchaseMinor {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("subtotal below minimum purchase amount"),
		}, nil
	}
	if coupon.UsageLimit != nil && coupon.UsageCount >= *coupon.UsageLimit {
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon usage limit exceeded"),
		}, nil
	}

	discount := int64(0)
	rateBPS := int32(0)
	flatAmt := int64(0)

	switch coupon.Kind {
	case PromotionKindPercentOff:
		if coupon.DiscountRateBPS != nil {
			rateBPS = int32(*coupon.DiscountRateBPS)
			discount = (req.Subtotal * int64(rateBPS)) / 10000
		}
	case PromotionKindFlatOff:
		if coupon.FlatDiscountMinor != nil {
			flatAmt = *coupon.FlatDiscountMinor
			discount = flatAmt
		}
	default:
		return &ApplyCouponResponse{
			Code:            req.Code,
			IsValid:         false,
			CurrencyCode:    currency,
			RejectionReason: strPtr("coupon type not supported"),
		}, nil
	}

	if coupon.MaxDiscountMinor != nil && discount > *coupon.MaxDiscountMinor {
		discount = *coupon.MaxDiscountMinor
	}

	if req.CustomerID != nil && coupon.PerCustomerLimit != nil {
		usage, err := s.repo.CheckPromotionRedemptionLimit(ctx, orgID, coupon.ID, *req.CustomerID, *coupon.PerCustomerLimit)
		if err != nil {
			return nil, errs.DatabaseError(err)
		}
		if usage >= *coupon.PerCustomerLimit {
			return &ApplyCouponResponse{
				Code:            req.Code,
				IsValid:         false,
				CurrencyCode:    currency,
				RejectionReason: strPtr("per-customer usage limit exceeded"),
			}, nil
		}
	}

	return &ApplyCouponResponse{
		Code:              req.Code,
		DiscountMinor:     discount,
		CurrencyCode:      currency,
		DiscountRateBPS:   rateBPS,
		FlatDiscountMinor: flatAmt,
		MaxDiscountMinor:  pointerValue(coupon.MaxDiscountMinor),
		IsValid:           true,
		RejectionReason:   nil,
	}, nil
}

func (s *PricingService) resolveEffectivePriceBook(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID, saleType SaleType) (*SalePriceBook, error) {
	var book *SalePriceBook
	var err error

	if branchID != nil {
		book, err = s.repo.FindPriceBookByCode(ctx, orgID, branchID, "default")
		if err != nil && !errors.Is(err, db.ErrNoRows) {
			return nil, errs.DatabaseError(err)
		}
	}

	if book == nil {
		book, err = s.repo.FindPriceBookByCode(ctx, orgID, nil, "default")
		if err != nil {
			return nil, errs.DatabaseError(err)
		}
	}
	if book == nil {
		return nil, errs.NotFound("default price book")
	}
	if book.DeletedAt != nil || !book.IsActive {
		return nil, errs.Business(errs.CodePriceInvalid, "price book is not active")
	}
	return book, nil
}

func (s *PricingService) resolveItemPrice(ctx context.Context, orgID, bookID, medID uuid.UUID, variantID *uuid.UUID, unitID uuid.UUID, at time.Time) (*ResolvedPrice, error) {
	version, err := s.repo.FindPriceVersion(ctx, &orgID, &bookID, &medID, variantID, unitID, at)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.Business(errs.CodePriceInvalid, "no price found for item")
		}
		return nil, errs.DatabaseError(err)
	}
	if version == nil {
		return nil, errs.Business(errs.CodePriceInvalid, "no price found for item")
	}
	if version.UnitPriceMinor < 0 {
		return nil, errs.Business(errs.CodePriceInvalid, "price must be positive")
	}

	snapshot := common.JSONMap{
		"price_version_id": version.ID.String(),
		"price_book_id":    version.PriceBookID.String(),
		"unit_price_minor": version.UnitPriceMinor,
		"currency_code":    version.CurrencyCode,
		"valid_from":       version.ValidFrom.Format(time.RFC3339),
	}
	if version.ValidTo != nil {
		snapshot["valid_to"] = version.ValidTo.Format(time.RFC3339)
	}

	return &ResolvedPrice{
		UnitPriceMinor: version.UnitPriceMinor,
		CurrencyCode:   version.CurrencyCode,
		PriceVersion:   version,
		Snapshot:       snapshot,
	}, nil
}

func (s *PricingService) resolveTaxForMedicine(ctx context.Context, orgID, taxRateID uuid.UUID, at time.Time) (*ResolvedTax, error) {
	taxRate, err := s.masterDataRepo.GetTaxRate(ctx, taxRateID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if taxRate == nil || taxRate.DeletedAt != nil || !taxRate.IsActive {
		return nil, errs.NotFound("active tax rate")
	}
	if math.IsNaN(taxRate.RatePercent) || math.IsInf(taxRate.RatePercent, 0) || taxRate.RatePercent < 0 || taxRate.RatePercent > 100 {
		return nil, errs.Business(errs.CodePriceInvalid, "tax rate is outside the supported range")
	}
	rateBPS := int32(math.Round(taxRate.RatePercent * 100))

	snapshot := common.JSONMap{
		"tax_rate_id":   taxRate.ID.String(),
		"tax_rate_code": taxRate.Code,
		"rate_bps":      rateBPS,
		"name":          taxRate.Name,
	}

	return &ResolvedTax{
		RateBPS:   rateBPS,
		TaxRateID: taxRate.ID,
		Snapshot:  snapshot,
	}, nil
}

func (s *PricingService) resolveCouponByID(ctx context.Context, orgID, couponID uuid.UUID, subtotal int64, currency string, customerID *uuid.UUID, at time.Time) (*AppliedCoupon, error) {
	promo, err := s.repo.FindPromotionByID(ctx, orgID, couponID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if promo == nil || promo.DeletedAt != nil || !promo.IsActive {
		return nil, nil
	}
	if at.Before(promo.ValidFrom) || at.After(promo.ValidTo) {
		return nil, nil
	}
	if promo.CurrencyCode != nil && *promo.CurrencyCode != currency {
		return nil, nil
	}
	if promo.MinPurchaseMinor != nil && subtotal < *promo.MinPurchaseMinor {
		return nil, nil
	}
	if promo.UsageLimit != nil && promo.UsageCount >= *promo.UsageLimit {
		return nil, nil
	}

	discount := int64(0)
	rateBPS := int32(0)

	switch promo.Kind {
	case PromotionKindPercentOff:
		if promo.DiscountRateBPS != nil {
			rateBPS = int32(*promo.DiscountRateBPS)
			discount = (subtotal * int64(rateBPS)) / 10000
		}
	case PromotionKindFlatOff:
		if promo.FlatDiscountMinor != nil {
			discount = *promo.FlatDiscountMinor
		}
	}

	if promo.MaxDiscountMinor != nil && discount > *promo.MaxDiscountMinor {
		discount = *promo.MaxDiscountMinor
	}

	if customerID != nil && promo.PerCustomerLimit != nil {
		usage, err := s.repo.CheckPromotionRedemptionLimit(ctx, orgID, promo.ID, *customerID, *promo.PerCustomerLimit)
		if err != nil {
			return nil, errs.DatabaseError(err)
		}
		if usage >= *promo.PerCustomerLimit {
			return nil, nil
		}
	}

	snapshot := common.JSONMap{
		"promotion_id":       promo.ID.String(),
		"promotion_code":     promo.Code,
		"kind":               string(promo.Kind),
		"discount_minor":     discount,
		"discount_rate_bps":  rateBPS,
		"max_discount_minor": promo.MaxDiscountMinor,
	}

	return &AppliedCoupon{
		CouponID:        promo.ID,
		DiscountMinor:   discount,
		DiscountRateBPS: rateBPS,
		Snapshot:        snapshot,
	}, nil
}

func strPtr(s string) *string { return &s }

func pointerValue[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

func roundHalfUp(n int64) int64 {
	return (n + 5) / 10 * 10
}

type RoundingMode int

const (
	RoundingModeHalfUp RoundingMode = iota
	RoundingModeBankers
	RoundingModeFloor
)

func applyRounding(value int64, mode RoundingMode) int64 {
	switch mode {
	case RoundingModeHalfUp:
		return roundHalfUp(value)
	case RoundingModeBankers:
		if value%10 == 5 {
			return value - 5
		}
		return roundHalfUp(value)
	case RoundingModeFloor:
		return value - value%10
	default:
		return roundHalfUp(value)
	}
}

func (s *PricingService) CalculateLineTotal(ctx context.Context, unitPriceMinor, quantity int64, discountRateBPS int32, taxRateBPS int32, roundingMode RoundingMode) (discountMinor, taxMinor, lineTotalMinor int64) {
	subtotal := unitPriceMinor * quantity

	discount := int64(0)
	if discountRateBPS > 0 {
		discount = applyRounding((subtotal*int64(discountRateBPS))/10000, roundingMode)
	}

	taxableBase := subtotal - discount
	tax := int64(0)
	if taxRateBPS > 0 {
		tax = applyRounding((taxableBase*int64(taxRateBPS))/10000, roundingMode)
	}

	lineTotal := applyRounding(taxableBase+tax, roundingMode)
	return discount, tax, lineTotal
}

func buildPricingSnapshot(items []CalculateItemResponse, priceBook *SalePriceBook, promotions []AppliedPromotion, coupon *AppliedCoupon, taxRate *ResolvedTax) common.JSONMap {
	snap := common.JSONMap{
		"price_book": common.JSONMap{
			"id":      priceBook.ID.String(),
			"code":    priceBook.Code,
			"version": priceBook.Version,
		},
		"currency_code": "BDT",
		"items":         items,
	}

	if len(promotions) > 0 {
		promoSnap := make([]common.JSONMap, len(promotions))
		for i, p := range promotions {
			promoSnap[i] = p.Snapshot
		}
		snap["promotions"] = promoSnap
	}

	if coupon != nil {
		snap["coupon"] = coupon.Snapshot
	}

	if taxRate != nil {
		snap["tax"] = taxRate.Snapshot
	}

	return snap
}

func (s *PricingService) ResolvePrice(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID, priceBookID uuid.UUID, medicineID, variantID, unitID uuid.UUID, at time.Time) (*ResolvedPrice, error) {
	book, err := s.repo.FindPriceBookByID(ctx, orgID, priceBookID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if book == nil || book.DeletedAt != nil || !book.IsActive {
		return nil, errs.NotFound("price book")
	}

	return s.resolveItemPrice(ctx, orgID, book.ID, medicineID, &variantID, unitID, at)
}

func (s *PricingService) ResolveTax(ctx context.Context, orgID uuid.UUID, taxRateID uuid.UUID, at time.Time) (*ResolvedTax, error) {
	return s.resolveTaxForMedicine(ctx, orgID, taxRateID, at)
}

func (s *PricingService) ResolvePromotions(ctx context.Context, orgID uuid.UUID, currencyCode string, at time.Time, subtotalMinor int64, customerID *uuid.UUID, items []PricingItem) ([]AppliedPromotion, int64, error) {
	promos, err := s.repo.ListActivePromotions(ctx, orgID, at, &currencyCode, 20)
	if err != nil {
		return nil, 0, errs.DatabaseError(err)
	}

	var applied []AppliedPromotion
	totalDiscount := int64(0)

	for _, promo := range promos {
		if promo.Kind == PromotionKindPercentOff && promo.DiscountRateBPS != nil {
			discount := (subtotalMinor * int64(*promo.DiscountRateBPS)) / 10000
			if promo.MaxDiscountMinor != nil && discount > *promo.MaxDiscountMinor {
				discount = *promo.MaxDiscountMinor
			}
			if discount > 0 {
				applied = append(applied, AppliedPromotion{
					PromotionID:   promo.ID,
					Kind:          promo.Kind,
					DiscountMinor: discount,
					Description:   fmt.Sprintf("%s (%d%%)", promo.Name, *promo.DiscountRateBPS/100),
					Snapshot:      promo.Config,
				})
				totalDiscount += discount
			}
		} else if promo.Kind == PromotionKindFlatOff && promo.FlatDiscountMinor != nil {
			discount := *promo.FlatDiscountMinor
			if discount > 0 {
				applied = append(applied, AppliedPromotion{
					PromotionID:   promo.ID,
					Kind:          promo.Kind,
					DiscountMinor: discount,
					Description:   fmt.Sprintf("%s (flat %d)", promo.Name, discount),
					Snapshot:      promo.Config,
				})
				totalDiscount += discount
			}
		}
	}

	return applied, totalDiscount, nil
}

func (s *PricingService) ResolveCoupon(ctx context.Context, orgID uuid.UUID, code string, subtotalMinor int64, currencyCode string, customerID *uuid.UUID, at time.Time) (*AppliedCoupon, error) {
	return s.resolveCouponByCode(ctx, orgID, code, subtotalMinor, currencyCode, customerID, at)
}

func (s *PricingService) resolveCouponByCode(ctx context.Context, orgID uuid.UUID, code string, subtotalMinor int64, currencyCode string, customerID *uuid.UUID, at time.Time) (*AppliedCoupon, error) {
	promo, err := s.repo.FindPromotionByCode(ctx, orgID, code)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if promo == nil || promo.DeletedAt != nil || !promo.IsActive {
		return nil, nil
	}
	if at.Before(promo.ValidFrom) || at.After(promo.ValidTo) {
		return nil, nil
	}
	if promo.CurrencyCode != nil && *promo.CurrencyCode != currencyCode {
		return nil, nil
	}
	if promo.MinPurchaseMinor != nil && subtotalMinor < *promo.MinPurchaseMinor {
		return nil, nil
	}
	if promo.UsageLimit != nil && promo.UsageCount >= *promo.UsageLimit {
		return nil, nil
	}

	discount := int64(0)
	rateBPS := int32(0)

	switch promo.Kind {
	case PromotionKindPercentOff:
		if promo.DiscountRateBPS != nil {
			rateBPS = int32(*promo.DiscountRateBPS)
			discount = (subtotalMinor * int64(rateBPS)) / 10000
		}
	case PromotionKindFlatOff:
		if promo.FlatDiscountMinor != nil {
			discount = *promo.FlatDiscountMinor
		}
	}

	if promo.MaxDiscountMinor != nil && discount > *promo.MaxDiscountMinor {
		discount = *promo.MaxDiscountMinor
	}

	if customerID != nil && promo.PerCustomerLimit != nil {
		usage, err := s.repo.CheckPromotionRedemptionLimit(ctx, orgID, promo.ID, *customerID, *promo.PerCustomerLimit)
		if err != nil {
			return nil, errs.DatabaseError(err)
		}
		if usage >= *promo.PerCustomerLimit {
			return nil, nil
		}
	}

	snapshot := common.JSONMap{
		"promotion_id":       promo.ID.String(),
		"promotion_code":     promo.Code,
		"kind":               string(promo.Kind),
		"discount_minor":     discount,
		"discount_rate_bps":  rateBPS,
		"max_discount_minor": promo.MaxDiscountMinor,
	}

	return &AppliedCoupon{
		CouponID:        promo.ID,
		DiscountMinor:   discount,
		DiscountRateBPS: rateBPS,
		Snapshot:        snapshot,
	}, nil
}

func singleBranchScope(scope SaleScope) (*uuid.UUID, error) {
	if scope.OrgWide {
		return nil, nil
	}
	if len(scope.BranchIDs) != 1 {
		return nil, fmt.Errorf("quote requires exactly one authorized branch")
	}
	branchID := scope.BranchIDs[0]
	return &branchID, nil
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left < 0 || right < 0 {
		return 0, false
	}
	if left != 0 && right > math.MaxInt64/left {
		return 0, false
	}
	return left * right, true
}

func checkedAdd(left, right int64) (int64, bool) {
	if (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right) {
		return 0, false
	}
	return left + right, true
}
