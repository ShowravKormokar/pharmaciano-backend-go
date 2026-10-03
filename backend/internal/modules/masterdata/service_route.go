package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateRoute inserts a route.
//
// A route has no foreign key and no cross-field rule, so this is a single INSERT
// and deliberately runs WITHOUT a transaction: wrapping one statement in
// db.WithTx buys a BEGIN/COMMIT round trip and nothing else (contract §15).
//
// IsActive is resolved here because CreateRoute writes every non-generated column,
// so an unresolved nil would store the model's zero value — false — instead of the
// column default TRUE (§28.5).
func (s *Service) CreateRoute(ctx context.Context, req *CreateRouteRequest) (*Route, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	r := &Route{
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	created, err := s.repo.CreateRoute(ctx, r)
	if err != nil {
		return nil, mapWriteErr(err, resRoute)
	}
	return created, nil
}

// GetRoute returns one live route, or NotFound.
func (s *Service) GetRoute(ctx context.Context, id uuid.UUID) (*Route, error) {
	r, err := s.repo.GetRoute(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resRoute)
	}
	return r, nil
}

// ListRoutes returns a filtered, paginated page of routes plus meta.
//
// routes has no parent column, so ?parent_id= is accepted by the shared query type
// and ignored by the repository rather than retargeted onto an unrelated column.
func (s *Service) ListRoutes(ctx context.Context, q *ListMasterDataQuery) ([]Route, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListRoutes)
}

// UpdateRoute applies a partial change.
//
// An empty PATCH degrades to a read, matching warehouse/service.go:128-130. The
// read-modify-write runs under a row lock inside a transaction so a concurrent
// write cannot be lost. code is immutable and is absent from the Update DTO, so it
// cannot be patched — renaming it would silently orphan every row and seed file
// that refers to a route by code.
func (s *Service) UpdateRoute(ctx context.Context, id uuid.UUID, req *UpdateRouteRequest) (*Route, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetRoute(ctx, id)
	}

	var out *Route
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetRouteForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resRoute)
		}
		applyRoutePatch(current, req)
		saved, err := s.repo.UpdateRoute(ctx, current)
		if err != nil {
			return mapWriteErr(err, resRoute)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteRoute soft-deletes a route.
//
// Nothing points at a route other than medicines.route_id, which is ON DELETE SET
// NULL — but a soft delete never fires a foreign key, so the D-19 probe is still
// consulted: a route that stocked medicines sell in is refused and the caller is
// pointed at is_active=false instead.
func (s *Service) DeleteRoute(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetRouteForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resRoute)
		}
		if err := checkUnreferenced(ctx, resRoute, id, s.repo.IsRouteReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteRoute(ctx, id)
		if err != nil {
			return mapWriteErr(err, resRoute)
		}
		if !ok {
			return errs.NotFound(resRoute)
		}
		return nil
	})
}

// applyRoutePatch copies the non-nil fields of the PATCH onto r. A nil pointer
// leaves the column untouched; a non-nil pointer overwrites it, including a pointer
// to an empty string.
func applyRoutePatch(r *Route, req *UpdateRouteRequest) {
	if req.Name != nil {
		r.Name = *req.Name
	}
	if req.Description != nil {
		r.Description = req.Description
	}
	if req.IsActive != nil {
		r.IsActive = *req.IsActive
	}
}
