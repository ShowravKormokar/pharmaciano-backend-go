package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMetricsHandlerAuth verifies the /metrics Bearer guard. A regression test
// for the missing-space bug where the handler compared the raw header to
// "Bearer"+token, so the well-formed "Bearer <token>" always 401'd.
func TestMetricsHandlerAuth(t *testing.T) {
	m := NewMetrics()
	h := m.Handler("s3cret-token")

	do := func(auth string) int {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	// Well-formed header with the standard single space must pass.
	if code := do("Bearer s3cret-token"); code != http.StatusOK {
		t.Fatalf("expected 200 for well-formed 'Bearer <token>', got %d", code)
	}
	// Scheme is case-insensitive; extra whitespace around token tolerated.
	if code := do("bearer  s3cret-token"); code != http.StatusOK {
		t.Fatalf("expected 200 for lowercase scheme + extra whitespace, got %d", code)
	}
	// Wrong token, missing scheme, and absent header must all fail closed.
	for _, bad := range []string{"Bearer wrong", "s3cret-token", "Basic s3cret-token", ""} {
		if code := do(bad); code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for header %q, got %d", bad, code)
		}
	}

	// No token configured => handler is open (not wrapped).
	// open := NewMetrics().Handler("")
	_ = NewMetrics().Handler("")
	if code := do(""); code != http.StatusOK {
		t.Fatalf("expected 200 when no token configured, got %d", code)
	}
}
