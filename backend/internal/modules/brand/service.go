package brand

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Service holds brand business logic.
type Service struct {
	repo *Repository
	db   *db.DB
}

// NewService assembles the service.
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

// Create inserts a brand.
func (s *Service) Create(ctx context.Context, req *CreateBrandRequest) (*Brand, error) {
	if req == nil {
		return nil, errs.Validation("brand request is required")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	b := &Brand{
		OrganizationID:  orgID,
		Code:            strings.TrimSpace(req.Code),
		Name:            strings.TrimSpace(req.Name),
		ManufacturerID:  req.ManufacturerID,
		CountryOfOrigin: trimOptional(req.CountryOfOrigin),
		Description:     trimOptional(req.Description),
		IsActive:        derefBool(req.IsActive, true),
	}
	if err := validateBrandFields(b.Code, b.Name, b.CountryOfOrigin); err != nil {
		return nil, err
	}
	var created *Brand
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		eligible, err := s.repo.ManufacturerIsEligible(ctx, orgID, b.ManufacturerID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !eligible {
			return errs.NotFound("manufacturer")
		}
		created, err = s.repo.Create(ctx, orgID, b)
		if err != nil {
			return mapWriteError(err, "brand code already exists")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// Get returns a brand by id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Brand, error) {
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.repo.FindByID(ctx, orgID, id)
	if err != nil {
		if err == db.ErrNoRows {
			return nil, errs.NotFound("brand")
		}
		return nil, errs.DatabaseError(err)
	}
	return b, nil
}

// List returns a page of brands.
func (s *Service) List(ctx context.Context, q ListBrandsQuery, o pagination.Offset) ([]Brand, pagination.Meta, error) {
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	sortFields, err := pagination.ParseSort(q.Sort, brandSortFields)
	if err != nil {
		return nil, pagination.Meta{}, errs.Validation("invalid brand sort")
	}
	if utf8.RuneCountInString(q.Q) > maxSearchLen {
		return nil, pagination.Meta{}, errs.Validation("brand search query is too long")
	}
	items, total, err := s.repo.List(ctx, orgID, q, o, sortFields)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	o.Normalize()
	return items, pagination.BuildOffsetMeta(o, total), nil
}

// Update applies a partial update.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateBrandRequest) (*Brand, error) {
	if req == nil {
		return nil, errs.Validation("brand update request is required")
	}
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, err
	}

	var updated *Brand
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		b, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadError(err)
		}
		if req.Name != nil {
			b.Name = strings.TrimSpace(*req.Name)
		}
		if req.CountryOfOrigin.Set {
			b.CountryOfOrigin = trimOptional(req.CountryOfOrigin.Value)
		}
		if req.Description.Set {
			b.Description = trimOptional(req.Description.Value)
		}
		if req.IsActive != nil {
			b.IsActive = *req.IsActive
		}
		if req.ManufacturerID != nil && *req.ManufacturerID != b.ManufacturerID {
			referenced, err := s.repo.IsReferencedByMedicine(ctx, orgID, b.ID)
			if err != nil {
				return errs.DatabaseError(err)
			}
			if referenced {
				return errs.Conflict("manufacturer cannot be changed while medicines reference this brand")
			}
			eligible, err := s.repo.ManufacturerIsEligible(ctx, orgID, *req.ManufacturerID)
			if err != nil {
				return errs.DatabaseError(err)
			}
			if !eligible {
				return errs.NotFound("manufacturer")
			}
			b.ManufacturerID = *req.ManufacturerID
		}
		if err := validateBrandFields(b.Code, b.Name, b.CountryOfOrigin); err != nil {
			return err
		}

		updated, err = s.repo.Update(ctx, orgID, b)
		if err != nil {
			return mapWriteError(err, "brand code already exists")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Delete soft-deletes a brand unless referenced by medicines.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	orgID, err := organizationID(ctx)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		b, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadError(err)
		}
		referenced, err := s.repo.IsReferencedByMedicine(ctx, orgID, b.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if referenced {
			return errs.Conflict("brand is referenced by medicines; deactivate it instead")
		}
		deleted, err := s.repo.SoftDelete(ctx, orgID, b.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !deleted {
			return errs.NotFound("brand")
		}
		return nil
	})
}

func organizationID(ctx context.Context) (uuid.UUID, error) {
	id := appctx.OrgID(ctx)
	if id == uuid.Nil {
		return uuid.Nil, errs.Unauthenticated()
	}
	return id, nil
}

func mapReadError(err error) error {
	if err == db.ErrNoRows {
		return errs.NotFound("brand")
	}
	return errs.DatabaseError(err)
}

func mapWriteError(err error, duplicateMessage string) error {
	if db.IsUniqueVilation(err) {
		return errs.Conflict(duplicateMessage)
	}
	if db.IsForeignKeyViolation(err) {
		return errs.NotFound("manufacturer")
	}
	return errs.DatabaseError(err)
}

func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func validateBrandFields(code, name string, country *string) error {
	switch {
	case code == "":
		return errs.Validation("brand code must not be blank")
	case utf8.RuneCountInString(code) > maxCodeLen:
		return errs.Validation("brand code is too long")
	case name == "":
		return errs.Validation("brand name must not be blank")
	case utf8.RuneCountInString(name) > maxNameLen:
		return errs.Validation("brand name is too long")
	case country != nil && utf8.RuneCountInString(*country) > maxCountryLen:
		return errs.Validation("country of origin is too long")
	default:
		return nil
	}
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
