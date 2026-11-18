package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
)

type salesReaderStub struct {
	dailyAgg   SalesDailyAggregate
	dailyErr   error
	dailyCalls int
	lastDaily  struct {
		scope        SalesReadScope
		businessDate time.Time
		currency     string
	}

	overviewBuckets []SalesOverviewAggregateBucket
	overviewMeta    SalesOverviewAggregateMeta
	overviewErr     error
	overviewCalls   int
	lastOverview    struct {
		scope    SalesReadScope
		from, to time.Time
		groupBy  GroupBy
		currency string
	}
}

func (s *salesReaderStub) SumSalesDaily(_ context.Context, scope SalesReadScope, businessDate time.Time, currency string) (SalesDailyAggregate, error) {
	s.dailyCalls++
	s.lastDaily.scope = scope
	s.lastDaily.businessDate = businessDate
	s.lastDaily.currency = currency
	return s.dailyAgg, s.dailyErr
}

func (s *salesReaderStub) AggregateSalesOverview(_ context.Context, scope SalesReadScope, from, to time.Time, groupBy GroupBy, currency string) ([]SalesOverviewAggregateBucket, SalesOverviewAggregateMeta, error) {
	s.overviewCalls++
	s.lastOverview.scope = scope
	s.lastOverview.from = from
	s.lastOverview.to = to
	s.lastOverview.groupBy = groupBy
	s.lastOverview.currency = currency
	return s.overviewBuckets, s.overviewMeta, s.overviewErr
}

func ctxWithAnalyticsView(orgID uuid.UUID, branchIDs []uuid.UUID, orgWide bool) context.Context {
	p := appctx.Principal{
		UserID:       uuid.New(),
		OrgID:        orgID,
		SessionID:    uuid.New(),
		RoleName:     constants.RoleAdmin,
		Permissions:  []string{constants.ModuleAnalytics + ":view"},
		AuthzVersion: 1,
	}
	if orgWide {
		// org-wide: no branch subset in principal; BranchScope returns orgWide=true
	} else if len(branchIDs) > 0 {
		b := make([]uuid.UUID, len(branchIDs))
		copy(b, branchIDs)
		p.BranchIDs = b
		p.BranchID = &b[0]
	}
	return appctx.WithPrincipal(context.Background(), p)
}

func TestSalesOverviewQueryNormalizeAndValidate(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	q := &SalesOverviewQuery{From: now, To: now.AddDate(0, 0, 1)}
	q.Normalize()
	if err := q.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	if q.GroupBy != GroupByDay || q.Currency != "BDT" {
		t.Fatalf("defaults not applied: group=%q currency=%q", q.GroupBy, q.Currency)
	}

	bad := &SalesOverviewQuery{From: now, To: now, GroupBy: GroupByDay, Currency: "BDT"}
	bad.Normalize()
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for from==to")
	}

	usd := &SalesOverviewQuery{From: now, To: now.AddDate(0, 0, 1), GroupBy: GroupByDay, Currency: "USD"}
	usd.Normalize()
	if err := usd.Validate(); err == nil {
		t.Fatal("expected error for non-BDT currency")
	}

	tooLong := &SalesOverviewQuery{From: now, To: now.AddDate(2, 0, 0), GroupBy: GroupByDay, Currency: "BDT"}
	tooLong.Normalize()
	if err := tooLong.Validate(); err == nil {
		t.Fatal("expected error for range > max")
	}
}

func TestDashboardQueryValidate(t *testing.T) {
	if err := (&DashboardQuery{}).Validate(); err != nil {
		t.Fatalf("nil-branch dashboard should be valid: %v", err)
	}
	nilID := uuid.Nil
	if err := (&DashboardQuery{BranchID: &nilID}).Validate(); err == nil {
		t.Fatal("expected error for nil branch uuid")
	}
}

func TestResolveSalesScope(t *testing.T) {
	orgID := uuid.New()
	branchA, branchB := uuid.New(), uuid.New()

	orgWideCtx := ctxWithAnalyticsView(orgID, nil, true)
	scope, selected, err := resolveSalesScope(orgWideCtx, nil)
	if err != nil || !scope.AllBranches || selected != nil {
		t.Fatalf("org-wide unresolved scope: scope=%+v selected=%v err=%v", scope, selected, err)
	}
	scope, selected, err = resolveSalesScope(orgWideCtx, &branchA)
	if err != nil || scope.AllBranches || len(scope.BranchIDs) != 1 || scope.BranchIDs[0] != branchA || selected == nil {
		t.Fatalf("org-wide with branch filter: scope=%+v selected=%v err=%v", scope, selected, err)
	}

	branchCtx := ctxWithAnalyticsView(orgID, []uuid.UUID{branchA}, false)
	scope, _, err = resolveSalesScope(branchCtx, &branchA)
	if err != nil || len(scope.BranchIDs) != 1 || scope.BranchIDs[0] != branchA {
		t.Fatalf("branch-bound allowed filter: scope=%+v err=%v", scope, err)
	}
	if _, _, err := resolveSalesScope(branchCtx, &branchB); err == nil {
		t.Fatal("expected forbidden for branch outside effective scope")
	}

	noPermCtx := appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID: uuid.New(), OrgID: orgID, SessionID: uuid.New(), RoleName: constants.RoleCashier,
	})
	if _, _, err := resolveSalesScope(noPermCtx, nil); err == nil {
		t.Fatal("expected forbidden without analytics:view")
	}
}

func TestGetDashboardUnavailableWhenProjectionMissing(t *testing.T) {
	orgID := uuid.New()
	repo := &salesReaderStub{dailyAgg: SalesDailyAggregate{Found: false}}
	svc := NewService(repo)
	fixed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	resp, err := svc.GetDashboard(ctxWithAnalyticsView(orgID, nil, true), &DashboardQuery{})
	if err != nil {
		t.Fatalf("GetDashboard() error: %v", err)
	}
	if resp.Freshness.SourceStatus != SourceStatusDegraded {
		t.Fatalf("expected degraded status, got %q", resp.Freshness.SourceStatus)
	}
	if resp.Freshness.FreshnessLagSeconds != nil {
		t.Fatal("expected no lag when projection missing")
	}
	if resp.Totals.NetSales.MinorUnits != 0 || resp.Totals.CompletedSaleCount != 0 {
		t.Fatalf("expected zero totals when unavailable, got %+v", resp.Totals)
	}
	if resp.Totals.NetSales.Currency != "BDT" {
		t.Fatalf("expected BDT currency, got %q", resp.Totals.NetSales.Currency)
	}
	if repo.dailyCalls != 1 || repo.lastDaily.currency != "BDT" {
		t.Fatalf("unexpected repo call: calls=%d currency=%q", repo.dailyCalls, repo.lastDaily.currency)
	}
}

func TestGetDashboardReturnsTotalsAndFreshness(t *testing.T) {
	orgID := uuid.New()
	asOf := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	repo := &salesReaderStub{dailyAgg: SalesDailyAggregate{
		Found: true, CompletedSaleCount: 3, GrossSales: 5000, DiscountTotal: 200,
		TaxTotal: 300, NetSales: 5100, ReturnTotal: 0, UnitsSold: 7,
		ProjectionVersion: 2, DataAsOf: &asOf, SourceStatus: SourceStatusCurrent,
	}}
	svc := NewService(repo)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }

	resp, err := svc.GetDashboard(ctxWithAnalyticsView(orgID, nil, true), &DashboardQuery{})
	if err != nil {
		t.Fatalf("GetDashboard() error: %v", err)
	}
	if resp.Freshness.SourceStatus != SourceStatusCurrent {
		t.Fatalf("expected current status, got %q", resp.Freshness.SourceStatus)
	}
	if resp.Freshness.FreshnessLagSeconds == nil || *resp.Freshness.FreshnessLagSeconds != 3600 {
		t.Fatalf("expected 3600s lag, got %#v", resp.Freshness.FreshnessLagSeconds)
	}
	if resp.Totals.NetSales.MinorUnits != 5100 || resp.Totals.CompletedSaleCount != 3 || resp.Totals.UnitsSold != 7 {
		t.Fatalf("unexpected totals: %+v", resp.Totals)
	}
}

func TestGetSalesOverviewReturnsBuckets(t *testing.T) {
	orgID := uuid.New()
	day1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	repo := &salesReaderStub{
		overviewBuckets: []SalesOverviewAggregateBucket{{
			PeriodStart: day1, PeriodEnd: day2,
			CompletedSaleCount: 2, GrossSales: 1000, DiscountTotal: 0, TaxTotal: 0, NetSales: 1000, ReturnTotal: 0, UnitsSold: 4,
		}},
		overviewMeta: SalesOverviewAggregateMeta{Found: true, ProjectionVersion: 1, SourceStatus: SourceStatusCurrent},
	}
	svc := NewService(repo)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }

	resp, err := svc.GetSalesOverview(ctxWithAnalyticsView(orgID, nil, true), &SalesOverviewQuery{
		From: day1, To: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), GroupBy: GroupByDay, Currency: "BDT",
	})
	if err != nil {
		t.Fatalf("GetSalesOverview() error: %v", err)
	}
	if len(resp.Buckets) != 1 || resp.Buckets[0].Totals.NetSales.MinorUnits != 1000 {
		t.Fatalf("unexpected buckets: %+v", resp.Buckets)
	}
	if resp.GroupBy != GroupByDay || resp.Freshness.SourceStatus != SourceStatusCurrent {
		t.Fatalf("unexpected response meta: group=%s status=%s", resp.GroupBy, resp.Freshness.SourceStatus)
	}
}

func TestGetSalesOverviewUnavailableWhenNoRows(t *testing.T) {
	orgID := uuid.New()
	repo := &salesReaderStub{overviewBuckets: []SalesOverviewAggregateBucket{}, overviewMeta: SalesOverviewAggregateMeta{Found: false}}
	svc := NewService(repo)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }

	resp, err := svc.GetSalesOverview(ctxWithAnalyticsView(orgID, nil, true), &SalesOverviewQuery{
		From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("GetSalesOverview() error: %v", err)
	}
	if resp.Freshness.SourceStatus != SourceStatusDegraded || len(resp.Buckets) != 0 {
		t.Fatalf("expected degraded empty response: status=%s buckets=%d", resp.Freshness.SourceStatus, len(resp.Buckets))
	}
}

func TestServiceRequiresAuthenticationAndPermission(t *testing.T) {
	svc := NewService(&salesReaderStub{})
	if _, err := svc.GetDashboard(context.Background(), &DashboardQuery{}); !errs.Has(err, errs.CodeUnauthenticated) && errs.HTTPStatus(err) != 401 {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
	ctx := appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID: uuid.New(), OrgID: uuid.New(), SessionID: uuid.New(), RoleName: constants.RoleCashier,
	})
	if _, err := svc.GetDashboard(ctx, &DashboardQuery{}); !errs.Has(err, errs.CodeForbidden) && errs.HTTPStatus(err) != 403 {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestHandlerRejectsInvalidDashboardQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: NewService(&salesReaderStub{}), log: zap.NewNop()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/analytics/dashboard?unknown=x", nil)
	h.GetDashboard(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown param, got %d body=%s", w.Code, w.Body.String())
	}
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

func TestHandlerDashboardSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	repo := &salesReaderStub{dailyAgg: SalesDailyAggregate{Found: true, NetSales: 1234, SourceStatus: SourceStatusCurrent}}
	svc := NewService(repo)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) }
	h := &Handler{svc: svc, log: zap.NewNop()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/analytics/dashboard", nil)
	req = req.WithContext(ctxWithAnalyticsView(orgID, nil, true))
	c.Request = req
	h.GetDashboard(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var resp DashboardResponse
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Totals.NetSales.MinorUnits != 1234 {
		t.Fatalf("unexpected net sales: %+v", resp.Totals.NetSales)
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unexpected cache-control: %q", w.Header().Get("Cache-Control"))
	}
}

func TestHandlerSalesOverviewRequiresDatesAndRejectsBadDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	h := &Handler{svc: NewService(&salesReaderStub{}), log: zap.NewNop()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/analytics/sales/overview?from=2026-11-01", nil)
	req = req.WithContext(ctxWithAnalyticsView(orgID, nil, true))
	c.Request = req
	h.GetSalesOverview(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing to, got %d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	req = httptest.NewRequest(http.MethodGet, "/analytics/sales/overview?from=not-a-date&to=2026-11-02", nil)
	req = req.WithContext(ctxWithAnalyticsView(orgID, nil, true))
	c.Request = req
	h.GetSalesOverview(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad date, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestParseAnalyticsDate(t *testing.T) {
	got, err := parseAnalyticsDate("from", "2026-11-17", true)
	if err != nil || got.Format("2006-01-02") != "2026-11-17" {
		t.Fatalf("date-only parse: got=%v err=%v", got, err)
	}
	got, err = parseAnalyticsDate("from", "2026-11-17T10:30:00Z", true)
	if err != nil || got.Hour() != 10 {
		t.Fatalf("rfc3339 parse: got=%v err=%v", got, err)
	}
	if _, err := parseAnalyticsDate("from", "", true); err == nil {
		t.Fatal("expected required error")
	}
}

func TestBucketExpressionsAndStatusResolve(t *testing.T) {
	start, end, err := bucketExpressions(GroupByDay)
	if err != nil || start == "" || end == "" {
		t.Fatalf("day bucket expr err=%v", err)
	}
	if _, _, err := bucketExpressions(GroupBy("year")); err == nil {
		t.Fatal("expected error for unsupported group")
	}
	if (aggregateStatusFlags{AnyDegraded: true}).resolve() != SourceStatusDegraded {
		t.Fatal("degraded should win")
	}
	if (aggregateStatusFlags{AnyDelayed: true}).resolve() != SourceStatusDelayed {
		t.Fatal("delayed expected")
	}
	if (aggregateStatusFlags{}).resolve() != SourceStatusCurrent {
		t.Fatal("current expected")
	}
}
