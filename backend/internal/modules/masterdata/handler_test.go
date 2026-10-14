package masterdata

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	errs "backend/internal/errors"
)

func TestParseIDParamValid(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	id := uuid.New()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: id.String()}}

	got, err := parseIDParam(c, "id")
	if err != nil {
		t.Fatalf("parseIDParam: %v", err)
	}
	if got != id {
		t.Fatalf("got %s, want %s", got, id)
	}
}

func TestParseIDParamInvalid(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	for _, val := range []string{"not-a-uuid", "", "12345", "gggggggg-gggg-gggg-gggg-gggggggggggg"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "id", Value: val}}

		_, err := parseIDParam(c, "id")
		if err == nil {
			t.Fatalf("parseIDParam(%q): expected error, got nil", val)
		}
		if !errs.Has(err, errs.CodeValidationError) {
			t.Fatalf("parseIDParam(%q): code = %q, want %q", val, errs.CodeOf(err), errs.CodeValidationError)
		}
	}
}

func TestParseIDParamCustomName(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	id := uuid.New()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "branch_id", Value: id.String()}}

	got, err := parseIDParam(c, "branch_id")
	if err != nil {
		t.Fatalf("parseIDParam: %v", err)
	}
	if got != id {
		t.Fatalf("got %s, want %s", got, id)
	}
}

func TestNewConstructor(t *testing.T) {
	t.Parallel()

	h := New(nil, nil, nil)
	if h == nil {
		t.Fatal("New returned nil handler")
	}
	if h.svc == nil {
		t.Fatal("New did not wire the service")
	}
}
