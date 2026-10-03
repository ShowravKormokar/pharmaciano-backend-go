package masterdata

// Tests for the query-building mechanics shared by all eleven resources. None of
// these need a database: they assert the shape and safety of the SQL the repository
// assembles, which is where the only injectable surface in this package lives.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

func TestBuildWhereAlwaysExcludesDeleted(t *testing.T) {
	// Every read and list goes through this helper, so a soft-deleted row must be
	// unreachable even when no other filter is set.
	where, args := buildWhere(routeTable, ListFilter{})
	if where != "deleted_at IS NULL" {
		t.Errorf("where = %q, want the soft-delete guard only", where)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

// TestBuildWhereBindsEveryValue is the SQL-injection test for this package.
//
// The rule is that no filter value is ever concatenated into the SQL: each becomes a
// bind parameter, so a client cannot end the predicate. The WHERE strings below are
// asserted exactly so that a future "optimisation" that interpolates a value — or
// reorders the filters and breaks the $n numbering — fails here.
func TestBuildWhereBindsEveryValue(t *testing.T) {
	parent := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	yes, no := true, false

	cases := []struct {
		name     string
		spec     tableSpec
		filter   ListFilter
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "no filters",
			spec:     routeTable,
			filter:   ListFilter{},
			wantSQL:  "deleted_at IS NULL",
			wantArgs: []any{},
		},
		{
			name:     "is_active false is a filter, not an absence",
			spec:     routeTable,
			filter:   ListFilter{IsActive: &no},
			wantSQL:  "deleted_at IS NULL AND is_active = $1",
			wantArgs: []any{false},
		},
		{
			name:     "search term is wrapped and bound",
			spec:     routeTable,
			filter:   ListFilter{Q: "tablet"},
			wantSQL:  `deleted_at IS NULL AND (name ILIKE $1 ESCAPE '\' OR code ILIKE $1 ESCAPE '\')`,
			wantArgs: []any{"%tablet%"},
		},
		{
			name:   "filters stack and keep argument numbering in order",
			spec:   routeTable,
			filter: ListFilter{IsActive: &yes, Q: "oral"},
			wantSQL: `deleted_at IS NULL AND is_active = $1 ` +
				`AND (name ILIKE $2 ESCAPE '\' OR code ILIKE $2 ESCAPE '\')`,
			wantArgs: []any{true, "%oral%"},
		},
		{
			name:     "a quote in the search term stays a parameter value",
			spec:     routeTable,
			filter:   ListFilter{Q: `'; DROP TABLE routes; --`},
			wantSQL:  `deleted_at IS NULL AND (name ILIKE $1 ESCAPE '\' OR code ILIKE $1 ESCAPE '\')`,
			wantArgs: []any{"%'; DROP TABLE routes; --%"},
		},
		{
			name:     "whitespace-only search is not a filter",
			spec:     routeTable,
			filter:   ListFilter{Q: "   \t "},
			wantSQL:  "deleted_at IS NULL",
			wantArgs: []any{},
		},
		{
			name:     "parent filter on a hierarchy",
			spec:     productCategoryTable,
			filter:   ListFilter{ParentID: &parent},
			wantSQL:  "deleted_at IS NULL AND parent_id = $1",
			wantArgs: []any{parent},
		},
		{
			name:     "parent filter uses the resource's own column",
			spec:     therapeuticClassTable,
			filter:   ListFilter{ParentID: &parent},
			wantSQL:  "deleted_at IS NULL AND drug_class_id = $1",
			wantArgs: []any{parent},
		},
		{
			name:     "root filter binds no argument",
			spec:     productCategoryTable,
			filter:   ListFilter{ParentIsNull: &yes},
			wantSQL:  "deleted_at IS NULL AND parent_id IS NULL",
			wantArgs: []any{},
		},
		{
			name:   "root filter wins over parent filter",
			spec:   productCategoryTable,
			filter: ListFilter{ParentIsNull: &yes, ParentID: &parent},
			// Combining the two would match nothing, which is a confusing way to
			// answer a question; the more specific filter wins instead.
			wantSQL:  "deleted_at IS NULL AND parent_id IS NULL",
			wantArgs: []any{},
		},
		{
			name:    "parent filter false means no root filter",
			spec:    productCategoryTable,
			filter:  ListFilter{ParentIsNull: &no},
			wantSQL: "deleted_at IS NULL",
			// ParentIsNull=false is not "parent_id IS NOT NULL" — it means the
			// caller did not ask for roots, so no parent predicate is added at all.
			wantArgs: []any{},
		},
		{
			name:     "parent filter ignored where no parent is exposed",
			spec:     dosageFormTable,
			filter:   ListFilter{ParentID: &parent},
			wantSQL:  "deleted_at IS NULL",
			wantArgs: []any{},
		},
		{
			name:   "every filter together",
			spec:   productCategoryTable,
			filter: ListFilter{IsActive: &yes, Q: "analgesic", ParentID: &parent},
			wantSQL: `deleted_at IS NULL AND is_active = $1 ` +
				`AND (name ILIKE $2 ESCAPE '\' OR code ILIKE $2 ESCAPE '\') ` +
				`AND parent_id = $3`,
			wantArgs: []any{true, "%analgesic%", parent},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := buildWhere(tc.spec, tc.filter)
			if gotSQL != tc.wantSQL {
				t.Errorf("where\n got: %s\nwant: %s", gotSQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args\n got: %#v\nwant: %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

// TestBuildWhereNeverInterpolates checks the invariant rather than the examples: no
// quoted literal from a filter may appear in the SQL text. Every value must be a
// bind parameter.
func TestBuildWhereNeverInterpolates(t *testing.T) {
	hostile := []string{
		"x' OR '1'='1",
		`"; DELETE FROM routes; --`,
		`tab	separated`,
		`%`,
	}
	for _, q := range hostile {
		sql, args := buildWhere(routeTable, ListFilter{Q: q})
		if len(args) != 1 {
			t.Errorf("term %q produced %d args, want the term bound once", q, len(args))
		}
		if strings.Contains(sql, "DELETE") || strings.Contains(sql, "1'='1") {
			t.Errorf("term %q leaked into the SQL: %s", q, sql)
		}
	}
}

// TestBuildOrderByOnlyUsesAllowListedColumns is the ORDER BY injection test. Sort
// strings are the one piece of client input that becomes SQL identifiers, so the
// allow-list has to be airtight and the default has to be the fallback.
func TestBuildOrderByOnlyUsesAllowListedColumns(t *testing.T) {
	cases := []struct {
		name string
		spec tableSpec
		sort string
		want string
	}{
		{
			name: "empty falls back to the deterministic default",
			spec: routeTable, sort: "",
			want: "ORDER BY code ASC, id ASC",
		},
		{
			name: "single ascending",
			spec: routeTable, sort: "name",
			want: "ORDER BY name ASC, id ASC",
		},
		{
			name: "leading dash is descending",
			spec: routeTable, sort: "-name",
			want: "ORDER BY name DESC, id ASC",
		},
		{
			name: "multiple fields keep the client's order",
			spec: routeTable, sort: "-created_at,name",
			want: "ORDER BY created_at DESC, name ASC, id ASC",
		},
		{
			name: "id tie-break is always appended",
			spec: routeTable, sort: "name",
			want: "ORDER BY name ASC, id ASC",
		},
		{
			name: "repeated field is deduplicated",
			spec: routeTable, sort: "name,name,code",
			want: "ORDER BY name ASC, code ASC, id ASC",
		},
		{
			name: "resource-specific column is allowed",
			spec: productCategoryTable, sort: "-sort_order",
			want: "ORDER BY sort_order DESC, id ASC",
		},
		{
			name: "sort_order belongs to no other resource",
			spec: routeTable, sort: "sort_order",
			want: "ORDER BY code ASC, id ASC",
		},
		{
			name: "tax rate specific columns",
			spec: taxRateTable, sort: "-rate_percent,is_default",
			want: "ORDER BY rate_percent DESC, is_default ASC, id ASC",
		},
		{
			name: "generic medicine specific column",
			spec: genericMedicineTable, sort: "is_controlled",
			want: "ORDER BY is_controlled ASC, id ASC",
		},
		{
			name: "unknown column is dropped",
			spec: routeTable, sort: "deleted_at",
			want: "ORDER BY code ASC, id ASC",
		},
		{
			name: "injection attempt is dropped",
			spec: routeTable, sort: "name; DROP TABLE routes",
			want: "ORDER BY code ASC, id ASC",
		},
		{
			name: "parent column is not sortable",
			spec: productCategoryTable, sort: "parent_id",
			want: "ORDER BY code ASC, id ASC",
		},
		{
			name: "too many fields fall back rather than error",
			spec: routeTable, sort: "code,name,created_at,is_active,code,name",
			want: "ORDER BY code ASC, id ASC",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildOrderBy(tc.spec, tc.sort); got != tc.want {
				t.Errorf("buildOrderBy(%q)\n got: %s\nwant: %s", tc.sort, got, tc.want)
			}
		})
	}
}

// TestLikePatternEscapesMetacharacters guards the reason likePattern exists. An
// unescaped "%" from a client would match a large part of the catalogue and push
// the query past the trigram index into a sequential scan.
func TestLikePatternEscapesMetacharacters(t *testing.T) {
	cases := map[string]string{
		"tablet":   `%tablet%`,
		"50%":      `%50\%%`,
		"a_b":      `%a\_b%`,
		`back\sl`:  `%back\\sl%`,
		"100%_x":   `%100\%\_x%`,
		"":         `%%`,
		"parens()": `%parens()%`,
	}
	for in, want := range cases {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeQReappliesTheLengthBound checks the persistence-boundary clamp. The
// DTO already normalises ?q=, but the repository must not depend on that.
func TestNormalizeQReappliesTheLengthBound(t *testing.T) {
	if got := normalizeQ("  padded  "); got != "padded" {
		t.Errorf("normalizeQ did not trim: %q", got)
	}
	if got := normalizeQ("   "); got != "" {
		t.Errorf("normalizeQ(%q) = %q, want empty", "   ", got)
	}

	long := strings.Repeat("x", pagination.MaxQueryLen+50)
	got := normalizeQ(long)
	if len(got) != pagination.MaxQueryLen {
		t.Errorf("length = %d, want the %d cap", len(got), pagination.MaxQueryLen)
	}

	// Exactly at the bound must pass through untouched.
	atBound := strings.Repeat("y", pagination.MaxQueryLen)
	if got := normalizeQ(atBound); got != atBound {
		t.Errorf("a term exactly at the cap was altered")
	}
}

// TestPageBoundsClamps covers the defensive clamp. Phase 03 normalises the DTO, but
// LIMIT -1 is invalid SQL and an unbounded limit reaches make() as a capacity, so
// the repository has to be safe on its own.
func TestPageBoundsClamps(t *testing.T) {
	cases := []struct {
		name       string
		in         pagination.Offset
		wantLimit  int
		wantOffset int
	}{
		{
			name:       "zero limit falls back to the default",
			in:         pagination.Offset{Page: 1, Limit: 0},
			wantLimit:  pagination.DefaultLimit,
			wantOffset: 0,
		},
		{
			name:       "negative limit falls back to the default",
			in:         pagination.Offset{Page: 1, Limit: -5},
			wantLimit:  pagination.DefaultLimit,
			wantOffset: 0,
		},
		{
			name:       "oversized limit is capped",
			in:         pagination.Offset{Page: 1, Limit: 100000},
			wantLimit:  pagination.MaxLimit,
			wantOffset: 0,
		},
		{
			name:       "offset follows the capped limit, not the raw one",
			in:         pagination.Offset{Page: 10, Limit: 100000},
			wantLimit:  pagination.MaxLimit,
			wantOffset: 9 * pagination.MaxLimit,
		},
		{
			name:       "offset follows the default limit when none was given",
			in:         pagination.Offset{Page: 3, Limit: 0},
			wantLimit:  pagination.DefaultLimit,
			wantOffset: 2 * pagination.DefaultLimit,
		},
		{
			name:       "an oversized limit with a small page still tiles correctly",
			in:         pagination.Offset{Page: 2, Limit: 5000},
			wantLimit:  pagination.MaxLimit,
			wantOffset: pagination.MaxLimit,
		},
		{
			name:       "valid limit and page",
			in:         pagination.Offset{Page: 3, Limit: 20},
			wantLimit:  20,
			wantOffset: 40,
		},
		{
			name:       "page below one is treated as the first page",
			in:         pagination.Offset{Page: -2, Limit: 20},
			wantLimit:  20,
			wantOffset: 0,
		},
		{
			name:       "last page is not clamped away",
			in:         pagination.Offset{Page: 1000, Limit: 50},
			wantLimit:  50,
			wantOffset: 49950,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := pageBounds(tc.in)
			if limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tc.wantLimit)
			}
			if offset != tc.wantOffset {
				t.Errorf("offset = %d, want %d", offset, tc.wantOffset)
			}
		})
	}
}

// TestEverySpecUsesTheSharedDefaultOrder documents that no resource invented its own
// ordering. A per-resource default would be a quiet API inconsistency, since
// ?sort= is documented as the same across the eleven endpoints.
func TestEverySpecUsesTheSharedDefaultOrder(t *testing.T) {
	for table, spec := range allSpecs {
		if spec.defaultOrder != defaultOrder {
			t.Errorf("%s defaultOrder = %q, want the shared %q",
				table, spec.defaultOrder, defaultOrder)
		}
	}
}

// TestEverySpecIsWiredToSortAllowLists keeps each spec pointed at its own Phase 03
// allow-list. Pointing two resources at the same map would let ?sort= name a column
// the other resource does not have, producing invalid SQL.
func TestEverySpecIsWiredToSortAllowLists(t *testing.T) {
	// Resources whose allow-list differs from the base set.
	want := map[string]int{
		"product_categories":  5,
		"tax_rates":           6,
		"generic_medicines":   5,
		"dosage_forms":        4,
		"routes":              4,
		"package_types":       4,
		"unit_types":          4,
		"storage_conditions":  4,
		"drug_groups":         4,
		"drug_classes":        4,
		"therapeutic_classes": 4,
	}
	for table, spec := range allSpecs {
		if got := len(spec.sortColumns); got != want[table] {
			t.Errorf("%s allow-list has %d fields, want %d", table, got, want[table])
		}
	}
}
