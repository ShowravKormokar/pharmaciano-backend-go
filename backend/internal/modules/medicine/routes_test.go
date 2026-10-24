package medicine

import (
	"testing"

	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// TestRegisterRoutesMountsTheContractSurface checks that the mounted routes
// are exactly the ones the contract names (contract §8).
//
// It also proves that the static /search path and the /:id wildcard coexist:
// gin has to prefer the static segment, otherwise a search would be parsed as
// a medicine id.
func TestRegisterRoutesMountsTheContractSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := &Handler{}
	handler.RegisterRoutes(engine.Group("/api/v1"), middleware.New(nil, nil, nil))

	got := make(map[string]struct{})
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = struct{}{}
	}

	want := []string{
		"POST /api/v1/medicines",
		"GET /api/v1/medicines",
		"GET /api/v1/medicines/search",
		"POST /api/v1/medicines/lookup",
		"GET /api/v1/medicines/:id",
		"PATCH /api/v1/medicines/:id",
		"DELETE /api/v1/medicines/:id",
		"GET /api/v1/medicines/:id/variants",
		"POST /api/v1/medicines/:id/variants",
		"PATCH /api/v1/medicines/:id/variants/:variant_id",
		"DELETE /api/v1/medicines/:id/variants/:variant_id",
		"GET /api/v1/medicines/:id/barcodes",
		"POST /api/v1/medicines/:id/barcodes",
		"DELETE /api/v1/medicines/:id/barcodes/:bc_id",
		"GET /api/v1/medicines/:id/price-history",
	}
	for _, route := range want {
		if _, ok := got[route]; !ok {
			t.Errorf("missing route %q", route)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("registered route count = %d, want only the %d contract routes: %v", len(got), len(want), got)
	}
}
