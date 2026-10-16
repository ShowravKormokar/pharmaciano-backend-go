package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIDocsServeAllAuditReadOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(filepath.Join("..", ".."))

	engine := gin.New()
	registerDocs(engine, Deps{})

	request := httptest.NewRequest(http.MethodGet, "/api-docs/openapi.yaml", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api-docs/openapi.yaml status = %d, want %d", recorder.Code, http.StatusOK)
	}

	spec := recorder.Body.String()
	for _, expected := range []string{
		"  /audit-logs:",
		"  /audit-logs/modules:",
		"  /audit-logs/actions:",
		"  /audit-logs/{id}:",
		"  /audit-logs/platform:",
		"  /audit-logs/platform/{id}:",
		"operationId: audit.list",
		"operationId: audit.listModules",
		"operationId: audit.listActions",
		"operationId: audit.get",
		"operationId: audit.listPlatformEvents",
		"operationId: audit.getPlatformEvent",
	} {
		if !strings.Contains(spec, expected) {
			t.Errorf("served OpenAPI spec is missing %q", expected)
		}
	}
	if strings.Contains(spec, "$ref:") {
		t.Error("served OpenAPI spec must be self-contained with all references inlined")
	}
}
