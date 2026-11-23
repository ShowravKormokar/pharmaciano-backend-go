package notification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// Service owns notification read/dismiss/broadcast business rules. It never
// mutates Inventory, Purchase, Sale, or Ledger tables.
type Service struct {
	repo *Repository
	pub  *publisher // optional realtime hint; nil disables Phase 4 push
}

func NewService(repo *Repository, pub *publisher) *Service {
	return &Service{repo: repo, pub: pub}
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, branchIDs []uuid.UUID, f InboxFilter) ([]NotificationDTO, int64, bool, error) {
	if s == nil || s.repo == nil {
		return nil, 0, false, errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil {
		return nil, 0, false, errs.Validation("organization is required")
	}
	if userID == uuid.Nil {
		return nil, 0, false, errs.Validation("user is required")
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	return s.repo.List(ctx, orgID, userID, branchIDs, f)
}

func (s *Service) UnreadCount(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (UnreadCountDTO, error) {
	if s == nil || s.repo == nil {
		return UnreadCountDTO{}, errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil || userID == uuid.Nil {
		return UnreadCountDTO{}, errs.Validation("organization and user are required")
	}
	return s.repo.UnreadCount(ctx, orgID, userID)
}

func (s *Service) Read(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	if s == nil || s.repo == nil {
		return errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil || userID == uuid.Nil || notificationID == uuid.Nil {
		return errs.Validation("organization, user and notification id are required")
	}
	if err := s.repo.MarkRead(ctx, orgID, userID, notificationID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return errs.NotFound("notification")
		}
		return errs.DatabaseError(err)
	}
	return nil
}

func (s *Service) ReadAll(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (ReadAllResponse, error) {
	if s == nil || s.repo == nil {
		return ReadAllResponse{}, errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil || userID == uuid.Nil {
		return ReadAllResponse{}, errs.Validation("organization and user are required")
	}
	affected, updatedAt, err := s.repo.MarkAllRead(ctx, orgID, userID)
	if err != nil {
		return ReadAllResponse{}, errs.DatabaseError(err)
	}
	return ReadAllResponse{Affected: affected, UpdatedAt: updatedAt}, nil
}

func (s *Service) Dismiss(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	if s == nil || s.repo == nil {
		return errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil || userID == uuid.Nil || notificationID == uuid.Nil {
		return errs.Validation("organization, user and notification id are required")
	}
	if err := s.repo.Dismiss(ctx, orgID, userID, notificationID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return errs.NotFound("notification")
		}
		return errs.DatabaseError(err)
	}
	return nil
}

func (s *Service) CreateBroadcast(ctx context.Context, orgID uuid.UUID, creatorID uuid.UUID, req BroadcastRequest) (BroadcastResponse, error) {
	if s == nil || s.repo == nil {
		return BroadcastResponse{}, errs.Internal(fmt.Errorf("notification repository is not configured"))
	}
	if orgID == uuid.Nil || creatorID == uuid.Nil {
		return BroadcastResponse{}, errs.Validation("organization and creator are required")
	}
	if !req.Scope.Valid() {
		return BroadcastResponse{}, errs.Validation("scope must be org, branch, role or user")
	}
	switch req.Scope {
	case BroadcastScopeOrg:
		if req.TargetID != nil {
			return BroadcastResponse{}, errs.Validation("target_id must be null for org scope")
		}
	case BroadcastScopeBranch, BroadcastScopeRole, BroadcastScopeUser:
		if req.TargetID == nil || *req.TargetID == uuid.Nil {
			return BroadcastResponse{}, errs.Validation("target_id is required for " + string(req.Scope) + " scope")
		}
	}
	if !req.Priority.Valid() {
		req.Priority = PriorityNormal
	}
	if req.Title == "" || req.Message == "" {
		return BroadcastResponse{}, errs.Validation("title and message are required")
	}
	broadcast, err := s.repo.CreateBroadcast(ctx, orgID, creatorID, req)
	if err != nil {
		return BroadcastResponse{}, errs.DatabaseError(err)
	}
	return BroadcastResponse{ID: broadcast.ID, Status: broadcast.Status}, nil
}

// publishHint fires a best-effort realtime ID hint after a commit. Failure is
// never fatal: polling recovers missed frames.
func (s *Service) publishHint(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	if s == nil || s.pub == nil {
		return nil
	}
	return s.pub.publishHint(ctx, notificationID.String())
}
