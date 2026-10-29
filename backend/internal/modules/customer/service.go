package customer

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Service holds the customer module's business logic: tenant binding,
// branch-visibility enforcement, and mapping of raw driver errors to domain errors.
type Service struct {
	repo *Repository
	db   *db.DB
}

// NewService assembles the service.
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

func mapReadErr(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return errs.NotFound("customer")
	}
	return errs.DatabaseError(err)
}

func mapWriteErr(err error) error {
	if errors.Is(err, ErrDuplicateCode) {
		return errs.Conflict("customer code already exists")
	}
	if errors.Is(err, ErrConflict) {
		return errs.Conflict("customer was modified by another request")
	}
	if err != nil && strings.Contains(err.Error(), "23505") {
		if strings.Contains(err.Error(), "code") {
			return errs.Conflict("customer code already exists")
		}
		return errs.AlreadyExists("customer")
	}
	return errs.DatabaseError(err)
}

// generateCode generates a simple customer code (e.g., CUST-XXXXXX).
// For production, should use a sequence/table-based generator as per contract.
func generateCode() string {
	// Simple implementation; in practice use DB sequence
	return "CUST-" + strings.ToUpper(uuid.New().String()[:8])
}

// Create creates a customer with initial branch visibility.
func (s *Service) Create(ctx context.Context, req *CreateCustomerRequest) (*Customer, error) {
	orgID := appctx.OrgID(ctx)
	actorID := appctx.UserID(ctx)

	c := &Customer{
		OrganizationID: orgID,
		Name:           strings.TrimSpace(req.Name),
		Phone:          req.Phone,
		Email:          req.Email,
		Address:        req.Address,
		City:           req.City,
		DateOfBirth:    req.DateOfBirth,
		Gender:         req.Gender,
		BloodGroup:     req.BloodGroup,
		LoyaltyPoints:  0,
		CreditLimit:    0,
		CurrentDue:     0,
		Notes:          req.Notes,
		IsActive:       true,
	}

	// Generate code
	code := generateCode()
	c.Code = &code

	var out *Customer
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		created, err := s.repo.Insert(ctx, c)
		if err != nil {
			return mapWriteErr(err)
		}
		if err := s.repo.AddBranchVisibility(ctx, orgID, created.ID, req.BranchIDs, &actorID); err != nil {
			return errs.DatabaseError(err)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns a customer if visible to caller's branch set.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Customer, error) {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return nil, errs.Forbidden("no branch access")
	}

	c, err := s.repo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, mapReadErr(err)
	}

	visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if !visible {
		return nil, errs.NotFound("customer")
	}

	return c, nil
}

// List returns customers visible to caller's branch set.
func (s *Service) List(ctx context.Context, q *ListCustomersQuery) ([]*Customer, pagination.Meta, error) {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return []*Customer{}, pagination.Meta{}, nil
	}

	q.Offset.Normalize()
	customers, total, err := s.repo.List(ctx, orgID, branchIDs, q.Q, q.IsActive, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return customers, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// Update updates allow-listed fields if visible.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateCustomerRequest) (*Customer, error) {
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return nil, errs.Forbidden("no branch access")
	}

	var out *Customer
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		c, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !visible {
			return errs.NotFound("customer")
		}

		if req.Name != nil {
			c.Name = strings.TrimSpace(*req.Name)
		}
		if req.Phone != nil {
			c.Phone = req.Phone
		}
		if req.Email != nil {
			c.Email = req.Email
		}
		if req.Address != nil {
			c.Address = req.Address
		}
		if req.City != nil {
			c.City = req.City
		}
		if req.DateOfBirth != nil {
			c.DateOfBirth = req.DateOfBirth
		}
		if req.Gender != nil {
			c.Gender = req.Gender
		}
		if req.BloodGroup != nil {
			c.BloodGroup = req.BloodGroup
		}
		if req.Notes != nil {
			c.Notes = req.Notes
		}

		saved, err := s.repo.Update(ctx, orgID, c)
		if err != nil {
			return mapWriteErr(err)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Archive soft-deletes a customer.
func (s *Service) Archive(ctx context.Context, id uuid.UUID, req *ArchiveRequest) error {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return errs.Forbidden("no branch access")
	}

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !visible {
			return errs.NotFound("customer")
		}

		ok, err := s.repo.SoftDelete(ctx, orgID, id)
		if err != nil {
			return mapWriteErr(err)
		}
		if !ok {
			return errs.NotFound("customer")
		}
		return nil
	})
}

// AddConsent appends consent evidence.
func (s *Service) AddConsent(ctx context.Context, id uuid.UUID, req *ConsentRequest) (*ConsentItem, error) {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	actorID := appctx.UserID(ctx)
	if len(branchIDs) == 0 {
		return nil, errs.Forbidden("no branch access")
	}

	var out *ConsentItem
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !visible {
			return errs.NotFound("customer")
		}

		item, err := s.repo.InsertConsent(ctx, orgID, id, req.Purpose, req.Channel, req.Decision, &actorID, req.Evidence, req.ExpiresAt)
		if err != nil {
			return errs.DatabaseError(err)
		}
		out = item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListConsents returns consent history.
func (s *Service) ListConsents(ctx context.Context, id uuid.UUID) ([]*ConsentItem, error) {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return nil, errs.Forbidden("no branch access")
	}

	if _, err := s.repo.FindByID(ctx, orgID, id); err != nil {
		return nil, mapReadErr(err)
	}
	visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if !visible {
		return nil, errs.NotFound("customer")
	}

	items, err := s.repo.ListConsents(ctx, orgID, id, 50)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	return items, nil
}

// GetBranchVisibility returns branch visibility.
func (s *Service) GetBranchVisibility(ctx context.Context, id uuid.UUID) ([]BranchVisibilityItem, error) {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return nil, errs.Forbidden("no branch access")
	}

	if _, err := s.repo.FindByID(ctx, orgID, id); err != nil {
		return nil, mapReadErr(err)
	}
	visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if !visible {
		return nil, errs.NotFound("customer")
	}

	bids, err := s.repo.GetBranchVisibility(ctx, orgID, id)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}

	items := make([]BranchVisibilityItem, 0, len(bids))
	for _, bid := range bids {
		items = append(items, BranchVisibilityItem{BranchID: bid})
	}
	return items, nil
}

// GrantBranchVisibility grants visibility to branches.
func (s *Service) GrantBranchVisibility(ctx context.Context, id uuid.UUID, req *GrantBranchVisibilityRequest) error {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	actorID := appctx.UserID(ctx)
	if len(branchIDs) == 0 {
		return errs.Forbidden("no branch access")
	}

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		// Caller must have visibility to the customer to grant visibility
		visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !visible {
			return errs.NotFound("customer")
		}

		if err := s.repo.AddBranchVisibility(ctx, orgID, id, req.BranchIDs, &actorID); err != nil {
			return errs.DatabaseError(err)
		}
		return nil
	})
}

// RevokeBranchVisibility revokes visibility.
func (s *Service) RevokeBranchVisibility(ctx context.Context, id uuid.UUID, req *RevokeBranchVisibilityRequest) error {
	orgID := appctx.OrgID(ctx)
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) == 0 {
		return errs.Forbidden("no branch access")
	}

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		visible, err := s.repo.HasBranchVisibility(ctx, orgID, id, branchIDs)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !visible {
			return errs.NotFound("customer")
		}

		if err := s.repo.RemoveBranchVisibility(ctx, orgID, id, req.BranchIDs); err != nil {
			return errs.DatabaseError(err)
		}
		return nil
	})
}
