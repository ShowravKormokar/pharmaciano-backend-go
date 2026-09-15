package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
)

// authzCacheErrors is the running counter for cache-corrupt / cache-error events
// surfaced from CachedAuthorizer. ADR §34 calls these out as a distinct metric
// (`authz_cache_error_total`), counted separately from normal misses. It is
// exposed for the Prometheus collector to scrape; the file-scoped variable
// keeps the surface tight without introducing a registry dependency here.
var authzCacheErrors atomic.Uint64

// AuthzCacheErrorCount returns the running total of cache errors since process
// start (used by the metrics collector).
func AuthzCacheErrorCount() uint64 { return authzCacheErrors.Load() }

// recordCacheError bumps the in-process counter and forwards the event to the
// Prometheus collector when one is wired. The in-process counter is the
// authoritative source for /metrics because the Prometheus registry may not
// have a collector registered in tests.
func recordCacheError(metrics *telemetry.Metrics) {
	authzCacheErrors.Add(1)
	if metrics != nil {
		metrics.IncAuthzCacheError()
	}
}

// flight is a tiny singleflight built on top of sync.Map so concurrent
// cache misses for the same key resolve through one backend query (ADR §36).
// We avoid the golang.org/x/sync/singleflight package to keep the dependency
// surface narrow; this implementation is sufficient for our access pattern
// (string keys, function pointer, pointer result).
type flight struct {
	dedup sync.Map // key → *flightCall
}

type flightCall struct {
	wg   sync.WaitGroup
	val  Access
	err  error
	once bool
}

// do runs fn for the given key, collapsing concurrent calls with the same
// key onto the same in-flight call. Subsequent callers receive the same
// result. On panic or duplicate-completion the call panics (consistent with
// the stdlib singleflight semantics — programming errors).
func (f *flight) do(key string, fn func() (Access, error)) (Access, error) {
	if existing, ok := f.dedup.Load(key); ok {
		existing.(*flightCall).wg.Wait()
		return existing.(*flightCall).val, existing.(*flightCall).err
	}
	call := &flightCall{once: true}
	call.wg.Add(1)
	actual, loaded := f.dedup.LoadOrStore(key, call)
	c := actual.(*flightCall)
	if loaded {
		c.wg.Wait()
		return c.val, c.err
	}
	defer f.dedup.Delete(key)
	defer c.wg.Done()
	c.val, c.err = fn()
	return c.val, c.err
}

// CachedAuthorizer separates authorization from authentication. It never accepts
// permissions from a JWT: it resolves the current server-side policy snapshot,
// optionally caching that immutable result in Redis under a versioned key.
// PostgreSQL remains the source of the Enforcer snapshot; Redis failure only
// bypasses this performance cache and never grants access by itself.
//
// Cache integrity is guarded by three things (ADR §17, §34, §36):
//   - Versioned keys (user authz_version + org rbac_generation) make stale
//     snapshots unreachable without per-entry invalidation; a single bump is
//     O(1).
//   - A singleflight (`flight`) collapses concurrent misses for the same
//     key onto one resolver call so a thundering-herd login spike does not
//     stampede the database.
//   - Cache corruption (JSON unmarshal failure, etc.) is counted as an
//     error (authzCacheErrors), the bad entry is deleted, and the call
//     resolves from the enforcer as a normal miss.
type CachedAuthorizer struct {
	enforcer *Enforcer
	redis    *redis.Client
	log      *zap.Logger
	metrics  *telemetry.Metrics
	ttl      time.Duration

	flight flight
}

func NewCachedAuthorizer(enforcer *Enforcer, rdb *redis.Client, metrics *telemetry.Metrics, log *zap.Logger) *CachedAuthorizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &CachedAuthorizer{enforcer: enforcer, redis: rdb, log: log, metrics: metrics, ttl: 5 * time.Minute}
}

func (a *CachedAuthorizer) Enforce(ctx context.Context, sub, dom, obj, act string) (bool, error) {
	userID, err := uuid.Parse(sub)
	if err != nil {
		a.observeDecision("error")
		return false, err
	}
	orgID, err := uuid.Parse(dom)
	if err != nil {
		a.observeDecision("error")
		return false, err
	}
	if appctx.AuthzVersion(ctx) < 1 {
		a.observeDecision("error")
		return false, errors.New("rbac: missing authorization version")
	}
	// Fold the org-level RBAC generation into the cache key so role definition
	// changes invalidate the snapshot at O(1) (ADR §30). The generation comes from
	// the enforcer's in-memory snapshot — loaded once per snapshot, not read from
	// the database per request — so the hot path stays DB-free. For a mutation
	// committed through the rbac API, the service reloads the enforcer
	// synchronously before the next request, so this value is already current; for
	// an out-of-band change it is bounded by the auto-reload interval, exactly as
	// the policy data it keys against (never fresher, never staler — key and value
	// always agree).
	var orgGen int64
	if a.enforcer != nil {
		orgGen = a.enforcer.orgGeneration(orgID)
	}
	started := time.Now()
	// The effective branch subset — finalized on the request context by the
	// Tenant middleware (before RBAC) — drives which branch-scoped role grants
	// apply. Folding it into resolution AND the cache key keeps the cached
	// Access correct per scope and never grants a branch-scoped role outside its
	// branch (ADR §19/§22).
	access, err := a.resolve(ctx, orgID, userID, appctx.AuthzVersion(ctx), orgGen, appctx.BranchIDs(ctx))
	if err != nil {
		a.observeDecision("error")
		return false, err
	}
	allowed := accessAllows(access, obj, act)
	if allowed {
		a.observeDecision("allow")
	} else {
		a.observeDecision("deny")
	}
	_ = started // resolution latency is observed inside resolve() with finer source attribution
	return allowed, nil
}

func (a *CachedAuthorizer) resolve(ctx context.Context, orgID, userID uuid.UUID, userVersion, orgGen int64, branchIDs []uuid.UUID) (Access, error) {
	// Per-user authz_version + org rbac_generation + effective branch subset form
	// the versioned key (ADR §25 / §30; branch scope extends it for branch-scoped
	// grants). Any of them advancing ⇒ the old cache entry is unreachable.
	key := buildAuthzKey(orgID, userID, userVersion, orgGen, branchIDs)

	// Fast path: Redis hit with a parseable value.
	if a.redis != nil {
		started := time.Now()
		raw, err := a.redis.Get(ctx, key)
		if err == nil {
			var cached Access
			if json.Unmarshal([]byte(raw), &cached) == nil {
				a.observeCache("hit")
				a.observeResolve("cache", time.Since(started))
				return cached, nil
			}
			// Malformed cached value: count + delete + miss (ADR §34).
			recordCacheError(a.metrics)
			a.observeCache("corrupt")
			a.log.Warn("authorization cache returned malformed entry; deleting and resolving fresh",
				zap.String("key", key))
			a.bestEffortDelete(ctx, key)
		} else if !errors.Is(err, redis.ErrKeyNotFound) {
			recordCacheError(a.metrics)
			a.observeCache("error")
			a.log.Warn("authorization cache unavailable; resolving server-side",
				zap.String("key", key), zap.Error(err))
		} else {
			a.observeCache("miss")
		}
	} else {
		a.observeCache("skip")
	}

	// Coalesce concurrent misses onto one resolver call (ADR §36).
	resolved, err := a.flight.do(key, func() (Access, error) {
		started := time.Now()
		access := a.enforcer.ResolveAccessScoped(orgID.String(), userID.String(), branchIDs)
		if a.redis != nil {
			if raw, mErr := json.Marshal(access); mErr == nil {
				if sErr := a.redis.Set(ctx, key, string(raw), a.ttl); sErr != nil {
					recordCacheError(a.metrics)
					a.log.Warn("authorization cache write failed",
						zap.String("key", key), zap.Error(sErr))
				}
			}
		}
		a.observeResolve("enforcer", time.Since(started))
		return access, nil
	})
	return resolved, err
}

// observeDecision / observeCache / observeResolve are nil-safe forwards to the
// shared telemetry registry so call sites never have to nil-check.
func (a *CachedAuthorizer) observeDecision(outcome string) {
	if a == nil || a.metrics == nil {
		return
	}
	a.metrics.ObserveAuthzDecision(outcome)
}
func (a *CachedAuthorizer) observeCache(outcome string) {
	if a == nil || a.metrics == nil {
		return
	}
	a.metrics.ObserveAuthzCache(outcome)
}
func (a *CachedAuthorizer) observeResolve(source string, dur time.Duration) {
	if a == nil || a.metrics == nil {
		return
	}
	a.metrics.ObserveAuthzResolve(source, dur)
}

// buildAuthzKey is the canonical versioned cache key (extracted so tests can
// exercise it directly). The "og" component carries the org-level RBAC
// generation so role definition changes invalidate every cached snapshot for
// that org at O(1); the "br" component carries the deterministic effective
// branch subset so a user cached under one branch scope can never be served an
// Access computed for a different scope (an org-wide request uses the "*"
// sentinel).
func buildAuthzKey(orgID, userID uuid.UUID, userVersion, orgGen int64, branchIDs []uuid.UUID) string {
	return fmt.Sprintf("mc:authz:v1:org:%s:user:%s:uv:%d:og:%d:br:%s",
		orgID.String(), userID.String(), userVersion, orgGen, branchToken(branchIDs))
}

// branchToken renders an effective branch subset as a deterministic, ordered
// string for use in a cache key. The org-wide scope (empty subset) maps to the
// "*" sentinel so it is distinct from any real branch subset.
func branchToken(ids []uuid.UUID) string {
	if len(ids) == 0 {
		return "*"
	}
	sorted := append([]uuid.UUID(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	var b strings.Builder
	for i, id := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(id.String())
	}
	return b.String()
}

func (a *CachedAuthorizer) bestEffortDelete(ctx context.Context, key string) {
	if a.redis == nil {
		return
	}
	if _, err := a.redis.Del(ctx, key); err != nil {
		recordCacheError(a.metrics)
		a.log.Warn("authorization cache delete failed",
			zap.String("key", key), zap.Error(err))
	}
}

func accessAllows(access Access, module, action string) bool {
	for _, p := range access.Permissions {
		if p == module+":"+action || p == module+":*" || p == "*" {
			return true
		}
	}
	return false
}
