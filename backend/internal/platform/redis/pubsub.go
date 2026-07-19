package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Envelop is the canonical shape published on every channel
type Envelope struct {
	Type      string          `json:"type"`
	Source    string          `json:"source,omitempty"` // "api" | "worker"
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
	Data      json.RawMessage `json:"data"`
}

type Publisher struct {
	rdb *redis.Client
	log *zap.Logger
	src string // "api" or "worker"
}

// NewPublisher constructs a Publisher
func NewPublisher(c *Client, source string, log *zap.Logger) *Publisher {
	return &Publisher{rdb: c.Underlying(), src: source, log: log}
}

// Publish sends `data` on `channel`. Marshal errors return without side effect.
func (p *Publisher) Publish(ctx context.Context, channel, eventType string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("publish marshal: %w", err)
	}

	env := Envelope{
		Type:      eventType,
		Source:    p.src,
		Timestamp: time.Now().UTC(),
		Data:      payload,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}

	if err := p.rdb.Publish(ctx, channel, body).Err(); err != nil {
		return fmt.Errorf("publish %s: %w", channel, err)
	}
	return nil
}

// Subscriber
// Handler processes one message
type Handler func(ctx context.Context, e Envelope) error

// Subscriber wraps go-redis PubSub with a typed handler loop
type Subscriber struct {
	c    *Client
	log  *zap.Logger
	mu   sync.Mutex
	subs map[string]*subscription
}

type subscription struct {
	ps     *redis.PubSub
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSubscriber constructs a Subscriber
func NewSubscriber(c *Client, log *zap.Logger) *Subscriber {
	return &Subscriber{c: c, log: log, subs: map[string]*subscription{}}
}

// Subscribe starts a handler loop on `channel`. Returns an error if already
// subscribed.
func (s *Subscriber) Subscribe(ctx context.Context, channel string, h Handler) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.subs[channel]; exists {
		return fmt.Errorf("already subscribed to %s", channel)
	}

	ps := s.c.Underlying().Subscribe(ctx, channel)
	// Ensure Redis accepted the subscription before starting the loop.
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return fmt.Errorf("subscribe %s: %w", channel, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{ps: ps, cancel: cancel, done: make(chan struct{})}
	s.subs[channel] = sub

	go s.run(runCtx, channel, ps, h, sub.done)
	s.log.Info("subscribed", zap.String("channel", channel))

	return nil
}

// Unsubscribe stops the handler loop and closes the PubSub.
func (s *Subscriber) Unsubscribe(channel string) error {
	s.mu.Lock()
	sub, ok := s.subs[channel]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.subs, channel)
	s.mu.Unlock()

	sub.cancel()
	err := sub.ps.Close()
	<-sub.done
	s.log.Info("unsubscribed", zap.String("channel", channel))

	return err
}

// Close stops every subscription.
func (s *Subscriber) Close() {
	s.mu.Lock()
	channels := make([]string, 0, len(s.subs))
	for k := range s.subs {
		channels = append(channels, k)
	}
	s.mu.Unlock()
	for _, ch := range channels {
		_ = s.Unsubscribe(ch)
	}
}

func (s *Subscriber) run(ctx context.Context, channel string, ps *redis.PubSub, h Handler, done chan struct{}) {
	defer close(done)
	ch := ps.Channel(redis.WithChannelSize(256))
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var env Envelope
			if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
				s.log.Warn("bad envelope",
					zap.String("channel", channel),
					zap.Error(err),
				)
				continue
			}
			if err := h(ctx, env); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Warn("handler error",
					zap.String("channel", channel),
					zap.String("type", env.Type),
					zap.Error(err),
				)
			}
		}
	}
}
