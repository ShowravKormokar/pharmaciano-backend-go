package analytics

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/telemetry"
)

const (
	datasetDashboard      = "analytics_sales_dashboard"
	datasetSalesOverview  = "analytics_sales_overview"
	defaultDashboardTZ    = "UTC"
)

// SalesReader is the read-only persistence boundary for sales analytics
// projections. Implementations must enforce tenant scope in SQL.
type SalesReader interface {
	SumSalesDaily(ctx context.Context, scope SalesReadScope, businessDate time.Time, currency string) (SalesDailyAggregate, error)
	AggregateSalesOverview(ctx context.Context, scope SalesReadScope, from, to time.Time, groupBy GroupBy, currency string) ([]SalesOverviewAggregateBucket, SalesOverviewAggregateMeta, error)
}

// Service provides authorized, tenant-scoped sales analytics reads over
// Analytics-owned projections. It never reads source-domain tables and never
// mutates business facts. Routes remain gated until projection/event
// prerequisites and reconciliation gates are satisfied.
type Service struct {
	repo    SalesReader
	metrics *telemetry.Metrics
	now     func() time.Time
}

func NewService(repo SalesReader, metrics ...*telemetry.Metrics) *Service {
	s := &Service{
		repo: repo,
		now:  func() time.Time { return time.Now().UTC() },
	}
	if len(metrics) > 0 {
		s.metrics = metrics[0]
	}
	return s
}

func (s *Service) observeQuery(endpoint, result string, started time.Time) {
	if s == nil || s.metrics == nil || s.metrics.AnalyticsQueryDuration == nil {
		return
	}
	s.metrics.AnalyticsQueryDuration.WithLabelValues(endpoint, result).Observe(time.Since(started).Seconds())
}

func (s *Service) resultLabel(err error, unavailable bool) string {
	if err != nil {
		switch errs.HTTPStatus(err) {
		case 400, 422:
			return "invalid"
		case 401, 403:
			return "denied"
		default:
			return "error"
		}
	}
	if unavailable {
		return "unavailable"
	}
	return "success"
}

func requireAnalyticsView(ctx context.Context) error {
	if !appctx.IsAuthenticated(ctx) {
		return errs.Unauthenticated()
	}
	if appctx.OrgID(ctx) == uuid.Nil {
		return errs.Forbidden("an active organization is required")
	}
	if !appctx.HasPermission(ctx, constants.ModuleAnalytics, "view") {
		return errs.Forbidden("analytics view permission is required")
	}
	return nil
}

// resolveSalesScope derives the effective organization/branch scope from trusted
// context and intersects an optional client-requested branch selector. A requested
// branch outside effective scope is denied; it never broadens access.
func resolveSalesScope(ctx context.Context, requestedBranch *uuid.UUID) (SalesReadScope, *uuid.UUID, error) {
	if err := requireAnalyticsView(ctx); err != nil {
		return SalesReadScope{}, nil, err
	}
	orgID := appctx.OrgID(ctx)
	effective, orgWide := appctx.BranchScope(ctx)

	if requestedBranch != nil {
		if *requestedBranch == uuid.Nil {
			return SalesReadScope{}, nil, errs.Validation("branch_id must be a valid UUID")
		}
		if orgWide {
			return SalesReadScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{*requestedBranch}}, requestedBranch, nil
		}
		if !slices.Contains(effective, *requestedBranch) {
			return SalesReadScope{}, nil, errs.Forbidden("requested branch is outside effective scope")
		}
		return SalesReadScope{OrganizationID: orgID, BranchIDs: []uuid.UUID{*requestedBranch}}, requestedBranch, nil
	}

	if orgWide {
		return SalesReadScope{OrganizationID: orgID, AllBranches: true}, nil, nil
	}
	if len(effective) == 0 {
		return SalesReadScope{}, nil, errs.Forbidden("a valid branch scope is required")
	}
	scopeBranch := make([]uuid.UUID, len(effective))
	copy(scopeBranch, effective)
	return SalesReadScope{OrganizationID: orgID, BranchIDs: scopeBranch}, nil, nil
}

func (s *Service) generatedAt() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

// GetDashboard returns the sales-only dashboard snapshot for the authorized scope
// and business date. A missing projection is reported as degraded/unavailable and
// must not be presented as current zero data.
func (s *Service) GetDashboard(ctx context.Context, query *DashboardQuery) (resp DashboardResponse, retErr error) {
	started := s.generatedAt()
	unavailable := false
	defer func() { s.observeQuery(datasetDashboard, s.resultLabel(retErr, unavailable), started) }()

	if s == nil || s.repo == nil {
		return DashboardResponse{}, errs.Internal(fmt.Errorf("analytics repository is not configured"))
	}
	if query == nil {
		query = &DashboardQuery{}
	}
	query.Normalize()
	if err := query.Validate(); err != nil {
		return DashboardResponse{}, errs.Validation(err.Error())
	}

	scope, selectedBranch, err := resolveSalesScope(ctx, query.BranchID)
	if err != nil {
		return DashboardResponse{}, err
	}

	// Business timezone policy is unresolved; until org timezone is frozen we use UTC.
	// Do not trust browser locale.
	tz := time.UTC
	businessDate := started.In(tz)
	if query.Date != nil {
		businessDate = query.Date.In(tz)
	}
	businessDate = dateOnly(businessDate)
	currency := "BDT"

	agg, err := s.repo.SumSalesDaily(ctx, scope, businessDate, currency)
	if err != nil {
		return DashboardResponse{}, errs.DatabaseError(err)
	}

	status := agg.SourceStatus
	if !agg.Found {
		unavailable = true
		status = SourceStatusDegraded
	}

	freshness := FreshnessMetadata{
		GeneratedAt:       started,
		DataAsOf:          agg.DataAsOf,
		ProjectionVersion: agg.ProjectionVersion,
		SourceStatus:      status,
		Timezone:          defaultDashboardTZ,
		Currency:          currency,
	}
	if agg.Found && agg.DataAsOf != nil {
		lag := int64(started.Sub(*agg.DataAsOf).Seconds())
		if lag < 0 {
			lag = 0
		}
		freshness.FreshnessLagSeconds = &lag
	}

	totals := SalesTotals{
		CompletedSaleCount: agg.CompletedSaleCount,
		GrossSales:         MoneyValue{Currency: currency, MinorUnits: agg.GrossSales},
		DiscountTotal:      MoneyValue{Currency: currency, MinorUnits: agg.DiscountTotal},
		TaxTotal:           MoneyValue{Currency: currency, MinorUnits: agg.TaxTotal},
		NetSales:           MoneyValue{Currency: currency, MinorUnits: agg.NetSales},
		ReturnTotal:        MoneyValue{Currency: currency, MinorUnits: agg.ReturnTotal},
		UnitsSold:          agg.UnitsSold,
	}
	if !agg.Found {
		// Projection missing for scope/date: totals are not authoritative current data.
		// Keep explicit values but freshness status is degraded/unavailable.
		totals = zeroSalesTotals(currency)
	}

	return DashboardResponse{
		BranchID:     selectedBranch,
		BusinessDate: businessDate.Format("2006-01-02"),
		Totals:       totals,
		Freshness:    freshness,
	}, nil
}

// GetSalesOverview returns grouped additive sales buckets for an authorized,
// bounded range. Missing/stale projection state is disclosed via freshness.
func (s *Service) GetSalesOverview(ctx context.Context, query *SalesOverviewQuery) (resp SalesOverviewResponse, retErr error) {
	started := s.generatedAt()
	unavailable := false
	defer func() { s.observeQuery(datasetSalesOverview, s.resultLabel(retErr, unavailable), started) }()

	if s == nil || s.repo == nil {
		return SalesOverviewResponse{}, errs.Internal(fmt.Errorf("analytics repository is not configured"))
	}
	if query == nil {
		return SalesOverviewResponse{}, errs.Validation("sales overview query is required")
	}
	query.Normalize()
	if err := query.Validate(); err != nil {
		return SalesOverviewResponse{}, errs.Validation(err.Error())
	}

	scope, selectedBranch, err := resolveSalesScope(ctx, query.BranchID)
	if err != nil {
		return SalesOverviewResponse{}, err
	}

	from := dateOnly(query.From.UTC())
	to := dateOnly(query.To.UTC())
	if !from.Before(to) {
		return SalesOverviewResponse{}, errs.Validation("from must be earlier than to")
	}

	bucketsAgg, meta, err := s.repo.AggregateSalesOverview(ctx, scope, from, to, query.GroupBy, query.Currency)
	if err != nil {
		return SalesOverviewResponse{}, errs.DatabaseError(err)
	}

	status := meta.SourceStatus
	if !meta.Found {
		unavailable = true
		status = SourceStatusDegraded
	}

	buckets := make([]SalesOverviewBucket, 0, len(bucketsAgg))
	for _, b := range bucketsAgg {
		buckets = append(buckets, SalesOverviewBucket{
			PeriodStart: b.PeriodStart.Format("2006-01-02"),
			PeriodEnd:   b.PeriodEnd.Format("2006-01-02"),
			Totals: SalesTotals{
				CompletedSaleCount: b.CompletedSaleCount,
				GrossSales:         MoneyValue{Currency: query.Currency, MinorUnits: b.GrossSales},
				DiscountTotal:      MoneyValue{Currency: query.Currency, MinorUnits: b.DiscountTotal},
				TaxTotal:           MoneyValue{Currency: query.Currency, MinorUnits: b.TaxTotal},
				NetSales:           MoneyValue{Currency: query.Currency, MinorUnits: b.NetSales},
				ReturnTotal:        MoneyValue{Currency: query.Currency, MinorUnits: b.ReturnTotal},
				UnitsSold:          b.UnitsSold,
			},
		})
	}

	freshness := FreshnessMetadata{
		GeneratedAt:       started,
		DataAsOf:          meta.DataAsOf,
		ProjectionVersion: meta.ProjectionVersion,
		SourceStatus:      status,
		Timezone:          defaultDashboardTZ,
		Currency:          query.Currency,
	}
	if meta.Found && meta.DataAsOf != nil {
		lag := int64(started.Sub(*meta.DataAsOf).Seconds())
		if lag < 0 {
			lag = 0
		}
		freshness.FreshnessLagSeconds = &lag
	}

	return SalesOverviewResponse{
		BranchID:  selectedBranch,
		From:      from.Format("2006-01-02"),
		To:        to.Format("2006-01-02"),
		GroupBy:   query.GroupBy,
		Buckets:   buckets,
		Freshness: freshness,
	}, nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func zeroSalesTotals(currency string) SalesTotals {
	z := MoneyValue{Currency: currency, MinorUnits: 0}
	return SalesTotals{
		GrossSales:    z,
		DiscountTotal: z,
		TaxTotal:      z,
		NetSales:      z,
		ReturnTotal:   z,
	}
}

var _ = strings.TrimSpace // keep import stable if adjusted; remove if unused
