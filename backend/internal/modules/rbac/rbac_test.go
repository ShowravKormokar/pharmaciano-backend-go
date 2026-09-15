package rbac

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
)

// These are white-box (package rbac) tests. They deliberately target the parts
// of the module that carry the real authorization risk and are pure/DB-free:
//   - the native enforcer decision path (Enforce) and its keyMatch matcher,
//   - ResolveAccess (which feeds a Principal's fast-path permissions),
//   - the seed grant-matrix expansion (which decides what every role can do).
// Repository/service integration is covered separately against a real Postgres.

// --- Enforcer.Enforce --------------------------------------------------------

// newLoaded builds an enforcer with a hand-crafted snapshot installed, without
// touching a database — mirroring what Load produces.
func newLoaded(grouping map[string]map[string]map[string]int, policy map[string][]grant) *Enforcer {
	e := NewEnforcer(nil, nil)
	e.snap.Store(&snapshot{grouping: grouping, policy: policy})
	return e
}

func TestEnforce_FailsClosedWhenNotLoaded(t *testing.T) {
	e := NewEnforcer(nil, nil) // no Load ⇒ nil snapshot
	ok, err := e.Enforce(context.Background(), "u", "org1", "sales", "create")
	if ok {
		t.Fatal("un-warmed enforcer must not grant access")
	}
	if err == nil {
		t.Fatal("un-warmed enforcer must return an error so the middleware denies")
	}
}

func TestEnforce_Matcher(t *testing.T) {
	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {"CUSTOM": 10}}, // holds CUSTOM in org1
		"u3": {"org2": {"CUSTOM": 10}}, // holds a same-named role in org2
		"u2": {"org1": {"SYS": 100}},   // holds a global (dom "*") role
		"u4": {"org1": {"WILD": 10}},   // action-wildcard grant
		"u5": {"org1": {"PREFIX": 10}}, // obj is a keyMatch prefix
	}
	policy := map[string][]grant{
		// Two tenants happen to use the same role NAME, so their grants collide
		// under one policy key. The matcher's (p.dom==r.dom || p.dom=="*") clause
		// is the ONLY thing keeping them isolated — these cases prove it does.
		"CUSTOM": {
			{dom: "org1", obj: "sales", act: "create"},
			{dom: "org2", obj: "inventory", act: "view"},
		},
		"SYS":    {{dom: "*", obj: "reports", act: "view"}},
		"WILD":   {{dom: "org1", obj: "inventory", act: "*"}},
		"PREFIX": {{dom: "org1", obj: "report*", act: "view"}},
	}
	e := newLoaded(grouping, policy)

	cases := []struct {
		name               string
		sub, dom, obj, act string
		want               bool
	}{
		{"tenant sees its own-org grant", "u1", "org1", "sales", "create", true},
		{"tenant cannot use another tenant's same-named grant", "u1", "org1", "inventory", "view", false},
		{"other tenant sees its own-org grant", "u3", "org2", "inventory", "view", true},
		{"other tenant blocked from first tenant's grant", "u3", "org2", "sales", "create", false},
		{"wrong action denies", "u1", "org1", "sales", "delete", false},
		{"wrong module denies", "u1", "org1", "inventory", "create", false},
		{"global (dom=*) grant applies in any org", "u2", "org1", "reports", "view", true},
		{"action wildcard grants any action", "u4", "org1", "inventory", "cancel", true},
		{"keyMatch obj prefix matches", "u5", "org1", "reports", "view", true},
		{"keyMatch obj prefix rejects non-prefix", "u5", "org1", "sales", "view", false},
		{"user with no role in domain denies", "u1", "org2", "sales", "create", false},
		{"unknown user denies", "ghost", "org1", "sales", "create", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Enforce(context.Background(), tc.sub, tc.dom, tc.obj, tc.act)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Enforce(%s,%s,%s,%s) = %v, want %v",
					tc.sub, tc.dom, tc.obj, tc.act, got, tc.want)
			}
		})
	}
}

// --- keyMatch ----------------------------------------------------------------

func TestKeyMatch(t *testing.T) {
	cases := []struct {
		key1, key2 string
		want       bool
	}{
		{"sales", "sales", true},      // plain equality
		{"sales", "inventory", false}, // inequality
		{"reports", "report*", true},  // wildcard suffix match
		{"report", "report*", true},   // key1 shorter than the '*' index → prefix compare
		{"sales", "report*", false},   // different prefix
		{"anything", "*", true},       // bare wildcard matches all
		{"", "", true},                // empty equality
	}
	for _, tc := range cases {
		if got := keyMatch(tc.key1, tc.key2); got != tc.want {
			t.Errorf("keyMatch(%q,%q) = %v, want %v", tc.key1, tc.key2, got, tc.want)
		}
	}
}

// --- Enforcer.ResolveAccess --------------------------------------------------

func TestResolveAccess(t *testing.T) {
	grouping := map[string]map[string]map[string]int{
		"admin": {"org1": {constants.RoleSuperAdmin: 1000, constants.RoleCashier: 400}},
	}
	policy := map[string][]grant{
		constants.RoleSuperAdmin: {{dom: "*", obj: "sales", act: "create"}},
		constants.RoleCashier:    {{dom: "org1", obj: "sales", act: "view"}},
	}
	e := newLoaded(grouping, policy)

	acc := e.ResolveAccess("org1", "admin")
	if acc.RoleName != constants.RoleSuperAdmin {
		t.Errorf("canonical role = %q, want highest-priority %q", acc.RoleName, constants.RoleSuperAdmin)
	}
	if !acc.IsSuperAdmin {
		t.Error("IsSuperAdmin should be true when the user holds SUPER_ADMIN")
	}
	want := []string{"sales:create", "sales:view"}
	if !reflect.DeepEqual(acc.Permissions, want) {
		t.Errorf("permissions = %v, want sorted %v", acc.Permissions, want)
	}
}

func TestResolveAccess_EmptyWhenUnwarmedOrNoRoles(t *testing.T) {
	if acc := NewEnforcer(nil, nil).ResolveAccess("org1", "u"); acc.RoleName != "" || len(acc.Permissions) != 0 || acc.IsSuperAdmin {
		t.Errorf("un-warmed enforcer must resolve to the zero Access, got %+v", acc)
	}
	e := newLoaded(map[string]map[string]map[string]int{}, map[string][]grant{})
	if acc := e.ResolveAccess("org1", "nobody"); acc.RoleName != "" || len(acc.Permissions) != 0 {
		t.Errorf("user with no roles must resolve to the zero Access, got %+v", acc)
	}
}

// --- Seed: grant-matrix expansion --------------------------------------------

func TestResolveGrantIDs_DedupExpandAndSkipUnknown(t *testing.T) {
	create := uuid.New()
	view := uuid.New()
	permID := map[string]uuid.UUID{
		"sales:" + constants.ActionCreate: create,
		"sales:" + constants.ActionView:   view,
	}

	// Duplicate action + an unknown action: unknown is skipped, dupes collapse,
	// first-seen order is preserved.
	specs := []grantSpec{
		{module: "sales", actions: []string{constants.ActionCreate, constants.ActionView, constants.ActionCreate, "does-not-exist"}},
	}
	got := resolveGrantIDs(specs, permID)
	want := []uuid.UUID{create, view}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveGrantIDs = %v, want %v", got, want)
	}
}

func TestResolveGrantIDs_NilActionsExpandsToAllActions(t *testing.T) {
	permID := make(map[string]uuid.UUID, len(constants.AllActions))
	for _, a := range constants.AllActions {
		permID["sales:"+a] = uuid.New()
	}
	got := resolveGrantIDs([]grantSpec{{module: "sales"}}, permID) // nil actions ⇒ all
	if len(got) != len(constants.AllActions) {
		t.Fatalf("nil-actions spec expanded to %d ids, want all %d actions", len(got), len(constants.AllActions))
	}
}

func TestAllModuleSpecsExcept(t *testing.T) {
	specs := allModuleSpecsExcept(constants.ModuleOrganizations)
	if len(specs) != len(constants.AllModules)-1 {
		t.Fatalf("expected %d specs, got %d", len(constants.AllModules)-1, len(specs))
	}
	for _, s := range specs {
		if s.module == constants.ModuleOrganizations {
			t.Fatalf("excluded module %q leaked into the spec set", constants.ModuleOrganizations)
		}
		if s.actions != nil {
			t.Errorf("all-module spec for %q should carry nil (all) actions", s.module)
		}
	}
}

func TestSystemRoleMatrix_SuperAdminSpansEverything(t *testing.T) {
	m := systemRoleMatrix()

	// Every system role must have an entry so the seed never leaves one ungranted.
	for _, name := range constants.SystemRoles {
		if _, ok := m[name]; !ok {
			t.Errorf("system role %q missing from the grant matrix", name)
		}
	}

	sa := m[constants.RoleSuperAdmin]
	if len(sa) != len(constants.AllModules) {
		t.Fatalf("SUPER_ADMIN covers %d modules, want all %d", len(sa), len(constants.AllModules))
	}
	for _, s := range sa {
		if s.actions != nil {
			t.Errorf("SUPER_ADMIN spec for %q must grant all actions (nil), got %v", s.module, s.actions)
		}
	}

	// ADMIN must not be able to manage organizations (platform-level).
	for _, s := range m[constants.RoleAdmin] {
		if s.module == constants.ModuleOrganizations {
			t.Error("ADMIN must not receive organization-management grants")
		}
	}
}

// --- Service helpers ---------------------------------------------------------

func TestIsReservedRoleName(t *testing.T) {
	if !isReservedRoleName("super_admin") {
		t.Error("system role names must be reserved case-insensitively")
	}
	if !isReservedRoleName(constants.RoleAdmin) {
		t.Error("exact system role name must be reserved")
	}
	if isReservedRoleName("Regional Manager") {
		t.Error("a non-system name must not be reserved")
	}
}

func TestDedupeIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := dedupeIDs([]uuid.UUID{a, a, b, b, a})
	if !reflect.DeepEqual(got, []uuid.UUID{a, b}) {
		t.Fatalf("dedupeIDs = %v, want first-seen order [a b]", got)
	}
	if dedupeIDs(nil) != nil {
		t.Error("dedupeIDs(nil) should be nil")
	}
}

func TestCapitalizeAndStrPtr(t *testing.T) {
	if capitalize("create") != "Create" {
		t.Error("capitalize should upper-case the first letter")
	}
	if capitalize("") != "" {
		t.Error("capitalize of empty string should be empty")
	}
	if strPtr("") != nil {
		t.Error(`strPtr("") should be nil so it stores as SQL NULL`)
	}
	if p := strPtr("x"); p == nil || *p != "x" {
		t.Error(`strPtr("x") should point to "x"`)
	}
}

// --- Branch-scoped role enforcement tests ------------------------------------

// newLoadedWithBranches is like newLoaded but also populates the roleBranches
// map on the snapshot so the enforcer can apply branch-scope filtering (ADR §22).
func newLoadedWithBranches(grouping map[string]map[string]map[string]int, policy map[string][]grant, roleBranches map[string]map[string]map[string]*uuid.UUID) *Enforcer {
	e := NewEnforcer(nil, nil)
	e.snap.Store(&snapshot{grouping: grouping, roleBranches: roleBranches, policy: policy})
	return e
}

// ctxWithBranches returns a request context whose effective branch scope is ids.
// Callers must pass the resulting context into Enforce (or ResolveAccessScoped).
func ctxWithBranches(ctx context.Context, ids ...uuid.UUID) context.Context {
	if len(ids) == 0 {
		return ctx
	}
	return appctx.WithPrincipal(ctx, appctx.Principal{BranchIDs: ids})
}

func TestEnforce_BranchScopedRoleDeniesOutsideScope(t *testing.T) {
	b1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	b2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {"CASHIER": 10}},
	}
	policy := map[string][]grant{
		"CASHIER": {{dom: "org1", obj: "sales", act: "create"}},
	}
	br := map[string]map[string]map[string]*uuid.UUID{
		"u1": {"org1": {"CASHIER": &b2}}, // CASHIER narrowed to branch b2
	}
	e := newLoadedWithBranches(grouping, policy, br)

	// Request in branch b2: should grant
	ctxB2 := ctxWithBranches(context.Background(), b2)
	ok, err := e.Enforce(ctxB2, "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("branch-scoped role should grant when request scope includes its branch")
	}

	// Request in branch b1: should deny (CASHIER is not scoped to b1)
	ctxB1 := ctxWithBranches(context.Background(), b1)
	ok, err = e.Enforce(ctxB1, "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("branch-scoped role must deny when request scope does not include its branch")
	}

	// Org-wide request (no scope): should deny (branch-scoped roles are not org-wide)
	ok, err = e.Enforce(context.Background(), "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("branch-scoped role must deny on an org-wide (nil scope) request")
	}
}

func TestEnforce_OrgWideRoleGrantsInAnyScope(t *testing.T) {
	b2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {"SUPER_ADMIN": 1000}},
	}
	policy := map[string][]grant{
		constants.RoleSuperAdmin: {{dom: "*", obj: "sales", act: "create"}},
	}
	br := map[string]map[string]map[string]*uuid.UUID{
		"u1": {"org1": {constants.RoleSuperAdmin: nil}}, // org-wide (branch_id IS NULL)
	}
	e := newLoadedWithBranches(grouping, policy, br)

	// Org-wide request: grants
	ok, err := e.Enforce(context.Background(), "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("org-wide role should grant on an org-wide (nil scope) request")
	}

	// Request in branch b2: also grants (org-wide roles apply in every branch scope)
	ctxB2 := ctxWithBranches(context.Background(), b2)
	ok, err = e.Enforce(ctxB2, "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("org-wide role should grant even when the request scope narrows to one branch")
	}
}

func TestEnforce_MixedOrgWideAndBranchScopedRoles(t *testing.T) {
	b2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {constants.RoleAdmin: 100, "CASHIER": 10}},
	}
	policy := map[string][]grant{
		constants.RoleAdmin: {{dom: "*", obj: "orgs", act: "view"}},
		"CASHIER":           {{dom: "org1", obj: "sales", act: "create"}},
	}
	br := map[string]map[string]map[string]*uuid.UUID{
		"u1": {"org1": {constants.RoleAdmin: nil, "CASHIER": &b2}},
	}
	e := newLoadedWithBranches(grouping, policy, br)

	// Org-wide request: ADMIN grants orgs:view; CASHIER denied (branch-scoped, not in scope)
	ok, err := e.Enforce(context.Background(), "u1", "org1", "orgs", "view")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("org-wide role should grant on a nil scope request")
	}
	ok, err = e.Enforce(context.Background(), "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("branch-scoped role must deny on an org-wide (nil scope) request")
	}

	// Request in branch b2: ADMIN still grants orgs:view; CASHIER now grants sales:create
	ctxB2 := ctxWithBranches(context.Background(), b2)
	ok, err = e.Enforce(ctxB2, "u1", "org1", "sales", "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("branch-scoped role should grant when the request scope includes its branch")
	}
}

func TestResolveAccessScoped_FiltersBranchScopedRoles(t *testing.T) {
	b2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {constants.RoleAdmin: 100, "CASHIER": 10}},
	}
	policy := map[string][]grant{
		constants.RoleAdmin: {{dom: "*", obj: "orgs", act: "view"}},
		"CASHIER":           {{dom: "org1", obj: "sales", act: "create"}},
	}
	br := map[string]map[string]map[string]*uuid.UUID{
		"u1": {"org1": {constants.RoleAdmin: nil, "CASHIER": &b2}},
	}
	e := newLoadedWithBranches(grouping, policy, br)

	// Org-wide scope (nil): only ADMIN perms present; canonical role = ADMIN
	acc := e.ResolveAccessScoped("org1", "u1", nil)
	if acc.RoleName != constants.RoleAdmin {
		t.Errorf("org-wide canonical role = %q, want ADMIN", acc.RoleName)
	}
	if acc.IsSuperAdmin {
		t.Error("IsSuperAdmin should be false for ADMIN (not SUPER_ADMIN)")
	}
	want := []string{"orgs:view"}
	if !reflect.DeepEqual(acc.Permissions, want) {
		t.Errorf("org-wide permissions = %v, want %v", acc.Permissions, want)
	}

	// Branch b2 scope: ADMIN + CASHIER perms; canonical role still ADMIN (higher priority)
	accB2 := e.ResolveAccessScoped("org1", "u1", []uuid.UUID{b2})
	if accB2.RoleName != constants.RoleAdmin {
		t.Errorf("branch-scoped canonical role = %q, want ADMIN", accB2.RoleName)
	}
	wantB2 := []string{"orgs:view", "sales:create"}
	if !reflect.DeepEqual(accB2.Permissions, wantB2) {
		t.Errorf("branch-scoped permissions = %v, want %v", accB2.Permissions, wantB2)
	}
}

func TestResolveAccess_OrgWideExcludesBranchScopedRoles(t *testing.T) {
	b2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	grouping := map[string]map[string]map[string]int{
		"u1": {"org1": {constants.RoleAdmin: 100, "CASHIER": 10}},
	}
	policy := map[string][]grant{
		constants.RoleAdmin: {{dom: "*", obj: "orgs", act: "view"}},
		"CASHIER":           {{dom: "org1", obj: "sales", act: "create"}},
	}
	br := map[string]map[string]map[string]*uuid.UUID{
		"u1": {"org1": {constants.RoleAdmin: nil, "CASHIER": &b2}},
	}
	e := newLoadedWithBranches(grouping, policy, br)

	// ResolveAccess (org-wide): only ADMIN perms; CASHIER excluded
	acc := e.ResolveAccess("org1", "u1")
	want := []string{"orgs:view"}
	if !reflect.DeepEqual(acc.Permissions, want) {
		t.Errorf("org-wide ResolveAccess permissions = %v, want %v", acc.Permissions, want)
	}
}

func TestBranchToken_Determinism(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	b := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	// Org-wide sentinel
	if got := branchToken(nil); got != "*" {
		t.Errorf("branchToken(nil) = %q, want \"*\"", got)
	}
	if got := branchToken([]uuid.UUID{}); got != "*" {
		t.Errorf("branchToken([]) = %q, want \"*\"", got)
	}

	// Deterministic regardless of input order
	key1 := branchToken([]uuid.UUID{a, b})
	key2 := branchToken([]uuid.UUID{b, a})
	if key1 != key2 {
		t.Errorf("branchToken([a,b]) = %q != branchToken([b,a]) = %q (not deterministic)", key1, key2)
	}
	if key1 == "*" {
		t.Error("branchToken with real ids must not equal the org-wide sentinel")
	}
}
