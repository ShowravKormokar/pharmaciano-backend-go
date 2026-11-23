package notification

import (
	"context"
	"fmt"
	"sync"

	"backend/internal/platform/redis"
)

// Hub manages a set of registered clients and fans out committed notification
// ID hints received over Redis Pub/Sub to the matching client.
// Phase 4 (optional realtime): not mounted in routes; polling remains
// authoritative. Kept as a compile-safe placeholder until a WebSocket library
// and deployment review are approved.
type Hub struct {
	mu      sync.Mutex
	clients map[string]*Client
	sub     *redis.Subscriber
}

func NewHub(sub *redis.Subscriber) *Hub {
	return &Hub{clients: map[string]*Client{}, sub: sub}
}

func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client.id] = client
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, client.id)
}

func (h *Hub) Notify(orgID, userID, notificationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := orgID + ":" + userID
	for _, c := range h.clients {
		if c.channel == key {
			c.Push(notificationID)
		}
	}
}

func (h *Hub) SubscribeOrgUser(ctx context.Context, orgID, userID string) error {
	if h.sub == nil {
		return nil
	}
	channel := fmt.Sprintf("notifications:%s:%s", orgID, userID)
	return h.sub.Subscribe(ctx, channel, func(ctx context.Context, e redis.Envelope) error {
		_ = e
		return nil
	})
}

var _ = context.Background
