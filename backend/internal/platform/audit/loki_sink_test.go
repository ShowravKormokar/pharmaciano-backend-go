package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/middleware"
	"backend/internal/platform/config"
	"backend/internal/platform/telemetry"
)

func TestLokiAuditSinkReportsPushOutcomes(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		wantResult string
	}{
		{name: "success", status: http.StatusNoContent, wantResult: "success"},
		{name: "server error", status: http.StatusServiceUnavailable, wantResult: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			metrics := telemetry.NewMetrics()
			sink := NewLokiAuditSink(config.LokiSinkState{
				Enabled: true, URL: server.URL, BatchSize: 1, Timeout: time.Second,
			}, nil, metrics)
			sink.Record(context.Background(), middleware.AuditEntry{
				Timestamp: time.Now().UTC(), OrgID: uuid.New(), Module: "audit", Action: "view",
			})
			sink.Stop()

			if got := sinkMetric(t, metrics, test.wantResult); got != 1 {
				t.Fatalf("Loki %s metric = %v, want 1", test.wantResult, got)
			}
		})
	}
}

func sinkMetric(t *testing.T, metrics *telemetry.Metrics, outcome string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "audit_sink_writes_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["sink"] == "loki" && labels["outcome"] == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
