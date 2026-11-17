package analytics

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
)

const maxAnalyticsQueryStringBytes = 2048

type Handler struct {
	svc *Service
	log *zap.Logger
}

type dashboardRequest struct {
	BranchID *uuid.UUID `form:"branch_id"`
}

type salesOverviewRequest struct {
	BranchID *uuid.UUID `form:"branch_id"`
	From     string     `form:"from"`
	To       string     `form:"to"`
	GroupBy  string     `form:"group_by"`
	Currency string     `form:"currency"`
}

// New assembles the read-only Analytics HTTP handler for the gated sales-first
// scope. Routes must remain disabled until projection/event prerequisites and
// reconciliation gates are satisfied.
func New(database *db.DB, log *zap.Logger, metrics ...*telemetry.Metrics) (*Handler, error) {
	if database == nil {
		return nil, fmt.Errorf("analytics handler: database is required")
	}
	repo := NewRepository(database)
	return &Handler{
		svc: NewService(repo, metrics...),
		log: log,
	}, nil
}

// GetDashboard returns the sales-only "today" dashboard snapshot for the
// caller's authorized organization/branch scope.
func (h *Handler) GetDashboard(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !bindAnalyticsQuery(c, h.log, "branch_id") {
		return
	}

	var req dashboardRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid analytics dashboard query").WithCause(err))
		return
	}

	result, err := h.svc.GetDashboard(c.Request.Context(), &DashboardQuery{
		BranchID: req.BranchID,
	})
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, result)
}

// GetSalesOverview returns bounded, grouped sales aggregates for the caller's
// authorized organization/branch scope.
func (h *Handler) GetSalesOverview(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !bindAnalyticsQuery(c, h.log, "branch_id", "from", "to", "group_by", "currency") {
		return
	}

	var req salesOverviewRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		httpx.Error(c, h.log, errs.Validation("invalid analytics sales overview query").WithCause(err))
		return
	}

	from, err := parseAnalyticsDate("from", req.From, true)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	to, err := parseAnalyticsDate("to", req.To, true)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}

	query := &SalesOverviewQuery{
		BranchID: req.BranchID,
		From:     from,
		To:       to,
		GroupBy:  GroupBy(req.GroupBy),
		Currency: req.Currency,
	}
	query.Normalize()

	result, err := h.svc.GetSalesOverview(c.Request.Context(), query)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, result)
}

// bindAnalyticsQuery rejects oversized, malformed, unknown, repeated, or empty
// query parameters to keep parsing deterministic and resource-bounded.
func bindAnalyticsQuery(c *gin.Context, log *zap.Logger, allowed ...string) bool {
	if len(c.Request.URL.RawQuery) > maxAnalyticsQueryStringBytes {
		httpx.Error(c, log, errs.Validation("analytics query string exceeds the maximum length"))
		return false
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		httpx.Error(c, log, errs.Validation("invalid analytics query parameters"))
		return false
	}

	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allow[key]; !ok {
			httpx.Error(c, log, errs.Validation("unsupported analytics query parameter", errs.FieldError{
				Field: key, Rule: "unknown", Message: "is not supported",
			}))
			return false
		}
		if len(entries) != 1 {
			httpx.Error(c, log, errs.Validation("repeated analytics query parameter", errs.FieldError{
				Field: key, Rule: "unique", Message: "must be supplied at most once",
			}))
			return false
		}
		if strings.TrimSpace(entries[0]) == "" {
			httpx.Error(c, log, errs.Validation("empty analytics query parameter", errs.FieldError{
				Field: key, Rule: "nonempty", Message: "must not be empty when supplied",
			}))
			return false
		}
		if key == "branch_id" {
			id, parseErr := uuid.Parse(strings.TrimSpace(entries[0]))
			if parseErr != nil || id == uuid.Nil {
				httpx.Error(c, log, errs.Validation("invalid analytics query parameter", errs.FieldError{
					Field: "branch_id", Rule: "uuid", Message: "must be a valid non-zero UUID",
				}))
				return false
			}
		}
	}
	return true
}

func parseAnalyticsDate(field, raw string, required bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return time.Time{}, errs.Validation("invalid analytics query parameter", errs.FieldError{
				Field: field, Rule: "required", Message: "is required",
			})
		}
		return time.Time{}, nil
	}
	// Accept canonical RFC3339 or date-only (YYYY-MM-DD, interpreted as UTC date).
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, errs.Validation("invalid analytics query parameter", errs.FieldError{
		Field: field, Rule: "datetime", Message: "must be RFC3339 or YYYY-MM-DD",
	})
}
