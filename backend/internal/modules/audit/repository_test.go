package audit

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"backend/pkg/pagination"
	"github.com/google/uuid"
)

func TestValidateAuditReadScope(t *testing.T) {
	orgID := uuid.New()
	branchA := uuid.New()
	branchB := uuid.New()

	tests := []struct {
		name  string
		scope AuditReadScope
		valid bool
	}{
		{name: "missing tenant scope", valid: false},
		{
			name:  "unrestricted tenant scope",
			scope: AuditReadScope{OrganizationID: &orgID, AllBranches: true},
			valid: true,
		},
		{
			name:  "branch-limited scope",
			scope: AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchA}},
			valid: true,
		},
		{
			name:  "unassigned branch only",
			scope: AuditReadScope{OrganizationID: &orgID, IncludeUnassignedBranch: true},
			valid: true,
		},
		{
			name:  "empty branch scope",
			scope: AuditReadScope{OrganizationID: &orgID},
			valid: false,
		},
		{
			name:  "platform wide",
			scope: AuditReadScope{PlatformWide: true},
			valid: true,
		},
		{
			name:  "platform events only",
			scope: AuditReadScope{PlatformEventsOnly: true},
			valid: true,
		},
		{
			name: "platform events cannot combine tenant scope",
			scope: AuditReadScope{
				OrganizationID: &orgID, PlatformEventsOnly: true,
			},
			valid: false,
		},
		{
			name:  "platform scope with tenant filter",
			scope: AuditReadScope{OrganizationID: &orgID, PlatformWide: true},
			valid: false,
		},
		{
			name:  "branch all plus explicit branches",
			scope: AuditReadScope{OrganizationID: &orgID, AllBranches: true, BranchIDs: []uuid.UUID{branchA}},
			valid: false,
		},
		{
			name:  "duplicate branch",
			scope: AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchA, branchA}},
			valid: false,
		},
		{
			name:  "zero branch",
			scope: AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{uuid.Nil}},
			valid: false,
		},
		{
			name:  "multiple branches",
			scope: AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchA, branchB}},
			valid: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAuditReadScope(test.scope)
			if (err == nil) != test.valid {
				t.Fatalf("validateAuditReadScope() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestBuildAuditWhereAppliesTimeTenantAndLiteralSearch(t *testing.T) {
	orgID := uuid.New()
	branchID := uuid.New()
	filters := AuditSearchFilters{
		Module: "inventory",
		Q:      `needle_%\`,
		From:   "2026-10-01T00:00:00Z",
		To:     "2026-10-02T00:00:00Z",
	}
	if errs := filters.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	where, args, err := buildAuditWhere(
		AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchID}},
		filters, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), nil,
	)
	if err != nil {
		t.Fatalf("buildAuditWhere() error = %v", err)
	}
	for _, fragment := range []string{
		"a.created_at >= $1", "a.created_at < $2",
		"a.organization_id = $3", "a.branch_id = ANY($4::uuid[])",
		"a.module = $5", "a.module ILIKE $6 ESCAPE",
		"a.request_id", "a.error_code",
	} {
		if !strings.Contains(where, fragment) {
			t.Errorf("where clause missing %q: %s", fragment, where)
		}
	}
	if got := args[5]; got != `%needle\_\%\\%` {
		t.Fatalf("escaped search pattern = %q", got)
	}
	if len(args) != 6 {
		t.Fatalf("argument count = %d, want 6", len(args))
	}
}

func TestBuildAuditWhereAddsTupleKeysetPredicate(t *testing.T) {
	filters := validAuditFilters()
	if errs := filters.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	cursor := &pagination.Cursor{
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		ID:        uuid.New(),
	}
	where, args, err := buildAuditWhere(
		AuditReadScope{PlatformWide: true},
		filters, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), cursor,
	)
	if err != nil {
		t.Fatalf("buildAuditWhere() error = %v", err)
	}
	if !strings.Contains(where, "(a.created_at, a.id) < ($3, $4)") {
		t.Fatalf("where clause missing stable keyset predicate: %s", where)
	}
	if len(args) != 4 || args[2] != cursor.CreatedAt || args[3] != cursor.ID {
		t.Fatalf("cursor args = %#v", args)
	}
}

func TestAuditCursorScopeKeyBindsScopeAndFiltersDeterministically(t *testing.T) {
	orgID := uuid.New()
	branchA := uuid.New()
	branchB := uuid.New()
	filters := validAuditFilters()
	if errs := filters.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	scopeA := AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchA, branchB}}
	scopeB := AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchB, branchA}}

	keyA, err := auditCursorScopeKey(scopeA, filters)
	if err != nil {
		t.Fatalf("auditCursorScopeKey() error = %v", err)
	}
	keyB, err := auditCursorScopeKey(scopeB, filters)
	if err != nil {
		t.Fatalf("auditCursorScopeKey() error = %v", err)
	}
	if keyA != keyB {
		t.Fatalf("branch ordering changed cursor binding: %q != %q", keyA, keyB)
	}

	otherOrg := uuid.New()
	differentScopeKey, err := auditCursorScopeKey(
		AuditReadScope{OrganizationID: &otherOrg, BranchIDs: []uuid.UUID{branchA, branchB}}, filters,
	)
	if err != nil {
		t.Fatalf("auditCursorScopeKey() error = %v", err)
	}
	if differentScopeKey == keyA {
		t.Fatal("cursor scope key did not bind organization")
	}

	filters.Module = "users"
	differentFilterKey, err := auditCursorScopeKey(scopeA, filters)
	if err != nil {
		t.Fatalf("auditCursorScopeKey() error = %v", err)
	}
	if differentFilterKey == keyA {
		t.Fatal("cursor scope key did not bind filters")
	}
}

func TestBuildScopeConditionsNeverCreatesUnscopedTenantQuery(t *testing.T) {
	if _, _, err := buildScopeConditions(AuditReadScope{}, 1); err == nil {
		t.Fatal("empty scope was accepted")
	}

	orgID := uuid.New()
	conditions, args, err := buildScopeConditions(
		AuditReadScope{OrganizationID: &orgID, IncludeUnassignedBranch: true}, 3,
	)
	if err != nil {
		t.Fatalf("buildScopeConditions() error = %v", err)
	}
	if !reflect.DeepEqual(conditions, []string{"a.organization_id = $3", "a.branch_id IS NULL"}) {
		t.Fatalf("conditions = %#v", conditions)
	}
	if len(args) != 1 || args[0] != orgID {
		t.Fatalf("scope args = %#v", args)
	}
}

func TestBuildAuditIDWhereIncludesPartitionKeyAndVisibilityScope(t *testing.T) {
	orgID := uuid.New()
	branchID := uuid.New()
	eventID := uuid.New()
	occurredAt := time.Date(2026, 10, 1, 12, 30, 0, 123456789, time.FixedZone("offset", 2*60*60))
	where, args, err := buildAuditIDWhere(
		AuditReadScope{OrganizationID: &orgID, BranchIDs: []uuid.UUID{branchID}},
		eventID,
		occurredAt,
	)
	if err != nil {
		t.Fatalf("buildAuditIDWhere() error = %v", err)
	}
	if !strings.Contains(where, "a.created_at = $2") ||
		!strings.Contains(where, "a.organization_id = $3") ||
		!strings.Contains(where, "a.branch_id = ANY($4::uuid[])") {
		t.Fatalf("detail predicate missing partition or visibility conditions: %s", where)
	}
	if len(args) != 4 || args[0] != eventID || !args[1].(time.Time).Equal(occurredAt) {
		t.Fatalf("detail args = %#v", args)
	}
}

func TestLikeAuditLiteralEscapesPatternMetacharacters(t *testing.T) {
	if got, want := likeAuditLiteral(`a%b_c\d`), `%a\%b\_c\\d%`; got != want {
		t.Fatalf("likeAuditLiteral() = %q, want %q", got, want)
	}
}
