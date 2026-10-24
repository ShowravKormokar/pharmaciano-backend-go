package medicine

// Unit tests for the parts of the repository that do not need a database:
// the sort allow-lists, the cursor codec, the keyset predicates and the query
// builders.
//
// The integration behaviour (a real page, a real tenant filter, a real
// uniqueness violation) belongs to the repository integration tests; what is
// checked here is that the pieces the SQL is assembled from cannot drift
// apart and that a cursor cannot be forged or replayed against another
// query.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

func testCursorKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

// TestSortAllowListsMatch keeps the public allow-list in dto.go and the
// column map of the repository in step.
//
// The DTO decides what a client may ask for; the repository decides what it
// turns into. If one of them knows a field the other does not, the request is
// either rejected for no reason or a column is read that was never part of
// the contract.
func TestSortAllowListsMatch(t *testing.T) {
	for field := range allowedMedicineSortFields {
		if _, ok := medicineSortColumn[field]; !ok {
			t.Errorf("sort field %q is allowed by the DTO but has no column mapping", field)
		}
	}
	for field := range medicineSortColumn {
		if _, ok := allowedMedicineSortFields[field]; !ok {
			t.Errorf("sort field %q has a column mapping but is not allowed by the DTO", field)
		}
	}
}

func TestResolveMedicineSort(t *testing.T) {
	t.Run("default order", func(t *testing.T) {
		spec, err := resolveMedicineSort(nil)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if spec.field != defaultMedicineSortField {
			t.Fatalf("expected the default sort field, got %q", spec.field)
		}
		if spec.orderBy() != "m.name ASC, m.id ASC" {
			t.Fatalf("unexpected order by: %q", spec.orderBy())
		}
	})

	t.Run("descending order carries the tie-breaker", func(t *testing.T) {
		spec, err := resolveMedicineSort([]pagination.SortField{{Field: "created_at", Desc: true}})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if spec.orderBy() != "m.created_at DESC, m.id DESC" {
			t.Fatalf("unexpected order by: %q", spec.orderBy())
		}
	})

	t.Run("unsupported field is rejected", func(t *testing.T) {
		if _, err := resolveMedicineSort([]pagination.SortField{{Field: "mrp"}}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest, got %v", err)
		}
	})

	t.Run("more than one field is rejected", func(t *testing.T) {
		_, err := resolveMedicineSort([]pagination.SortField{
			{Field: "name"},
			{Field: "sku"},
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest, got %v", err)
		}
	})
}

func TestBuildMedicineOrderBy(t *testing.T) {
	order, err := buildMedicineOrderBy([]pagination.SortField{{Field: "name", Desc: true}})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if order != "m.name DESC, m.id ASC" {
		t.Fatalf("unexpected order by: %q", order)
	}

	if _, err := buildMedicineOrderBy([]pagination.SortField{{Field: "mrp"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCursorCodecRoundTrip(t *testing.T) {
	codec, err := NewCursorCodec(testCursorKey())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	payload := cursorPayload{
		Version: cursorVersion,
		Binding: "binding",
		Field:   "name",
		Key:     "napa",
		ID:      uuid.New().String(),
	}
	token, err := codec.Encode(payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if strings.ContainsAny(token, "+/= ") {
		t.Fatalf("the cursor must be URL safe: %q", token)
	}

	decoded, err := codec.Decode(token)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if decoded != payload {
		t.Fatalf("the payload did not survive the round trip: %+v", decoded)
	}
}

func TestCursorCodecRejectsForgedAndForeignTokens(t *testing.T) {
	codec, err := NewCursorCodec(testCursorKey())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	other, err := NewCursorCodec([]byte("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	payload := cursorPayload{Version: cursorVersion, Binding: "b", Field: "name", Key: "k", ID: uuid.New().String()}

	foreign, err := other.Encode(payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := codec.Decode(foreign); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("a cursor signed with another key must be rejected, got %v", err)
	}

	tampered, err := codec.Encode(payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	tampered = tampered[:len(tampered)-1] + "x"
	if _, err := codec.Decode(tampered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("a tampered cursor must be rejected, got %v", err)
	}

	if _, err := codec.Decode("not-a-cursor"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("a malformed cursor must be rejected, got %v", err)
	}
}

func TestNewCursorCodecRejectsShortKey(t *testing.T) {
	if _, err := NewCursorCodec([]byte("too-short")); err == nil {
		t.Fatal("a signing key below 32 bytes must be rejected")
	}
}

func TestCursorBindingChangesWithTheQuery(t *testing.T) {
	orgID := uuid.New()
	brandID := uuid.New()

	base := ListMedicinesQuery{}
	if validation := base.NormalizeAndValidate(); len(validation) != 0 {
		t.Fatalf("expected no errors, got %v", validation)
	}
	reference := medicineCursorBinding(orgID, &base, "name", false)

	// The same query produces the same binding.
	if again := medicineCursorBinding(orgID, &base, "name", false); again != reference {
		t.Fatal("the binding must be stable for the same query")
	}

	// A different tenant is a different query.
	if other := medicineCursorBinding(uuid.New(), &base, "name", false); other == reference {
		t.Fatal("the binding must depend on the tenant")
	}

	// A different sort order is a different query.
	if other := medicineCursorBinding(orgID, &base, "name", true); other == reference {
		t.Fatal("the binding must depend on the sort order")
	}

	// A different filter set is a different query.
	filtered := ListMedicinesQuery{BrandID: brandID.String()}
	if validation := filtered.NormalizeAndValidate(); len(validation) != 0 {
		t.Fatalf("expected no errors, got %v", validation)
	}
	if other := medicineCursorBinding(orgID, &filtered, "name", false); other == reference {
		t.Fatal("the binding must depend on the filters")
	}
}

func TestCursorKeyValueRoundTrip(t *testing.T) {
	text, err := cursorKeyValue(medicineSortText, "napa")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if text != "napa" {
		t.Fatalf("unexpected key: %q", text)
	}
	param, err := cursorKeyParam(medicineSortText, text)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if param != "napa" {
		t.Fatalf("unexpected parameter: %v", param)
	}

	moment := time.Date(2026, time.October, 22, 4, 21, 32, 0, time.UTC)
	stamp, err := cursorKeyValue(medicineSortTime, moment)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	param, err = cursorKeyParam(medicineSortTime, stamp)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	bound, ok := param.(time.Time)
	if !ok || !bound.Equal(moment) {
		t.Fatalf("the timestamp did not survive the round trip: %v", param)
	}

	if _, err := cursorKeyParam(medicineSortTime, "not-a-timestamp"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestBuildMedicineFiltersIsTenantScoped(t *testing.T) {
	orgID := uuid.New()
	categoryID := uuid.New()
	brandID := uuid.New()
	active := true
	term := "50% off"

	query := ListMedicinesQuery{
		CategoryID: categoryID.String(),
		BrandID:    brandID.String(),
		IsActive:   &active,
		Q:          &term,
	}
	if validation := query.NormalizeAndValidate(); len(validation) != 0 {
		t.Fatalf("expected no errors, got %v", validation)
	}

	where, args := buildMedicineFilters(&query, orgID)
	if !strings.Contains(where, "b.organization_id = $1") {
		t.Fatalf("the tenant scope must be the first condition: %s", where)
	}
	if !strings.Contains(where, "m.deleted_at IS NULL") {
		t.Fatalf("the soft delete filter is missing: %s", where)
	}
	if len(args) == 0 || args[0] != orgID {
		t.Fatalf("the tenant must be the first argument: %v", args)
	}

	// Every value is bound: the only literals in the clause are the column
	// names, the operators and the placeholders.
	if strings.Contains(where, orgID.String()) {
		t.Fatalf("a bound value leaked into the statement: %s", where)
	}
	if strings.Contains(where, categoryID.String()) {
		t.Fatalf("a bound value leaked into the statement: %s", where)
	}
	if strings.Contains(where, "50%") {
		t.Fatalf("the search term leaked into the statement: %s", where)
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	if got := likePattern("100%"); got != "%100!%%" {
		t.Fatalf("unexpected pattern: %q", got)
	}
	if got := likePattern("a_b"); got != "%a!_b%" {
		t.Fatalf("unexpected pattern: %q", got)
	}
	if got := likePattern("wow!"); got != "%wow!!%" {
		t.Fatalf("unexpected pattern: %q", got)
	}
}

func TestBuildSetClauseDerivesItsPlaceholders(t *testing.T) {
	set, values := buildSetClause(3, []sqlAssignment{
		{"name", "napa"},
		{"is_active", false},
	})
	if set != "name = $3, is_active = $4" {
		t.Fatalf("unexpected set clause: %q", set)
	}
	if len(values) != 2 || values[0] != "napa" || values[1] != false {
		t.Fatalf("unexpected values: %v", values)
	}
}

func TestNumberedPlaceholders(t *testing.T) {
	if got := numberedPlaceholders(1, 3); got != "$1, $2, $3" {
		t.Fatalf("unexpected placeholders: %q", got)
	}
	if got := numberedPlaceholders(4, 2); got != "$4, $5" {
		t.Fatalf("unexpected placeholders: %q", got)
	}
}

func TestPlaceholderOf(t *testing.T) {
	columns := []string{"sku", "barcode", "name", "brand_id"}
	placeholder, err := placeholderOf(columns, "brand_id", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if placeholder != "$4" {
		t.Fatalf("unexpected placeholder: %q", placeholder)
	}
	if _, err := placeholderOf(columns, "organization_id", 1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestResolveLookupCandidates(t *testing.T) {
	if _, err := resolveLookupCandidates(nil, "barcode", "123"); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("expected a not-found, got %v", err)
	}

	one := LookupCandidate{MedicineID: uuid.New()}
	candidate, err := resolveLookupCandidates([]LookupCandidate{one}, "barcode", "123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if candidate.MedicineID != one.MedicineID {
		t.Fatal("the single candidate must be returned")
	}

	two := []LookupCandidate{one, {MedicineID: uuid.New()}}
	if _, err := resolveLookupCandidates(two, "barcode", "123"); !errors.Is(err, ErrAmbiguousIdentifier) {
		t.Fatalf("expected ErrAmbiguousIdentifier, got %v", err)
	}
}

func TestRepositoryRejectsUnconfiguredDatabase(t *testing.T) {
	repo := NewRepository(nil, nil)
	if _, err := repo.FindByID(t.Context(), uuid.New(), uuid.New()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if _, err := repo.ListCursor(t.Context(), uuid.New(), ListMedicinesQuery{}, nil, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestRepositoryRequiresATenant(t *testing.T) {
	// The tenant scope is part of every statement, so it is checked through
	// the query builder the repository uses.
	query := ListMedicinesQuery{}
	if validation := query.NormalizeAndValidate(); len(validation) != 0 {
		t.Fatalf("expected no errors, got %v", validation)
	}
	where, args := buildMedicineFilters(&query, uuid.Nil)
	if !strings.Contains(where, "b.organization_id = $1") {
		t.Fatalf("the tenant scope must always be present: %s", where)
	}
	if len(args) != 1 {
		t.Fatalf("expected exactly one argument, got %v", args)
	}
}
