package audit

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/pkg/pagination"
)

const maxAuditQueryStringBytes = 4096

type Handler struct {
	svc *Service
	log *zap.Logger
}

// New builds the read-only Audit HTTP handler. The cursor key must be the
// stable application key from pagination.cursor_signing_key; changing it
// invalidates outstanding cursors across every application instance.
func New(database *db.DB, cursorSigningKey string, log *zap.Logger, metrics ...*telemetry.Metrics) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("audit handler: database is required")
	}
	codec, err := pagination.NewCursorCodec([]byte(cursorSigningKey))
	if err != nil {
		return nil, fmt.Errorf("audit handler: configure cursor signing: %w", err)
	}
	return &Handler{
		svc: NewService(NewRepository(database, codec), metrics...),
		log: log,
	}, nil
}

func (h *Handler) List(c *gin.Context) {
	if !bindAuditQuery(c, h.log,
		"action", "cursor", "entity_id", "entity_type", "from", "ip",
		"limit", "module", "outcome", "q", "to", "user_id",
	) {
		return
	}
	var query ListAuditLogsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid audit query parameters").WithCause(err))
		return
	}
	response, err := h.svc.List(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, response)
}

func (h *Handler) Get(c *gin.Context) {
	if !bindAuditQuery(c, h.log, "occurred_at") {
		return
	}
	id, err := parseAuditEventID(c.Param("id"))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var query AuditEventLookupQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid audit detail query").WithCause(err))
		return
	}
	response, err := h.svc.Get(c.Request.Context(), id, &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, response)
}

func (h *Handler) ListModules(c *gin.Context) {
	h.listValues(c, true)
}

func (h *Handler) ListActions(c *gin.Context) {
	h.listValues(c, false)
}

func (h *Handler) listValues(c *gin.Context, modules bool) {
	if !bindAuditQuery(c, h.log, "from", "limit", "to") {
		return
	}
	var query AuditValuesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid audit metadata query").WithCause(err))
		return
	}
	var (
		response AuditValuesResponse
		err      error
	)
	if modules {
		response, err = h.svc.ListModules(c.Request.Context(), &query)
	} else {
		response, err = h.svc.ListActions(c.Request.Context(), &query)
	}
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, response)
}

func (h *Handler) ListPlatformEvents(c *gin.Context) {
	if !bindAuditQuery(c, h.log,
		"action", "cursor", "entity_id", "entity_type", "from", "ip",
		"limit", "module", "outcome", "q", "to", "user_id",
	) {
		return
	}
	var query ListAuditLogsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid audit query parameters").WithCause(err))
		return
	}
	response, err := h.svc.ListPlatformEvents(c.Request.Context(), &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, response)
}

func (h *Handler) GetPlatformEvent(c *gin.Context) {
	if !bindAuditQuery(c, h.log, "occurred_at") {
		return
	}
	id, err := parseAuditEventID(c.Param("id"))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var query AuditEventLookupQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid audit detail query").WithCause(err))
		return
	}
	response, err := h.svc.GetPlatformEvent(c.Request.Context(), id, &query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, response)
}

func parseAuditEventID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errs.Validation("invalid audit event ID", errs.FieldError{
			Field: "id", Rule: "uuid", Message: "must be a valid non-zero UUID",
		})
	}
	return id, nil
}

// bindAuditQuery rejects unknown and repeated keys rather than silently
// accepting ambiguous query strings that proxies and binders may interpret
// differently.
func bindAuditQuery(c *gin.Context, log *zap.Logger, allowed ...string) bool {
	if len(c.Request.URL.RawQuery) > maxAuditQueryStringBytes {
		httpx.Error(c, log, errs.Validation("audit query string exceeds the maximum length"))
		return false
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		httpx.Error(c, log, errs.Validation("invalid audit query parameters"))
		return false
	}

	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allow[key]; !ok {
			httpx.Error(c, log, errs.Validation("unsupported audit query parameter", errs.FieldError{
				Field: key, Rule: "unknown", Message: "is not supported",
			}))
			return false
		}
		if len(entries) != 1 {
			httpx.Error(c, log, errs.Validation("repeated audit query parameter", errs.FieldError{
				Field: key, Rule: "unique", Message: "must be supplied at most once",
			}))
			return false
		}
		if strings.TrimSpace(entries[0]) == "" {
			httpx.Error(c, log, errs.Validation("empty audit query parameter", errs.FieldError{
				Field: key, Rule: "nonempty", Message: "must not be empty when supplied",
			}))
			return false
		}
	}
	return true
}
