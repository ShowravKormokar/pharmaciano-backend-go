package masterdata

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func TestRegisterRoutesCoversEveryMasterDataOperation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	mw := middleware.New(nil, nil, nil)
	(&Handler{}).RegisterRoutes(engine.Group("/api/v1"), mw)

	type resourceRoutes struct {
		slug, create, list, get, update, delete string
	}
	resources := []resourceRoutes{
		{"product-categories", "CreateProductCategory", "ListProductCategories", "GetProductCategory", "UpdateProductCategory", "DeleteProductCategory"},
		{"routes", "CreateRoute", "ListRoutes", "GetRoute", "UpdateRoute", "DeleteRoute"},
		{"dosage-forms", "CreateDosageForm", "ListDosageForms", "GetDosageForm", "UpdateDosageForm", "DeleteDosageForm"},
		{"package-types", "CreatePackageType", "ListPackageTypes", "GetPackageType", "UpdatePackageType", "DeletePackageType"},
		{"unit-types", "CreateUnitType", "ListUnitTypes", "GetUnitType", "UpdateUnitType", "DeleteUnitType"},
		{"storage-conditions", "CreateStorageCondition", "ListStorageConditions", "GetStorageCondition", "UpdateStorageCondition", "DeleteStorageCondition"},
		{"tax-rates", "CreateTaxRate", "ListTaxRates", "GetTaxRate", "UpdateTaxRate", "DeleteTaxRate"},
		{"drug-groups", "CreateDrugGroup", "ListDrugGroups", "GetDrugGroup", "UpdateDrugGroup", "DeleteDrugGroup"},
		{"drug-classes", "CreateDrugClass", "ListDrugClasses", "GetDrugClass", "UpdateDrugClass", "DeleteDrugClass"},
		{"therapeutic-classes", "CreateTherapeuticClass", "ListTherapeuticClasses", "GetTherapeuticClass", "UpdateTherapeuticClass", "DeleteTherapeuticClass"},
		{"generic-medicines", "CreateGenericMedicine", "ListGenericMedicines", "GetGenericMedicine", "UpdateGenericMedicine", "DeleteGenericMedicine"},
	}

	want := make(map[string]string, len(resources)*5)
	for _, resource := range resources {
		collection := "/api/v1/master/" + resource.slug
		item := collection + "/:id"
		want["POST "+collection] = resource.create
		want["GET "+collection] = resource.list
		want["GET "+item] = resource.get
		want["PATCH "+item] = resource.update
		want["DELETE "+item] = resource.delete
	}

	got := engine.Routes()
	if len(got) != len(want) {
		t.Fatalf("registered %d routes, want %d", len(got), len(want))
	}
	for _, route := range got {
		key := route.Method + " " + route.Path
		handler, ok := want[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if !strings.Contains(route.Handler, "."+handler) {
			t.Errorf("%s is bound to %q, want handler %s", key, route.Handler, handler)
		}
		delete(want, key)
	}
	for route := range want {
		t.Errorf("missing route %s", route)
	}
}

func TestMasterDataOperationsRejectAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	mw := middleware.New(nil, nil, nil)
	(&Handler{}).RegisterRoutes(engine.Group("/api/v1"), mw)

	resources := []string{
		"product-categories", "routes", "dosage-forms", "package-types", "unit-types",
		"storage-conditions", "tax-rates", "drug-groups", "drug-classes",
		"therapeutic-classes", "generic-medicines",
	}
	operations := []struct {
		method, suffix string
	}{
		{http.MethodPost, ""},
		{http.MethodGet, ""},
		{http.MethodGet, "/00000000-0000-0000-0000-000000000001"},
		{http.MethodPatch, "/00000000-0000-0000-0000-000000000001"},
		{http.MethodDelete, "/00000000-0000-0000-0000-000000000001"},
	}

	for _, slug := range resources {
		for _, operation := range operations {
			name := operation.method + " /api/v1/master/" + slug + operation.suffix
			t.Run(name, func(t *testing.T) {
				req := httptest.NewRequest(operation.method, "/api/v1/master/"+slug+operation.suffix, nil)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, req)
				wantStatus := http.StatusUnauthorized
				if operation.method != http.MethodGet {
					wantStatus = http.StatusServiceUnavailable
				}
				if recorder.Code != wantStatus {
					t.Errorf("anonymous request returned %d, want %d", recorder.Code, wantStatus)
				}
			})
		}
	}
}
