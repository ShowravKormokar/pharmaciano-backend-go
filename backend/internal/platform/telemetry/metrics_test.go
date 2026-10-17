package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
