package medicine

// Medicine business rules.
//
// The service is where the contract's rules live: reference validation,
// lifecycle, write-once identity keys, the protection of semantic fields once
// a product has been used operationally, the reason requirement for regulatory
// changes, and the translation of repository errors into the shared error
// envelope (contract §9, §10, §11).
//
// What the service deliberately does NOT do:
//
//   - It never trusts a tenant identifier from the request. The organization
//     comes from the authenticated context (appctx.OrgID) and nowhere else
//     (contract §3, "Tenant boundary").
//
//   - It never owns price, cost or reorder policy. mrp, default_cost,
//     min_stock_threshold and max_stock_threshold are not written here
//     (contract §2, §17).
//
//   - It does not serve price history. Pricing owns that fact and no approved
//     Pricing interface exists yet, so the price-history endpoint stays blocked
//     (contract §1.12, §8).
//
//   - It does not merge similar products. A duplicate SKU is a conflict, not an
//     invitation to guess (contract §7, "Duplicate products").

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/pkg/pagination"
)

// Service holds the Medicine business rules.
type Service struct {
	repo    *Repository
	db      *db.DB
	metrics *telemetry.Metrics
}

// NewService assembles the service.
//
// metrics is optional: when nil the service still works, it just skips the
// per-operation Prometheus counters.
func NewService(repo *Repository, database *db.DB, metrics ...*telemetry.Metrics) *Service {
	s := &Service{repo: repo, db: database}
	if len(metrics) > 0 {
		s.metrics = metrics[0]
	}
	return s
}

// MedicineList is one page of catalogue entries together with the joined
// labels of the references they point at.
//
// The labels are loaded in one statement per page, so a list costs two queries
// instead of one per row and per reference (contract §9, "Performance").
type MedicineList struct {
	Items  []Medicine
	Labels map[uuid.UUID]MedicineReferenceLabels
	Meta   pagination.Meta
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// Create inserts a catalogue entry.
//
// Every reference is validated before the insert: a brand that does not exist,
// is inactive or belongs to another organization is rejected, and the same
// holds for the dictionary references (category, generic, dosage form, route,
// storage condition, package type, sales unit, tax rate). The database
// foreign keys remain the final integrity guard; this check exists so the
// caller gets a precise error instead of a constraint violation (contract §7,
// "References").
//
// The SKU and the barcode are unique among live rows across the whole table,
// not per organization, so a duplicate is reported as a conflict. The check
// below is tenant-scoped on purpose: it must not reveal whether another
// organization owns a SKU, while the database constraint still prevents the
// duplicate.
func (s *Service) Create(ctx context.Context, req *CreateMedicineRequest) (retMed *Medicine, retErr error) {
	defer s.observe("create", retErr)
	if req == nil {
		return nil, errs.Validation("medicine request is required")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	m := &Medicine{
		SKU:                  strings.TrimSpace(req.SKU),
		Barcode:              trimOptional(req.Barcode),
		Name:                 strings.TrimSpace(req.Name),
		DisplayName:          trimOptional(req.DisplayName),
		Description:          trimOptional(req.Description),
		GenericMedicineID:    req.GenericMedicineID,
		BrandID:              req.BrandID,
		CategoryID:           req.CategoryID,
		DosageFormID:         req.DosageFormID,
		RouteID:              req.RouteID,
		ManufacturerID:       req.ManufacturerID,
		StorageConditionID:   req.StorageConditionID,
		Strength:             trimOptional(req.Strength),
		PackSize:             trimOptional(req.PackSize),
		UnitsPerPack:         req.UnitsPerPack,
		UnitsPerStrip:        req.UnitsPerStrip,
		PackageTypeID:        req.PackageTypeID,
		SalesUnitID:          req.SalesUnitID,
		RequiresPrescription: derefBool(req.RequiresPrescription, false),
		IsControlled:         derefBool(req.IsControlled, false),
		IsRefrigerated:       derefBool(req.IsRefrigerated, false),
		IsImported:           derefBool(req.IsImported, false),
		CountryOfOrigin:      trimOptional(req.CountryOfOrigin),
		ImporterName:         trimOptional(req.ImporterName),
		ImportLicenseNo:      trimOptional(req.ImportLicenseNo),
		TaxRateID:            req.TaxRateID,
		HSNCode:              trimOptional(req.HSNCode),
		ShelfLifeMonths:      req.ShelfLifeMonths,
		ImageURL:             trimOptional(req.ImageURL),
		IsActive:             derefBool(req.IsActive, true),
	}

	if m.SKU == "" {
		return nil, errs.Validation("medicine sku must not be blank")
	}
	if m.Name == "" {
		return nil, errs.Validation("medicine name must not be blank")
	}
	if err := validateMedicineText(m); err != nil {
		return nil, err
	}

	var created *Medicine
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := s.validateBrand(ctx, orgID, m.BrandID); err != nil {
			return err
		}
		if err := s.validateReferences(ctx, medicineReferences(m)); err != nil {
			return err
		}
		if err := s.validateManufacturer(ctx, orgID, m); err != nil {
			return err
		}
		if err := s.ensureSKUFree(ctx, orgID, m.SKU); err != nil {
			return err
		}
		if m.Barcode != nil {
			if err := s.ensureBarcodeFree(ctx, orgID, *m.Barcode); err != nil {
				return err
			}
		}

		var err error
		created, err = s.repo.Create(ctx, orgID, m)
		if err != nil {
			return mapMedicineWriteError(err)
		}

		// The initial variants belong to the same transaction as the medicine:
		// a product that exists without its package identities is a catalogue
		// entry nobody can sell (contract §8, §10).
		taken := map[string]struct{}{}
		for i := range req.InitialVariants {
			variant, err := buildVariant(&req.InitialVariants[i])
			if err != nil {
				return err
			}
			if err := s.ensureVariantSKUFree(ctx, orgID, variant.SKU, taken); err != nil {
				return err
			}
			if _, err := s.repo.CreateVariant(ctx, orgID, created.ID, variant); err != nil {
				return mapMedicineWriteError(err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

// Get returns one catalogue entry.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (retMed *Medicine, retErr error) {
	defer s.observe("get", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.repo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, mapMedicineReadError(err)
	}
	return m, nil
}

// GetWithReferences returns one catalogue entry together with the joined
// labels of the MasterData and Brand references it points at.
//
// The labels are loaded in one statement, so the detail view costs two
// queries instead of one per reference (contract §9, "Performance").
func (s *Service) GetWithReferences(ctx context.Context, id uuid.UUID) (retMed *Medicine, retLabels *MedicineReferenceLabels, retErr error) {
	defer s.observe("get_with_references", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.repo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, nil, mapMedicineReadError(err)
	}
	labels, err := s.repo.LoadReferenceLabels(ctx, orgID, []uuid.UUID{m.ID})
	if err != nil {
		return nil, nil, errs.DatabaseError(err)
	}
	label, ok := labels[m.ID]
	if !ok {
		return m, nil, nil
	}
	return m, &label, nil
}

// ---------------------------------------------------------------------------
// List and search
// ---------------------------------------------------------------------------

// List returns one page of the catalogue.
//
// Two pagination modes share the endpoint: a request without a cursor uses the
// offset page (which also reports the total count, what an administration
// screen needs), and a request with ?cursor= uses the keyset page (whose cost
// does not grow with the depth).
//
// The sort is taken from the allow-list only; a client can never influence the
// shape of the ORDER BY (contract §8.2, §11).
func (s *Service) List(ctx context.Context, q ListMedicinesQuery) (retMed MedicineList, retErr error) {
	defer s.observe("list", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return MedicineList{}, err
	}
	if problems := q.NormalizeAndValidate(); len(problems) > 0 {
		return MedicineList{}, validationError("invalid medicine list query", problems)
	}
	sortFields, err := pagination.ParseSort(q.Sort, allowedMedicineSortFields)
	if err != nil {
		return MedicineList{}, errs.Validation("invalid medicine sort")
	}

	if q.Cursor != "" {
		page, err := s.repo.ListCursor(ctx, orgID, q, sortFields, q.Cursor)
		if err != nil {
			return MedicineList{}, mapMedicineReadError(err)
		}
		labels, err := s.repo.LoadReferenceLabels(ctx, orgID, medicineIDs(page.Items))
		if err != nil {
			return MedicineList{}, errs.DatabaseError(err)
		}
		return MedicineList{
			Items:  page.Items,
			Labels: labels,
			Meta:   pagination.BuildCursorMeta(q.Limit, page.HasMore, page.NextCursor),
		}, nil
	}

	items, total, err := s.repo.List(ctx, orgID, q, sortFields)
	if err != nil {
		return MedicineList{}, mapMedicineReadError(err)
	}
	labels, err := s.repo.LoadReferenceLabels(ctx, orgID, medicineIDs(items))
	if err != nil {
		return MedicineList{}, errs.DatabaseError(err)
	}
	return MedicineList{
		Items:  items,
		Labels: labels,
		Meta:   pagination.BuildOffsetMeta(q.Offset, total),
	}, nil
}

// Search returns the catalogue entries that match a bounded free-text term.
//
// Search is not an exact lookup and it is not a clinical substitution aid: it
// returns candidates for a human to choose from, and the caller still has to
// resolve the exact variant before a sale (contract §1.9, §7).
func (s *Service) Search(ctx context.Context, q SearchMedicinesQuery) (retMed MedicineList, retErr error) {
	defer s.observe("search", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return MedicineList{}, err
	}
	if problems := q.NormalizeAndValidate(); len(problems) > 0 {
		return MedicineList{}, validationError("invalid medicine search query", problems)
	}
	sortFields, err := pagination.ParseSort(q.Sort, allowedMedicineSortFields)
	if err != nil {
		return MedicineList{}, errs.Validation("invalid medicine sort")
	}

	page, err := s.repo.Search(ctx, orgID, q, sortFields, q.Cursor)
	if err != nil {
		return MedicineList{}, mapMedicineReadError(err)
	}
	labels, err := s.repo.LoadReferenceLabels(ctx, orgID, medicineIDs(page.Items))
	if err != nil {
		return MedicineList{}, errs.DatabaseError(err)
	}
	return MedicineList{
		Items:  page.Items,
		Labels: labels,
		Meta:   pagination.BuildCursorMeta(q.Limit, page.HasMore, page.NextCursor),
	}, nil
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update applies a partial update.
//
// Three rules shape the update:
//
//  1. Identity keys are write-once. sku, barcode and brand_id are not part of
//     the update DTO, so they cannot be rewritten at all (contract §5.3, §10).
//
//  2. A regulatory or import-provenance change needs a reason. The flags are
//     flat and single-jurisdiction, so a change to them is an audited event
//     with a stated justification (contract §5.4, §11).
//
//  3. Semantic fields are frozen once the product has been used. Rewriting
//     strength, pack size or the package conversion after stock, purchases or
//     sales reference the product would reinterpret that history, so the
//     change is refused and a new identity has to be created instead
//     (contract §5.2, §10).
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateMedicineRequest) (retMed *Medicine, retErr error) {
	defer s.observe("update", retErr)
	if req == nil {
		return nil, errs.Validation("medicine update request is required")
	}
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	var updated *Medicine
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		m, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapMedicineReadError(err)
		}

		applyMedicinePatch(m, req)
		if err := validateMedicineText(m); err != nil {
			return err
		}
		if err := s.requireReasonForRegulatoryChange(m, req); err != nil {
			return err
		}
		if err := s.requireUnchangedSemantics(ctx, m, req); err != nil {
			return err
		}
		if err := s.validateReferences(ctx, medicineReferences(m)); err != nil {
			return err
		}
		if err := s.validateManufacturer(ctx, orgID, m); err != nil {
			return err
		}

		updated, err = s.repo.Update(ctx, orgID, m)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

// Delete archives a catalogue entry.
//
// A DELETE never removes history: the row is soft-deleted, and a product that
// is referenced by stock, purchases or sales is not deleted at all — the
// caller has to deactivate it instead, so the operational records keep
// pointing at a live identity (contract §7, "Deletion", §10).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) (retErr error) {
	defer s.observe("delete", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		m, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapMedicineReadError(err)
		}
		referenced, err := s.repo.MedicineIsReferenced(ctx, m.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if referenced {
			return errs.Conflict("medicine is referenced by stock, purchases or sales; deactivate it instead")
		}
		deleted, err := s.repo.SoftDelete(ctx, orgID, m.ID)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		if !deleted {
			return errs.NotFound("medicine")
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Exact lookup
// ---------------------------------------------------------------------------

// Lookup resolves a barcode or a SKU to exactly one product.
//
// The request must carry exactly one of the two keys. A lookup that matches
// nothing is a not-found; a lookup that matches more than one active variant
// is an integrity error and fails closed, because a POS screen must never
// have to guess which product an identifier means (contract §5.3, §8.2).
//
// The response carries catalogue and variant metadata only: no stock, no
// price. The sale workflow revalidates branch stock, price and safety state
// itself (contract §7, §8.2).
func (s *Service) Lookup(ctx context.Context, req *LookupMedicineRequest) (retMed *LookupMedicineResponse, retErr error) {
	defer s.observe("lookup", retErr)
	if req == nil {
		return nil, errs.Validation("lookup request is required")
	}
	if problems := req.Validate(); len(problems) > 0 {
		return nil, validationError("invalid medicine lookup request", problems)
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	var candidate LookupCandidate
	switch {
	case req.Barcode != nil && strings.TrimSpace(*req.Barcode) != "":
		candidate, err = s.repo.LookupByBarcode(ctx, orgID, strings.TrimSpace(*req.Barcode))
	case req.SKU != nil && strings.TrimSpace(*req.SKU) != "":
		candidate, err = s.repo.LookupBySKU(ctx, orgID, strings.TrimSpace(*req.SKU))
	default:
		return nil, errs.Validation("provide exactly one of barcode or sku")
	}
	if err != nil {
		return nil, mapLookupError(err)
	}

	return &LookupMedicineResponse{
		MedicineID:           candidate.MedicineID,
		VariantID:            candidate.VariantID,
		MedicineName:         candidate.MedicineName,
		VariantName:          candidate.VariantName,
		BrandName:            candidate.BrandName,
		SKU:                  candidate.SKU,
		Barcode:              candidate.Barcode,
		Strength:             candidate.Strength,
		PackSize:             candidate.PackSize,
		UnitsPerPack:         candidate.UnitsPerPack,
		RequiresPrescription: candidate.RequiresPrescription,
		IsControlled:         candidate.IsControlled,
		IsActive:             candidate.IsActive,
	}, nil
}

// ---------------------------------------------------------------------------
// Variants
// ---------------------------------------------------------------------------

// ListVariants returns one page of the package variants of a medicine.
func (s *Service) ListVariants(ctx context.Context, medicineID uuid.UUID, limit int, cursor string) (retPage VariantPage, retMeta pagination.Meta, retErr error) {
	defer s.observe("list_variants", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return VariantPage{}, pagination.Meta{}, err
	}
	if err := s.requireMedicine(ctx, orgID, medicineID); err != nil {
		return VariantPage{}, pagination.Meta{}, err
	}
	page, err := s.repo.ListVariants(ctx, orgID, medicineID, limit, cursor)
	if err != nil {
		return VariantPage{}, pagination.Meta{}, mapMedicineReadError(err)
	}
	return page, pagination.BuildCursorMeta(limit, page.HasMore, page.NextCursor), nil
}

// CreateVariant adds a package variant to a medicine.
//
// The SKU is the sellable identity of the package and is unique among live
// rows across the whole table. A duplicate is a conflict, never a silent
// overwrite (contract §1.4, §5.2).
func (s *Service) CreateVariant(ctx context.Context, medicineID uuid.UUID, req *CreateMedicineVariantRequest) (retVar *MedicineVariant, retErr error) {
	defer s.observe("create_variant", retErr)
	if req == nil {
		return nil, errs.Validation("medicine variant request is required")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	v, err := buildVariant(req)
	if err != nil {
		return nil, err
	}

	var created *MedicineVariant
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireMedicine(ctx, orgID, medicineID); err != nil {
			return err
		}
		if err := s.ensureVariantSKUFree(ctx, orgID, v.SKU, nil); err != nil {
			return err
		}

		created, err = s.repo.CreateVariant(ctx, orgID, medicineID, v)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// buildVariant turns a variant request into the model that is written.
//
// The name and the SKU are required and bounded by the schema; the barcode
// and the packaging fields are optional.
func buildVariant(req *CreateMedicineVariantRequest) (*MedicineVariant, error) {
	v := &MedicineVariant{
		Name:         strings.TrimSpace(req.Name),
		SKU:          strings.TrimSpace(req.SKU),
		Barcode:      trimOptional(req.Barcode),
		Strength:     trimOptional(req.Strength),
		PackSize:     trimOptional(req.PackSize),
		UnitsPerPack: req.UnitsPerPack,
		IsActive:     derefBool(req.IsActive, true),
	}
	if v.Name == "" {
		return nil, errs.Validation("variant name must not be blank")
	}
	if v.SKU == "" {
		return nil, errs.Validation("variant sku must not be blank")
	}
	if utf8.RuneCountInString(v.Name) > maxVariantNameLen {
		return nil, errs.Validation("variant name is too long")
	}
	if utf8.RuneCountInString(v.SKU) > maxSKULen {
		return nil, errs.Validation("variant sku is too long")
	}
	if v.Barcode != nil && utf8.RuneCountInString(*v.Barcode) > maxBarcodeLen {
		return nil, errs.Validation("variant barcode is too long")
	}
	if v.Strength != nil && utf8.RuneCountInString(*v.Strength) > maxStrengthLen {
		return nil, errs.Validation("variant strength is too long")
	}
	if v.PackSize != nil && utf8.RuneCountInString(*v.PackSize) > maxPackSizeLen {
		return nil, errs.Validation("variant pack size is too long")
	}
	return v, nil
}

// UpdateVariant applies a partial update to a package variant.
//
// Only the label and the packaging fields are updatable: the SKU and the
// barcode are the variant's identity, and a barcode is never silently
// transferred between variants (contract §5.3).
//
// The conversion fields (pack size, units per pack) may only change while the
// variant is still unused. Once a purchase, an inventory movement or a sale
// references it, the conversion is protected and a new variant has to be
// created instead (contract §5.2, §10).
func (s *Service) UpdateVariant(ctx context.Context, medicineID, variantID uuid.UUID, req *UpdateMedicineVariantRequest) (retVar *MedicineVariant, retErr error) {
	defer s.observe("update_variant", retErr)
	if req == nil {
		return nil, errs.Validation("medicine variant update request is required")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		v, err := s.repo.FindVariant(ctx, orgID, medicineID, variantID)
		if err != nil {
			return nil, mapMedicineReadError(err)
		}
		return v, nil
	}

	var updated *MedicineVariant
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		v, err := s.repo.FindVariantForUpdate(ctx, orgID, medicineID, variantID)
		if err != nil {
			return mapMedicineReadError(err)
		}
		if req.Name != nil {
			v.Name = strings.TrimSpace(*req.Name)
		}
		if req.Strength != nil {
			v.Strength = trimOptional(req.Strength)
		}
		if req.PackSize != nil {
			v.PackSize = trimOptional(req.PackSize)
		}
		if req.UnitsPerPack != nil {
			v.UnitsPerPack = req.UnitsPerPack
		}
		if req.IsActive != nil {
			v.IsActive = *req.IsActive
		}
		if v.Name == "" {
			return errs.Validation("variant name must not be blank")
		}
		if utf8.RuneCountInString(v.Name) > maxVariantNameLen {
			return errs.Validation("variant name is too long")
		}

		if err := s.requireUnchangedVariantConversion(ctx, v, req); err != nil {
			return err
		}

		updated, err = s.repo.UpdateVariant(ctx, orgID, medicineID, v)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteVariant archives a package variant.
//
// A variant that is still unused is archived (soft-deleted). A variant that
// operational records already reference is deactivated instead: its identity
// has to stay resolvable so historical invoices and movements remain
// renderable (contract §5.2, §10).
func (s *Service) DeleteVariant(ctx context.Context, medicineID, variantID uuid.UUID) (retErr error) {
	defer s.observe("delete_variant", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		v, err := s.repo.FindVariantForUpdate(ctx, orgID, medicineID, variantID)
		if err != nil {
			return mapMedicineReadError(err)
		}
		referenced, err := s.repo.VariantIsReferenced(ctx, v.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if referenced {
			if v.IsActive {
				v.IsActive = false
				if _, err := s.repo.UpdateVariant(ctx, orgID, medicineID, v); err != nil {
					return mapMedicineWriteError(err)
				}
			}
			return nil
		}
		deleted, err := s.repo.SoftDeleteVariant(ctx, orgID, medicineID, v.ID)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		if !deleted {
			return errs.NotFound("medicine variant")
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Barcodes
// ---------------------------------------------------------------------------

// ListBarcodes returns one page of the barcode assignments of a medicine.
func (s *Service) ListBarcodes(ctx context.Context, medicineID uuid.UUID, limit int, cursor string) (retPage BarcodePage, retMeta pagination.Meta, retErr error) {
	defer s.observe("list_barcodes", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return BarcodePage{}, pagination.Meta{}, err
	}
	if err := s.requireMedicine(ctx, orgID, medicineID); err != nil {
		return BarcodePage{}, pagination.Meta{}, err
	}
	page, err := s.repo.ListBarcodes(ctx, orgID, medicineID, limit, cursor)
	if err != nil {
		return BarcodePage{}, pagination.Meta{}, mapMedicineReadError(err)
	}
	return page, pagination.BuildCursorMeta(limit, page.HasMore, page.NextCursor), nil
}

// CreateBarcode binds a barcode to a medicine and, optionally, to one of its
// variants.
//
// A barcode belongs to a variant, not to a medicine alone. The variant has to
// be a variant of the same medicine, and the code itself is unique among live
// rows — a duplicate is a conflict rather than a silent reassignment
// (contract §5.3).
func (s *Service) CreateBarcode(ctx context.Context, medicineID uuid.UUID, req *CreateMedicineBarcodeRequest) (retBarcode *MedicineBarcode, retErr error) {
	defer s.observe("create_barcode", retErr)
	if req == nil {
		return nil, errs.Validation("medicine barcode request is required")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	code := strings.TrimSpace(req.Barcode)
	if code == "" {
		return nil, errs.Validation("barcode must not be blank")
	}
	if utf8.RuneCountInString(code) > maxBarcodeLen {
		return nil, errs.Validation("barcode is too long")
	}
	barcodeType := req.BarcodeType
	if barcodeType == "" {
		barcodeType = MedicineBarcodeTypeEAN13
	}
	if !barcodeType.Valid() {
		return nil, errs.Validation("unsupported barcode symbology")
	}

	b := &MedicineBarcode{
		MedicineID:  medicineID,
		VariantID:   req.VariantID,
		Barcode:     code,
		BarcodeType: barcodeType,
		IsPrimary:   derefBool(req.IsPrimary, false),
	}

	var created *MedicineBarcode
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireMedicine(ctx, orgID, medicineID); err != nil {
			return err
		}
		if b.VariantID != nil {
			variant, err := s.repo.FindVariant(ctx, orgID, medicineID, *b.VariantID)
			if err != nil {
				if errors.Is(err, db.ErrNoRows) {
					return errs.NotFound("medicine variant")
				}
				return mapMedicineReadError(err)
			}
			if variant == nil {
				return errs.NotFound("medicine variant")
			}
		}

		var err error
		created, err = s.repo.CreateBarcode(ctx, orgID, medicineID, b)
		if err != nil {
			return mapMedicineWriteError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// DeleteBarcode deactivates a barcode assignment.
//
// The row is kept (soft-deleted) so the history of what the code used to
// point at stays readable, and the code is never silently transferred to
// another variant (contract §5.3, §8).
func (s *Service) DeleteBarcode(ctx context.Context, medicineID, barcodeID uuid.UUID) (retErr error) {
	defer s.observe("delete_barcode", retErr)
	orgID, err := organizationID(ctx)
	if err != nil {
		return err
	}
	if err := s.requireMedicine(ctx, orgID, medicineID); err != nil {
		return err
	}
	deactivated, err := s.repo.DeactivateBarcode(ctx, orgID, medicineID, barcodeID)
	if err != nil {
		return mapMedicineWriteError(err)
	}
	if !deactivated {
		return errs.NotFound("medicine barcode")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Price history
// ---------------------------------------------------------------------------

// PriceHistory is deliberately not implemented.
//
// Price history belongs to the pricing module, and the Medicine endpoint is
// only a compatibility adapter towards an approved Pricing interface. No such
// interface exists yet, so the route answers with NOT_IMPLEMENTED instead of
// serving the medicine_price_history table directly (contract §1.12, §8).
//
// Serving the table from here would create a second, competing source of
// price history, which is exactly what the contract forbids.
func (s *Service) PriceHistory(ctx context.Context, id uuid.UUID) (retErr error) {
	defer s.observe("price_history", retErr)
	return errs.New(errs.CodeNotImplemented,
		"medicine price history is owned by the pricing module and no pricing interface is available yet")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// organizationID resolves the tenant from the authenticated context.
//
// It is the only source of tenant scope in this module: a medicine's tenant is
// the organization of its brand, and that organization comes from the trusted
// request context, never from a request body (contract §3).
func organizationID(ctx context.Context) (uuid.UUID, error) {
	id := appctx.OrgID(ctx)
	if id == uuid.Nil {
		return uuid.Nil, errs.Unauthenticated()
	}
	return id, nil
}

// requireMedicine checks that the medicine exists in the caller's organization.
func (s *Service) requireMedicine(ctx context.Context, orgID, medicineID uuid.UUID) error {
	if medicineID == uuid.Nil {
		return errs.Validation("medicine id is required")
	}
	if _, err := s.repo.FindByID(ctx, orgID, medicineID); err != nil {
		return mapMedicineReadError(err)
	}
	return nil
}

// validateBrand checks that the brand exists, is active and belongs to the
// caller's organization.
//
// The database trigger (migration 000024) enforces the same rule at write
// time; checking here turns a race into a precise error.
func (s *Service) validateBrand(ctx context.Context, orgID, brandID uuid.UUID) error {
	if brandID == uuid.Nil {
		return errs.Validation("brand_id is required")
	}
	usable, err := s.repo.BrandIsUsable(ctx, orgID, brandID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !usable {
		return errs.NotFound("brand")
	}
	return nil
}

// medicineReferences collects the dictionary references of a medicine.
func medicineReferences(m *Medicine) map[string]*uuid.UUID {
	return map[string]*uuid.UUID{
		"product_categories": &m.CategoryID,
		"generic_medicines":  m.GenericMedicineID,
		"dosage_forms":       m.DosageFormID,
		"routes":             m.RouteID,
		"storage_conditions": m.StorageConditionID,
		"package_types":      m.PackageTypeID,
		"unit_types":         m.SalesUnitID,
		"tax_rates":          m.TaxRateID,
	}
}

// validateReferences checks that every dictionary reference points at an
// existing, active row.
//
// The dictionary tables are global, so "usable" means "exists, not deleted and
// active" — not "belongs to my organization" (contract §4, §7).
func (s *Service) validateReferences(ctx context.Context, refs map[string]*uuid.UUID) error {
	for table, id := range refs {
		if id == nil || *id == uuid.Nil {
			continue
		}
		usable, err := s.repo.ReferenceIsUsable(ctx, table, *id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !usable {
			return errs.NotFound(referenceResource(table))
		}
	}
	return nil
}

// validateManufacturer checks the manufacturer reference and rejects a value
// that contradicts the brand.
//
// The medicine carries a denormalized manufacturer_id while the brand already
// references one. Two disagreeing sources of truth about who makes a product
// are worse than one, so a contradiction is refused (contract §17).
func (s *Service) validateManufacturer(ctx context.Context, orgID uuid.UUID, m *Medicine) error {
	if m.ManufacturerID == nil || *m.ManufacturerID == uuid.Nil {
		return nil
	}
	usable, err := s.repo.ManufacturerIsUsable(ctx, orgID, *m.ManufacturerID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !usable {
		return errs.NotFound("manufacturer")
	}

	brandManufacturer, err := s.repo.BrandManufacturerID(ctx, orgID, m.BrandID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if brandManufacturer != nil && *brandManufacturer != *m.ManufacturerID {
		return errs.Validation("manufacturer_id must match the manufacturer of the selected brand")
	}
	return nil
}

// ensureSKUFree reports a conflict when the SKU is already in use.
func (s *Service) ensureSKUFree(ctx context.Context, orgID uuid.UUID, sku string) error {
	existing, err := s.repo.FindBySKU(ctx, orgID, sku)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		return errs.DatabaseError(err)
	}
	if existing != nil {
		return errs.Conflict("a medicine with this SKU already exists")
	}
	return nil
}

// ensureBarcodeFree reports a conflict when the barcode is already in use.
func (s *Service) ensureBarcodeFree(ctx context.Context, orgID uuid.UUID, barcode string) error {
	existing, err := s.repo.FindByBarcode(ctx, orgID, barcode)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		return errs.DatabaseError(err)
	}
	if existing != nil {
		return errs.Conflict("a medicine with this barcode already exists")
	}
	return nil
}

// ensureVariantSKUFree reports a conflict when the variant SKU is already in
// use.
//
// The check is tenant-scoped on purpose: it must not reveal whether another
// organization owns a SKU, while the database constraint still prevents the
// duplicate. The taken set carries the SKUs of the variants that are created
// within the same request, so two inline variants cannot claim one SKU.
func (s *Service) ensureVariantSKUFree(ctx context.Context, orgID uuid.UUID, sku string, taken map[string]struct{}) error {
	if taken != nil {
		if _, ok := taken[sku]; ok {
			return errs.Conflict("a variant with this SKU already exists")
		}
		taken[sku] = struct{}{}
	}
	existing, err := s.repo.FindVariantBySKU(ctx, orgID, sku)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		return errs.DatabaseError(err)
	}
	if existing != nil {
		return errs.Conflict("a variant with this SKU already exists")
	}
	return nil
}

// requireReasonForRegulatoryChange refuses a regulatory or import-provenance
// change that carries no reason.
//
// The flat flags are the documented single-jurisdiction simplification of a
// regulatory profile, so changing one is an audited event with a stated
// justification (contract §5.4, §11).
func (s *Service) requireReasonForRegulatoryChange(m *Medicine, req *UpdateMedicineRequest) error {
	changed := false
	switch {
	case req.RequiresPrescription != nil && *req.RequiresPrescription != m.RequiresPrescription:
		changed = true
	case req.IsControlled != nil && *req.IsControlled != m.IsControlled:
		changed = true
	case req.IsRefrigerated != nil && *req.IsRefrigerated != m.IsRefrigerated:
		changed = true
	case req.IsImported != nil && *req.IsImported != m.IsImported:
		changed = true
	case req.CountryOfOrigin != nil && !sameString(req.CountryOfOrigin, m.CountryOfOrigin):
		changed = true
	case req.ImporterName != nil && !sameString(req.ImporterName, m.ImporterName):
		changed = true
	case req.ImportLicenseNo != nil && !sameString(req.ImportLicenseNo, m.ImportLicenseNo):
		changed = true
	}
	if !changed {
		return nil
	}
	if req.Reason == nil || strings.TrimSpace(*req.Reason) == "" {
		return errs.Validation("a reason is required when a regulatory or import provenance field changes",
			errs.FieldError{Field: "reason", Rule: "required", Message: "must be provided for regulatory changes"})
	}
	return nil
}

// requireUnchangedSemantics refuses a change to a semantic field once the
// product has been referenced by operational records.
func (s *Service) requireUnchangedSemantics(ctx context.Context, m *Medicine, req *UpdateMedicineRequest) error {
	if !semanticChangeRequested(m, req) {
		return nil
	}
	referenced, err := s.repo.MedicineIsReferenced(ctx, m.ID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !referenced {
		return nil
	}
	return errs.Conflict(
		"strength, pack size, units per pack and the generic reference cannot be changed once the medicine is referenced by stock, purchases or sales")
}

// semanticChangeRequested reports whether the request touches a field that
// carries the product's meaning.
func semanticChangeRequested(m *Medicine, req *UpdateMedicineRequest) bool {
	switch {
	case req.Strength != nil && !sameString(req.Strength, m.Strength):
		return true
	case req.PackSize != nil && !sameString(req.PackSize, m.PackSize):
		return true
	case req.UnitsPerPack != nil && !sameInt(req.UnitsPerPack, m.UnitsPerPack):
		return true
	case req.UnitsPerStrip != nil && !sameInt(req.UnitsPerStrip, m.UnitsPerStrip):
		return true
	case req.GenericMedicineID != nil && !sameUUID(req.GenericMedicineID, m.GenericMedicineID):
		return true
	}
	return false
}

// requireUnchangedVariantConversion refuses a change to the package
// conversion once the variant has been referenced operationally.
func (s *Service) requireUnchangedVariantConversion(ctx context.Context, v *MedicineVariant, req *UpdateMedicineVariantRequest) error {
	changes := false
	switch {
	case req.Strength != nil && !sameString(req.Strength, v.Strength):
		changes = true
	case req.PackSize != nil && !sameString(req.PackSize, v.PackSize):
		changes = true
	case req.UnitsPerPack != nil && !sameInt(req.UnitsPerPack, v.UnitsPerPack):
		changes = true
	}
	if !changes {
		return nil
	}
	referenced, err := s.repo.VariantIsReferenced(ctx, v.ID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !referenced {
		return nil
	}
	return errs.Conflict(
		"the package conversion cannot be changed once the variant is referenced by stock, purchases or sales; create a new variant instead")
}

// applyMedicinePatch copies the requested changes onto the model.
//
// Every field is a pointer, so a nil pointer means "leave unchanged" and an
// explicit false is a real change. sku, barcode and brand_id are absent from
// the request DTO, which is what makes them write-once (contract §5.3).
func applyMedicinePatch(m *Medicine, req *UpdateMedicineRequest) {
	if req.Name != nil {
		m.Name = strings.TrimSpace(*req.Name)
	}
	if req.DisplayName != nil {
		m.DisplayName = trimOptional(req.DisplayName)
	}
	if req.Description != nil {
		m.Description = trimOptional(req.Description)
	}
	if req.GenericMedicineID != nil {
		m.GenericMedicineID = req.GenericMedicineID
	}
	if req.CategoryID != nil {
		m.CategoryID = *req.CategoryID
	}
	if req.DosageFormID != nil {
		m.DosageFormID = req.DosageFormID
	}
	if req.RouteID != nil {
		m.RouteID = req.RouteID
	}
	if req.ManufacturerID != nil {
		m.ManufacturerID = req.ManufacturerID
	}
	if req.StorageConditionID != nil {
		m.StorageConditionID = req.StorageConditionID
	}
	if req.Strength != nil {
		m.Strength = trimOptional(req.Strength)
	}
	if req.PackSize != nil {
		m.PackSize = trimOptional(req.PackSize)
	}
	if req.UnitsPerPack != nil {
		m.UnitsPerPack = req.UnitsPerPack
	}
	if req.UnitsPerStrip != nil {
		m.UnitsPerStrip = req.UnitsPerStrip
	}
	if req.PackageTypeID != nil {
		m.PackageTypeID = req.PackageTypeID
	}
	if req.SalesUnitID != nil {
		m.SalesUnitID = req.SalesUnitID
	}
	if req.RequiresPrescription != nil {
		m.RequiresPrescription = *req.RequiresPrescription
	}
	if req.IsControlled != nil {
		m.IsControlled = *req.IsControlled
	}
	if req.IsRefrigerated != nil {
		m.IsRefrigerated = *req.IsRefrigerated
	}
	if req.IsImported != nil {
		m.IsImported = *req.IsImported
	}
	if req.CountryOfOrigin != nil {
		m.CountryOfOrigin = trimOptional(req.CountryOfOrigin)
	}
	if req.ImporterName != nil {
		m.ImporterName = trimOptional(req.ImporterName)
	}
	if req.ImportLicenseNo != nil {
		m.ImportLicenseNo = trimOptional(req.ImportLicenseNo)
	}
	if req.TaxRateID != nil {
		m.TaxRateID = req.TaxRateID
	}
	if req.HSNCode != nil {
		m.HSNCode = trimOptional(req.HSNCode)
	}
	if req.ShelfLifeMonths != nil {
		m.ShelfLifeMonths = req.ShelfLifeMonths
	}
	if req.ImageURL != nil {
		m.ImageURL = trimOptional(req.ImageURL)
	}
	if req.IsActive != nil {
		m.IsActive = *req.IsActive
	}
}

// validateMedicineText enforces the schema widths on the values the service
// writes.
//
// The struct tags already check the request, but the service is also reachable
// from other callers, so the widths are checked again where the write
// happens (contract §9).
func validateMedicineText(m *Medicine) error {
	switch {
	case utf8.RuneCountInString(m.SKU) > maxSKULen:
		return errs.Validation("medicine sku is too long")
	case m.Barcode != nil && utf8.RuneCountInString(*m.Barcode) > maxBarcodeLen:
		return errs.Validation("medicine barcode is too long")
	case utf8.RuneCountInString(m.Name) > maxNameLen:
		return errs.Validation("medicine name is too long")
	case m.DisplayName != nil && utf8.RuneCountInString(*m.DisplayName) > maxDisplayNameLen:
		return errs.Validation("medicine display name is too long")
	case m.Description != nil && utf8.RuneCountInString(*m.Description) > maxDescriptionLen:
		return errs.Validation("medicine description is too long")
	case m.Strength != nil && utf8.RuneCountInString(*m.Strength) > maxStrengthLen:
		return errs.Validation("medicine strength is too long")
	case m.PackSize != nil && utf8.RuneCountInString(*m.PackSize) > maxPackSizeLen:
		return errs.Validation("medicine pack size is too long")
	case m.CountryOfOrigin != nil && utf8.RuneCountInString(*m.CountryOfOrigin) > maxCountryOfOriginLen:
		return errs.Validation("medicine country of origin is too long")
	case m.ImporterName != nil && utf8.RuneCountInString(*m.ImporterName) > maxImporterNameLen:
		return errs.Validation("medicine importer name is too long")
	case m.ImportLicenseNo != nil && utf8.RuneCountInString(*m.ImportLicenseNo) > maxImportLicenseNoLen:
		return errs.Validation("medicine import licence number is too long")
	case m.HSNCode != nil && utf8.RuneCountInString(*m.HSNCode) > maxHSNCodeLen:
		return errs.Validation("medicine hsn code is too long")
	case m.ImageURL != nil && utf8.RuneCountInString(*m.ImageURL) > maxImageURLLen:
		return errs.Validation("medicine image url is too long")
	}
	return nil
}

// referenceResource maps a dictionary table to the resource name used in the
// not-found message.
func referenceResource(table string) string {
	switch table {
	case "product_categories":
		return "product category"
	case "generic_medicines":
		return "generic medicine"
	case "dosage_forms":
		return "dosage form"
	case "routes":
		return "route"
	case "storage_conditions":
		return "storage condition"
	case "package_types":
		return "package type"
	case "unit_types":
		return "unit type"
	case "tax_rates":
		return "tax rate"
	default:
		return table
	}
}

// validationError turns the field-level problems of this module into the
// shared validation error.
func validationError(message string, problems []error) error {
	details := make([]errs.FieldError, 0, len(problems))
	for _, problem := range problems {
		var fieldErr *FieldError
		if errors.As(problem, &fieldErr) {
			details = append(details, errs.FieldError{
				Field:   fieldErr.Field,
				Rule:    fieldErr.Rule,
				Message: fieldErr.Message,
			})
			continue
		}
		details = append(details, errs.FieldError{Message: problem.Error()})
	}
	return errs.Validation(message, details...)
}

// mapMedicineReadError translates a repository read error.
func mapMedicineReadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("medicine")
	case errors.Is(err, ErrInvalidCursor):
		return errs.New(errs.CodeInvalidCursor, "the cursor is malformed or was issued for a different query")
	case errors.Is(err, ErrInvalidRequest):
		return errs.Validation(err.Error())
	default:
		return errs.DatabaseError(err)
	}
}

// mapMedicineWriteError translates a repository write error.
//
// A unique violation is a conflict (a duplicate SKU or barcode), a foreign-key
// violation is a missing reference, and the brand trigger's own constraint
// means the brand was deactivated in between the check and the write
// (contract §10, "Database constraints are the final defense").
func mapMedicineWriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("medicine")
	case db.IsUniqueVilation(err):
		switch db.ConstraintName(err) {
		case "ux_medicine_variants_sku":
			return errs.Conflict("a variant with this SKU already exists")
		case "ux_medicine_variants_barcode":
			return errs.Conflict("a variant with this barcode already exists")
		case "ux_medicines_barcode":
			return errs.Conflict("a medicine with this barcode already exists")
		default:
			return errs.Conflict("a medicine with this SKU or barcode already exists")
		}
	case db.IsForeignKeyViolation(err):
		if db.ConstraintName(err) == "medicines_brand_active_fkey" {
			return errs.Conflict("the brand of this medicine must be active")
		}
		return errs.Validation("a referenced record does not exist")
	case errors.Is(err, ErrInvalidRequest):
		return errs.Validation(err.Error())
	default:
		return errs.DatabaseError(err)
	}
}

// mapLookupError translates a lookup error.
//
// An ambiguous identifier is an integrity error, not a not-found: the caller
// has to resolve the conflict instead of selling the wrong product
// (contract §5.3).
func mapLookupError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("medicine")
	case errors.Is(err, ErrAmbiguousIdentifier):
		return errs.Conflict("the identifier matches more than one active product")
	case errors.Is(err, ErrInvalidRequest):
		return errs.Validation(err.Error())
	default:
		return errs.DatabaseError(err)
	}
}

// medicineIDs collects the ids of a page for the batch label lookup.
func medicineIDs(items []Medicine) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	return ids
}

// trimOptional trims a string pointer and turns a blank value into nil.
func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// derefBool reads a boolean pointer, defaulting to def.
func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// sameString compares two optional strings by value.
func sameString(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameInt compares two optional integers by value.
func sameInt(a *int, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameUUID compares two optional identifiers by value.
func sameUUID(a *uuid.UUID, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// observe records one medicine catalogue operation as a Prometheus counter
// labelled by operation and outcome. It is a no-op when metrics is nil.
//
// The outcome bucket mirrors the audit module: 4xx client errors split into
// invalid (400/422), denied (401/403) and not_found (404); everything else is
// "error", and a nil error is "success".
func (s *Service) observe(operation string, err error) {
	if s == nil || s.metrics == nil {
		return
	}
	outcome := "success"
	if err != nil {
		switch status := errs.HTTPStatus(err); status {
		case 400, 422:
			outcome = "invalid"
		case 401, 403:
			outcome = "denied"
		case 404:
			outcome = "not_found"
		case 409:
			outcome = "conflict"
		default:
			outcome = "error"
		}
	}
	s.metrics.ObserveMedicine(operation, outcome)
}
