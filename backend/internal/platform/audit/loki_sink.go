// Package auditor: production observability copy of the audit trail.
//
// PostgresAuditSink remains the durable, append-only authority for every
// state-changing request. LokiAuditSink is a PARALLEL, best-effort feed that
// streams the same entries to Grafana Loki so the Audit dashboard can render
// real-time "who did what, where, and with what result" views that the
// strong-storage Postgres schema is not optimised for (Loki is built for
// high-cardinality log-literal search, not transactional reads).
//
// The two are combined with a FanoutAuditSink: Postgres always wins; Loki is
// fire-and-forget and can never block or break the request path.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"backend/internal/middleware"
	"backend/internal/platform/config"
)

// newLokiAuditSink accepts config.LokiSinkState (the `audit.loki` block) so the
// config layer is the single source of truth for these settings.
// Env overrides: AUDIT_LOKI_ENABLED, AUDIT_LOKI_URL, AUDIT_LOKI_BATCH_SIZE,
// AUDIT_LOKI_FLUSH_INTERVAL, AUDIT_LOKI_TIMEOUT.

// LokiAuditSink buffers entries and pushes them to Loki in batches. It is
// safe for concurrent use. It never blocks the caller for longer than a
// channel send.
type LokiAuditSink struct {
	cfg    config.LokiSinkState
	log    *zap.Logger
	client *http.Client

	mu      sync.Mutex
	pending []middleware.AuditEntry

	flushed chan struct{} // signals a full batch is ready to drain
	closed  chan struct{}
	done    chan struct{}

	startOnce sync.Once
}

type lokiStream struct {
	labels map[string]string
	values [][2]string // [nanosecond-ts, json-line]
}

// NewLokiAuditSink builds the sink. Returns nil when cfg.Enabled is false (the
// fanout wrapper simply skips it). When Enabled but URL is empty, it degrades
// to nil + a warning so the app still boots.
func NewLokiAuditSink(cfg config.LokiSinkState, log *zap.Logger) *LokiAuditSink {
	if log == nil {
		log = zap.NewNop()
	}
	if !cfg.Enabled {
		return nil
	}
	if cfg.URL == "" {
		log.Warn("audit.loki.enabled is true but audit.loki.url is empty; Loki sink disabled")
		return nil
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 256
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 500 * time.Millisecond
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}

	return &LokiAuditSink{
		cfg:     cfg,
		log:     log,
		client:  &http.Client{Timeout: cfg.Timeout},
		flushed: make(chan struct{}),
		closed:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Record buffers an entry for the next flush. It is non-blocking in the happy
// path; a slow/concurrent reader can only wait on the mutex briefly.
func (s *LokiAuditSink) Record(_ context.Context, entry middleware.AuditEntry) {
	if s == nil {
		return
	}
	s.start()
	s.mu.Lock()
	s.pending = append(s.pending, entry)
	full := len(s.pending) >= s.cfg.BatchSize
	s.mu.Unlock()
	if full {
		select {
		case s.flushed <- struct{}{}:
		default:
		}
	}
}

// Stop flushes any remaining buffered entries and stops the worker goroutine.
// Call during graceful shutdown.
func (s *LokiAuditSink) Stop() {
	if s == nil {
		return
	}
	s.start()
	close(s.closed)
	<-s.done
}

// start launches the worker goroutine exactly once.
func (s *LokiAuditSink) start() {
	s.startOnce.Do(func() {
		go s.worker()
	})
}

func (s *LokiAuditSink) worker() {
	defer close(s.done)
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			s.mu.Lock()
			entries := s.pending
			s.pending = nil
			s.mu.Unlock()
			s.push(entries)
			return
		case <-ticker.C:
			s.flushIfRequired()
		case <-s.flushed:
			s.flushIfRequired()
		}
	}
}

// flushIfRequired drains the pending buffer (if non-empty) and pushes.
func (s *LokiAuditSink) flushIfRequired() {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	entries := s.pending
	s.pending = nil
	s.mu.Unlock()
	s.push(entries)
}

// push builds streams from a batch and sends one HTTP request to Loki. Any
// outcome (network error, non-2xx, marshal error) is logged and dropped: the
// audit trail is safe in Postgres regardless.
func (s *LokiAuditSink) push(entries []middleware.AuditEntry) {
	if len(entries) == 0 {
		return
	}

	// Group by label tuple, preserving first-seen order.
	order := make([]string, 0, 8)
	batchStreams := make(map[string]*lokiStream)
	for i := range entries {
		key, labels := labelsFor(&entries[i])
		st, ok := batchStreams[key]
		if !ok {
			st = &lokiStream{labels: labels, values: make([][2]string, 0, len(entries))}
			batchStreams[key] = st
			order = append(order, key)
		}
		st.values = append(st.values, [2]string{
			strconv.FormatInt(entries[i].Timestamp.UnixNano(), 10),
			string(mustJSON(&entries[i])),
		})
	}

	streamsOut := make([]map[string]any, 0, len(order))
	for _, key := range order {
		st := batchStreams[key]
		streamsOut = append(streamsOut, map[string]any{
			"stream": st.labels,
			"values": st.values,
		})
	}

	body, err := json.Marshal(map[string]any{"streams": streamsOut})
	if err != nil {
		s.log.Warn("audit loki: marshal failed", zap.Error(err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		s.log.Warn("audit loki: build request failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scope-OrgID", "primary")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Warn("audit loki: push failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		s.log.Warn("audit loki: non-2xx response",
			zap.Int("status", resp.StatusCode),
			zap.Int("entries", len(entries)))
	}
}

// labelsFor returns the stream key and label map for one entry. Only the
// low-cardinality set is promoted to stream labels; the rest ride in the JSON.
func labelsFor(e *middleware.AuditEntry) (string, map[string]string) {
	l := map[string]string{
		"job":     "pharmaciano-audit",
		"module":  orDash(e.Module),
		"action":  orDash(e.Action),
		"outcome": outcomeLabel(e.Success),
		"org_id":  e.OrgID.String(),
	}
	key := l["job"] + "." + l["module"] + "." + l["action"] + "." + l["outcome"] + "." + l["org_id"]
	return key, l
}

func outcomeLabel(success bool) string {
	if success {
		return "success"
	}
	return "failure"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// mustJSON serialises the entry as the log line. The string is always valid
// JSON; on a pathological marshal error we fall back to a shell JSON object.
func mustJSON(e *middleware.AuditEntry) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return []byte(fmt.Sprintf(`{"action":%q,"outcome":"failure","serialize_error":%q}`,
			e.Action, err.Error()))
	}
	return b
}

// FanoutAuditSink records to every sink in order. Postgres first (authority),
// then any optional sinks (Loki). A sink's error never propagates upward.
type FanoutAuditSink struct {
	sinks []middleware.AuditSink
}

// NewFanoutAuditSink composes the durable Postgres sink with optional extra
// sinks (e.g. Loki). Pass nil entries to skip.
func NewFanoutAuditSink(pg middleware.AuditSink, loki middleware.AuditSink) *FanoutAuditSink {
	s := &FanoutAuditSink{}
	if pg != nil {
		s.sinks = append(s.sinks, pg)
	}
	if loki != nil {
		s.sinks = append(s.sinks, loki)
	}
	return s
}

func (f *FanoutAuditSink) Record(ctx context.Context, entry middleware.AuditEntry) {
	for _, s := range f.sinks {
		s.Record(ctx, entry)
	}
}

// compile-time assertions
var (
	_ middleware.AuditSink = (*LokiAuditSink)(nil)
	_ middleware.AuditSink = (*FanoutAuditSink)(nil)
)