package sale

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common"
	appctx "backend/internal/common/context"
	"backend/internal/middleware"
	"backend/pkg/pagination"
)

type saleHTTPServiceStub struct {
	listScope SaleScope
	listQuery ListSalesQuery
	listCalls int
	sales     []Sale
	total     int64
	hasMore   bool
	detail    *SaleDetail
}

func (s *saleHTTPServiceStub) GetSale(_ context.Context, scope SaleScope, _ uuid.UUID) (*SaleDetail, error) {
	s.listScope = scope
	return s.detail, nil
}

func (s *saleHTTPServiceStub) ListSales(_ context.Context, scope SaleScope, query ListSalesQuery) ([]Sale, int64, bool, error) {
	s.listScope = scope
	s.listQuery = query
	s.listCalls++
	return s.sales, s.total, s.hasMore, nil
}

type invoiceHTTPServiceStub struct{}

func (invoiceHTTPServiceStub) GetInvoice(_ context.Context, _ SaleScope, _ uuid.UUID) (*SaleInvoiceResponse, error) {
	return &SaleInvoiceResponse{InvoiceNo: "INV-1"}, nil
}

func newTestSaleHandler(t *testing.T, svc *saleHTTPServiceStub) (*Handler, *pagination.CursorCodec) {
	t.Helper()
	key := []byte("sale-handler-test-signing-key-32bytes")
	codec, err := pagination.NewCursorCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(svc, invoiceHTTPServiceStub{}, codec, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return handler, codec
}

func TestSaleListUsesAuthenticatedScopeAndReturnsCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID, branchID, saleID := uuid.New(), uuid.New(), uuid.New()
	soldAt := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	svc := &saleHTTPServiceStub{
		sales:   []Sale{{BaseModel: common.BaseModel{ID: saleID}, BranchID: branchID, SaleNo: "S-1", SoldAt: soldAt}},
		total:   2,
		hasMore: true,
	}
	handler, codec := newTestSaleHandler(t, svc)
	engine := gin.New()
	engine.GET("/sales", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/sales?limit=1", nil)
	req = req.WithContext(appctx.WithPrincipal(req.Context(), appctx.Principal{
		UserID:    uuid.New(),
		OrgID:     orgID,
		BranchID:  &branchID,
		BranchIDs: []uuid.UUID{branchID},
	}))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("List returned %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if svc.listCalls != 1 || svc.listScope.OrganizationID != orgID || !reflect.DeepEqual(svc.listScope.BranchIDs, []uuid.UUID{branchID}) {
		t.Fatalf("service received unexpected authenticated scope: %+v", svc.listScope)
	}

	var body struct {
		Success    bool            `json:"success"`
		Data       []SaleListItem  `json:"data"`
		Pagination pagination.Meta `json:"pagination"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || len(body.Data) != 1 || !body.Pagination.HasMore || body.Pagination.NextCursor == "" {
		t.Fatalf("unexpected list response: %+v", body)
	}
	cursor, err := codec.Decode(body.Pagination.NextCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	if !cursor.CreatedAt.Equal(soldAt) || cursor.ID != saleID {
		t.Fatalf("cursor points to (%s, %s), want (%s, %s)", cursor.CreatedAt, cursor.ID, soldAt, saleID)
	}
}

func TestSaleListRejectsUnsupportedQueryParameter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &saleHTTPServiceStub{}
	handler, _ := newTestSaleHandler(t, svc)
	engine := gin.New()
	engine.GET("/sales", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/sales?sort=total_minor", nil)
	req = req.WithContext(appctx.WithPrincipal(req.Context(), appctx.Principal{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
	}))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("List returned %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if svc.listCalls != 0 {
		t.Fatalf("unsupported query reached the service")
	}
}

func TestSaleListRejectsInvalidCursorBeforeServiceCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &saleHTTPServiceStub{}
	handler, _ := newTestSaleHandler(t, svc)
	engine := gin.New()
	engine.GET("/sales", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/sales?cursor=not-a-signed-cursor", nil)
	req = req.WithContext(appctx.WithPrincipal(req.Context(), appctx.Principal{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
	}))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("List returned %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if svc.listCalls != 0 {
		t.Fatalf("invalid cursor reached the service")
	}
}

func TestSaleListDoesNotEmitCursorOnLastPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &saleHTTPServiceStub{
		sales: []Sale{{BaseModel: common.BaseModel{ID: uuid.New()}, SoldAt: time.Now().UTC()}},
		total: 1,
	}
	handler, _ := newTestSaleHandler(t, svc)
	engine := gin.New()
	engine.GET("/sales", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/sales?limit=1", nil)
	req = req.WithContext(appctx.WithPrincipal(req.Context(), appctx.Principal{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
	}))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("List returned %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Pagination pagination.Meta `json:"pagination"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Pagination.HasMore || body.Pagination.NextCursor != "" {
		t.Fatalf("last page advertised another page: %+v", body.Pagination)
	}
}

func TestSaleRoutesExposeOnlyReadOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newTestSaleHandler(t, &saleHTTPServiceStub{})
	engine := gin.New()
	handler.RegisterRoutes(engine.Group("/api/v1"), middleware.New(nil, nil, nil))

	var got []string
	for _, route := range engine.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	want := []string{
		"GET /api/v1/sales",
		"GET /api/v1/sales/:id",
		"GET /api/v1/sales/:id/invoice",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered routes = %v, want %v", got, want)
	}
}
