package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSaleMetricsAreRegistered(t *testing.T) {
	metrics := NewMetrics()
	metrics.SaleOperationsTotal.WithLabelValues("checkout", "failure").Inc()
	metrics.SaleOperationDuration.WithLabelValues("checkout").Observe(0.1)

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}
	for _, family := range families {
		found[family.GetName()] = true
	}
	for _, name := range []string{"sale_operations_total", "sale_operation_duration_seconds"} {
		if !found[name] {
			t.Errorf("metric %q was not registered", name)
		}
	}
}

func TestAnalyticsMetricsAreRegistered(t *testing.T) {
	metrics := NewMetrics()
	metrics.AnalyticsProjectionEvents.WithLabelValues("sales", "success").Inc()
	metrics.AnalyticsProjectionLag.WithLabelValues("sales").Set(1)
	metrics.AnalyticsProjectionFailures.WithLabelValues("sales", "temporary").Inc()
	metrics.AnalyticsQueryDuration.WithLabelValues("sales_overview", "success").Observe(0.1)
	metrics.AnalyticsCacheHits.WithLabelValues("sales_dashboard").Inc()
	metrics.AnalyticsExportJobs.WithLabelValues("sales", "ready").Inc()
	metrics.AnalyticsExportDuration.WithLabelValues("sales").Observe(1)
	metrics.AnalyticsExportBytes.Add(100)
	metrics.AnalyticsReconciliationMismatch.WithLabelValues("sale", "daily_total", "warning").Inc()
	metrics.AnalyticsWorkerQueueDepth.WithLabelValues("projection").Set(1)
	metrics.OutboxEventsTotal.WithLabelValues("sale.completed", "completed").Inc()
	metrics.OutboxProcessingDuration.WithLabelValues("sale.completed", "completed").Observe(0.1)
	metrics.OutboxQueueDepth.WithLabelValues("sale.completed").Set(1)

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}
	for _, family := range families {
		found[family.GetName()] = true
	}
	for _, name := range []string{
		"analytics_projection_events_total",
		"analytics_projection_lag_seconds",
		"analytics_projection_failures_total",
		"analytics_query_duration_seconds",
		"analytics_cache_hits_total",
		"analytics_export_jobs_total",
		"analytics_export_duration_seconds",
		"analytics_export_bytes_total",
		"analytics_reconciliation_mismatches_total",
		"analytics_worker_queue_depth",
		"outbox_events_total",
		"outbox_processing_duration_seconds",
		"outbox_queue_depth",
	} {
		if !found[name] {
			t.Errorf("metric %q was not registered", name)
		}
	}
}

func TestMetricsHandlerAuth(t *testing.T) {
	token := "metrics-test-token"
	protected := NewMetrics().Handler(token)

	do := func(handler http.Handler, auth string) int {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Code
	}

	for _, header := range []string{
		"Bearer " + token,
		"bearer  " + token,
	} {
		if code := do(protected, header); code != http.StatusOK {
			t.Errorf("valid authorization header %q returned %d, want %d", header, code, http.StatusOK)
		}
	}

	for _, header := range []string{
		"Bearer wrong-token",
		token,
		"Basic " + token,
		"",
	} {
		if code := do(protected, header); code != http.StatusUnauthorized {
			t.Errorf("invalid authorization header %q returned %d, want %d", header, code, http.StatusUnauthorized)
		}
	}

	open := NewMetrics().Handler("")
	if code := do(open, ""); code != http.StatusOK {
		t.Fatalf("metrics with no configured token returned %d, want %d", code, http.StatusOK)
	}
}
