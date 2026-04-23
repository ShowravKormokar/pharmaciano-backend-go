package tasks

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

const TypeAIInsightGeneration = "ai:insight:generation"

// AIInsightPayload represents the payload for AI insight generation
type AIInsightPayload struct {
	InsightID string
	DataType  string
	Params    map[string]interface{}
}

// NewAIInsightTask creates a new AI insight generation task
func NewAIInsightTask(insightID, dataType string, params map[string]interface{}) *asynq.Task {
	payload, _ := json.Marshal(AIInsightPayload{
		InsightID: insightID,
		DataType:  dataType,
		Params:    params,
	})
	return asynq.NewTask(TypeAIInsightGeneration, payload)
}

// HandleAIInsightGeneration handles AI insight generation task
func HandleAIInsightGeneration(ctx context.Context, t *asynq.Task) error {
	var p AIInsightPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	// Implement AI insight generation logic here

	return nil
}
