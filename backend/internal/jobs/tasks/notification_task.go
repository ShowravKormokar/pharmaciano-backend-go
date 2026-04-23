package tasks

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

const TypeNotificationSend = "notification:send"

// NotificationPayload represents the payload for sending notifications
type NotificationPayload struct {
	NotificationID string
	UserID         string
	Message        string
	Type           string
}

// NewNotificationTask creates a new notification sending task
func NewNotificationTask(notificationID, userID, message, notificationType string) *asynq.Task {
	payload, _ := json.Marshal(NotificationPayload{
		NotificationID: notificationID,
		UserID:         userID,
		Message:        message,
		Type:           notificationType,
	})
	return asynq.NewTask(TypeNotificationSend, payload)
}

// HandleNotificationSend handles notification sending task
func HandleNotificationSend(ctx context.Context, t *asynq.Task) error {
	var p NotificationPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	// Implement notification sending logic here

	return nil
}
