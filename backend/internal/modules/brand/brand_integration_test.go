package brand

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	apperrors "backend/internal/errors"
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

const brandTestDSNEnv = "BRAND_TEST_DSN"

// Run only against a disposable database migrated through 000024. The test
// inserts uniquely identified fixtures and cleans up only those rows.
func TestBrandTenantIsolationAndReferences(t *testing.T) {
	database := newBrandIntegrationDB(t)
	ctx := context.Background()
	orgIDs := []uuid.UUID{uuid.New(), uuid.New()}
	manufacturerIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	brandIDs := make([]uuid.UUID, 0, 3)
	medicineIDs := make([]uuid.UUID, 0, 2)
	categoryID := uuid.New()

	t.Cleanup(func() {
		for _, id := range medicineIDs {
			if _, err := database.Pool().Exec(ctx, `DELETE FROM medicines WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup medicine: %v", err)
			}
		}
		for _, id := range brandIDs {
			if _, err := database.Pool().Exec(ctx, `DELETE FROM brands WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup brand: %v", err)
			}
		}
		if _, err := database.Pool().Exec(ctx, `DELETE FROM product_categories WHERE id = $1`, categoryID); err != nil {
			t.Errorf("cleanup category: %v", err)
		}
		for _, id := range manufacturerIDs {
			if _, err := database.Pool().Exec(ctx, `DELETE FROM manufacturers WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup manufacturer: %v", err)
			}
		}
		for _, id := range orgIDs {
			if _, err := database.Pool().Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup organization: %v", err)
			}
		}
		database.Close()
	})

	for _, id := range orgIDs {
		if _, err := database.Pool().Exec(ctx,
			`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)`,
			id, "Brand test organization", "brand-test-"+id.String(),
		); err != nil {
			t.Fatalf("insert organization: %v", err)
		}
	}
	for i, id := range manufacturerIDs {
		orgID := orgIDs[0]
		if i == 2 {
			orgID = orgIDs[1]
		}
		if _, err := database.Pool().Exec(ctx,
			`INSERT INTO manufacturers (id, organization_id, code, name) VALUES ($1, $2, $3, $4)`,
			id, orgID, "MFR-"+id.String(), "Brand test manufacturer",
		); err != nil {
			t.Fatalf("insert manufacturer: %v", err)
		}
	}
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO product_categories (id, code, name) VALUES ($1, $2, $3)`,
		categoryID, "CAT-"+categoryID.String(), "Brand test category",
	); err != nil {
		t.Fatalf("insert category: %v", err)
	}

	repo := NewRepository(database)
	service := NewService(repo, database)
	orgContexts := []context.Context{
		appctx.WithPrincipal(ctx, appctx.Principal{OrgID: orgIDs[0]}),
		appctx.WithPrincipal(ctx, appctx.Principal{OrgID: orgIDs[1]}),
	}
	create := func(orgCtx context.Context, manufacturerID uuid.UUID, code, name string, active bool) *Brand {
		t.Helper()
		created, err := service.Create(orgCtx, &CreateBrandRequest{
			Code:           code,
			Name:           name,
			ManufacturerID: manufacturerID,
			IsActive:       boolPtr(active),
		})
		if err != nil {
			t.Fatalf("create brand: %v", err)
		}
		brandIDs = append(brandIDs, created.ID)
		return created
	}

	first := create(orgContexts[0], manufacturerIDs[0], "SHARED-CODE", "Alpha Brand", true)
	create(orgContexts[0], manufacturerIDs[0], "INACTIVE", "Alpha Inactive", false)
	secondTenant := create(orgContexts[1], manufacturerIDs[2], "SHARED-CODE", "Alpha Brand", true)

	type createResult struct {
		brand *Brand
		err   error
	}
	start := make(chan struct{})
	results := make(chan createResult, 8)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			created, err := service.Create(orgContexts[0], &CreateBrandRequest{
				Code: "RACE-CODE", Name: "Concurrent contender", ManufacturerID: manufacturerIDs[0],
			})
			results <- createResult{brand: created, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successCount := 0
	for result := range results {
		if result.err == nil {
			successCount++
			brandIDs = append(brandIDs, result.brand.ID)
		} else if apperrors.HTTPStatus(result.err) != 409 {
			t.Errorf("concurrent duplicate create status = %d, want 409 (err=%v)", apperrors.HTTPStatus(result.err), result.err)
		}
	}
	if successCount != 1 {
		t.Fatalf("concurrent duplicate creates succeeded %d times, want exactly once", successCount)
	}

	if _, err := service.Create(orgContexts[0], &CreateBrandRequest{
		Code: "FOREIGN-MFR", Name: "Foreign manufacturer", ManufacturerID: manufacturerIDs[2],
	}); apperrors.HTTPStatus(err) != 404 {
		t.Fatalf("cross-tenant manufacturer create status = %d, want 404 (err=%v)", apperrors.HTTPStatus(err), err)
	}
	_, detailErr := service.Get(orgContexts[0], secondTenant.ID)
	_, updateErr := service.Update(orgContexts[0], secondTenant.ID, &UpdateBrandRequest{Name: strPtr("nope")})
	for name, err := range map[string]error{
		"detail": detailErr,
		"update": updateErr,
		"delete": service.Delete(orgContexts[0], secondTenant.ID),
	} {
		if apperrors.HTTPStatus(err) != 404 {
			t.Errorf("cross-tenant %s status = %d, want 404 (err=%v)", name, apperrors.HTTPStatus(err), err)
		}
	}
	if err := service.Delete(orgContexts[1], secondTenant.ID); err != nil {
		t.Fatalf("delete unused brand: %v", err)
	}
	if _, err := service.Get(orgContexts[1], secondTenant.ID); apperrors.HTTPStatus(err) != 404 {
		t.Fatalf("soft-deleted brand detail status = %d, want 404 (err=%v)", apperrors.HTTPStatus(err), err)
	}

	active := true
	items, meta, err := service.List(orgContexts[0], ListBrandsQuery{
		ManufacturerID: &manufacturerIDs[0],
		Q:              "Alpha",
		IsActive:       &active,
	}, pagination.Offset{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list brands: %v", err)
	}
	if meta.Total != 1 || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("composed tenant/filter list returned total=%d items=%d, want only the active matching brand", meta.Total, len(items))
	}

	medicineID := uuid.New()
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO medicines (id, sku, name, brand_id, category_id) VALUES ($1, $2, $3, $4, $5)`,
		medicineID, "MED-"+uuid.NewString(), "Brand reference test medicine", first.ID, categoryID,
	); err != nil {
		t.Fatalf("insert medicine: %v", err)
	} else {
		medicineIDs = append(medicineIDs, medicineID)
	}

	if err := service.Delete(orgContexts[0], first.ID); apperrors.HTTPStatus(err) != 409 {
		t.Fatalf("delete referenced brand status = %d, want 409 (err=%v)", apperrors.HTTPStatus(err), err)
	}
	if _, err := service.Update(orgContexts[0], first.ID, &UpdateBrandRequest{ManufacturerID: &manufacturerIDs[1]}); apperrors.HTTPStatus(err) != 409 {
		t.Fatalf("reassign referenced brand manufacturer status = %d, want 409 (err=%v)", apperrors.HTTPStatus(err), err)
	}
	if _, err := service.Update(orgContexts[0], first.ID, &UpdateBrandRequest{IsActive: boolPtr(false)}); err != nil {
		t.Fatalf("deactivate referenced brand: %v", err)
	}

	newMedicineID := uuid.New()
	_, err = database.Pool().Exec(ctx,
		`INSERT INTO medicines (id, sku, name, brand_id, category_id) VALUES ($1, $2, $3, $4, $5)`,
		newMedicineID, "MED-"+uuid.NewString(), "Inactive brand should be rejected", first.ID, categoryID,
	)
	if err == nil {
		medicineIDs = append(medicineIDs, newMedicineID)
		t.Fatal("database accepted a new medicine referencing an inactive brand")
	}
}

func newBrandIntegrationDB(t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv(brandTestDSNEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", brandTestDSNEnv)
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("%s must be a PostgreSQL URL", brandTestDSNEnv)
	}
	port := 5432
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			t.Fatalf("%s has an invalid port", brandTestDSNEnv)
		}
	}
	if strings.Trim(parsed.Path, "/") == "" {
		t.Fatalf("%s must include a database name", brandTestDSNEnv)
	}
	user, password := "", ""
	if parsed.User != nil {
		user = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	sslMode := parsed.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}

	database, err := db.New(context.Background(), config.DatabaseConfig{
		Host:             parsed.Hostname(),
		Port:             port,
		Name:             strings.Trim(parsed.Path, "/"),
		User:             user,
		Password:         password,
		SSLMode:          sslMode,
		Timezone:         "UTC",
		StatementTimeout: 30 * time.Second,
		Pool: config.DBPoolConfig{
			MaxConns:          4,
			MinConns:          1,
			HealthCheckPeriod: time.Minute,
		},
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("connect to %s: %v", brandTestDSNEnv, err)
	}
	return database
}

func boolPtr(value bool) *bool { return &value }
