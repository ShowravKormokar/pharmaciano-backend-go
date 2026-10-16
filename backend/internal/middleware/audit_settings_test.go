package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	"backend/internal/platform/telemetry"
)

type auditReadSinkStub struct {
	entries []AuditEntry
}

func (s *auditReadSinkStub) Record(_ context.Context, entry AuditEntry) {
	s.entries = append(s.entries, entry)
}

func TestAuditReadRecordsAuthorizedReadWithoutQueryValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sink := &auditReadSinkStub{}
	orgID := uuid.New()
	userID := uuid.New()
	ctx := appctx.WithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil).Context(), appctx.Principal{
		UserID: userID, OrgID: orgID, RoleName: constants.RoleAuditor,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/private-id?occurred_at=secret", nil).
		WithContext(ctx)
	recorder := httptest.NewRecorder()

	router := gin.New()
	mw := &Middleware{audit: sink}
	router.GET("/api/v1/audit-logs/:id", mw.AuditRead(), func(c *gin.Context) {
		c.Set(ginKeyAuditModule, constants.ModuleAudit)
		c.Set(ginKeyAuditAction, constants.ActionView)
		c.Status(http.StatusOK)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("response status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if len(sink.entries) != 1 {
		t.Fatalf("recorded entries = %d, want 1", len(sink.entries))
	}
	entry := sink.entries[0]
	if entry.UserID != userID || entry.OrgID != orgID ||
		entry.Module != constants.ModuleAudit || entry.Action != constants.ActionView ||
		entry.Method != http.MethodGet || !entry.Success {
		t.Fatalf("read event missing identity or route metadata: %+v", entry)
	}
	if entry.Path != "/api/v1/audit-logs/private-id" ||
		strings.Contains(entry.Path, "occurred_at") || len(entry.RequestBody) != 0 {
		t.Fatalf("read event includes sensitive query/body data: %+v", entry)
	}
}

func TestAuditReadRateLimitFailsClosed(t *testing.T) {
	for _, policy := range []string{"audit_read", "audit_read_ip"} {
		if !securityCriticalRatePolicy(policy) {
			t.Errorf("%s is not configured as a fail-closed rate policy", policy)
		}
	}
}

func TestAuditRateLimitDenialsAreExportedToTelemetry(t *testing.T) {
	metrics := telemetry.NewMetrics()
	mw := &Middleware{metrics: metrics}
	mw.observeRateLimited("audit_read")

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "security_rate_limited_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "policy" && label.GetValue() == "audit_read" {
					if got := metric.GetCounter().GetValue(); got != 1 {
						t.Fatalf("audit rate-limit counter = %v, want 1", got)
					}
					return
				}
			}
		}
	}
	t.Fatal("audit rate-limit metric was not exported")
}

func TestSettingsAuditRedactsValuesRecursively(t *testing.T) {
	raw := []byte(`{"scope":"organization","value":{"nested":"secret"},"items":[{"key":"app.name","value":"Pharmaciano"}]}`)
	body := maskBodyForModule(raw, "settings")

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal masked audit body: %v", err)
	}
	if result["value"] != auditRedaction {
		t.Fatalf("top-level setting value was not redacted: %v", result["value"])
	}
	item := result["items"].([]any)[0].(map[string]any)
	if item["value"] != auditRedaction {
		t.Fatalf("bulk setting value was not redacted: %v", item["value"])
	}
}

func TestOtherModuleAuditPreservesNonSensitiveValueFields(t *testing.T) {
	body := maskBodyForModule([]byte(`{"value":"visible"}`), "products")
	if string(body) != `{"value":"visible"}` {
		t.Fatalf("unexpected audit body: %s", body)
	}
}
