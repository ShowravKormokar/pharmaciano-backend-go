package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Checker returns nil when the dependency is healthy
type Checker func(ctx context.Context) error

// Health is the registry for named checks + a short-lived cache of the last
// result, so /readyz doesn't hammer Postgres/Redis under a traffic spike.
type Health struct {
	mu           sync.RWMutex
	checkers     map[string]Checker
	cache        map[string]checkResult
	interval     time.Duration
	appName      string
	version      string
	started      time.Time
	exposeErrors bool
	log          *zap.Logger
}

type checkResult struct {
	OK         bool      `json:"ok"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// NewHealth creates a Health registry.
//   - exposeErrors: when false (recommended in prod), the public JSON only
//     ever says "check failed" — the real error still goes to the log via
//     zap, where it belongs, instead of leaking connection strings/internal
//     hostnames to anyone who can reach /readyz.
//   - cacheTTL: how long a check result is reused before re-running it;
//     <= 0 falls back to 2s.

func NewHealth(appName, version string, exposeErrors bool, cacheTTL time.Duration, log *zap.Logger) *Health {
	if cacheTTL <= 0 {
		cacheTTL = 2 * time.Second
	}
	if log == nil {
		log = zap.NewNop()
	}

	return &Health{
		checkers:     map[string]Checker{},
		cache:        map[string]checkResult{},
		interval:     cacheTTL,
		appName:      appName,
		version:      version,
		started:      time.Now(),
		exposeErrors: exposeErrors,
		log:          log,
	}
}

// Register adds a check under a name (e.g. "postgres", "redis", "worker").
func (h *Health) Register(name string, c Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[name] = c
}

// LivenessHandler always responds 200 while the process is up. Orchestrators
// use this to decide whether to restart the container — it must NOT depend
// on Postgres/Redis, or a DB outage would cause a restart loop instead of a
// clean "not ready, but alive" state.
func (h *Health) LivenessHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, rq *http.Request) {
		writeJSON(rw, http.StatusOK, map[string]any{
			"status":  "ok",
			"app":     h.appName,
			"version": h.version,
			"uptime":  time.Since(h.started).String(),
		})
	}
}

// ReadinessHandler runs every registered check (cached briefly) and returns
// 200 only if all pass, otherwise 503.
func (h *Health) ReadinessHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, rq *http.Request) {
		ctx, cancel := context.WithTimeout(rq.Context(), 3*time.Second)
		defer cancel()

		results := h.runAll(ctx)
		status := http.StatusOK
		for _, res := range results {
			if !res.OK {
				status = http.StatusServiceUnavailable
				break
			}
		}
		writeJSON(rw, status, map[string]any{
			"status": http.StatusText(status),
			"app":    h.appName,
			"checks": results,
		})
	}
}

func (h *Health) runAll(ctx context.Context) map[string]checkResult {
	h.mu.RLock()
	names := make([]string, 0, len(h.checkers))
	for n := range h.checkers {
		names = append(names, n)
	}

	cache := make(map[string]checkResult, len(names))
	for k, v := range h.cache {
		cache[k] = v
	}
	h.mu.RUnlock()

	now := time.Now()
	out := make(map[string]checkResult, len(names))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, name := range names {
		if prev, ok := cache[name]; ok && now.Sub(prev.CheckedAt) < h.interval {
			out[name] = prev
			continue
		}
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			h.mu.RLock()
			c := h.checkers[n]
			h.mu.RUnlock()

			start := time.Now()
			err := c(ctx)
			r := checkResult{
				OK:         err == nil,
				DurationMS: time.Since(start).Milliseconds(),
				CheckedAt:  time.Now(),
			}
			if err != nil {
				h.log.Warn("health check faild", zap.String("check", n), zap.Error(err))
				if h.exposeErrors {
					r.Error = err.Error()
				} else {
					r.Error = "check failed"
				}
			}
			mu.Lock()
			out[n] = r
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	h.mu.Lock()
	for k, v := range out {
		h.cache[k] = v
	}
	h.mu.Unlock()
	return out
}

func statusText(code int) string {
	if code == http.StatusOK {
		return "ok"
	}
	return "degraded"
}

func writeJSON(rw http.ResponseWriter, code int, body any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(body)
}
