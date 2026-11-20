package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"backend/internal/platform/outbox"
)

// ReportTaskHandler handles the outbox "report.requested" event by executing
// the corresponding report generation job. Registered on the outbox registry
// when the report module is enabled (REPORT_CONTRACT §22).
type ReportTaskHandler struct {
	svc ReportService
	log *zap.Logger
}

// ReportService is the subset of report.Service needed by the task handler.
type ReportService interface {
	Execute(ctx context.Context, jobID string) error
}

// NewReportTaskHandler constructs a handler for report generation outbox events.
func NewReportTaskHandler(svc ReportService, log *zap.Logger) outbox.Handler {
	if svc == nil {
		panic("report task handler: service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	h := &ReportTaskHandler{svc: svc, log: log}
	return h.Handle
}

// Handle processes a single report.requested outbox event.
func (h *ReportTaskHandler) Handle(ctx context.Context, event outbox.Event) error {
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("report task: decode payload: %w", err)
	}
	if payload.JobID == "" {
		return fmt.Errorf("report task: missing job_id in payload")
	}
	if err := h.svc.Execute(ctx, payload.JobID); err != nil {
		h.log.Error("report task execution failed",
			zap.String("event_id", event.ID.String()),
			zap.String("job_id", payload.JobID),
			zap.Error(err),
		)
		return err
	}
	h.log.Info("report task executed",
		zap.String("event_id", event.ID.String()),
		zap.String("job_id", payload.JobID),
	)
	return nil
}
