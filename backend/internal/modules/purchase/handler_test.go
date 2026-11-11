package purchase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/pkg/pagination"
)

type purchaseHTTPServiceStub struct {
	order     *PurchaseOrderResponse
	list      []PurchaseOrderResponse
	meta      pagination.Meta
	getErr    error
	listErr   error
	gotID     uuid.UUID
	gotQuery  *ListPurchaseOrdersQuery
	getCalled bool
}

func (s *purchaseHTTPServiceStub) Get(_ context.Context, id uuid.UUID) (*PurchaseOrderResponse, error) {
	s.gotID = id
	s.getCalled = true
	return s.order, s.getErr
}

func (s *purchaseHTTPServiceStub) List(_ context.Context, query *ListPurchaseOrdersQuery) ([]PurchaseOrderResponse, pagination.Meta, error) {
	s.gotQuery = query
	return s.list, s.meta, s.listErr
}

func newPurchaseHTTPTestRouter(t *testing.T, service *purchaseHTTPServiceStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, err := newHandler(service, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}
	router := gin.New()
	router.GET("/purchases", handler.List)
	router.GET("/purchases/:id", handler.Get)
	return router
}

func TestNewRequiresDatabase(t *testing.T) {
	if _, err := New(nil, nil, zap.NewNop()); err == nil {
		t.Fatal("New() accepted a nil database")
	}
}

func TestPurchaseHandlerListBindsFiltersAndReturnsList(t *testing.T) {
	service := &purchaseHTTPServiceStub{
		list: []PurchaseOrderResponse{{PurchaseNo: "PO-123"}},
		meta: pagination.Meta{Page: 2, Limit: 5, Total: 6, TotalPages: 2, HasMore: false},
	}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/purchases?page=2&limit=5&q=urgent", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("List status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if service.gotQuery == nil || service.gotQuery.Page != 2 || service.gotQuery.Limit != 5 ||
		service.gotQuery.Q == nil || *service.gotQuery.Q != "urgent" {
		t.Fatalf("List query = %+v, want page=2 limit=5 q=urgent", service.gotQuery)
	}
	if !strings.Contains(recorder.Body.String(), "PO-123") {
		t.Fatalf("List response does not contain the purchase order: %s", recorder.Body.String())
	}
}

func TestPurchaseHandlerListRejectsUnsupportedQueryParameters(t *testing.T) {
	service := &purchaseHTTPServiceStub{}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/purchases?organization_id=another-tenant", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("List status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if service.gotQuery != nil {
		t.Fatal("service was called for an unsupported query parameter")
	}
}

func TestPurchaseHandlerListRejectsRepeatedQueryParameters(t *testing.T) {
	service := &purchaseHTTPServiceStub{}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/purchases?page=1&page=2", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("List status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if service.gotQuery != nil {
		t.Fatal("service was called for a repeated query parameter")
	}
}

func TestPurchaseHandlerGetParsesIDAndReturnsOrder(t *testing.T) {
	id := uuid.New()
	service := &purchaseHTTPServiceStub{order: &PurchaseOrderResponse{ID: id, PurchaseNo: "PO-123"}}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/purchases/"+id.String(), nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("Get status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.getCalled || service.gotID != id {
		t.Fatalf("Get called with %v, want %v", service.gotID, id)
	}
	if !strings.Contains(recorder.Body.String(), "PO-123") {
		t.Fatalf("Get response does not contain the purchase order: %s", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Get Cache-Control = %q, want private, no-store", got)
	}
}

func TestPurchaseHandlerGetRejectsMalformedIDWithoutCallingService(t *testing.T) {
	service := &purchaseHTTPServiceStub{}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/purchases/not-a-uuid", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Get status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if service.getCalled {
		t.Fatal("service was called with a malformed purchase ID")
	}
}

func TestPurchaseHandlerGetMapsServiceError(t *testing.T) {
	service := &purchaseHTTPServiceStub{getErr: errors.New("database details")}
	router := newPurchaseHTTPTestRouter(t, service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/purchases/"+uuid.NewString(), nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("Get status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "database details") {
		t.Fatalf("internal error leaked through response: %s", recorder.Body.String())
	}
}
