package audit

import (
	"testing"

	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func TestRegisterRoutesMountsReadOnlyAuditSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := &Handler{}
	handler.RegisterRoutes(engine.Group("/api/v1"), middleware.New(nil, nil, nil))

	got := make(map[string]struct{})
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = struct{}{}
	}
	want := []string{
		"GET /api/v1/audit-logs",
		"GET /api/v1/audit-logs/actions",
		"GET /api/v1/audit-logs/modules",
		"GET /api/v1/audit-logs/:id",
		"GET /api/v1/audit-logs/platform",
		"GET /api/v1/audit-logs/platform/:id",
	}
	for _, route := range want {
		if _, ok := got[route]; !ok {
			t.Errorf("missing route %q", route)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("registered route count = %d, want only %d read routes: %v", len(got), len(want), got)
	}
}
