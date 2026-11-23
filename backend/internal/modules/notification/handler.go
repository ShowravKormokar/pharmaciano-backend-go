package notification

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

type notificationService interface {
	List(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, InboxFilter) ([]NotificationDTO, int64, bool, error)
	UnreadCount(context.Context, uuid.UUID, uuid.UUID) (UnreadCountDTO, error)
	Read(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	ReadAll(context.Context, uuid.UUID, uuid.UUID) (ReadAllResponse, error)
	Dismiss(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	CreateBroadcast(context.Context, uuid.UUID, uuid.UUID, BroadcastRequest) (BroadcastResponse, error)
}

// Handler exposes the durable in-app inbox and broadcast API. It never mutates
// Inventory, Purchase, Sale, or Ledger tables.
type Handler struct {
	svc notificationService
	val *validator.Validator
	log *zap.Logger
	cc  *pagination.CursorCodec
}

func newHandler(svc notificationService, cc *pagination.CursorCodec, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if svc == nil || cc == nil {
		return nil, fmt.Errorf("notification handler: service and cursor codec are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, cc: cc, val: v, log: log}, nil
}

func New(database *db.DB, cursorSigningKey []byte, v *validator.Validator, log *zap.Logger) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("notification handler: database is required")
	}
	cursorCodec, err := pagination.NewCursorCodec(cursorSigningKey)
	if err != nil {
		return nil, fmt.Errorf("notification handler: create cursor codec: %w", err)
	}
	repo := NewRepository(database, cursorCodec)
	return newHandler(NewService(repo, nil), cursorCodec, v, log)
}

func (h *Handler) List(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	if err := validateInboxQuery(c.Request.URL.Query()); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	f, err := parseInboxFilter(c.Request.URL.Query())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	branchIDs := appctx.BranchIDs(c.Request.Context())
	items, total, hasMore, err := h.svc.List(c.Request.Context(), principal.OrgID, principal.UserID, branchIDs, f)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var nextCursor string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = h.cc.Encode(pagination.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
			SortKey:   inboxSortKey(principal.OrgID, principal.UserID, branchIDs, f),
		})
	}
	httpx.List(c, items, pagination.Meta{
		Limit:      f.Limit,
		Total:      total,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func (h *Handler) UnreadCount(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	count, err := h.svc.UnreadCount(c.Request.Context(), principal.OrgID, principal.UserID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, count)
}

func (h *Handler) Read(c *gin.Context) {
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.Read(c.Request.Context(), principal.OrgID, principal.UserID, id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, map[string]string{"status": "read"})
}

func (h *Handler) ReadAll(c *gin.Context) {
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	resp, err := h.svc.ReadAll(c.Request.Context(), principal.OrgID, principal.UserID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) Dismiss(c *gin.Context) {
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.Dismiss(c.Request.Context(), principal.OrgID, principal.UserID, id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

func (h *Handler) CreateBroadcast(c *gin.Context) {
	principal, ok := appctx.CurrentPrincipal(c.Request.Context())
	if !ok {
		httpx.Error(c, h.log, errs.Unauthenticated())
		return
	}
	var req BroadcastRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.CreateBroadcast(c.Request.Context(), principal.OrgID, principal.UserID, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	c.Header("Location", "/api/v1/notifications/broadcasts/"+resp.ID.String())
	httpx.OK(c, resp)
}

func validateInboxQuery(values url.Values) error {
	allowed := map[string]struct{}{
		"is_read": {}, "type": {}, "priority": {}, "from": {}, "to": {},
		"limit": {}, "cursor": {},
	}
	for k, vs := range values {
		if _, ok := allowed[k]; !ok || len(vs) != 1 {
			return errs.Validation("unsupported or repeated notifications query parameter")
		}
	}
	return nil
}

func parseInboxFilter(values url.Values) (InboxFilter, error) {
	f := InboxFilter{Limit: 20}
	if v := values.Get("is_read"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, errs.Validation("is_read must be true or false")
		}
		f.IsRead = &b
	}
	if v := values.Get("type"); v != "" {
		f.Type = v
	}
	if v := values.Get("priority"); v != "" {
		p := Priority(v)
		if !p.Valid() {
			return f, errs.Validation("priority must be low, normal, high or critical")
		}
		f.Priority = p
	}
	if v := values.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, errs.Validation("from must be RFC3339 UTC")
		}
		f.From = &t
	}
	if v := values.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, errs.Validation("to must be RFC3339 UTC")
		}
		f.To = &t
	}
	if v := values.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 100 {
			return f, errs.Validation("limit must be between 1 and 100")
		}
		f.Limit = n
	}
	f.Cursor = values.Get("cursor")
	return f, nil
}

func parseUUIDParam(c *gin.Context, key string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(key))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + key + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}

func inboxSortKey(orgID uuid.UUID, userID uuid.UUID, branchIDs []uuid.UUID, f InboxFilter) string {
	branchIDStrs := make([]string, 0, len(branchIDs))
	for _, id := range branchIDs {
		branchIDStrs = append(branchIDStrs, id.String())
	}
	payload := struct {
		OrgID     string
		UserID    string
		BranchIDs []string
		Type      string
		Priority  string
		From      string
		To        string
	}{
		OrgID:     orgID.String(),
		UserID:    userID.String(),
		BranchIDs: branchIDStrs,
		Type:      f.Type,
		Priority:  string(f.Priority),
		From:      f.From.String(),
		To:        f.To.String(),
	}
	return "notifications:inbox:v1:" + payload.OrgID + ":" + payload.UserID + ":" + payload.Type
}

func branchIDsString(ids []uuid.UUID) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return joinStrings(out, ",")
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	b := make([]byte, 0, 64)
	for i, s := range ss {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, s...)
	}
	return string(b)
}

var _ = context.Background
var _ = strconv.Itoa
