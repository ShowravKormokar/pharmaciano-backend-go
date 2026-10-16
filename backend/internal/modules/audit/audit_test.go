package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	"backend/internal/middleware"
	platformaudit "backend/internal/platform/audit"
	"backend/internal/platform/telemetry"
)

func TestAuditReadServiceExportsBoundedOutcomeMetrics(t *testing.T) {
	metrics := telemetry.NewMetrics()
	repo := &auditReaderStub{
		listResult: AuditLogListResponse{Items: []AuditLogListItem{}},
	}
	service := NewService(repo, metrics)
	orgID := uuid.New()
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"})

	query := &ListAuditLogsQuery{AuditSearchFilters: validAuditFilters()}
	if _, err := service.List(ctx, query); err != nil {
		t.Fatalf("authorized List() error = %v", err)
	}
	if got := auditCounterValue(t, metrics, "audit_read_requests_total", map[string]string{
		"operation": "list", "outcome": "success",
	}); got != 1 {
		t.Fatalf("successful list metric = %v, want 1", got)
	}

	invalid := &ListAuditLogsQuery{AuditSearchFilters: AuditSearchFilters{
		From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z", Q: "ab",
	}}
	if _, err := service.List(ctx, invalid); err == nil {
		t.Fatal("invalid query unexpectedly succeeded")
	}
	if got := auditCounterValue(t, metrics, "audit_read_requests_total", map[string]string{
		"operation": "list", "outcome": "invalid",
	}); got != 1 {
		t.Fatalf("invalid list metric = %v, want 1", got)
	}

	if _, err := service.List(context.Background(), &ListAuditLogsQuery{
		AuditSearchFilters: validAuditFilters(),
	}); err == nil {
		t.Fatal("unauthorized query unexpectedly succeeded")
	}
	if got := auditCounterValue(t, metrics, "audit_read_requests_total", map[string]string{
		"operation": "list", "outcome": "denied",
	}); got != 1 {
		t.Fatalf("denied list metric = %v, want 1", got)
	}

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "audit_read_requests_total" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("audit read metric is not registered in the telemetry registry")
	}
}

func TestPostgresAuditSinkExposesUnavailableWriteMetric(t *testing.T) {
	metrics := telemetry.NewMetrics()
	sink := platformaudit.NewPostgresAuditSink(nil, nil, metrics)
	sink.Record(context.Background(), middleware.AuditEntry{})

	if got := auditCounterValue(t, metrics, "audit_sink_writes_total", map[string]string{
		"sink": "postgres", "outcome": "skipped",
	}); got != 1 {
		t.Fatalf("skipped sink write metric = %v, want 1", got)
	}
}

func auditCounterValue(t *testing.T, metrics *telemetry.Metrics, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if len(metric.GetLabel()) != len(labels) {
				continue
			}
			matched := true
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] != label.GetValue() {
					matched = false
					break
				}
			}
			if matched {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
