package rbac

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
)

// Enforcer is the platform's authorization decision engine: an in-process,
// native implementation of config/casbin_model.conf. It is the concrete type
// behind the middleware.Authorizer port every Protected route consults.
//
// # Why native instead of the Casbin library
//
// The model is small and fixed (RBAC with domains + deny-override), the policy
// lives in our own tables, and the hot path must be allocation- and lock-free.
// A hand-written enforcer over an immutable, atomically-swapped snapshot gives us
// exactly that, with no third-party dependency and no adapter translating our
// schema into Casbin's CSV shape. The matcher below mirrors the .conf clause for
// clause (see Enforce).
//
// # Snapshot model
//
// The enforcer never touches the database on the request path. A background
// loader flattens the grants (role_permissions) and assignments (user_roles)
// into an immutable *snapshot and stores it in an atomic pointer; Enforce reads
// the current snapshot with a single atomic load and never blocks a writer. A
// reload builds a fresh snapshot and swaps the pointer, so readers always see a
// wholly-consistent view (never a half-updated map).
type Enforcer struct {
	repo *Repository
	log  *zap.Logger
	snap atomic.Pointer[snapshot]
}

// errNotLoaded is returned by Enforce before the first successful Load. The
// middleware treats any enforcer error as a denial, so an un-warmed enforcer
// fails closed rather than granting access.
var errNotLoaded = errors.New("rbac: enforcer snapshot not loaded")

// snapshot is an immutable authorization view. Once built it is only ever read,
// which is what makes lock-free atomic swapping safe.
type snapshot struct {
	// grouping is g: does a user hold a role within a domain, and at what
	// priority. Keyed grouping[userID][dom][role] = role priority. Membership is
	// the key's existence (used by Enforce); the priority value is used by
	// ResolveAccess to pick a user's canonical role. Nested-map indexing of a
	// missing key yields a nil map, so lookups never panic.
	grouping map[string]map[string]map[string]int

	// roleBranches records, parallel to grouping's membership, the branch an
	// assignment is narrowed to: roleBranches[userID][dom][role] is the
	// assignment's user_roles.branch_id, or nil when the role is granted
	// org-wide (applies in every environment of the org). The enforcer
	// consults it to decide whether a role is active for a request's effective
	// branch scope (ADR §19/§22). A nil map (or nil value) means org-wide —
	// which is exactly what every pre-branch-scoping snapshot and test fixture
	// produces, so the behavior is unchanged when no role is branch-scoped.
	roleBranches map[string]map[string]map[string]*uuid.UUID

	// policy is p keyed by role name: the grants (dom, obj, act) that role
	// carries. A system role's grants carry dom "*" (applies in every domain); a
	// tenant role's grants carry its org id. Role names are unique per org and
	// the service forbids tenants from reusing a system role name, so keying by
	// name never conflates a tenant role with a system role.
	policy map[string][]grant

	// orgGen is the per-organization RBAC generation (organizations.rbac_generation)
	// as of this snapshot (ADR §30). The CachedAuthorizer folds it into the versioned
	// cache key so a role/permission definition change invalidates every cached
	// snapshot for that org at O(1). Carrying it here — rather than reading it from
	// the database on every request — keeps the hot path DB-free AND makes the cache
	// key exactly consistent with the policy data this snapshot resolves: a key
	// built from orgGen can never advertise a fresher generation than the snapshot
	// that produced the value it maps to.
	orgGen map[uuid.UUID]int64
}

// grant is one policy tuple's authorization-relevant fields: p = (sub, dom, obj,
// act, eft). sub (the role) is the map key in snapshot.policy; eft is omitted
// because role_permissions expresses only additive allow grants (there is no
// deny row), so the .conf effect some(allow) && !some(deny) reduces to "any
// matching grant ⇒ allow". If a deny channel is ever added, grant must carry eft
// and Enforce must apply deny-override.
type grant struct {
	dom string
	obj string
	act string
}

// NewEnforcer builds an unwarmed enforcer. The caller MUST invoke Load once
// (and should fail startup if it errors) before serving traffic, then optionally
// StartAutoReload for periodic refresh. Until the first successful Load, Enforce
// fails closed.
func NewEnforcer(repo *Repository, log *zap.Logger) *Enforcer {
	if log == nil {
		log = zap.NewNop()
	}
	return &Enforcer{repo: repo, log: log}
}

// Load rebuilds the snapshot from the database and atomically installs it. It is
// safe to call concurrently with Enforce (readers keep using the old snapshot
// until the swap) and is the method the rbac service calls after any grant or
// assignment change so a policy edit takes effect immediately rather than at the
// next auto-reload tick.
func (e *Enforcer) Load(ctx context.Context) error {
	policies, err := e.repo.LoadPolicies(ctx)
	if err != nil {
		return err
	}
	groupings, err := e.repo.LoadGroupings(ctx)
	if err != nil {
		return err
	}
	orgGens, err := e.repo.LoadOrgGenerations(ctx)
	if err != nil {
		return err
	}

	snap := &snapshot{
		grouping:     make(map[string]map[string]map[string]int, len(groupings)),
		roleBranches: make(map[string]map[string]map[string]*uuid.UUID, len(groupings)),
		policy:       make(map[string][]grant, 64),
		orgGen:       orgGens,
	}
	for _, p := range policies {
		snap.policy[p.Role] = append(snap.policy[p.Role], grant{dom: p.Dom, obj: p.Module, act: p.Action})
	}
	for _, g := range groupings {
		byDom := snap.grouping[g.UserID]
		if byDom == nil {
			byDom = make(map[string]map[string]int, 1)
			snap.grouping[g.UserID] = byDom
		}
		roles := byDom[g.Dom]
		if roles == nil {
			roles = make(map[string]int, 2)
			byDom[g.Dom] = roles
		}
		brByDom := snap.roleBranches[g.UserID]
		if brByDom == nil {
			brByDom = make(map[string]map[string]*uuid.UUID, 1)
			snap.roleBranches[g.UserID] = brByDom
		}
		brRoles := brByDom[g.Dom]
		if brRoles == nil {
			brRoles = make(map[string]*uuid.UUID, 2)
			brByDom[g.Dom] = brRoles
		}
		// Keep the highest priority if the same (user, dom, role) appears twice,
		// carrying that row's branch restriction alongside.
		if cur, ok := roles[g.Role]; !ok || g.Priority > cur {
			roles[g.Role] = g.Priority
			brRoles[g.Role] = g.Branch
		}
	}

	e.snap.Store(snap)
	e.log.Info("rbac enforcer snapshot loaded",
		zap.Int("policies", len(policies)),
		zap.Int("groupings", len(groupings)),
		zap.Int("roles", len(snap.policy)),
		zap.Int("users", len(snap.grouping)),
	)
	return nil
}

// StartAutoReload performs an immediate reload, then reloads on every interval
// tick until ctx is cancelled. It runs in its own goroutine and returns
// immediately. Reload failures are logged and the previous snapshot is retained
// (stale-but-safe) rather than cleared. interval <= 0 defaults to 30s.
//
// This is a safety net that bounds staleness (e.g. after an out-of-band DB
// change or a missed post-mutation Load); the primary freshness mechanism is the
// service calling Load right after each mutation.
func (e *Enforcer) StartAutoReload(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		if err := e.Load(ctx); err != nil {
			e.log.Error("rbac enforcer initial auto-reload failed; retaining prior snapshot", zap.Error(err))
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.Load(ctx); err != nil {
					e.log.Error("rbac enforcer auto-reload failed; retaining prior snapshot", zap.Error(err))
				}
			}
		}
	}()
}

// Enforce answers the middleware.Authorizer contract: may the user sub, acting
// in domain dom, perform action act on module obj? It implements the
// config/casbin_model.conf matcher exactly:
//
//	m = g(r.sub, p.sub, r.dom)                  // user holds the role in this domain
//	    && (p.dom == r.dom || p.dom == "*")     // grant applies to this org (or is global)
//	    && keyMatch(r.obj, p.obj)               // module matches (supports a trailing '*')
//	    && (p.act == r.act || p.act == "*")     // action matches (or grant is action-wildcard)
//
// with effect some(allow) && !some(deny); since all grants are allow, the first
// matching grant is a grant. The context supplies the request's effective branch
// scope (finalized by the Tenant middleware before RBAC runs), so a role whose
// assignment is narrowed to a single branch only grants while the request is
// scoped to that branch (ADR §19/§22). The context is otherwise unused (the
// snapshot is in memory) but kept to satisfy the port.
//
// Fail-closed: before the first successful Load, Enforce returns errNotLoaded
// (the middleware logs and denies). A user with no active roles in the domain,
// or a role with no matching grant, denies with a nil error.
func (e *Enforcer) Enforce(ctx context.Context, sub, dom, obj, act string) (bool, error) {
	snap := e.snap.Load()
	if snap == nil {
		return false, errNotLoaded
	}

	roles := snap.activeRoles(ctx, sub, dom) // roles active for the effective branch scope
	if len(roles) == 0 {
		return false, nil
	}
	for role := range roles {
		for _, g := range snap.policy[role] {
			if (g.dom == dom || g.dom == "*") && keyMatch(obj, g.obj) && (g.act == act || g.act == "*") {
				return true, nil
			}
		}
	}
	return false, nil
}

// activeRoles returns the roles the user holds in a domain that are active for
// the request's effective branch scope. The scope is read from the request
// context, which the Tenant middleware finalizes from the principal's assigned
// subset (and any X-Branch-IDs selector) before RBAC runs — so
// appctx.BranchIDs(ctx) is nil/empty only for a genuinely org-wide request.
// ADR §22 filtering: an assignment applies in an environment iff it has no
// branch restriction (user_roles.branch_id IS NULL ⇒ org-wide) or its branch is
// in the effective scope. A nil roleBranches map (e.g. a hand-built snapshot
// without branch data, or fixed before scoping existed) treats every role as
// org-wide, preserving prior behavior.
func (s *snapshot) activeRoles(ctx context.Context, sub, dom string) map[string]bool {
	grants := s.grouping[sub][dom]
	if len(grants) == 0 {
		return nil
	}
	branches := s.roleBranches[sub][dom]

	eff := appctx.BranchIDs(ctx) // nil for org-wide requests (no narrowing)
	var effSet map[uuid.UUID]bool
	if len(eff) > 0 {
		effSet = make(map[uuid.UUID]bool, len(eff))
		for _, b := range eff {
			effSet[b] = true
		}
	}

	out := make(map[string]bool, len(grants))
	for role := range grants {
		br := branches[role]
		if br == nil || (effSet != nil && effSet[*br]) {
			out[role] = true
		}
	}
	return out
}

// Access is the resolved authorization view of a user within one domain, used to
// build a Principal without a database round-trip. It is derived from the same
// snapshot Enforce reads, so a Principal's embedded fast-path permissions can
// never grant more than the enforcer itself would.
type Access struct {
	// RoleName is the user's canonical (highest-priority) active role in the
	// domain, or "" if the user holds none. Ties break on the lexically smaller
	// name for determinism.
	RoleName string
	// IsSuperAdmin is true iff the user holds the SUPER_ADMIN system role.
	IsSuperAdmin bool
	// Permissions is the flattened, sorted set of "module:action" keys the user's
	// roles grant in this domain (matching appctx.HasPermission's token format).
	Permissions []string
}

// ResolveAccess computes the org-wide Access view for a user in a domain from
// the current snapshot — i.e. the view with no branch narrowing, considering
// only org-wide (branch_id IS NULL) role grants. It is the data source behind
// the auth module's AccessResolver port when minting an access token / Principal,
// where no per-request branch scope exists yet. Because branch-scoped roles are
// NOT granted org-wide, they are excluded here so the embedded fast-path
// permission set can never exceed the branch-aware Enforce (the projection
// invariant). Returns the zero Access (no role, no permissions) if the enforcer
// is unwarmed or the user holds no org-wide roles in the domain.
//
// Freshness note: this reads the in-memory snapshot, which the service reloads
// after every assignment change, so a just-granted role is reflected on the
// user's next authentication.
func (e *Enforcer) ResolveAccess(dom, userID string) Access {
	return e.resolveScoped(dom, userID, nil)
}

// ResolveAccessScoped computes the Access view for a user in a domain
// considering only roles active for the given effective branch scope (the
// "empty ou org-wide" scope is represented by a nil slice). It is used by the
// CachedAuthorizer on the request path, where the Tenant middleware has already
// narrowed the request to an effective branch subset. It returns the same view
// Enforce would grant for that scope, so a cached Access can never disagree with
// the authoritative Enforce.
func (e *Enforcer) ResolveAccessScoped(dom, userID string, eff []uuid.UUID) Access {
	return e.resolveScoped(dom, userID, eff)
}

// resolveScoped is the shared implementation; eff is the effective branch
// subset (nil/empty = org-wide, only branch_id IS NULL grants apply). It mirrors
// snapshot.activeRoles so the resolved Access always matches Enforce's decision.
func (e *Enforcer) resolveScoped(dom, userID string, eff []uuid.UUID) Access {
	var acc Access
	snap := e.snap.Load()
	if snap == nil {
		return acc
	}
	domRoles := snap.grouping[userID][dom]
	if len(domRoles) == 0 {
		return acc
	}
	branches := snap.roleBranches[userID][dom]

	var effSet map[uuid.UUID]bool
	if len(eff) > 0 {
		effSet = make(map[uuid.UUID]bool, len(eff))
		for _, b := range eff {
			effSet[b] = true
		}
	}

	perms := make(map[string]struct{}, 32)
	bestPriority := 0
	for role, priority := range domRoles {
		if br := branches[role]; br != nil && (effSet == nil || !effSet[*br]) {
			continue // assignment narrowed to a branch outside this scope → inactive
		}
		if acc.RoleName == "" || priority > bestPriority || (priority == bestPriority && role < acc.RoleName) {
			acc.RoleName = role
			bestPriority = priority
		}
		if role == constants.RoleSuperAdmin {
			acc.IsSuperAdmin = true
		}
		for _, g := range snap.policy[role] {
			if g.dom == dom || g.dom == "*" {
				perms[g.obj+":"+g.act] = struct{}{}
			}
		}
	}

	acc.Permissions = make([]string, 0, len(perms))
	for k := range perms {
		acc.Permissions = append(acc.Permissions, k)
	}
	sort.Strings(acc.Permissions)
	return acc
}

// orgGeneration returns the organization's RBAC generation as of the current
// snapshot (0 when the org is not present in the snapshot — a brand-new org not
// yet reloaded — or before the first Load). It is a single immutable-map lookup
// with no database access, giving the CachedAuthorizer a cheap, consistent
// generation-of-record for its versioned cache key. Because it always agrees with
// the snapshot ResolveAccess reads, a cache key built from it can never alias a
// value computed from a different generation of policy.
func (e *Enforcer) orgGeneration(orgID uuid.UUID) int64 {
	snap := e.snap.Load()
	if snap == nil {
		return 0
	}
	return snap.orgGen[orgID]
}

// keyMatch is a faithful reimplementation of Casbin's KeyMatch used by the .conf
// matcher's keyMatch(r.obj, p.obj): key1 is the request value, key2 the policy
// pattern. A '*' in key2 matches any suffix from that position; with no '*' it is
// plain equality. Our seeded permissions use concrete module names, so this
// behaves as equality for them while still honoring a wildcard pattern if one is
// ever stored.
func keyMatch(key1, key2 string) bool {
	i := strings.Index(key2, "*")
	if i == -1 {
		return key1 == key2
	}
	if len(key1) > i {
		return key1[:i] == key2[:i]
	}
	return key1 == key2[:i]
}
