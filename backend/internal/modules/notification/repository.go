package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var ErrNotFound = errors.New("notification: not found or not owned")

type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.Ping(ctx)
}

func (r *Repository) List(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, branchIDs []uuid.UUID, f InboxFilter) ([]NotificationDTO, int64, bool, error) {
	if orgID == uuid.Nil || userID == uuid.Nil {
		return nil, 0, false, errs.Validation("organization and user are required")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{orgID, userID}
	where := []string{"r.organization_id = $1", "r.user_id = $2"}
	argN := 2

	if f.IsRead != nil {
		argN++
		args = append(args, *f.IsRead)
		where = append(where, "r.read_at IS NOT NULL = $"+strconv.Itoa(argN))
	}
	if f.Type != "" {
		argN++
		args = append(args, f.Type)
		where = append(where, "n.type = $"+strconv.Itoa(argN))
	}
	if f.Priority != "" {
		if !f.Priority.Valid() {
			return nil, 0, false, errs.Validation("priority must be low, normal, high or critical")
		}
		argN++
		args = append(args, string(f.Priority))
		where = append(where, "n.priority = $"+strconv.Itoa(argN))
	}
	if f.From != nil {
		argN++
		args = append(args, *f.From)
		where = append(where, "n.created_at >= $"+strconv.Itoa(argN))
	}
	if f.To != nil {
		argN++
		args = append(args, *f.To)
		where = append(where, "n.created_at < $"+strconv.Itoa(argN))
	}
	if len(branchIDs) > 0 {
		argN++
		args = append(args, branchIDs)
		where = append(where, "(n.branch_id IS NULL OR n.branch_id = ANY($"+strconv.Itoa(argN)+"::uuid[]))")
	} else {
		where = append(where, "n.branch_id IS NULL")
	}
	argN++
	args = append(args, time.Now().UTC())
	where = append(where, "(n.expires_at IS NULL OR n.expires_at > $"+strconv.Itoa(argN)+")")

	var cursor *pagination.Cursor
	if f.Cursor != "" {
		decoded, err := r.cursorCodec.Decode(f.Cursor)
		if err != nil || decoded.CreatedAt.IsZero() || decoded.ID == uuid.Nil {
			return nil, 0, false, errs.Validation("invalid pagination cursor")
		}
		cursor = &decoded
	}
	if cursor != nil {
		argN++
		args = append(args, cursor.CreatedAt)
		argN++
		args = append(args, cursor.ID)
		where = append(where, "(n.created_at, n.id) < ($"+strconv.Itoa(argN-1)+", $"+strconv.Itoa(argN)+")")
	}

	query := `
		SELECT n.id, n.type, n.priority, n.title, n.message, n.data, n.action_url,
		       n.branch_id, n.created_at, n.expires_at,
		       r.read_at, r.dismissed_at
		FROM notification_recipients AS r
		JOIN notifications AS n ON n.id = r.notification_id AND n.organization_id = r.organization_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY n.created_at DESC, n.id DESC
		LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit+1)

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, 0, false, errs.DatabaseError(err)
	}
	defer rows.Close()

	items := make([]NotificationDTO, 0, limit)
	for rows.Next() {
		var item NotificationDTO
		var branchID *uuid.UUID
		var readAt, dismissedAt *time.Time
		var data []byte
		var actionURL *string
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Priority, &item.Title, &item.Message,
			&data, &actionURL, &branchID, &item.CreatedAt, &item.ExpiresAt,
			&readAt, &dismissedAt,
		); err != nil {
			return nil, 0, false, errs.DatabaseError(err)
		}
		if len(items) == limit {
			return items, 0, true, nil
		}
		if branchID != nil {
			item.BranchID = branchID
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &item.Data); err != nil {
				return nil, 0, false, errs.DatabaseError(err)
			}
		}
		item.ActionURL = actionURL
		item.ReadAt = readAt
		item.DismissedAt = dismissedAt
		item.Priority = Priority(item.Priority)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, errs.DatabaseError(err)
	}

	hasMore := false
	if len(items) > 0 && len(items) == limit {
		hasMore = true
		items = items[:len(items)-1]
	}
	var total int64
	if !hasMore {
		total = int64(len(items))
	}

	return items, total, hasMore, nil
}

func (r *Repository) UnreadCount(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (UnreadCountDTO, error) {
	if orgID == uuid.Nil || userID == uuid.Nil {
		return UnreadCountDTO{}, errs.Validation("organization and user are required")
	}
	const query = `
		SELECT COUNT(*), COALESCE(MAX(n.created_at), MAX(r.created_at))
		FROM notification_recipients AS r
		JOIN notifications AS n ON n.id = r.notification_id AND n.organization_id = r.organization_id
		WHERE r.organization_id = $1 AND r.user_id = $2
		  AND r.read_at IS NULL AND r.dismissed_at IS NULL
		  AND (n.expires_at IS NULL OR n.expires_at > $3)`
	var count int64
	var updatedAt time.Time
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, query, orgID, userID, time.Now().UTC()).Scan(&count, &updatedAt)
	if err != nil {
		return UnreadCountDTO{}, errs.DatabaseError(err)
	}
	return UnreadCountDTO{Count: count, UpdatedAt: updatedAt}, nil
}

func (r *Repository) MarkRead(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	if orgID == uuid.Nil || userID == uuid.Nil || notificationID == uuid.Nil {
		return errs.Validation("organization, user and notification id are required")
	}
	now := time.Now().UTC()
	const query = `
		UPDATE notification_recipients AS r
		SET read_at = COALESCE(r.read_at, $4)
		FROM notifications AS n
		WHERE n.id = r.notification_id
		  AND n.organization_id = r.organization_id
		  AND n.organization_id = $1
		  AND r.user_id = $2
		  AND r.notification_id = $3
		  AND (n.expires_at IS NULL OR n.expires_at > $4)`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, orgID, userID, notificationID, now)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFound("notification")
	}
	return nil
}

func (r *Repository) MarkAllRead(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (int64, time.Time, error) {
	if orgID == uuid.Nil || userID == uuid.Nil {
		return 0, time.Time{}, errs.Validation("organization and user are required")
	}
	now := time.Now().UTC()
	const query = `
		UPDATE notification_recipients AS r
		SET read_at = COALESCE(r.read_at, $3)
		FROM notifications AS n
		WHERE n.id = r.notification_id
		  AND n.organization_id = r.organization_id
		  AND n.organization_id = $1
		  AND r.user_id = $2
		  AND r.read_at IS NULL
		  AND r.dismissed_at IS NULL
		  AND (n.expires_at IS NULL OR n.expires_at > $3)
		RETURNING r.id`
	rows, err := r.db.FromCtx(ctx).Query(ctx, query, orgID, userID, now)
	if err != nil {
		return 0, time.Time{}, errs.DatabaseError(err)
	}
	defer rows.Close()
	var affected int64
	for rows.Next() {
		affected++
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, errs.DatabaseError(err)
	}
	return affected, now, nil
}

func (r *Repository) Dismiss(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, notificationID uuid.UUID) error {
	if orgID == uuid.Nil || userID == uuid.Nil || notificationID == uuid.Nil {
		return errs.Validation("organization, user and notification id are required")
	}
	now := time.Now().UTC()
	const query = `
		UPDATE notification_recipients AS r
		SET dismissed_at = COALESCE(r.dismissed_at, $4)
		FROM notifications AS n
		WHERE n.id = r.notification_id
		  AND n.organization_id = r.organization_id
		  AND n.organization_id = $1
		  AND r.user_id = $2
		  AND r.notification_id = $3`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, query, orgID, userID, notificationID, now)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFound("notification")
	}
	return nil
}

func (r *Repository) CreateBroadcast(ctx context.Context, orgID uuid.UUID, creatorID uuid.UUID, req BroadcastRequest) (*NotificationBroadcast, error) {
	if orgID == uuid.Nil || creatorID == uuid.Nil {
		return nil, errs.Validation("organization and creator are required")
	}
	if !req.Scope.Valid() {
		return nil, errs.Validation("scope must be org, branch, role or user")
	}
	switch req.Scope {
	case BroadcastScopeOrg:
		if req.TargetID != nil {
			return nil, errs.Validation("target_id must be null for org scope")
		}
	case BroadcastScopeBranch, BroadcastScopeRole, BroadcastScopeUser:
		if req.TargetID == nil || *req.TargetID == uuid.Nil {
			return nil, errs.Validation("target_id is required for " + string(req.Scope) + " scope")
		}
	}
	if !req.Priority.Valid() {
		req.Priority = PriorityNormal
	}
	if req.Title == "" || req.Message == "" {
		return nil, errs.Validation("title and message are required")
	}
	id := uuid.New()
	now := time.Now().UTC()
	return &NotificationBroadcast{
		BaseModel:      common.BaseModel{ID: id, CreatedAt: now, UpdatedAt: now},
		OrganizationID: orgID,
		CreatorUserID:  creatorID,
		Scope:          req.Scope,
		TargetID:       req.TargetID,
		Title:          req.Title,
		Message:        req.Message,
		Priority:       req.Priority,
		ActionURL:      req.ActionURL,
		Status:         BroadcastStatusPending,
		RequestedAt:    now,
	}, nil
}
