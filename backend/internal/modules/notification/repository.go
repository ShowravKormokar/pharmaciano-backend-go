package notification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// ErrNotImplemented is returned by repository methods whose backing tables
// have no applied migration yet (notification Phase 0/1 schema). Callers
// should treat this as "schema not ready", not as a transient failure.
var ErrNotImplemented = errors.New("notification: repository method not implemented (migrations not applied)")

type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

func (r *Repository) Ping(ctx context.Context) error {
	return nil
}

func (r *Repository) List(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, branchIDs []uuid.UUID, f InboxFilter) ([]NotificationDTO, int64, bool, error) {
	return nil, 0, false, ErrNotImplemented
}

func (r *Repository) UnreadCount(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (UnreadCountDTO, error) {
	return UnreadCountDTO{}, ErrNotImplemented
}

func (r *Repository) MarkRead(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	return ErrNotImplemented
}

func (r *Repository) MarkAllRead(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (int64, time.Time, error) {
	return 0, time.Time{}, ErrNotImplemented
}

func (r *Repository) Dismiss(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	return ErrNotImplemented
}

func (r *Repository) CreateBroadcast(ctx context.Context, orgID uuid.UUID, creatorID uuid.UUID, req BroadcastRequest) (*NotificationBroadcast, error) {
	return nil, ErrNotImplemented
}
