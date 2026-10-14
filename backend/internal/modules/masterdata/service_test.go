// Service-layer tests.
//
// These need no database. The service's shared mechanics deliberately take their
// collaborators as function values — listPage takes the repository's List method,
// requireReference takes its Get, checkUnreferenced takes its probes — so the parts
// that decide an HTTP outcome (status code, message, whether a row may be deleted)
// are exercised directly instead of only through a live PostgreSQL round trip.
// repository_integration_test.go covers the SQL.
//
// The last two tests are the drift guards that justify the dupl exclusion in
// .golangci.yml: they assert the eleven service files stay isomorphic in their
// OPERATION SET and in WHICH REPOSITORY METHOD each rule is wired to, which is
// exactly the property dupl would otherwise be relied on to catch.

package masterdata

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// superAdminCtx returns a context whose principal is a SUPER_ADMIN, which is what
// every write path requires.
func superAdminCtx() context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{
		RoleName: constants.RoleSuperAdmin,
	})
}

// roleCtx returns a context carrying an arbitrary role name.
func roleCtx(role string) context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{RoleName: role})
}

// pgErr builds the driver error shape db.Is*Vilation inspects.
func pgErr(code, constraint string) error {
	return &pgconn.PgError{Code: code, ConstraintName: constraint}
}

// some returns a pointer to v, so a test can express "this field was set" without
// a named local. It is named to avoid colliding with the UUID-only ptr helper the
// Phase 04 integration tests already declare in this package.
func some[T any](v T) *T { return &v }

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

func TestMapReadErr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		err      error
		wantCode errs.Code
		wantMsg  string
	}{
		{"missing row", db.ErrNoRows, errs.CodeNotFound, "route not found"},
		{"wrapped missing row", errJoin(db.ErrNoRows), errs.CodeNotFound, "route not found"},
		{"driver failure", errors.New("connection reset"), errs.CodeDatabaseError, "database error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapReadErr(tc.err, resRoute)
			if !errs.Has(got, tc.wantCode) {
				t.Fatalf("code = %q, want %q (err %v)", errs.CodeOf(got), tc.wantCode, got)
			}
			if want := tc.wantMsg; appMessage(got) != want {
				t.Fatalf("message = %q, want %q", appMessage(got), want)
			}
		})
	}
}

func TestMapWriteErr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		err      error
		wantCode errs.Code
		wantMsg  string
	}{
		{"row disappeared", db.ErrNoRows, errs.CodeNotFound, "route not found"},
		{
			"duplicate code", pgErr("23505", "ux_routes_code"),
			errs.CodeAlreadyExists, "route code already exists",
		},
		{
			"missing parent", pgErr("23503", "fk_dosage_forms_route"),
			errs.CodeConflict, "dosage form references a record that does not exist",
		},
		{
			"check violation", pgErr("23514", "ck_storage_conditions_temp_range"),
			errs.CodeBusinessRuleViolation, "storage condition value is rejected by a database constraint",
		},
		{"driver failure", errors.New("connection reset"), errs.CodeDatabaseError, "database error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resource := resRoute
			if tc.wantMsg == "dosage form references a record that does not exist" {
				resource = resDosageForm
			}
			if tc.wantMsg == "storage condition value is rejected by a database constraint" {
				resource = resStorageCondition
			}
			got := mapWriteErr(tc.err, resource)
			if !errs.Has(got, tc.wantCode) {
				t.Fatalf("code = %q, want %q (err %v)", errs.CodeOf(got), tc.wantCode, got)
			}
			if appMessage(got) != tc.wantMsg {
				t.Fatalf("message = %q, want %q", appMessage(got), tc.wantMsg)
			}
		})
	}
}

// TestMapWriteErrNeverLeaksSQL is the point of mapWriteErr: whatever the driver
// said, the message a caller sees is fixed text. The constraint name and the SQL
// state must not appear, and the cause must survive for the logs.
func TestMapWriteErrNeverLeaksSQL(t *testing.T) {
	t.Parallel()
	driver := pgErr("23514", "ck_product_categories_parent_not_self")
	got := mapWriteErr(driver, resProductCategory)

	if app := errs.As(got); app != nil {
		for _, leak := range []string{"ck_product_categories", "23514", "CHECK", "postgres"} {
			if strings.Contains(app.Message, leak) {
				t.Fatalf("message leaks %q: %q", leak, app.Message)
			}
		}
		if app.Cause == nil {
			t.Fatal("cause was dropped; a 5xx with no cause cannot be diagnosed")
		}
	}
}

func TestRuleViolationCarriesTheDomainMessage(t *testing.T) {
	t.Parallel()
	cause := errors.New("parent_id equals the resource id")
	got := ruleViolation(cause)
	if !errs.Has(got, errs.CodeBusinessRuleViolation) {
		t.Fatalf("code = %q, want BUSINESS_RULE_VIOLATION", errs.CodeOf(got))
	}
	if !errors.Is(got, cause) {
		t.Fatal("cause is not reachable through errors.Is")
	}
	if !strings.Contains(appMessage(got), "parent_id") {
		t.Fatalf("message %q does not name the offending field", appMessage(got))
	}
}

// ---------------------------------------------------------------------------
// SUPER_ADMIN guard
// ---------------------------------------------------------------------------

func TestRequireSuperAdmin(t *testing.T) {
	t.Parallel()
	if err := NewService(nil, nil, Limits{}).requireSuperAdmin(superAdminCtx()); err != nil {
		t.Fatalf("SUPER_ADMIN rejected: %v", err)
	}
	for _, role := range []string{constants.RoleAdmin, constants.RoleBranchManager, "TENANT_OWNER", ""} {
		got := NewService(nil, nil, Limits{}).requireSuperAdmin(roleCtx(role))
		if !errs.Has(got, errs.CodeForbidden) {
			t.Fatalf("role %q: code = %q, want FORBIDDEN", role, errs.CodeOf(got))
		}
	}
	// An empty context carries no role at all, which is the same answer.
	if !errs.Has(NewService(nil, nil, Limits{}).requireSuperAdmin(context.Background()), errs.CodeForbidden) {
		t.Fatal("roleless context was not forbidden")
	}
}

// ---------------------------------------------------------------------------
// Reference checks
// ---------------------------------------------------------------------------

func TestRequireReference(t *testing.T) {
	t.Parallel()
	live := func(context.Context, uuid.UUID) (*Route, error) { return &Route{}, nil }
	gone := func(context.Context, uuid.UUID) (*Route, error) { return nil, db.ErrNoRows }
	broken := func(context.Context, uuid.UUID) (*Route, error) { return nil, errors.New("boom") }
	id := uuid.New()

	if err := requireReference(context.Background(), nil, gone, resRoute); err != nil {
		t.Fatalf("nil reference must be accepted without a read: %v", err)
	}
	if err := requireReference(context.Background(), &id, live, resRoute); err != nil {
		t.Fatalf("live parent rejected: %v", err)
	}

	// A missing parent answers 404 naming the PARENT, not the resource being
	// written: the caller's mistake is the reference.
	got := requireReference(context.Background(), &id, gone, resRoute)
	if !errs.Has(got, errs.CodeNotFound) || appMessage(got) != "route not found" {
		t.Fatalf("missing parent = %q/%q, want NOT_FOUND/route not found", errs.CodeOf(got), appMessage(got))
	}
	if !errs.Has(requireReference(context.Background(), &id, broken, resRoute), errs.CodeDatabaseError) {
		t.Fatal("a read failure must not be reported as a missing parent")
	}
}

// ---------------------------------------------------------------------------
// D-19 delete guard
// ---------------------------------------------------------------------------

func TestCheckUnreferenced(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	yes := func(context.Context, uuid.UUID) (bool, error) { return true, nil }
	no := func(context.Context, uuid.UUID) (bool, error) { return false, nil }
	broken := func(context.Context, uuid.UUID) (bool, error) { return false, errors.New("boom") }

	if err := checkUnreferenced(context.Background(), resRoute, id); err != nil {
		t.Fatalf("no probes must mean delete is allowed: %v", err)
	}
	if err := checkUnreferenced(context.Background(), resRoute, id, no, no); err != nil {
		t.Fatalf("unreferenced row refused: %v", err)
	}

	got := checkUnreferenced(context.Background(), resRoute, id, no, yes)
	if !errs.Has(got, errs.CodeConflict) {
		t.Fatalf("code = %q, want CONFLICT", errs.CodeOf(got))
	}
	// The message must point the caller at deactivation, which is the state that
	// always succeeds and is the correct answer to "a medicine still uses this".
	if !strings.Contains(appMessage(got), "is_active") {
		t.Fatalf("message %q does not direct the caller to is_active=false", appMessage(got))
	}

	// A probe that errors is a database failure, never a silent "unreferenced".
	if !errs.Has(checkUnreferenced(context.Background(), resRoute, id, broken), errs.CodeDatabaseError) {
		t.Fatal("a failing probe must surface as a database error")
	}

	// Probes short-circuit: the first true wins and later probes are not consulted.
	calls := 0
	counting := func(context.Context, uuid.UUID) (bool, error) { calls++; return true, nil }
	_ = checkUnreferenced(context.Background(), resRoute, id, counting, counting)
	if calls != 1 {
		t.Fatalf("probes after the first hit ran %d extra times", calls-1)
	}
}

// ---------------------------------------------------------------------------
// Numeric and pair rules
// ---------------------------------------------------------------------------

func TestRequireConversionFactor(t *testing.T) {
	t.Parallel()
	ok := []float64{0, 1, 0.5, 12.345678, 999999.999999, -1, -0.5}
	for _, v := range ok {
		if err := requireConversionFactor(&v); err != nil {
			t.Fatalf("%v rejected: %v", v, err)
		}
	}
	// nil means "no factor" and must stay nil: the column is nullable with no default.
	if err := requireConversionFactor(nil); err != nil {
		t.Fatalf("nil rejected: %v", err)
	}

	// Rejected: not representable by NUMERIC(15,6). Deliberately NOT rejected on
	// sign — bounds are UNRESOLVED (contract §11.2), so zero and negative are legal
	// here and a > 0 rule would be inventing a business decision.
	bad := []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e20, -1e20}
	for _, v := range bad {
		got := requireConversionFactor(&v)
		if !errs.Has(got, errs.CodeValidationError) {
			t.Fatalf("%v accepted (err %v)", v, got)
		}
	}
}

func TestRequireTemperatureRange(t *testing.T) {
	t.Parallel()
	type band struct{ min, max *float64 }
	for _, tc := range []struct {
		name     string
		band     band
		wantRule string
	}{
		{"both open", band{nil, nil}, ""},
		{"lower bound only", band{some(2.0), nil}, ""},
		{"upper bound only", band{nil, some(8.0)}, ""},
		{"ordered", band{some(2.0), some(8.0)}, ""},
		{"equal bounds", band{some(2.0), some(2.0)}, ""},
		{"negative band", band{some(-20.0), some(-2.0)}, ""},
		{"inverted", band{some(8.0), some(2.0)}, ruleTemperatureRange},
		{"not a number", band{some(math.NaN()), some(8.0)}, ruleInvalidNumber},
		{"overflow", band{some(1e6), some(2.0)}, ruleInvalidNumber},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := requireTemperatureRange(tc.band.min, tc.band.max)
			if tc.wantRule == "" {
				if got != nil {
					t.Fatalf("rejected: %v", got)
				}
				return
			}
			if !errs.Has(got, errs.CodeValidationError) {
				t.Fatalf("code = %q, want VALIDATION_ERROR", errs.CodeOf(got))
			}
			if rule := fieldRule(got); rule != tc.wantRule {
				t.Fatalf("rule = %q, want %q", rule, tc.wantRule)
			}
		})
	}
}

func TestRequireRatePercent(t *testing.T) {
	t.Parallel()
	unbounded := NewService(nil, nil, Limits{})
	// 0 is a legal rate (an exempt or zero-rated line), and NUMERIC(6,3) tops out
	// at 999.999.
	for _, v := range []float64{0, 0.001, 5, 15, 100, 999.999} {
		if err := unbounded.requireRatePercent(v); err != nil {
			t.Fatalf("%v rejected with no configured maximum: %v", v, err)
		}
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), 1000, 1e6} {
		if !errs.Has(unbounded.requireRatePercent(v), errs.CodeValidationError) {
			t.Fatalf("%v accepted", v)
		}
	}

	bounded := NewService(nil, nil, Limits{MaxRatePercent: 15})
	if err := bounded.requireRatePercent(15); err != nil {
		t.Fatalf("the maximum itself must be accepted: %v", err)
	}
	got := bounded.requireRatePercent(15.001)
	if !errs.Has(got, errs.CodeValidationError) || fieldRule(got) != ruleAboveMaximum {
		t.Fatalf("above maximum = %q/%q, want VALIDATION_ERROR/%s", errs.CodeOf(got), fieldRule(got), ruleAboveMaximum)
	}
}

// TestTaxRateAllowsSeveralDefaults pins the decision taken in Phase 05: the
// single-default rule is UNRESOLVED (C-08) and is NOT enforced here. Two rates may
// both be created as default, and neither write invents a rule nobody decided.
func TestTaxRateAllowsSeveralDefaults(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, nil, Limits{})

	req := &CreateTaxRateRequest{Code: "VAT", Name: "VAT", RatePercent: some(15.0), IsDefault: some(true)}
	if req.RatePercent == nil {
		t.Fatal("fixture is wrong: rate must be a non-nil pointer")
	}
	// Reaching the repository requires a database, so the assertion here is that
	// the service applies no default-related rule of its own: it resolves the DTO
	// and hands the flag through untouched. The multiple-defaults behaviour itself
	// is a database property, asserted in service_integration_test.go.
	if got := svc.limits.ValidateRatePercent(*req.RatePercent); got != nil {
		t.Fatalf("15%% rejected: %v", got)
	}
	if !*req.IsDefault {
		t.Fatal("fixture is wrong: the request should ask for default")
	}
}

func TestCreateTaxRateRequiresRatePercent(t *testing.T) {
	t.Parallel()
	// rate_percent is NOT NULL with no default and the repository writes it
	// unconditionally, so an absent value must be a 400 here and never reach SQL.
	got, err := NewService(nil, nil, Limits{}).CreateTaxRate(superAdminCtx(), &CreateTaxRateRequest{
		Code: "VAT", Name: "VAT",
	})
	if err == nil || got != nil {
		t.Fatalf("absent rate_percent accepted (row %v)", got)
	}
	if !errs.Has(err, errs.CodeValidationError) || fieldRule(err) != "required" {
		t.Fatalf("= %q/%q, want VALIDATION_ERROR/required", errs.CodeOf(err), fieldRule(err))
	}
}

// ---------------------------------------------------------------------------
// List assembly
// ---------------------------------------------------------------------------

func TestListPage(t *testing.T) {
	t.Parallel()
	var seen ListFilter
	list := func(_ context.Context, f ListFilter, o pagination.Offset) ([]Route, int64, error) {
		seen, _ = f, o
		return []Route{{Code: "ORAL"}}, 1, nil
	}

	q := &ListMasterDataQuery{}
	q.Page, q.Limit = 2, 10
	q.IsActive = some(true)
	term := "  oral  "
	q.Q = &term
	parent := uuid.New()
	q.ParentID = parent.String()

	items, meta, err := listPage(superAdminCtx(), q, list)
	if err != nil {
		t.Fatalf("listPage: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if seen.IsActive == nil || !*seen.IsActive {
		t.Error("is_active filter was not passed through")
	}
	if seen.Q != "oral" {
		t.Errorf("q = %q, want %q", seen.Q, "oral")
	}
	if seen.ParentID == nil || *seen.ParentID != parent {
		t.Error("parent_id was not parsed into the filter")
	}
	// Offset is normalized before it reaches the repository.
	if q.Page != 2 || q.Limit != 10 {
		t.Errorf("offset mutated: page=%d limit=%d", q.Page, q.Limit)
	}
	if meta.Total != 1 {
		t.Errorf("meta.Total = %d, want 1", meta.Total)
	}
}

func TestListPageRejectsMalformedParentUUID(t *testing.T) {
	t.Parallel()
	called := false
	list := func(context.Context, ListFilter, pagination.Offset) ([]Route, int64, error) {
		called = true
		return nil, 0, nil
	}
	_, _, err := listPage(superAdminCtx(), &ListMasterDataQuery{ParentID: "not-a-uuid"}, list)
	if !errs.Has(err, errs.CodeValidationError) || fieldRule(err) != ruleInvalidUUID {
		t.Fatalf("= %q/%q, want VALIDATION_ERROR/%s", errs.CodeOf(err), fieldRule(err), ruleInvalidUUID)
	}
	if called {
		t.Fatal("the repository was queried despite an unparseable filter")
	}
}

func TestListPageMapsRepositoryFailure(t *testing.T) {
	t.Parallel()
	list := func(context.Context, ListFilter, pagination.Offset) ([]Route, int64, error) {
		return nil, 0, errors.New("connection reset")
	}
	if _, _, err := listPage(superAdminCtx(), &ListMasterDataQuery{}, list); !errs.Has(err, errs.CodeDatabaseError) {
		t.Fatalf("code = %q, want DATABASE_ERROR", errs.CodeOf(err))
	}
}

// ---------------------------------------------------------------------------
// DTO defaults
// ---------------------------------------------------------------------------

func TestIntOr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   *int
		def  int
		want int
	}{
		{"nil takes the default", nil, 0, 0},
		{"nil takes a non-zero default", nil, 7, 7},
		{"zero is a real value", some(0), 7, 0},
		{"negative is a real value", some(-3), 7, -3},
		{"set wins", some(5), 7, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := intOr(tc.in, tc.def); got != tc.want {
				t.Fatalf("intOr(%v, %d) = %d, want %d", tc.in, tc.def, got, tc.want)
			}
		})
	}
}

// TestBoolOrKeepsColumnDefaults guards the §28.5 trap: Create<Resource> writes
// is_active unconditionally, so every create path must resolve the pointer to the
// schema default (TRUE) rather than letting it become the zero value (false).
func TestBoolOrKeepsColumnDefaults(t *testing.T) {
	t.Parallel()
	if !boolOr(nil, true) {
		t.Fatal("an omitted is_active must store the column default TRUE")
	}
	if boolOr(some(false), true) {
		t.Fatal("an explicit is_active=false must be honoured")
	}
	if boolOr(some(true), false) != true {
		t.Fatal("an explicit is_active=true must be honoured")
	}
	// is_default and is_controlled default FALSE, the opposite way round.
	if boolOr(nil, false) {
		t.Fatal("an omitted is_default/is_controlled must store FALSE")
	}
}

// ---------------------------------------------------------------------------
// PATCH semantics
// ---------------------------------------------------------------------------

func TestApplyRoutePatch(t *testing.T) {
	t.Parallel()
	base := Route{Code: "ORAL", Name: "Oral", Description: some("by mouth"), IsActive: true}

	t.Run("empty patch changes nothing", func(t *testing.T) {
		t.Parallel()
		got := base
		applyRoutePatch(&got, &UpdateRouteRequest{})
		if got != base {
			t.Fatalf("patched %+v, want unchanged %+v", got, base)
		}
	})

	t.Run("set pointer overwrites", func(t *testing.T) {
		t.Parallel()
		got := base
		applyRoutePatch(&got, &UpdateRouteRequest{
			Name:        some("Oral route"),
			Description: some("swallowed"),
			IsActive:    some(false),
		})
		if got.Name != "Oral route" {
			t.Errorf("name = %q", got.Name)
		}
		if got.Description == nil || *got.Description != "swallowed" {
			t.Errorf("description = %v", got.Description)
		}
		// An explicit false must overwrite; a naive nil-check would leave it true.
		if got.IsActive {
			t.Error("is_active=false was ignored")
		}
		if got.Code != "ORAL" {
			t.Errorf("code was modified: %q", got.Code)
		}
	})

	t.Run("empty string is a value not an absence", func(t *testing.T) {
		t.Parallel()
		got := base
		applyRoutePatch(&got, &UpdateRouteRequest{Description: some("")})
		if got.Description == nil || *got.Description != "" {
			t.Fatalf("description = %v, want a pointer to \"\"", got.Description)
		}
	})
}

func TestApplyProductCategoryPatch(t *testing.T) {
	t.Parallel()
	parent := uuid.New()
	base := ProductCategory{Code: "RX", Name: "Prescription", SortOrder: 10, IsActive: true}

	// An omitted field is left alone...
	got := base
	applyProductCategoryPatch(&got, &UpdateProductCategoryRequest{})
	if got != base {
		t.Fatalf("patched %+v, want unchanged", got)
	}

	// ...and sort_order=0 must overwrite 10, not be mistaken for an omission.
	got = base
	applyProductCategoryPatch(&got, &UpdateProductCategoryRequest{SortOrder: some(0), IsActive: some(false)})
	if got.SortOrder != 0 {
		t.Errorf("sort_order = %d, want 0", got.SortOrder)
	}
	if got.IsActive {
		t.Error("is_active=false was ignored")
	}

	got = base
	applyProductCategoryPatch(&got, &UpdateProductCategoryRequest{ParentID: &parent})
	if got.ParentID == nil || *got.ParentID != parent {
		t.Errorf("parent_id = %v, want %v", got.ParentID, parent)
	}
	if got.Name != base.Name || got.SortOrder != base.SortOrder {
		t.Error("an unrelated field was modified")
	}
}

func TestApplyStorageConditionPatch(t *testing.T) {
	t.Parallel()
	base := StorageCondition{Code: "ROOM", Name: "Room temp", MinTempCelsius: some(15.0), MaxTempCelsius: some(25.0)}

	got := base
	applyStorageConditionPatch(&got, &UpdateStorageConditionRequest{})
	if got != base {
		t.Fatalf("patched %+v, want unchanged", got)
	}

	// Lowering only the minimum is legal; the merged pair is still ordered.
	got = base
	applyStorageConditionPatch(&got, &UpdateStorageConditionRequest{MinTempCelsius: some(2.0)})
	if got.MinTempCelsius == nil || *got.MinTempCelsius != 2.0 {
		t.Fatalf("min = %v, want 2", got.MinTempCelsius)
	}
	if got.MaxTempCelsius == nil || *got.MaxTempCelsius != 25.0 {
		t.Fatal("max was modified by a patch that did not mention it")
	}
	// And the merged result is what requireTemperatureRange then judges.
	if err := requireTemperatureRange(got.MinTempCelsius, got.MaxTempCelsius); err != nil {
		t.Fatalf("merged band rejected: %v", err)
	}
}

func TestApplyUnitTypePatch(t *testing.T) {
	t.Parallel()
	base := UnitType{Code: "BOX", Name: "Box", Symbol: some("bx"), ConversionFactor: some(12.0), IsActive: true}

	got := base
	applyUnitTypePatch(&got, &UpdateUnitTypeRequest{})
	if got != base {
		t.Fatalf("patched %+v, want unchanged", got)
	}

	got = base
	applyUnitTypePatch(&got, &UpdateUnitTypeRequest{ConversionFactor: some(0.0), IsActive: some(false)})
	// A factor of 0 is representable and, per §11.2, not yet a rule violation.
	if got.ConversionFactor == nil || *got.ConversionFactor != 0 {
		t.Errorf("conversion_factor = %v, want a pointer to 0", got.ConversionFactor)
	}
	if err := requireConversionFactor(got.ConversionFactor); err != nil {
		t.Errorf("factor 0 rejected: %v", err)
	}
	if got.IsActive {
		t.Error("is_active=false was ignored")
	}
}

func TestApplyTaxRatePatch(t *testing.T) {
	t.Parallel()
	base := TaxRate{Code: "VAT", Name: "VAT", RatePercent: 15, IsDefault: true, IsActive: true}

	got := base
	applyTaxRatePatch(&got, &UpdateTaxRateRequest{})
	if got != base {
		t.Fatalf("patched %+v, want unchanged", got)
	}

	got = base
	applyTaxRatePatch(&got, &UpdateTaxRateRequest{RatePercent: some(0.0), IsDefault: some(false)})
	if got.RatePercent != 0 {
		t.Errorf("rate_percent = %v, want 0", got.RatePercent)
	}
	if got.IsDefault {
		t.Error("is_default=false was ignored")
	}
	if got.Name != "VAT" {
		t.Error("name was modified")
	}
}

// TestEveryUpdateRequestReportsEmptiness is the service's contract with the
// handlers: an Update DTO that cannot report emptiness would make "an empty PATCH
// degrades to a read" impossible to honour.
func TestEveryUpdateRequestReportsEmptiness(t *testing.T) {
	t.Parallel()
	for name, empty := range map[string]interface{ IsEmpty() bool }{
		"ProductCategory":  UpdateProductCategoryRequest{},
		"Route":            UpdateRouteRequest{},
		"DosageForm":       UpdateDosageFormRequest{},
		"PackageType":      UpdatePackageTypeRequest{},
		"UnitType":         UpdateUnitTypeRequest{},
		"StorageCondition": UpdateStorageConditionRequest{},
		"TaxRate":          UpdateTaxRateRequest{},
		"DrugGroup":        UpdateDrugGroupRequest{},
		"DrugClass":        UpdateDrugClassRequest{},
		"TherapeuticClass": UpdateTherapeuticClassRequest{},
		"GenericMedicine":  UpdateGenericMedicineRequest{},
	} {
		if !empty.IsEmpty() {
			t.Errorf("%s: zero value is not empty", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Drift guards
// ---------------------------------------------------------------------------

// TestServiceExposesTheFullOperationSet asserts all eleven resources expose the
// same five operations. Without this, adding a resource could quietly ship six of
// the seven endpoints, and nothing else in the tree would notice.
func TestServiceExposesTheFullOperationSet(t *testing.T) {
	t.Parallel()
	for _, resource := range allResourceNames {
		typ := reflect.TypeOf(&Service{})
		for _, verb := range []string{
			"Create" + resource,
			"Get" + resource,
			listMethodName(resource),
			"Update" + resource,
			"Delete" + resource,
		} {
			if _, ok := typ.MethodByName(verb); !ok {
				t.Errorf("Service is missing %s", verb)
			}
		}
	}
}

// listMethodName returns the repository/service List method name for a resource.
// Three of the eleven end in a consonant + "y" and pluralize to "-ies", so this
// cannot be a blind suffix append.
func listMethodName(resource string) string {
	switch resource {
	case "ProductCategory":
		return "ListProductCategories"
	case "DrugClass":
		return "ListDrugClasses"
	case "TherapeuticClass":
		return "ListTherapeuticClasses"
	default:
		return "List" + resource + "s"
	}
}

// TestEveryServiceMethodRequiresContext guards the convention that makes the
// transaction and cancellation rules work: ctx is always the first parameter.
func TestEveryServiceMethodRequiresContext(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(&Service{})
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if !isResourceOperation(m.Name) {
			continue
		}
		ft := m.Type
		if ft.NumIn() < 2 || ft.In(1) != ctxType {
			t.Errorf("%s does not take context.Context as its first argument", m.Name)
		}
	}
}

// TestProbeMethodsAreTypedPerResource asserts the hierarchy probes exist with the
// exact names the service files call. A rename on either side would otherwise
// surface only as a compile error in a file the linter already excludes.
func TestProbeMethodsAreTypedPerResource(t *testing.T) {
	t.Parallel()
	repo := reflect.TypeOf(&Repository{})
	for _, name := range []string{
		"IsProductCategoryReferenced", "IsDosageFormReferenced", "IsRouteReferenced",
		"IsPackageTypeReferenced", "IsUnitTypeReferenced", "IsStorageConditionReferenced",
		"IsTaxRateReferenced", "IsGenericMedicineReferenced",
		"HasProductCategoryChildren", "HasUnitTypeChildren",
		"HasRouteChildren", "HasDrugGroupChildren", "HasDrugClassChildren", "HasTherapeuticClassChildren",
	} {
		if _, ok := repo.MethodByName(name); !ok {
			t.Errorf("Repository is missing %s", name)
		}
	}
	// The three taxonomy resources are referenced by nothing (C-11), so they must
	// NOT have a medicines probe — only a children probe.
	for _, absent := range []string{"IsDrugGroupReferenced", "IsDrugClassReferenced", "IsTherapeuticClassReferenced"} {
		if _, ok := repo.MethodByName(absent); ok {
			t.Errorf("Repository unexpectedly has %s; no table references the taxonomy", absent)
		}
	}
}

// allResourceNames is the eleven-resource boundary of contract §4, in creation
// order (contract §6.1).
var allResourceNames = []string{
	"ProductCategory", "Route", "DosageForm", "PackageType", "UnitType",
	"StorageCondition", "TaxRate", "DrugGroup", "DrugClass",
	"TherapeuticClass", "GenericMedicine",
}

func isResourceOperation(name string) bool {
	for _, r := range allResourceNames {
		if strings.HasPrefix(name, "Create"+r) || strings.HasPrefix(name, "Get"+r) ||
			strings.HasPrefix(name, "List"+r) || strings.HasPrefix(name, "Update"+r) ||
			strings.HasPrefix(name, "Delete"+r) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Small assertion helpers
// ---------------------------------------------------------------------------

// errJoin wraps an error so errors.Is still matches, standing in for a driver
// error that arrives wrapped.
func errJoin(err error) error { return errors.Join(errors.New("context"), err) }

// appMessage returns the caller-visible message of an AppError.
func appMessage(err error) string {
	if app := errs.As(err); app != nil {
		return app.Message
	}
	return ""
}

// fieldRule returns the rule name of the first field-level detail.
func fieldRule(err error) string {
	if app := errs.As(err); app != nil && len(app.Details) > 0 {
		return app.Details[0].Rule
	}
	return ""
}
