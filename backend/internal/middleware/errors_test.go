package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	errs "backend/internal/errors"
)

func TestAbortErrorPreserves5xxStatusAndHidesDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := New(nil, nil, nil)
	engine := gin.New()
	engine.GET("/", func(c *gin.Context) {
		mw.abortError(c, errs.New(errs.CodeServiceUnavailable, "private backend detail"))
	})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success || body.Error.Code != string(errs.CodeInternal) || body.Error.Message != "internal server error" {
		t.Fatalf("unexpected generic error response: %+v", body)
	}
}
