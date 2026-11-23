package notification

// Client represents one authenticated WebSocket connection and its derived
// subscription channel. Phase 4 (optional realtime): not mounted in routes;
// polling remains authoritative.
type Client struct {
	id      string
	orgID   string
	userID  string
	channel string
	send    chan string
}

func NewClient(id, orgID, userID string) *Client {
	return &Client{
		id:      id,
		orgID:   orgID,
		userID:  userID,
		channel: "notifications:" + orgID + ":" + userID,
		send:    make(chan string, 256),
	}
}

func (c *Client) Push(notificationID string) {
	select {
	case c.send <- notificationID:
	default:
	}
}
