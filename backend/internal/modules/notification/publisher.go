package notification

import (
	"context"
	"fmt"
	"time"

	"backend/internal/platform/redis"
	"go.uber.org/zap"
)

// publisher publishes committed notification ID hints to Redis Pub/Sub.
// Realtime is a low-latency hint only; PostgreSQL is authoritative.
type publisher struct {
	redis  *redis.Publisher
	orgID  string
	userID string
}

// publishHint publishes a minimal {notification_id, event} envelope on the
// server-derived channel for the authenticated principal.
func (p *publisher) publishHint(ctx context.Context, notificationID string) error {
	if p.redis == nil {
		return nil
	}
	payload := map[string]any{
		"notification_id": notificationID,
		"event":           "notification.created",
		"timestamp":       time.Now().UTC(),
	}
	channel := fmt.Sprintf("notifications:%s:%s", p.orgID, p.userID)
	return p.redis.Publish(ctx, channel, "notification.created", payload)
}

// newPublisher builds a hint publisher wired to the existing Redis client.
func newPublisher(client *redis.Client, orgID, userID string, log *zap.Logger) *publisher {
	return &publisher{
		redis:  redis.NewPublisher(client, "notification", log),
		orgID:  orgID,
		userID: userID,
	}
}
