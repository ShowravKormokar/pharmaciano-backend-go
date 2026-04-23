package tasks

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

const TypeReportGeneration = "report:generation"

// ReportPayload represents the payload for report generation task
type ReportPayload struct {
	ReportID   string
	ReportType string
}

// NewReportGenerationTask creates a new report generation task
func NewReportGenerationTask(reportID, reportType string) *asynq.Task {
	payload, _ := json.Marshal(ReportPayload{
		ReportID:   reportID,
		ReportType: reportType,
	})
	return asynq.NewTask(TypeReportGeneration, payload)
}

// HandleReportGeneration handles report generation task
func HandleReportGeneration(ctx context.Context, t *asynq.Task) error {
	var p ReportPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	// Implement report generation logic here

	return nil
}
