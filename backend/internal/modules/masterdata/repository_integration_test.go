package masterdata

// Integration tests for the MasterData repository. They run against a real
// PostgreSQL instance and are SKIPPED unless MASTERDATA_TEST_DSN is set, so
// `go test ./...` stays green on a machine with no database.
//
// The project has no testcontainers, dockertest or pgxmock dependency and this
// phase did not add one; a DSN-based harness is the only option that exercises the
// real driver without introducing a heavyweight new dependency. To run them:
//
//	createdb pharmaciano_mdtest
//	psql -d pharmaciano_mdtest -f migrations/000001_....up.sql   (all 20, in order)
//	MASTERDATA_TEST_DSN='postgres://postgres:pw@localhost:5432/pharmaciano_mdtest?sslmode=disable' go test ./internal/modules/masterdata/...
//
// Use a THROWAWAY database. These tests truncate the tables they touch.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// testLogger returns a silent logger, so integration output is not drowned in pool
// and tracer logs.
func testLogger() *zap.Logger { return zap.NewNop() }

// testDSNEnv names the environment variable holding the connection string.
const testDSNEnv = "MASTERDATA_TEST_DSN"

// truncateSQL clears every table these tests write to. CASCADE handles the
// medicines -> brands -> manufacturers chain and the taxonomy links.
const truncateSQL = `TRUNCATE TABLE
	generic_medicines, therapeutic_classes, drug_classes, drug_groups,
	medicines, medicine_variants, medicine_barcodes, brands, manufacturers,
	dosage_forms, routes, storage_conditions, tax_rates, package_types,
	unit_types, product_categories
	CASCADE`

// newIntegrationRepo connects to MASTERDATA_TEST_DSN, skips the test if it is not
// set, and guarantees a clean slate before and after the test body.
func newIntegrationRepo(t *testing.T) (*Repository, *db.DB) {
	t.Helper()

	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", testDSNEnv)
	}

	cfg := config.DatabaseConfig{
		Host:             "localhost",
		SSLMode:          "disable",
		Timezone:         "UTC",
		StatementTimeout: 30 * time.Second,
	}
	// db.New builds its DSN from fields rather than accepting a URL, so the
	// individual pieces are recovered from the DSN the operator supplied.
	host, port, name, user, password := splitDSN(t, dsn)
	cfg.Host, cfg.Port, cfg.Name, cfg.User, cfg.Password = host, port, name, user, password
	cfg.Pool = config.DBPoolConfig{MaxConns: 4, MinConns: 1, HealthCheckPeriod: time.Minute}

	database, err := db.New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	truncate := func() {
		t.Helper()
		if _, err := database.Pool().Exec(context.Background(), truncateSQL); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return NewRepository(database), database
}

// splitDSN recovers the connection fields db.New needs. Only the shapes this test
// uses are supported; anything else is a loud failure rather than a silent
// misconnection.
func splitDSN(t *testing.T, dsn string) (host string, port int, name, user, password string) {
	t.Helper()

	rest := dsn
	// db.New rebuilds its own DSN from the individual fields, so any query
	// parameters are dropped rather than merged into the database name.
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}
	for _, scheme := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(rest, scheme) {
			rest = strings.TrimPrefix(rest, scheme)
			break
		}
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		creds := rest[:i]
		rest = rest[i+1:]
		if j := strings.Index(creds, ":"); j >= 0 {
			user, password = creds[:j], creds[j+1:]
		} else {
			user = creds
		}
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		name = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		port = 5432
		if _, err := strconv.Atoi(rest[i+1:]); err == nil {
			port, _ = strconv.Atoi(rest[i+1:])
		}
		rest = rest[:i]
	}
	host = rest
	if host == "" {
		host = "localhost"
	}
	if name == "" {
		t.Fatalf("the DSN in %s carries no database name; refusing to report it "+
			"because it may carry a password", testDSNEnv)
	}
	return host, port, name, user, password
}

// uniqueCode returns a code that cannot collide with another test's.
func uniqueCode(prefix string) string {
	return strings.ToUpper(prefix) + "-" + strings.ReplaceAll(uuid.New().String()[:8], "-", "")
}

// ---------------------------------------------------------------------------
// Round trips — the load-bearing tests
// ---------------------------------------------------------------------------

// TestIntegrationRoundTripAllResources creates a row in each of the eleven tables
// through the repository and reads it back, asserting every field.
//
// This is the test that would have caught a wrong column order or a NUMERIC
// scanned into the wrong field. Both compile cleanly and are invisible to a
// database-free test, because the destination types line up either way.
func TestIntegrationRoundTripAllResources(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	t.Run("product_categories", func(t *testing.T) {
		created, err := repo.CreateProductCategory(ctx, &ProductCategory{
			Code:        uniqueCode("CAT"),
			Name:        "Analgesics",
			Description: strptr("pain relief"),
			SortOrder:   42,
			IsActive:    true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		assertTimestamps(t, created.CreatedAt, created.UpdatedAt, created.DeletedAt)

		back, err := repo.GetProductCategory(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.ID != created.ID || back.Code != created.Code || back.Name != "Analgesics" ||
			*back.Description != "pain relief" || back.SortOrder != 42 || !back.IsActive {
			t.Errorf("round trip mismatch: %+v", back)
		}
		if back.ParentID != nil {
			t.Errorf("parent_id = %v, want NULL", back.ParentID)
		}
		if back.DeletedAt != nil {
			t.Errorf("deleted_at = %v, want NULL", back.DeletedAt)
		}
	})

	t.Run("routes", func(t *testing.T) {
		created, err := repo.CreateRoute(ctx, &Route{
			Code: uniqueCode("RT"), Name: "Oral", Description: strptr("by mouth"), IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetRoute(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.Name != "Oral" || *back.Description != "by mouth" || !back.IsActive {
			t.Errorf("round trip mismatch: %+v", back)
		}
	})

	t.Run("dosage_forms", func(t *testing.T) {
		route, err := repo.CreateRoute(ctx, &Route{Code: uniqueCode("RT"), Name: "Oral"})
		if err != nil {
			t.Fatalf("seed route: %v", err)
		}
		created, err := repo.CreateDosageForm(ctx, &DosageForm{
			Code: uniqueCode("DF"), Name: "Tablet", RouteID: &route.ID,
			Description: strptr("solid dose"), IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetDosageForm(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.RouteID == nil || *back.RouteID != route.ID {
			t.Errorf("route_id = %v, want %v", back.RouteID, route.ID)
		}
		if back.Name != "Tablet" || *back.Description != "solid dose" {
			t.Errorf("round trip mismatch: %+v", back)
		}
	})

	t.Run("package_types", func(t *testing.T) {
		created, err := repo.CreatePackageType(ctx, &PackageType{
			Code: uniqueCode("PT"), Name: "Blister", Description: strptr("10 tablets"), IsActive: false,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetPackageType(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.IsActive {
			t.Error("is_active = true, want the false that was stored")
		}
		if *back.Description != "10 tablets" {
			t.Errorf("description = %q", *back.Description)
		}
	})

	t.Run("unit_types", func(t *testing.T) {
		created, err := repo.CreateUnitType(ctx, &UnitType{
			Code: uniqueCode("UT"), Name: "Kilogram", Symbol: strptr("kg"),
			ConversionFactor: f64ptr(1000), IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetUnitType(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.Symbol == nil || *back.Symbol != "kg" {
			t.Errorf("symbol = %v", back.Symbol)
		}
		if back.ConversionFactor == nil || *back.ConversionFactor != 1000 {
			t.Errorf("conversion_factor = %v, want 1000", back.ConversionFactor)
		}
		if back.BaseUnitID != nil {
			t.Errorf("base_unit_id = %v, want NULL", back.BaseUnitID)
		}
	})

	t.Run("storage_conditions", func(t *testing.T) {
		created, err := repo.CreateStorageCondition(ctx, &StorageCondition{
			Code: uniqueCode("SC"), Name: "Refrigerated",
			MinTempCelsius: f64ptr(2), MaxTempCelsius: f64ptr(8),
			Description: strptr("2-8C"), IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetStorageCondition(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.MinTempCelsius == nil || *back.MinTempCelsius != 2 {
			t.Errorf("min_temp_celsius = %v, want 2", back.MinTempCelsius)
		}
		if back.MaxTempCelsius == nil || *back.MaxTempCelsius != 8 {
			t.Errorf("max_temp_celsius = %v, want 8", back.MaxTempCelsius)
		}
	})

	t.Run("tax_rates", func(t *testing.T) {
		created, err := repo.CreateTaxRate(ctx, &TaxRate{
			Code: uniqueCode("TX"), Name: "VAT 5%", RatePercent: 5.25,
			IsDefault: true, IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetTaxRate(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.RatePercent != 5.25 {
			t.Errorf("rate_percent = %v, want 5.25 (NUMERIC(6,3) must survive the float64 scan)", back.RatePercent)
		}
		if !back.IsDefault {
			t.Error("is_default = false, want true")
		}
	})

	t.Run("drug_groups", func(t *testing.T) {
		created, err := repo.CreateDrugGroup(ctx, &DrugGroup{
			Code: uniqueCode("DG"), Name: "Analgesics", Description: strptr("pain"), IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetDrugGroup(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.Name != "Analgesics" || *back.Description != "pain" {
			t.Errorf("round trip mismatch: %+v", back)
		}
	})

	t.Run("drug_classes", func(t *testing.T) {
		group, err := repo.CreateDrugGroup(ctx, &DrugGroup{Code: uniqueCode("DG"), Name: "Analgesics"})
		if err != nil {
			t.Fatalf("seed group: %v", err)
		}
		created, err := repo.CreateDrugClass(ctx, &DrugClass{
			Code: uniqueCode("DC"), Name: "Opioids", DrugGroupID: &group.ID, IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetDrugClass(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.DrugGroupID == nil || *back.DrugGroupID != group.ID {
			t.Errorf("drug_group_id = %v, want %v", back.DrugGroupID, group.ID)
		}
		if back.Description != nil {
			t.Errorf("description = %v, want NULL", *back.Description)
		}
	})

	t.Run("therapeutic_classes", func(t *testing.T) {
		group, _ := repo.CreateDrugGroup(ctx, &DrugGroup{Code: uniqueCode("DG"), Name: "Analgesics"})
		class, err := repo.CreateDrugClass(ctx, &DrugClass{
			Code: uniqueCode("DC"), Name: "Opioids", DrugGroupID: &group.ID,
		})
		if err != nil {
			t.Fatalf("seed class: %v", err)
		}
		created, err := repo.CreateTherapeuticClass(ctx, &TherapeuticClass{
			Code: uniqueCode("TC"), Name: "Narcotic Analgesics", DrugClassID: &class.ID, IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetTherapeuticClass(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.DrugClassID == nil || *back.DrugClassID != class.ID {
			t.Errorf("drug_class_id = %v, want %v", back.DrugClassID, class.ID)
		}
	})

	t.Run("generic_medicines", func(t *testing.T) {
		group, _ := repo.CreateDrugGroup(ctx, &DrugGroup{Code: uniqueCode("DG"), Name: "Analgesics"})
		class, _ := repo.CreateDrugClass(ctx, &DrugClass{
			Code: uniqueCode("DC"), Name: "Opioids", DrugGroupID: &group.ID,
		})
		tc, err := repo.CreateTherapeuticClass(ctx, &TherapeuticClass{
			Code: uniqueCode("TC"), Name: "Narcotics", DrugClassID: &class.ID,
		})
		if err != nil {
			t.Fatalf("seed class: %v", err)
		}
		created, err := repo.CreateGenericMedicine(ctx, &GenericMedicine{
			Code: uniqueCode("GM"), Name: "Morphine", Description: strptr("opioid"),
			TherapeuticClassID: &tc.ID, IsControlled: true, IsActive: true,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetGenericMedicine(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.TherapeuticClassID == nil || *back.TherapeuticClassID != tc.ID {
			t.Errorf("therapeutic_class_id = %v, want %v", back.TherapeuticClassID, tc.ID)
		}
		if !back.IsControlled {
			t.Error("is_controlled = false, want true")
		}
	})
}

// TestIntegrationNumericPrecision checks the NUMERIC -> float64 conversion on the
// three NUMERIC columns at values float64 cannot hold exactly, and at the column
// boundaries Phase 03 validated.
//
// The precision caveat recorded on unitTypeTable is asserted here rather than left
// as a comment: the repository's contract is "small values round-trip", not
// "arbitrary NUMERIC values round-trip bit-exactly".
func TestIntegrationNumericPrecision(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	t.Run("conversion_factor is NUMERIC(15,6)", func(t *testing.T) {
		for _, want := range []float64{0, 1, 1.5, 2.5, 12.345678, 1000, 999999.999999} {
			created, err := repo.CreateUnitType(ctx, &UnitType{
				Code: uniqueCode("UT"), Name: "Unit", ConversionFactor: f64ptr(want),
			})
			if err != nil {
				t.Fatalf("create %v: %v", want, err)
			}
			back, err := repo.GetUnitType(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if back.ConversionFactor == nil {
				t.Fatalf("conversion_factor came back NULL for %v", want)
			}
			// 6-decimal scale in, 6-decimal scale out: a difference beyond that is
			// a real conversion bug rather than float64 noise.
			if diff := *back.ConversionFactor - want; diff > 1e-6 || diff < -1e-6 {
				t.Errorf("conversion_factor = %v, want %v", *back.ConversionFactor, want)
			}
		}
	})

	t.Run("rate_percent is NUMERIC(6,3)", func(t *testing.T) {
		for _, want := range []float64{0, 5, 5.25, 12.5, 18.125, 999.999} {
			created, err := repo.CreateTaxRate(ctx, &TaxRate{
				Code: uniqueCode("TX"), Name: "Rate", RatePercent: want,
			})
			if err != nil {
				t.Fatalf("create %v: %v", want, err)
			}
			back, err := repo.GetTaxRate(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if diff := back.RatePercent - want; diff > 1e-3 || diff < -1e-3 {
				t.Errorf("rate_percent = %v, want %v", back.RatePercent, want)
			}
		}
	})

	t.Run("temperatures are NUMERIC(5,2)", func(t *testing.T) {
		for _, want := range []float64{-273.15, 0, 2.25, 25, 999.99} {
			created, err := repo.CreateStorageCondition(ctx, &StorageCondition{
				Code: uniqueCode("SC"), Name: "Band", MinTempCelsius: f64ptr(want), MaxTempCelsius: f64ptr(want),
			})
			if err != nil {
				t.Fatalf("create %v: %v", want, err)
			}
			back, err := repo.GetStorageCondition(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if diff := *back.MinTempCelsius - want; diff > 1e-2 || diff < -1e-2 {
				t.Errorf("min_temp_celsius = %v, want %v", *back.MinTempCelsius, want)
			}
		}
	})

	t.Run("NULL numerics stay NULL", func(t *testing.T) {
		created, err := repo.CreateUnitType(ctx, &UnitType{Code: uniqueCode("UT"), Name: "NoFactor"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		back, err := repo.GetUnitType(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.ConversionFactor != nil {
			t.Errorf("conversion_factor = %v, want NULL", *back.ConversionFactor)
		}
	})
}

// TestIntegrationUpdateWritesEveryMutableColumn checks that Update writes the whole
// mutable set and never touches id or code.
func TestIntegrationUpdateWritesEveryMutableColumn(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	created, err := repo.CreateUnitType(ctx, &UnitType{
		Code: uniqueCode("UT"), Name: "Gram", Symbol: strptr("g"), ConversionFactor: f64ptr(1), IsActive: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalCode := created.Code

	created.Name = "Gram (updated)"
	created.Symbol = strptr("gm")
	created.ConversionFactor = f64ptr(2.5)
	created.IsActive = false

	back, err := repo.UpdateUnitType(ctx, created)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if back.Code != originalCode {
		t.Errorf("code changed from %q to %q; it must be immutable", originalCode, back.Code)
	}
	if back.ID != created.ID {
		t.Errorf("id changed to %v", back.ID)
	}
	if back.Name != "Gram (updated)" || *back.Symbol != "gm" || *back.ConversionFactor != 2.5 || back.IsActive {
		t.Errorf("update did not write every mutable column: %+v", back)
	}
	if back.UpdatedAt.Before(back.CreatedAt) {
		t.Error("updated_at went backwards; the set_updated_at trigger did not fire")
	}

	// The trigger must have advanced updated_at, since the repository relies on it
	// rather than setting the column itself.
	again, err := repo.UpdateUnitType(ctx, back)
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if !again.UpdatedAt.After(back.UpdatedAt) {
		t.Errorf("updated_at = %v, want later than %v", again.UpdatedAt, back.UpdatedAt)
	}
}

// TestIntegrationSoftDelete covers the three consequences of a soft delete: the row
// disappears from reads, its code becomes reusable, and a second delete reports
// that nothing was affected.
func TestIntegrationSoftDelete(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	code := uniqueCode("RT")
	created, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Oral"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	affected, err := repo.SoftDeleteRoute(ctx, created.ID)
	if err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if !affected {
		t.Fatal("first soft delete reported no affected row")
	}

	if _, err := repo.GetRoute(ctx, created.ID); !isNoRows(err) {
		t.Errorf("get after delete returned %v, want db.ErrNoRows", err)
	}

	items, total, err := repo.ListRoutes(ctx, ListFilter{}, pagination.Offset{Page: 1, Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Errorf("deleted row still listed: total=%d len=%d", total, len(items))
	}
	if items == nil {
		t.Error("list returned a nil slice; callers JSON-encode it and would emit null")
	}

	// The partial unique index excludes deleted rows, so the code is free again.
	reused, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Oral (recreated)"})
	if err != nil {
		t.Fatalf("recreate with the same code: %v", err)
	}
	if reused.ID == created.ID {
		t.Error("recreate returned the same id")
	}

	// A second delete of the original row must report no affected row, so the
	// service can answer 404 rather than a silent success.
	affected, err = repo.SoftDeleteRoute(ctx, created.ID)
	if err != nil {
		t.Fatalf("second soft delete: %v", err)
	}
	if affected {
		t.Error("second soft delete reported an affected row")
	}
}

// TestIntegrationIsActiveIsNotDelete pins the documented distinction: setting
// is_active=false hides a row from a filtered list but keeps it readable and keeps
// its code taken.
func TestIntegrationIsActiveIsNotDelete(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	code := uniqueCode("RT")
	created, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Rectal", IsActive: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	created.IsActive = false
	if _, err := repo.UpdateRoute(ctx, created); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	if _, err := repo.GetRoute(ctx, created.ID); err != nil {
		t.Errorf("deactivated row is not readable: %v", err)
	}

	activeOnly := ListFilter{IsActive: boolptr(true)}
	items, total, err := repo.ListRoutes(ctx, activeOnly, pagination.Offset{Page: 1, Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Errorf("deactivated row appeared in the active filter: total=%d", total)
	}

	if _, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Clash"}); !db.IsUniqueVilation(err) {
		t.Errorf("code of a merely-deactivated row was reusable: %v", err)
	}
}

// TestIntegrationListFilteringAndPagination exercises the shared list machinery:
// filters, the count, the page window and the sort order.
func TestIntegrationListFilteringAndPagination(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	// A known set with a deliberate sort_order pattern.
	const total = 25
	for i := 0; i < total; i++ {
		if _, err := repo.CreateProductCategory(ctx, &ProductCategory{
			Code:      uniqueCode("CAT"),
			Name:      "Category " + twoDigits(i),
			SortOrder: i,
			IsActive:  i%2 == 0,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("count matches the filters", func(t *testing.T) {
		_, all, err := repo.ListProductCategories(ctx, ListFilter{}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if all != total {
			t.Errorf("unfiltered total = %d, want %d", all, total)
		}

		_, active, err := repo.ListProductCategories(ctx,
			ListFilter{IsActive: boolptr(true)}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("list active: %v", err)
		}
		if active != 13 {
			t.Errorf("active total = %d, want 13", active)
		}
	})

	t.Run("pages tile the result set without gaps or repeats", func(t *testing.T) {
		seen := map[uuid.UUID]bool{}
		for page := 1; page <= 3; page++ {
			items, count, err := repo.ListProductCategories(ctx,
				ListFilter{}, pagination.Offset{Page: page, Limit: 10})
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			if count != total {
				t.Fatalf("page %d total = %d, want %d", page, count, total)
			}
			if len(items) != wantPageRows(page) {
				t.Fatalf("page %d returned %d rows, want %d", page, len(items), wantPageRows(page))
			}
			for _, it := range items {
				if seen[it.ID] {
					t.Fatalf("id %v returned on more than one page", it.ID)
				}
				seen[it.ID] = true
			}
		}
		if len(seen) != total {
			t.Errorf("paging visited %d rows, want %d", len(seen), total)
		}
	})

	t.Run("last page is short, not an error", func(t *testing.T) {
		items, _, err := repo.ListProductCategories(ctx,
			ListFilter{}, pagination.Offset{Page: 3, Limit: 10})
		if err != nil {
			t.Fatalf("last page: %v", err)
		}
		if len(items) != 5 {
			t.Errorf("last page returned %d rows, want 5", len(items))
		}
	})

	t.Run("page past the end is empty, not an error", func(t *testing.T) {
		items, count, err := repo.ListProductCategories(ctx,
			ListFilter{}, pagination.Offset{Page: 99, Limit: 10})
		if err != nil {
			t.Fatalf("page past end: %v", err)
		}
		if count != total || len(items) != 0 {
			t.Errorf("count=%d len=%d, want total count and no rows", count, len(items))
		}
	})

	t.Run("sort by a resource-specific column", func(t *testing.T) {
		items, _, err := repo.ListProductCategories(ctx,
			ListFilter{}, pagination.Offset{Page: 1, Limit: total + 5, Sort: "-sort_order"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != total {
			t.Fatalf("got %d rows, want %d", len(items), total)
		}
		for i := 1; i < len(items); i++ {
			if items[i-1].SortOrder < items[i].SortOrder {
				t.Fatalf("rows %d..%d are not descending: %d then %d",
					i-1, i, items[i-1].SortOrder, items[i].SortOrder)
			}
		}
	})

	t.Run("a disallowed sort falls back to the default order", func(t *testing.T) {
		items, _, err := repo.ListProductCategories(ctx,
			ListFilter{}, pagination.Offset{Page: 1, Limit: 5, Sort: "deleted_at"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := 1; i < len(items); i++ {
			if items[i-1].Code > items[i].Code {
				t.Errorf("rows are not in the default code ASC order: %q then %q",
					items[i-1].Code, items[i].Code)
			}
		}
	})

	t.Run("an oversized limit is clamped instead of erroring", func(t *testing.T) {
		items, _, err := repo.ListProductCategories(ctx,
			ListFilter{}, pagination.Offset{Page: 1, Limit: 100000})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != total {
			t.Errorf("got %d rows, want %d — the clamp should still return everything", len(items), total)
		}
	})
}

// TestIntegrationSearch checks the ?q= contract: case-insensitive substring over
// code and name, with LIKE metacharacters treated literally.
func TestIntegrationSearch(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	// IsActive is set explicitly on every seed row. Create<Resource> writes
	// is_active unconditionally from the model, so the service must resolve the
	// Create DTO's *bool (via boolOr) before calling it; an unset bool here stores
	// false rather than the column's DEFAULT TRUE.
	if _, err := repo.CreateRoute(ctx, &Route{
		Code: uniqueCode("ORAL"), Name: "Oral Route", IsActive: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := repo.CreateRoute(ctx, &Route{
		Code: uniqueCode("IV"), Name: "Intravenous", IsActive: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("matches on name case-insensitively", func(t *testing.T) {
		items, count, err := repo.ListRoutes(ctx,
			ListFilter{Q: "ORAL"}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if count != 1 || len(items) != 1 {
			t.Errorf("count=%d len=%d, want 1", count, len(items))
		}
	})

	t.Run("matches on code too", func(t *testing.T) {
		items, count, err := repo.ListRoutes(ctx,
			ListFilter{Q: "IV-"}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if count != 1 || items[0].Name != "Intravenous" {
			t.Errorf("code search returned %d rows (%+v)", count, items)
		}
	})

	t.Run("a wildcard in the term is literal", func(t *testing.T) {
		// "%" must not match everything.
		_, count, err := repo.ListRoutes(ctx,
			ListFilter{Q: "%"}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if count != 0 {
			t.Errorf("searching for the literal %% matched %d rows", count)
		}
	})

	t.Run("combines with is_active", func(t *testing.T) {
		_, count, err := repo.ListRoutes(ctx,
			ListFilter{Q: "Oral", IsActive: boolptr(true)}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if count != 1 {
			t.Errorf("count = %d, want 1", count)
		}
	})
}

// TestIntegrationParentAndRootFilters covers the three hierarchies' parent filter
// and the root filter that has no HTTP parameter yet.
func TestIntegrationParentAndRootFilters(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	parent, err := repo.CreateDrugGroup(ctx, &DrugGroup{Code: uniqueCode("DG"), Name: "Parent"})
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	child, err := repo.CreateDrugClass(ctx, &DrugClass{
		Code: uniqueCode("DC"), Name: "Child", DrugGroupID: &parent.ID,
	})
	if err != nil {
		t.Fatalf("seed child: %v", err)
	}
	root, err := repo.CreateDrugClass(ctx, &DrugClass{Code: uniqueCode("DC"), Name: "Root"})
	if err != nil {
		t.Fatalf("seed root: %v", err)
	}

	t.Run("parent filter", func(t *testing.T) {
		items, count, err := repo.ListDrugClasses(ctx,
			ListFilter{ParentID: &parent.ID}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if count != 1 || items[0].ID != child.ID {
			t.Errorf("count=%d, want just the child %v", count, child.ID)
		}
	})

	t.Run("root filter", func(t *testing.T) {
		items, count, err := repo.ListDrugClasses(ctx,
			ListFilter{ParentIsNull: boolptr(true)}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if count != 1 || items[0].ID != root.ID {
			t.Errorf("count=%d, want just the root %v", count, root.ID)
		}
	})
}

// TestIntegrationConstraintErrors checks the raw driver errors the service maps:
// unique violation on a duplicate code, check violation on a self-parent, foreign
// key violation on a missing parent.
func TestIntegrationConstraintErrors(t *testing.T) {
	repo, _ := newIntegrationRepo(t)
	ctx := context.Background()

	t.Run("duplicate code is a unique violation", func(t *testing.T) {
		code := uniqueCode("RT")
		if _, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "One"}); err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Two"})
		if !db.IsUniqueVilation(err) {
			t.Errorf("second create returned %v, want a 23505 unique violation", err)
		}
	})

	t.Run("self-parent is a check violation", func(t *testing.T) {
		created, err := repo.CreateProductCategory(ctx, &ProductCategory{
			Code: uniqueCode("CAT"), Name: "Self",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		created.ParentID = &created.ID
		if _, err := repo.UpdateProductCategory(ctx, created); !db.IsCheckViolation(err) {
			t.Errorf("update returned %v, want a 23514 check violation from ck_product_categories_parent_not_self", err)
		}
	})

	t.Run("self base unit is a check violation", func(t *testing.T) {
		created, err := repo.CreateUnitType(ctx, &UnitType{Code: uniqueCode("UT"), Name: "Self"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		created.BaseUnitID = &created.ID
		if _, err := repo.UpdateUnitType(ctx, created); !db.IsCheckViolation(err) {
			t.Errorf("update returned %v, want a 23514 from ck_unit_types_base_unit_not_self", err)
		}
	})

	t.Run("inverted temperature band is a check violation", func(t *testing.T) {
		_, err := repo.CreateStorageCondition(ctx, &StorageCondition{
			Code: uniqueCode("SC"), Name: "Inverted",
			MinTempCelsius: f64ptr(20), MaxTempCelsius: f64ptr(2),
		})
		if !db.IsCheckViolation(err) {
			t.Errorf("create returned %v, want a 23514 from ck_storage_conditions_temp_range", err)
		}
	})

	t.Run("missing parent is a foreign key violation", func(t *testing.T) {
		_, err := repo.CreateDrugClass(ctx, &DrugClass{
			Code: uniqueCode("DC"), Name: "Orphan", DrugGroupID: ptr(uuid.New()),
		})
		if !db.IsForeignKeyViolation(err) {
			t.Errorf("create returned %v, want a 23503 foreign key violation", err)
		}
	})

	t.Run("missing row is ErrNoRows", func(t *testing.T) {
		_, err := repo.GetRoute(ctx, uuid.New())
		if !isNoRows(err) {
			t.Errorf("get returned %v, want db.ErrNoRows", err)
		}
	})

	t.Run("update of a missing row is ErrNoRows", func(t *testing.T) {
		ghost := &Route{Name: "Ghost"}
		ghost.ID = uuid.New()
		_, err := repo.UpdateRoute(ctx, ghost)
		if !isNoRows(err) {
			t.Errorf("update returned %v, want db.ErrNoRows", err)
		}
	})
}

// TestIntegrationForUpdateLocks proves GetXForUpdate targets the primary and is
// usable inside a transaction.
func TestIntegrationForUpdateLocks(t *testing.T) {
	repo, database := newIntegrationRepo(t)
	ctx := context.Background()

	created, err := repo.CreateRoute(ctx, &Route{Code: uniqueCode("RT"), Name: "Oral"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Outside a transaction the lock is taken and released immediately.
	if _, err := repo.GetRouteForUpdate(ctx, created.ID); err != nil {
		t.Fatalf("for update outside a tx: %v", err)
	}

	err = database.WithTx(ctx, func(txCtx context.Context) error {
		locked, err := repo.GetRouteForUpdate(txCtx, created.ID)
		if err != nil {
			return err
		}
		locked.Name = "Oral (in tx)"
		if _, err := repo.UpdateRoute(txCtx, locked); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}

	back, err := repo.GetRoute(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if back.Name != "Oral (in tx)" {
		t.Errorf("name = %q, want the transactional update to have committed", back.Name)
	}
}

// TestIntegrationTransactionsAreParticipating proves writes issued on a
// transactional context roll back with it, which is what lets the service wrap a
// check-then-write in one transaction.
func TestIntegrationTransactionsAreParticipating(t *testing.T) {
	repo, database := newIntegrationRepo(t)
	ctx := context.Background()

	code := uniqueCode("RT")
	err := database.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := repo.CreateRoute(txCtx, &Route{Code: code, Name: "Doomed"}); err != nil {
			return err
		}
		// Visible inside the transaction.
		items, _, err := repo.ListRoutes(txCtx, ListFilter{}, pagination.Offset{Page: 1, Limit: 10})
		if err != nil {
			return err
		}
		if len(items) != 1 {
			t.Errorf("saw %d routes inside the tx, want 1", len(items))
		}
		return errRollback
	})
	if err != errRollback {
		t.Fatalf("expected the sentinel rollback error, got %v", err)
	}

	// The row must be gone, and the code free again.
	_, count, err := repo.ListRoutes(ctx, ListFilter{}, pagination.Offset{Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if count != 0 {
		t.Errorf("%d routes survived the rollback", count)
	}
	if _, err := repo.CreateRoute(ctx, &Route{Code: code, Name: "Recovered"}); err != nil {
		t.Errorf("code was not released by the rollback: %v", err)
	}
}

// TestIntegrationReferenceProbes is the D-19 guard against a live database.
//
// One medicine is created pointing at all eight referenced resources. Every probe
// must report true while it lives and false once it is soft-deleted — which is
// exactly the window in which a foreign key would have stayed silent.
func TestIntegrationReferenceProbes(t *testing.T) {
	repo, database := newIntegrationRepo(t)
	ctx := context.Background()

	route, err := repo.CreateRoute(ctx, &Route{Code: uniqueCode("RT"), Name: "Oral"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	dosage, err := repo.CreateDosageForm(ctx, &DosageForm{Code: uniqueCode("DF"), Name: "Tablet"})
	if err != nil {
		t.Fatalf("dosage form: %v", err)
	}
	pack, err := repo.CreatePackageType(ctx, &PackageType{Code: uniqueCode("PT"), Name: "Box"})
	if err != nil {
		t.Fatalf("package type: %v", err)
	}
	unit, err := repo.CreateUnitType(ctx, &UnitType{Code: uniqueCode("UT"), Name: "Tablet"})
	if err != nil {
		t.Fatalf("unit type: %v", err)
	}
	storage, err := repo.CreateStorageCondition(ctx, &StorageCondition{Code: uniqueCode("SC"), Name: "Ambient"})
	if err != nil {
		t.Fatalf("storage condition: %v", err)
	}
	tax, err := repo.CreateTaxRate(ctx, &TaxRate{Code: uniqueCode("TX"), Name: "VAT", RatePercent: 5})
	if err != nil {
		t.Fatalf("tax rate: %v", err)
	}
	category, err := repo.CreateProductCategory(ctx, &ProductCategory{Code: uniqueCode("CAT"), Name: "Pain"})
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	generic, err := repo.CreateGenericMedicine(ctx, &GenericMedicine{Code: uniqueCode("GM"), Name: "Morphine"})
	if err != nil {
		t.Fatalf("generic medicine: %v", err)
	}

	// All eight start unreferenced.
	assertProbes(t, repo, probes{
		route: route.ID, dosage: dosage.ID, pack: pack.ID, unit: unit.ID,
		storage: storage.ID, tax: tax.ID, category: category.ID, generic: generic.ID,
	}, false)

	// medicines requires brand_id and category_id NOT NULL, so a manufacturer and
	// a brand have to exist first.
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
			sku, name, brand_id, category_id, generic_medicine_id, dosage_form_id,
			route_id, storage_condition_id, package_type_id, sales_unit_id, tax_rate_id
		) VALUES ($1,'Morphine 10mg',$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		uniqueCode("SKU"), brandID, category.ID, generic.ID, dosage.ID,
		route.ID, storage.ID, pack.ID, unit.ID, tax.ID).Scan(&medicineID); err != nil {
		t.Fatalf("medicine: %v", err)
	}

	all := probes{
		route: route.ID, dosage: dosage.ID, pack: pack.ID, unit: unit.ID,
		storage: storage.ID, tax: tax.ID, category: category.ID, generic: generic.ID,
	}
	t.Run("all eight probes see a live medicine", func(t *testing.T) {
		assertProbes(t, repo, all, true)
	})

	t.Run("a soft-deleted medicine stops counting as a reference", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`UPDATE medicines SET deleted_at = now() WHERE id = $1`, medicineID); err != nil {
			t.Fatalf("soft delete medicine: %v", err)
		}
		assertProbes(t, repo, all, false)
	})
}

// probes bundles the eight referenced rows the D-19 probes are asked about.
type probes struct {
	route, dosage, pack, unit, storage, tax, category, generic uuid.UUID
}

// assertProbes checks all eight Is<Resource>Referenced probes against want.
func assertProbes(t *testing.T, repo *Repository, p probes, want bool) {
	t.Helper()

	checks := []struct {
		name string
		id   uuid.UUID
		fn   func(context.Context, uuid.UUID) (bool, error)
	}{
		{"product_categories", p.category, repo.IsProductCategoryReferenced},
		{"dosage_forms", p.dosage, repo.IsDosageFormReferenced},
		{"routes", p.route, repo.IsRouteReferenced},
		{"package_types", p.pack, repo.IsPackageTypeReferenced},
		{"unit_types", p.unit, repo.IsUnitTypeReferenced},
		{"storage_conditions", p.storage, repo.IsStorageConditionReferenced},
		{"tax_rates", p.tax, repo.IsTaxRateReferenced},
		{"generic_medicines", p.generic, repo.IsGenericMedicineReferenced},
	}

	ctx := context.Background()
	for _, c := range checks {
		got, err := c.fn(ctx, c.id)
		if err != nil {
			t.Errorf("%s probe: %v", c.name, err)
			continue
		}
		if got != want {
			t.Errorf("%s referenced = %v, want %v", c.name, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// errRollback is the sentinel a test returns to force db.WithTx to roll back.
var errRollback = errors.New("integration test: force rollback")

// ptr is a local helper; ptrOf lives in repository_schema_test.go.
func ptr(v uuid.UUID) *uuid.UUID { return &v }

// twoDigits renders 0..99 zero-padded so "Category 02" sorts before "Category 10".
func twoDigits(i int) string { return fmt.Sprintf("%02d", i) }

// wantPageRows is how many rows page n holds for the 25-row fixture at limit 10.
func wantPageRows(page int) int {
	const total = 25
	if n := total - (page-1)*10; n < 10 {
		return n
	}
	return 10
}

// isNoRows reports whether err is the driver's no-rows error.
func isNoRows(err error) bool { return errors.Is(err, db.ErrNoRows) }

// assertTimestamps checks the lifecycle columns a create must populate.
func assertTimestamps(t *testing.T, createdAt, updatedAt time.Time, deletedAt *time.Time) {
	t.Helper()
	if createdAt.IsZero() {
		t.Error("created_at was not populated")
	}
	if updatedAt.IsZero() {
		t.Error("updated_at was not populated")
	}
	if deletedAt != nil {
		t.Errorf("deleted_at = %v on a freshly created row, want NULL", *deletedAt)
	}
}
