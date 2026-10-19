package brand

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"

	"backend/internal/middleware"
	"backend/internal/platform/telemetry"
	"backend/pkg/pagination"
)

func TestUpdateBrandRequestIsEmpty(t *testing.T) {
	if !(UpdateBrandRequest{}).IsEmpty() {
		t.Fatal("zero value should be empty")
	}
	r := UpdateBrandRequest{Name: strPtr("x")}
	if r.IsEmpty() {
		t.Fatal("non-zero should not be empty")
	}
}

func TestPatchNullableStringsDistinguishOmittedNullAndValue(t *testing.T) {
	var omitted UpdateBrandRequest
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatalf("unmarshal omitted patch: %v", err)
	}
	if omitted.CountryOfOrigin.Set || omitted.Description.Set {
		t.Fatal("omitted nullable fields must remain unset")
	}

	var cleared UpdateBrandRequest
	if err := json.Unmarshal([]byte(`{"country_of_origin":null,"description":null}`), &cleared); err != nil {
		t.Fatalf("unmarshal clear patch: %v", err)
	}
	if !cleared.CountryOfOrigin.Set || cleared.CountryOfOrigin.Value != nil ||
		!cleared.Description.Set || cleared.Description.Value != nil {
		t.Fatal("explicit null must be represented as a set, nil value")
	}

	var provided UpdateBrandRequest
	if err := json.Unmarshal([]byte(`{"country_of_origin":"Bangladesh"}`), &provided); err != nil {
		t.Fatalf("unmarshal value patch: %v", err)
	}
	if !provided.CountryOfOrigin.Set || provided.CountryOfOrigin.Value == nil ||
		*provided.CountryOfOrigin.Value != "Bangladesh" {
		t.Fatal("explicit value must be preserved")
	}
	if cleared.IsEmpty() || !omitted.IsEmpty() {
		t.Fatal("IsEmpty must treat explicit null as a change")
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	got := likePattern(`A_!%`)
	if want := `%A!_!!!%%`; got != want {
		t.Fatalf("likePattern() = %q, want %q", got, want)
	}
}

func TestValidateBrandFields(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		brand   string
		country *string
		wantErr bool
	}{
		{name: "valid", code: "A-1", brand: "Brand"},
		{name: "blank code", code: " ", brand: "Brand", wantErr: true},
		{name: "blank name", code: "A-1", brand: " \t", wantErr: true},
		{name: "long code", code: strings.Repeat("x", maxCodeLen+1), brand: "Brand", wantErr: true},
		{name: "long country", code: "A-1", brand: "Brand", country: strPtr(strings.Repeat("x", maxCountryLen+1)), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBrandFields(strings.TrimSpace(tt.code), strings.TrimSpace(tt.brand), trimOptional(tt.country))
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBrandFields() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestBuildBrandOrderByUsesStableAllowListedColumns(t *testing.T) {
	got, err := buildBrandOrderBy([]pagination.SortField{
		{Field: "name", Desc: true},
		{Field: "created_at"},
	})
	if err != nil {
		t.Fatalf("buildBrandOrderBy() error = %v", err)
	}
	if want := "name DESC, created_at ASC, id ASC"; got != want {
		t.Fatalf("buildBrandOrderBy() = %q, want %q", got, want)
	}
	if _, err := buildBrandOrderBy([]pagination.SortField{{Field: "name; DROP TABLE brands"}}); err == nil {
		t.Fatal("unsafe sort field should return an error")
	}
}

func TestRegisterRoutesMountsBrandOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	mw := middleware.New(nil, nil, nil)
	(&Handler{}).RegisterRoutes(engine.Group("/api/v1"), mw)

	want := map[string]string{
		"POST /api/v1/brands":       "Create",
		"GET /api/v1/brands":        "List",
		"GET /api/v1/brands/:id":    "Get",
		"PATCH /api/v1/brands/:id":  "Update",
		"DELETE /api/v1/brands/:id": "Delete",
	}
	for _, route := range engine.Routes() {
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

func TestBrandRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	(&Handler{}).RegisterRoutes(engine.Group("/api/v1"), middleware.New(nil, nil, nil))

	for _, operation := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/brands"},
		{http.MethodGet, "/api/v1/brands"},
		{http.MethodGet, "/api/v1/brands/00000000-0000-0000-0000-000000000001"},
		{http.MethodPatch, "/api/v1/brands/00000000-0000-0000-0000-000000000001"},
		{http.MethodDelete, "/api/v1/brands/00000000-0000-0000-0000-000000000001"},
	} {
		request := httptest.NewRequest(operation.method, operation.path, nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		wantStatus := http.StatusUnauthorized
		if operation.method != http.MethodGet {
			wantStatus = http.StatusServiceUnavailable
		}
		if recorder.Code != wantStatus {
			t.Errorf("%s %s returned %d, want %d", operation.method, operation.path, recorder.Code, wantStatus)
		}
	}
}

func TestBrandRequestsAreRecordedBySharedHTTPMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.NewMetrics()
	engine := gin.New()
	engine.Use(telemetry.GinMiddleware(metrics))
	(&Handler{}).RegisterRoutes(engine.Group("/api/v1"), middleware.New(nil, nil, nil))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/brands?q=private-search-term", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if !hasBrandRequestMetric(families, http.MethodGet, "/api/v1/brands", http.StatusUnauthorized) {
		t.Fatal("shared HTTP metrics did not record the bounded brand route and response status")
	}
}

func hasBrandRequestMetric(families []*dto.MetricFamily, method, route string, status int) bool {
	for _, family := range families {
		if family.GetName() != "http_requests_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["method"] == method &&
				labels["route"] == route &&
				labels["status"] == strconv.Itoa(status) &&
				metric.GetCounter().GetValue() == 1 {
				return true
			}
		}
	}
	return false
}

func strPtr(s string) *string { return &s }
