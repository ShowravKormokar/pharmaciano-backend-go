// Service-layer integration tests against a real PostgreSQL.
//
// Skipped unless MASTERDATA_TEST_DSN is set, so the package stays green on a
// machine with no database. newIntegrationRepo (repository_integration_test.go)
// owns the connection, the migrations requirement and the truncate.
//
// These tests exist because the service's transaction behaviour cannot be observed
// any other way: the guard, the reference checks and the numeric rules are unit
// tested, but "the update and its dependency check commit together", "a failed
// check leaves the row untouched" and "a soft delete is refused while a live
// medicine points at the row" are all statements about the database.
//
// The Phase 05 decisions that must not be silently reversed are asserted here too:
// conversion_factor accepts 0 and negative values (§11.2 left the bounds
// unresolved) and two tax rates may both be marked default (C-08).

package masterdata

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// newIntegrationService wires a Service over the repository and database that
// newIntegrationRepo already established, with default (unbounded) Limits.
func newIntegrationService(t *testing.T) (*Service, *db.DB) {
	t.Helper()
	repo, database := newIntegrationRepo(t)
	return NewService(repo, database, Limits{}), database
}

// asRole returns a context for the given role so the SUPER_ADMIN guard can be
// exercised for real.
func asRole(role string) context.Context { return roleCtx(role) }

// ---------------------------------------------------------------------------
// Authorization
// ---------------------------------------------------------------------------

// TestWritesRequireSuperAdmin is the C-03 containment: a tenant ADMIN holds
// masterdata:* in the RBAC seed matrix, so the service must be what actually stops
// a global reference table being rewritten by one tenant.
func TestWritesRequireSuperAdmin(t *testing.T) {
	svc, _ := newIntegrationService(t)

	admin := asRole("ADMIN")
	if _, err := svc.CreateRoute(admin, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"}); !errs.Has(err, errs.CodeForbidden) {
		t.Fatalf("ADMIN create = %q, want FORBIDDEN (err %v)", errs.CodeOf(err), err)
	}

	route, err := svc.CreateRoute(superAdminCtx(), &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("SUPER_ADMIN create: %v", err)
	}
	_, updateErr := svc.UpdateRoute(admin, route.ID, &UpdateRouteRequest{Name: some("Changed")})
	deleteErr := svc.DeleteRoute(admin, route.ID)
	for name, err := range map[string]error{"update": updateErr, "delete": deleteErr} {
		if !errs.Has(err, errs.CodeForbidden) {
			t.Errorf("ADMIN %s = %q, want FORBIDDEN", name, errs.CodeOf(err))
		}
	}

	// A read is not restricted: RBAC already authorized it.
	if _, err := svc.GetRoute(admin, route.ID); err != nil {
		t.Fatalf("ADMIN read refused: %v", err)
	}
	if _, _, err := svc.ListRoutes(admin, &ListMasterDataQuery{}); err != nil {
		t.Fatalf("ADMIN list refused: %v", err)
	}
}

// TestForbiddenWriteLeavesNoTrace proves the guard runs before any statement, so a
// rejected write cannot leave a partial row behind.
func TestForbiddenWriteLeavesNoTrace(t *testing.T) {
	svc, database := newIntegrationService(t)
	admin := asRole("ADMIN")

	code := uniqueCode("RTE")
	if _, err := svc.CreateRoute(admin, &CreateRouteRequest{Code: code, Name: "Oral"}); !errs.Has(err, errs.CodeForbidden) {
		t.Fatalf("create was not forbidden: %v", err)
	}
	var count int
	if err := database.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM routes WHERE code = $1`, code).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a forbidden create still wrote %d row(s)", count)
	}
}

// ---------------------------------------------------------------------------
// CRUD round trip and defaults
// ---------------------------------------------------------------------------

// TestCreateResolvesSchemaDefaults is the §28.5 trap in the field: Create writes
// is_active unconditionally, so an omitted flag must store the column default TRUE
// rather than the model's zero value false.
func TestCreateResolvesSchemaDefaults(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !route.IsActive {
		t.Error("is_active stored false for an omitted flag; expected the column default TRUE")
	}

	cat, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Analgesics"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if !cat.IsActive {
		t.Error("category is_active did not default to TRUE")
	}
	if cat.SortOrder != 0 {
		t.Errorf("sort_order = %d, want the column default 0", cat.SortOrder)
	}

	// is_controlled and is_default default FALSE.
	gen, err := svc.CreateGenericMedicine(ctx, &CreateGenericMedicineRequest{Code: uniqueCode("GEN"), Name: "Paracetamol"})
	if err != nil {
		t.Fatalf("create generic: %v", err)
	}
	if gen.IsControlled {
		t.Error("is_controlled stored true for an omitted flag; expected the column default FALSE")
	}
}

// TestPatchOmissionVersusExplicitValue pins the PATCH contract end to end.
func TestPatchOmissionVersusExplicitValue(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{
		Code: uniqueCode("RTE"), Name: "Oral", Description: some("by mouth"), IsActive: some(true),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// An empty PATCH degrades to a read: same values, no error.
	same, err := svc.UpdateRoute(ctx, route.ID, &UpdateRouteRequest{})
	if err != nil {
		t.Fatalf("empty patch: %v", err)
	}
	if same.Name != route.Name || same.IsActive != route.IsActive {
		t.Errorf("empty patch changed the row: %+v", same)
	}

	// An omitted field is untouched...
	patched, err := svc.UpdateRoute(ctx, route.ID, &UpdateRouteRequest{Name: some("Oral route")})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if patched.Name != "Oral route" {
		t.Errorf("name = %q", patched.Name)
	}
	if patched.Description == nil || *patched.Description != "by mouth" {
		t.Errorf("description = %v, want it untouched", patched.Description)
	}

	// ...and an explicit false is honoured rather than read as an omission.
	deactivated, err := svc.UpdateRoute(ctx, route.ID, &UpdateRouteRequest{IsActive: some(false)})
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if deactivated.IsActive {
		t.Error("is_active=false was ignored")
	}
	if deactivated.Name != "Oral route" {
		t.Error("deactivating also changed the name")
	}

	// code is immutable: it is not in the Update DTO at all.
	if _, err := svc.UpdateRoute(ctx, route.ID, &UpdateRouteRequest{Name: some("x")}); err != nil {
		t.Fatalf("second patch: %v", err)
	}
	var code string
	if err := svc.db.Pool().QueryRow(ctx, `SELECT code FROM routes WHERE id = $1`, route.ID).Scan(&code); err != nil {
		t.Fatalf("read code: %v", err)
	}
	if code != route.Code {
		t.Errorf("code changed from %q to %q", route.Code, code)
	}
}

func TestDuplicateCodeIsAlreadyExists(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()
	code := uniqueCode("RTE")

	if _, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: code, Name: "Oral"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: code, Name: "Duplicate"})
	if !errs.Has(err, errs.CodeAlreadyExists) {
		t.Fatalf("duplicate code = %q, want ALREADY_EXISTS (err %v)", errs.CodeOf(err), err)
	}
}

func TestMissingRowIsNotFound(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()
	missing := uuid.New()

	if _, err := svc.GetRoute(ctx, missing); !errs.Has(err, errs.CodeNotFound) {
		t.Errorf("get = %q, want NOT_FOUND", errs.CodeOf(err))
	}
	if err := svc.DeleteRoute(ctx, missing); !errs.Has(err, errs.CodeNotFound) {
		t.Errorf("delete = %q, want NOT_FOUND", errs.CodeOf(err))
	}
	if _, err := svc.UpdateRoute(ctx, missing, &UpdateRouteRequest{Name: some("x")}); !errs.Has(err, errs.CodeNotFound) {
		t.Errorf("update = %q, want NOT_FOUND", errs.CodeOf(err))
	}
	// A second delete must not report success.
	created, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteRoute(ctx, created.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := svc.DeleteRoute(ctx, created.ID); !errs.Has(err, errs.CodeNotFound) {
		t.Errorf("second delete = %q, want NOT_FOUND", errs.CodeOf(err))
	}
}

// TestSoftDeleteReleasesTheCode documents that a soft delete frees the code
// through the partial unique index, while deactivation does not.
func TestSoftDeleteReleasesTheCode(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()
	code := uniqueCode("RTE")

	first, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: code, Name: "Oral"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateRoute(ctx, first.ID, &UpdateRouteRequest{IsActive: some(false)}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	// Still taken: the row is live, just inactive.
	if _, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: code, Name: "Reused"}); !errs.Has(err, errs.CodeAlreadyExists) {
		t.Fatalf("deactivated row released the code: %v", err)
	}
	if err := svc.DeleteRoute(ctx, first.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: code, Name: "Reused"}); err != nil {
		t.Fatalf("code not reusable after delete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cross-row references
// ---------------------------------------------------------------------------

// TestReferenceMustExist covers the six foreign keys. A missing parent is a 404
// naming the PARENT, because that is what the caller has to fix.
func TestReferenceMustExist(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()
	missing := uuid.New()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	group, err := svc.CreateDrugGroup(ctx, &CreateDrugGroupRequest{Code: uniqueCode("DG"), Name: "Analgesics"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	class, err := svc.CreateDrugClass(ctx, &CreateDrugClassRequest{Code: uniqueCode("DC"), Name: "Opioids"})
	if err != nil {
		t.Fatalf("create class: %v", err)
	}
	therapeutic, err := svc.CreateTherapeuticClass(ctx, &CreateTherapeuticClassRequest{Code: uniqueCode("TC"), Name: "Opioid"})
	if err != nil {
		t.Fatalf("create therapeutic: %v", err)
	}
	unit, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{Code: uniqueCode("UOM"), Name: "Box"})
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	cat, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Rx"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"dosage form -> route", func() error {
			_, err := svc.CreateDosageForm(ctx, &CreateDosageFormRequest{Code: uniqueCode("DF"), Name: "Tablet", RouteID: &missing})
			return err
		}},
		{"drug class -> group", func() error {
			_, err := svc.CreateDrugClass(ctx, &CreateDrugClassRequest{Code: uniqueCode("DC"), Name: "X", DrugGroupID: &missing})
			return err
		}},
		{"therapeutic class -> class", func() error {
			_, err := svc.CreateTherapeuticClass(ctx, &CreateTherapeuticClassRequest{Code: uniqueCode("TC"), Name: "X", DrugClassID: &missing})
			return err
		}},
		{"generic -> therapeutic", func() error {
			_, err := svc.CreateGenericMedicine(ctx, &CreateGenericMedicineRequest{Code: uniqueCode("GEN"), Name: "X", TherapeuticClassID: &missing})
			return err
		}},
		{"unit -> base unit", func() error {
			_, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{Code: uniqueCode("UOM"), Name: "X", BaseUnitID: &missing})
			return err
		}},
		{"category -> parent", func() error {
			_, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "X", ParentID: &missing})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errs.Has(err, errs.CodeNotFound) {
				t.Fatalf("= %q, want NOT_FOUND (err %v)", errs.CodeOf(err), err)
			}
		})
	}

	// The live references all succeed, which is the control for the six above.
	if _, err := svc.CreateDosageForm(ctx, &CreateDosageFormRequest{Code: uniqueCode("DF"), Name: "Tablet", RouteID: &route.ID}); err != nil {
		t.Errorf("live route rejected: %v", err)
	}
	if _, err := svc.CreateDrugClass(ctx, &CreateDrugClassRequest{Code: uniqueCode("DC"), Name: "X", DrugGroupID: &group.ID}); err != nil {
		t.Errorf("live group rejected: %v", err)
	}
	if _, err := svc.CreateTherapeuticClass(ctx, &CreateTherapeuticClassRequest{Code: uniqueCode("TC"), Name: "X", DrugClassID: &class.ID}); err != nil {
		t.Errorf("live class rejected: %v", err)
	}
	if _, err := svc.CreateGenericMedicine(ctx, &CreateGenericMedicineRequest{Code: uniqueCode("GEN"), Name: "X", TherapeuticClassID: &therapeutic.ID}); err != nil {
		t.Errorf("live therapeutic class rejected: %v", err)
	}
	if _, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{Code: uniqueCode("UOM"), Name: "Dozen", BaseUnitID: &unit.ID, ConversionFactor: some(12.0)}); err != nil {
		t.Errorf("live base unit rejected: %v", err)
	}
	if _, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Child", ParentID: &cat.ID}); err != nil {
		t.Errorf("live parent rejected: %v", err)
	}
}

// TestUpdateRejectsSelfReference covers the two resources with a CHECK behind them.
func TestUpdateRejectsSelfReference(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	cat, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Rx"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.UpdateProductCategory(ctx, cat.ID, &UpdateProductCategoryRequest{ParentID: &cat.ID})
	if !errs.Has(err, errs.CodeBusinessRuleViolation) {
		t.Fatalf("category self-parent = %q, want BUSINESS_RULE_VIOLATION (err %v)", errs.CodeOf(err), err)
	}
	if got != nil {
		t.Fatal("a rejected update returned a row")
	}

	unit, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{Code: uniqueCode("UOM"), Name: "Box"})
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	if _, err := svc.UpdateUnitType(ctx, unit.ID, &UpdateUnitTypeRequest{BaseUnitID: &unit.ID}); !errs.Has(err, errs.CodeBusinessRuleViolation) {
		t.Fatalf("unit self-base = %q, want BUSINESS_RULE_VIOLATION", errs.CodeOf(err))
	}

	// The rows are untouched: the check runs before the write, inside the same
	// transaction.
	reread, err := svc.GetProductCategory(ctx, cat.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if reread.ParentID != nil {
		t.Errorf("parent_id = %v after a rejected patch, want NULL", reread.ParentID)
	}
}

// TestStorageConditionMergedRange is the rule the DTO cannot decide: a PATCH that
// moves only one bound is only judgeable against the stored value of the other.
func TestStorageConditionMergedRange(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	cond, err := svc.CreateStorageCondition(ctx, &CreateStorageConditionRequest{
		Code: uniqueCode("SC"), Name: "Room temperature", MinTempCelsius: some(15.0), MaxTempCelsius: some(25.0),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Legal: lowering the minimum keeps the pair ordered.
	if _, err := svc.UpdateStorageCondition(ctx, cond.ID, &UpdateStorageConditionRequest{MinTempCelsius: some(2.0)}); err != nil {
		t.Fatalf("legal merge rejected: %v", err)
	}
	// Illegal: raising the minimum above the STORED maximum inverts the band, and
	// the request never mentions the maximum at all.
	if _, err := svc.UpdateStorageCondition(ctx, cond.ID, &UpdateStorageConditionRequest{MinTempCelsius: some(30.0)}); !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("inverted merge = %q, want VALIDATION_ERROR (err %v)", errs.CodeOf(err), err)
	}

	after, err := svc.GetStorageCondition(ctx, cond.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if after.MinTempCelsius == nil || *after.MinTempCelsius != 2.0 {
		t.Errorf("min = %v, want the last accepted value 2", after.MinTempCelsius)
	}
}

// TestConversionFactorBoundsStayUnresolved pins the Phase 05 decision (D-14):
// representability is enforced, the sign is not.
func TestConversionFactorBoundsStayUnresolved(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	for _, factor := range []float64{0, -1, -0.5, 12.5} {
		u, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{
			Code: uniqueCode("UOM"), Name: "Unit", ConversionFactor: some(factor),
		})
		if err != nil {
			t.Fatalf("conversion_factor %v rejected: %v", factor, err)
		}
		if u.ConversionFactor == nil || *u.ConversionFactor != factor {
			t.Errorf("conversion_factor = %v, want %v", u.ConversionFactor, factor)
		}
	}
	// A value the column cannot hold is still rejected.
	if _, err := svc.CreateUnitType(ctx, &CreateUnitTypeRequest{
		Code: uniqueCode("UOM"), Name: "Unit", ConversionFactor: some(1e20),
	}); !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("overflowing factor = %q, want VALIDATION_ERROR", errs.CodeOf(err))
	}
}

// TestSeveralDefaultTaxRatesAreAllowed pins the Phase 05 decision (C-08): the
// single-default rule is UNRESOLVED, so nothing here enforces it.
func TestSeveralDefaultTaxRatesAreAllowed(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	first, err := svc.CreateTaxRate(ctx, &CreateTaxRateRequest{
		Code: uniqueCode("TAX"), Name: "Standard", RatePercent: some(15.0), IsDefault: some(true),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := svc.CreateTaxRate(ctx, &CreateTaxRateRequest{
		Code: uniqueCode("TAX"), Name: "Reduced", RatePercent: some(5.0), IsDefault: some(true),
	})
	if err != nil {
		t.Fatalf("a second default was refused, which would enforce the unresolved rule: %v", err)
	}

	var defaults int
	if err := svc.db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM tax_rates WHERE is_default AND deleted_at IS NULL`).Scan(&defaults); err != nil {
		t.Fatalf("count: %v", err)
	}
	if defaults < 2 {
		t.Fatalf("defaults = %d, want both rows to remain default", defaults)
	}
	if !first.IsDefault || !second.IsDefault {
		t.Error("a default flag did not round-trip")
	}

	// The configured business maximum is still enforced.
	bounded := NewService(svc.repo, svc.db, Limits{MaxRatePercent: 10})
	if _, err := bounded.CreateTaxRate(ctx, &CreateTaxRateRequest{
		Code: uniqueCode("TAX"), Name: "Too high", RatePercent: some(15.0),
	}); !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("above the configured maximum = %q, want VALIDATION_ERROR", errs.CodeOf(err))
	}
}

// ---------------------------------------------------------------------------
// D-19 referenced-row guard
// ---------------------------------------------------------------------------

// TestDeleteRefusesWhileReferenced is the D-19 policy: a soft delete cannot fire a
// foreign key, so the service is the only thing preventing a category from being
// removed from under stocked medicines.
func TestDeleteRefusesWhileReferenced(t *testing.T) {
	svc, database := newIntegrationService(t)
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	cat, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Rx"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	insertMedicine(t, database, cat.ID, route.ID)

	if err := svc.DeleteRoute(ctx, route.ID); !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("delete referenced route = %q, want CONFLICT (err %v)", errs.CodeOf(err), err)
	}
	if err := svc.DeleteProductCategory(ctx, cat.ID); !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("delete referenced category = %q, want CONFLICT", errs.CodeOf(err))
	}

	// Deactivation always succeeds, and is what the message directs the caller to.
	deactivated, err := svc.UpdateRoute(ctx, route.ID, &UpdateRouteRequest{IsActive: some(false)})
	if err != nil {
		t.Fatalf("deactivating a referenced route failed: %v", err)
	}
	if deactivated.IsActive {
		t.Error("route is still active")
	}
}

// TestDeleteRefusesWhileChildrenExist covers the hierarchy half of the guard, which
// no medicines probe can see.
func TestDeleteRefusesWhileChildrenExist(t *testing.T) {
	svc, _ := newIntegrationService(t)
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	if _, err := svc.CreateDosageForm(ctx, &CreateDosageFormRequest{
		Code: uniqueCode("DF"), Name: "Tablet", RouteID: &route.ID,
	}); err != nil {
		t.Fatalf("create dosage form: %v", err)
	}
	if err := svc.DeleteRoute(ctx, route.ID); !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("delete route with dosage-form child = %q, want CONFLICT", errs.CodeOf(err))
	}

	group, err := svc.CreateDrugGroup(ctx, &CreateDrugGroupRequest{Code: uniqueCode("DG"), Name: "Analgesics"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	class, err := svc.CreateDrugClass(ctx, &CreateDrugClassRequest{Code: uniqueCode("DC"), Name: "Opioids", DrugGroupID: &group.ID})
	if err != nil {
		t.Fatalf("create class: %v", err)
	}
	therapeutic, err := svc.CreateTherapeuticClass(ctx, &CreateTherapeuticClassRequest{Code: uniqueCode("TC"), Name: "Opioid", DrugClassID: &class.ID})
	if err != nil {
		t.Fatalf("create therapeutic: %v", err)
	}

	// Every link of the chain refuses while the next one exists.
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		call func(uuid.UUID) error
	}{
		{"group", group.ID, func(id uuid.UUID) error { return svc.DeleteDrugGroup(ctx, id) }},
		{"class", class.ID, func(id uuid.UUID) error { return svc.DeleteDrugClass(ctx, id) }},
	} {
		if err := tc.call(tc.id); !errs.Has(err, errs.CodeConflict) {
			t.Errorf("delete %s with children = %q, want CONFLICT", tc.name, errs.CodeOf(err))
		}
	}

	// Self-referencing hierarchies are guarded the same way.
	parent, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{
		Code: uniqueCode("CAT"), Name: "Child", ParentID: &parent.ID,
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := svc.DeleteProductCategory(ctx, parent.ID); !errs.Has(err, errs.CodeConflict) {
		t.Errorf("delete category with children = %q, want CONFLICT", errs.CodeOf(err))
	}

	// Draining the chain leaf-first lets every delete through.
	gen, err := svc.CreateGenericMedicine(ctx, &CreateGenericMedicineRequest{
		Code: uniqueCode("GEN"), Name: "Morphine", TherapeuticClassID: &therapeutic.ID,
	})
	if err != nil {
		t.Fatalf("create generic: %v", err)
	}
	if err := svc.DeleteTherapeuticClass(ctx, therapeutic.ID); !errs.Has(err, errs.CodeConflict) {
		t.Errorf("delete therapeutic class with generic children = %q, want CONFLICT", errs.CodeOf(err))
	}
	if err := svc.DeleteGenericMedicine(ctx, gen.ID); err != nil {
		t.Fatalf("delete generic medicine: %v", err)
	}
	if err := svc.DeleteTherapeuticClass(ctx, therapeutic.ID); err != nil {
		t.Errorf("delete therapeutic class: %v", err)
	}
	if err := svc.DeleteDrugClass(ctx, class.ID); err != nil {
		t.Errorf("delete drug class: %v", err)
	}
	if err := svc.DeleteDrugGroup(ctx, group.ID); err != nil {
		t.Errorf("delete drug group: %v", err)
	}
}

// TestReferenceCheckSerializesWithSoftDelete ensures a reference cannot be
// created after its parent has passed the service's live-row check but before a
// concurrent soft delete commits. PostgreSQL FKs do not consider deleted_at.
func TestReferenceCheckSerializesWithSoftDelete(t *testing.T) {
	repo, database := newIntegrationRepo(t)
	svc := NewService(repo, database, Limits{})
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}

	locked := make(chan error, 1)
	releaseDelete := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- database.WithTx(ctx, func(txCtx context.Context) error {
			if _, err := repo.GetRouteForUpdate(txCtx, route.ID); err != nil {
				locked <- err
				return err
			}
			locked <- nil
			<-releaseDelete
			_, err := repo.SoftDeleteRoute(txCtx, route.ID)
			return err
		})
	}()
	if err := <-locked; err != nil {
		t.Fatalf("lock route: %v", err)
	}

	createStarted := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		close(createStarted)
		_, err := svc.CreateDosageForm(ctx, &CreateDosageFormRequest{
			Code: uniqueCode("DF"), Name: "Tablet", RouteID: &route.ID,
		})
		createDone <- err
	}()
	<-createStarted

	select {
	case err := <-createDone:
		close(releaseDelete)
		if deleteErr := <-deleteDone; deleteErr != nil {
			t.Fatalf("soft delete route: %v", deleteErr)
		}
		t.Fatalf("reference creation completed while the parent was locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseDelete)
	if err := <-deleteDone; err != nil {
		t.Fatalf("soft delete route: %v", err)
	}
	if err := <-createDone; !errs.Has(err, errs.CodeNotFound) {
		t.Fatalf("create against deleted parent = %q, want NOT_FOUND (err %v)", errs.CodeOf(err), err)
	}
}

// TestSoftDeletedMedicineStopsCounting confirms the probe filters deleted_at on the
// referencing side, so a retired medicine releases its reference.
func TestSoftDeletedMedicineStopsCounting(t *testing.T) {
	svc, database := newIntegrationService(t)
	ctx := superAdminCtx()

	route, err := svc.CreateRoute(ctx, &CreateRouteRequest{Code: uniqueCode("RTE"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	cat, err := svc.CreateProductCategory(ctx, &CreateProductCategoryRequest{Code: uniqueCode("CAT"), Name: "Rx"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	medicineID := insertMedicine(t, database, cat.ID, route.ID)

	if err := svc.DeleteRoute(ctx, route.ID); !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("expected the live medicine to block the delete: %v", err)
	}
	if _, err := database.Pool().Exec(ctx,
		`UPDATE medicines SET deleted_at = now() WHERE id = $1`, medicineID); err != nil {
		t.Fatalf("soft-delete medicine: %v", err)
	}
	if err := svc.DeleteRoute(ctx, route.ID); err != nil {
		t.Fatalf("a soft-deleted medicine still counted as a reference: %v", err)
	}
}

// insertMedicine creates the manufacturer -> brand -> medicine chain that
// medicines.category_id and medicines.route_id need, and returns the medicine id.
//
// Written with SQL rather than the repository because the medicine module is a stub
// (contract C-13) — this is the same technique Phase 04 used.
func insertMedicine(t *testing.T, database *db.DB, categoryID, routeID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	pool := database.Pool()

	var manufacturerID, brandID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO manufacturers (code, name) VALUES ($1, 'Acme') RETURNING id`,
		uniqueCode("MF")).Scan(&manufacturerID); err != nil {
		t.Fatalf("manufacturer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO brands (code, name, manufacturer_id) VALUES ($1, 'Acme Oral', $2) RETURNING id`,
		uniqueCode("BR"), manufacturerID).Scan(&brandID); err != nil {
		t.Fatalf("brand: %v", err)
	}
	var medicineID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO medicines (
			sku, name, brand_id, category_id, route_id
		) VALUES ($1, 'Morphine 10mg', $2, $3, $4) RETURNING id`,
		uniqueCode("SKU"), brandID, categoryID, routeID).Scan(&medicineID); err != nil {
		t.Fatalf("medicine: %v", err)
	}
	return medicineID
}
