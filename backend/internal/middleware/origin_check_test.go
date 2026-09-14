package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	errs "backend/internal/errors"
	"backend/internal/platform/config"
)

const (
	goodOrigin = "http://localhost:5173"
	badOrigin  = "https://evil.example.com"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newOriginGuardEngine builds a gin engine wired with just the OriginGuard
// middleware, plus a handler that answers 204 so a pass-through is observable.
func newOriginGuardEngine(mw *Middleware) *gin.Engine {
	r := gin.New()
	r.Use(mw.OriginGuard())
	r.Any("/auth/password/change", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return r
}

func testConfig(originCheckEnabled bool) *config.Config {
	return &config.Config{
		CORS: config.CORSConfig{
			AllowOrigins: []string{goodOrigin},
		},
		Security: config.SecurityConfig{
			OriginCheck: struct {
				Enabled bool `mapstructure:"enabled"`
			}{originCheckEnabled},
		},
	}
}

func doPOST(r *gin.Engine, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/password/change", strings.NewReader("{}"))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOriginGuard_DisallowedCrossSiteOriginRejected(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	w := doPOST(r, badOrigin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), string(errs.CodeCrossOriginRequest)) {
		t.Fatalf("expected code %s in body, got %s", string(errs.CodeCrossOriginRequest), w.Body.String())
	}
}

func TestOriginGuard_AllowedOriginPasses(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	w := doPOST(r, goodOrigin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 pass-through, got %d", w.Code)
	}
}

func TestOriginGuard_SafeMethodIgnoresOrigin(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/auth/password/change", nil)
	req.Header.Set("Origin", badOrigin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected GET to pass through, got %d", w.Code)
	}
}

func TestOriginGuard_NoOriginNoRefererPasses(t *testing.T) {
	// curl / mobile / server-to-server send no Origin.
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	w := doPOST(r, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected non-browser POST to pass through, got %d", w.Code)
	}
}

func TestOriginGuard_DisallowedRefererRejected(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	req := httptest.NewRequest(http.MethodPost, "/auth/password/change", strings.NewReader("{}"))
	req.Header.Set("Referer", badOrigin+"/login")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from disallowed referer, got %d", w.Code)
	}
}

func TestOriginGuard_AllowedRefererPasses(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(true), nil, nil))
	req := httptest.NewRequest(http.MethodPost, "/auth/password/change", strings.NewReader("{}"))
	req.Header.Set("Referer", goodOrigin+"/settings")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from allowed referer, got %d", w.Code)
	}
}

func TestOriginGuard_DisabledIsNoop(t *testing.T) {
	r := newOriginGuardEngine(New(testConfig(false), nil, nil))
	w := doPOST(r, badOrigin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected disabled origin guard to pass through, got %d", w.Code)
	}
}

func TestOriginGuard_AllowAllOrigins_Noop(t *testing.T) {
	cfg := testConfig(true)
	cfg.CORS.AllowOrigins = []string{"*"}
	r := newOriginGuardEngine(New(cfg, nil, nil))
	w := doPOST(r, badOrigin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected allow-all to pass through, got %d", w.Code)
	}
}