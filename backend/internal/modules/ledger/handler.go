package ledger

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
)

type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

func New(database *db.DB, v *validator.Validator, log *zap.Logger, metrics ...*telemetry.Metrics) *Handler {
	repo := NewRepository(database)
	var metricSet *telemetry.Metrics
	if len(metrics) > 0 {
		metricSet = metrics[0]
	}
	return &Handler{
		svc: NewService(repo, database, metricSet),
		val: v,
		log: log,
	}
}

func (h *Handler) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.CreateAccount(c.Request.Context(), orgID, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, resp)
}

func (h *Handler) UpdateAccount(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateAccountRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.UpdateAccount(c.Request.Context(), orgID, id, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) DeactivateAccount(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req DeactivateAccountRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeactivateAccount(c.Request.Context(), orgID, id, req.ExpectedVersion, req.Reason); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, gin.H{"id": id, "is_active": false})
}

func (h *Handler) ListAccounts(c *gin.Context) {
	var q ListAccountsQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ListAccounts(c.Request.Context(), orgID, q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) GetAccount(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.GetAccount(c.Request.Context(), orgID, id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) GetAccountBalance(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var q AccountBalanceQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.GetAccountBalance(c.Request.Context(), orgID, id, q.PeriodYear, q.PeriodMonth, q.BranchID)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) CreatePeriod(c *gin.Context) {
	var req CreatePeriodRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.CreatePeriod(c.Request.Context(), orgID, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, resp)
}

func (h *Handler) ListPeriods(c *gin.Context) {
	var q ListPeriodsQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ListPeriods(c.Request.Context(), orgID, q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) ClosePeriod(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ClosePeriodRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ClosePeriod(c.Request.Context(), orgID, id, req, appctx.UserID(c.Request.Context()))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) ReopenPeriod(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ReopenPeriodRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ReopenPeriod(c.Request.Context(), orgID, id, req, appctx.UserID(c.Request.Context()))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) CreateJournal(c *gin.Context) {
	var req CreateJournalRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.CreateJournal(c.Request.Context(), orgID, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, resp)
}

func (h *Handler) UpdateJournal(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateJournalRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.UpdateJournalDraft(c.Request.Context(), orgID, id, req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) GetJournal(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.GetJournal(c.Request.Context(), orgID, id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) ListJournals(c *gin.Context) {
	var q ListJournalsQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ListJournals(c.Request.Context(), orgID, q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) PostJournal(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req PostJournalRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.PostJournal(c.Request.Context(), orgID, id, req, appctx.UserID(c.Request.Context()))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) ReverseJournal(c *gin.Context) {
	id, err := requestID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ReverseJournalRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.ReverseJournal(c.Request.Context(), orgID, id, req, appctx.UserID(c.Request.Context()))
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func (h *Handler) TrialBalance(c *gin.Context) {
	var q TrialBalanceQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	orgID, err := requestOrganizationID(c)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	resp, err := h.svc.TrialBalance(c.Request.Context(), orgID, q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, resp)
}

func requestOrganizationID(c *gin.Context) (uuid.UUID, error) {
	orgID := appctx.OrgID(c.Request.Context())
	if orgID == uuid.Nil {
		return uuid.Nil, errs.Forbidden("organization context is required")
	}
	return orgID, nil
}

func requestID(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, errs.Validation("id must be a UUID")
	}
	return id, nil
}
