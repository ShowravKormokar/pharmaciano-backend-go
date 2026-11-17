package analytics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
)

var (
	ErrInvalidReadScope = errors.New("analytics: invalid read scope")
	ErrInvalidQuery     = errors.New("analytics: invalid query")
)

// SalesReadScope is derived from authenticated identity and effective
// authorization. Callers must not populate it from request query/body fields.
// When AllBranches is true, the read spans all branches in OrganizationID that
// the caller is authorized to view. Otherwise BranchIDs is the effective,
// authorized subset (including any client-requested branch filter intersected
// server-side); an empty slice means no access.
type SalesReadScope struct {
	OrganizationID uuid.UUID
	AllBranches    bool
	BranchIDs      []uuid.UUID
}

func (s SalesReadScope) hasAccess() bool {
	return s.AllBranches || len(s.BranchIDs) > 0
}

// Repository provides tenant-scoped, parameterized access to Analytics-owned
// projection tables. Reads use the replica when no transaction is active;
// writes (consumer/checkpoint operations) must run in the caller transaction
// via db.FromCtx. All queries enforce organization scope as defense-in-depth.
type Repository struct {
	db *db.DB
}

func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

func validateSalesReadScope(scope SalesReadScope) error {
	if scope.OrganizationID == uuid.Nil {
		return fmt.Errorf("%w: organization_id is required", ErrInvalidReadScope)
	}
	for _, id := range scope.BranchIDs {
		if id == uuid.Nil {
			return fmt.Errorf("%w: branch scope contains invalid id", ErrInvalidReadScope)
		}
	}
	return nil
}

func appendBranchScope(where []string, args []any, scope SalesReadScope) ([]string, []any) {
	if scope.AllBranches {
		return where, args
	}
	args = append(args, scope.BranchIDs)
	where = append(where, fmt.Sprintf("branch_id = ANY($%d::uuid[])", len(args)))
	return where, args
}

// SumSalesDaily returns additive totals for a single business date within the
// authorized scope. Found=false means the projection has no rows for the
// requested scope/date and must be treated as unavailable or empty by policy,
// never fabricated as zero current data.
func (r *Repository) SumSalesDaily(
	ctx context.Context,
	scope SalesReadScope,
	businessDate time.Time,
	currency string,
) (SalesDailyAggregate, error) {
	if r == nil || r.db == nil {
		return SalesDailyAggregate{}, fmt.Errorf("%w: repository database is not configured", ErrInvalidQuery)
	}
	if err := validateSalesReadScope(scope); err != nil {
		return SalesDailyAggregate{}, err
	}
	if !scope.hasAccess() {
		return SalesDailyAggregate{}, nil
	}
	if businessDate.IsZero() || strings.TrimSpace(currency) == "" {
		return SalesDailyAggregate{}, fmt.Errorf("%w: business_date and currency are required", ErrInvalidQuery)
	}

	args := []any{scope.OrganizationID, businessDate.UTC(), currency}
	where := []string{"organization_id = $1", "business_date = $2::date", "currency = $3"}
	where, args = appendBranchScope(where, args, scope)

	q := `
SELECT
	COUNT(*)::bigint AS row_count,
	COALESCE(SUM(completed_sale_count), 0)::bigint,
	COALESCE(SUM(gross_sales), 0)::bigint,
	COALESCE(SUM(discount_total), 0)::bigint,
	COALESCE(SUM(tax_total), 0)::bigint,
	COALESCE(SUM(net_sales), 0)::bigint,
	COALESCE(SUM(return_total), 0)::bigint,
	COALESCE(SUM(units_sold), 0)::bigint,
	COALESCE(MAX(projection_version), 0)::int,
	MAX(data_as_of),
	MAX(source_watermark),
	BOOL_OR(source_status = 'degraded') AS any_degraded,
	BOOL_OR(source_status = 'rebuilding') AS any_rebuilding,
	BOOL_OR(source_status = 'delayed') AS any_delayed
FROM analytics_sales_daily
WHERE ` + strings.Join(where, " AND ")

	var (
		rowCount  int64
		status    aggregateStatusFlags
		out       SalesDailyAggregate
		watermark *string
		dataAsOf  *time.Time
	)
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, args...).Scan(
		&rowCount,
		&out.CompletedSaleCount,
		&out.GrossSales,
		&out.DiscountTotal,
		&out.TaxTotal,
		&out.NetSales,
		&out.ReturnTotal,
		&out.UnitsSold,
		&out.ProjectionVersion,
		&dataAsOf,
		&watermark,
		&status.AnyDegraded,
		&status.AnyRebuilding,
		&status.AnyDelayed,
	)
	if err != nil {
		return SalesDailyAggregate{}, fmt.Errorf("analytics sum sales daily: %w", err)
	}
	out.Found = rowCount > 0
	out.DataAsOf = dataAsOf
	out.SourceWatermark = watermark
	out.SourceStatus = status.resolve()
	return out, nil
}

// AggregateSalesOverview returns grouped additive sales buckets for [from, to)
// within authorized scope. Buckets with no rows are omitted; the caller is
// responsible for freshness/unavailable signaling when projection data is missing
// or stale.
func (r *Repository) AggregateSalesOverview(
	ctx context.Context,
	scope SalesReadScope,
	from, to time.Time,
	groupBy GroupBy,
	currency string,
) ([]SalesOverviewAggregateBucket, SalesOverviewAggregateMeta, error) {
	if r == nil || r.db == nil {
		return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("%w: repository database is not configured", ErrInvalidQuery)
	}
	if err := validateSalesReadScope(scope); err != nil {
		return nil, SalesOverviewAggregateMeta{}, err
	}
	if !scope.hasAccess() {
		return []SalesOverviewAggregateBucket{}, SalesOverviewAggregateMeta{}, nil
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("%w: valid from/to range is required", ErrInvalidQuery)
	}
	if !groupBy.Valid() || strings.TrimSpace(currency) == "" {
		return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("%w: valid group_by and currency are required", ErrInvalidQuery)
	}

	bucketExpr, periodEndExpr, err := bucketExpressions(groupBy)
	if err != nil {
		return nil, SalesOverviewAggregateMeta{}, err
	}

	args := []any{scope.OrganizationID, from.UTC(), to.UTC(), currency}
	where := []string{
		"organization_id = $1",
		"business_date >= $2::date",
		"business_date < $3::date",
		"currency = $4",
	}
	where, args = appendBranchScope(where, args, scope)

	q := fmt.Sprintf(`
SELECT
	%s AS period_start,
	%s AS period_end,
	COALESCE(SUM(completed_sale_count), 0)::bigint,
	COALESCE(SUM(gross_sales), 0)::bigint,
	COALESCE(SUM(discount_total), 0)::bigint,
	COALESCE(SUM(tax_total), 0)::bigint,
	COALESCE(SUM(net_sales), 0)::bigint,
	COALESCE(SUM(return_total), 0)::bigint,
	COALESCE(SUM(units_sold), 0)::bigint,
	COALESCE(MAX(projection_version), 0)::int,
	MAX(data_as_of),
	MAX(source_watermark),
	BOOL_OR(source_status = 'degraded') AS any_degraded,
	BOOL_OR(source_status = 'rebuilding') AS any_rebuilding,
	BOOL_OR(source_status = 'delayed') AS any_delayed
FROM analytics_sales_daily
WHERE %s
GROUP BY period_start, period_end
ORDER BY period_start ASC`, bucketExpr, periodEndExpr, strings.Join(where, " AND "))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("analytics aggregate sales overview: %w", err)
	}
	defer rows.Close()

	buckets := make([]SalesOverviewAggregateBucket, 0)
	meta := SalesOverviewAggregateMeta{}
	for rows.Next() {
		var (
			b     SalesOverviewAggregateBucket
			flags aggregateStatusFlags
			asOf  *time.Time
			wm    *string
			pv    int32
		)
		if err := rows.Scan(
			&b.PeriodStart,
			&b.PeriodEnd,
			&b.CompletedSaleCount,
			&b.GrossSales,
			&b.DiscountTotal,
			&b.TaxTotal,
			&b.NetSales,
			&b.ReturnTotal,
			&b.UnitsSold,
			&pv,
			&asOf,
			&wm,
			&flags.AnyDegraded,
			&flags.AnyRebuilding,
			&flags.AnyDelayed,
		); err != nil {
			return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("scan sales overview bucket: %w", err)
		}
		buckets = append(buckets, b)
		meta.merge(pv, asOf, wm, flags)
	}
	if err := rows.Err(); err != nil {
		return nil, SalesOverviewAggregateMeta{}, fmt.Errorf("iterate sales overview buckets: %w", err)
	}
	meta.Found = len(buckets) > 0
	meta.SourceStatus = meta.statusFlags.resolve()
	return buckets, meta, nil
}

func bucketExpressions(groupBy GroupBy) (string, string, error) {
	switch groupBy {
	case GroupByDay:
		return "business_date", "(business_date + INTERVAL '1 day')::date", nil
	case GroupByWeek:
		// ISO week bucketing via date_trunc('week', ...). Week-start policy is an
		// unresolved org setting; callers must not treat this as finalized policy.
		return "date_trunc('week', business_date::timestamp)::date",
			"(date_trunc('week', business_date::timestamp) + INTERVAL '1 week')::date", nil
	case GroupByMonth:
		return "date_trunc('month', business_date::timestamp)::date",
			"(date_trunc('month', business_date::timestamp) + INTERVAL '1 month')::date", nil
	default:
		return "", "", fmt.Errorf("%w: unsupported group_by", ErrInvalidQuery)
	}
}

type aggregateStatusFlags struct {
	AnyDegraded   bool
	AnyRebuilding bool
	AnyDelayed    bool
}

func (f aggregateStatusFlags) resolve() SourceStatus {
	switch {
	case f.AnyDegraded:
		return SourceStatusDegraded
	case f.AnyRebuilding:
		return SourceStatusRebuilding
	case f.AnyDelayed:
		return SourceStatusDelayed
	default:
		return SourceStatusCurrent
	}
}

// SalesDailyAggregate is a repository read model for a single-date sum.
type SalesDailyAggregate struct {
	Found              bool
	CompletedSaleCount int64
	GrossSales         MoneyMinor
	DiscountTotal      MoneyMinor
	TaxTotal           MoneyMinor
	NetSales           MoneyMinor
	ReturnTotal        MoneyMinor
	UnitsSold          Quantity
	ProjectionVersion  int32
	DataAsOf           *time.Time
	SourceWatermark    *string
	SourceStatus       SourceStatus
}

// SalesOverviewAggregateBucket is a repository read model for one grouped bucket.
type SalesOverviewAggregateBucket struct {
	PeriodStart        time.Time
	PeriodEnd          time.Time
	CompletedSaleCount int64
	GrossSales         MoneyMinor
	DiscountTotal      MoneyMinor
	TaxTotal           MoneyMinor
	NetSales           MoneyMinor
	ReturnTotal        MoneyMinor
	UnitsSold          Quantity
}

// SalesOverviewAggregateMeta carries projection metadata aggregated across buckets.
type SalesOverviewAggregateMeta struct {
	Found             bool
	ProjectionVersion int32
	DataAsOf          *time.Time
	SourceWatermark   *string
	SourceStatus      SourceStatus
	statusFlags       aggregateStatusFlags
}

func (m *SalesOverviewAggregateMeta) merge(pv int32, asOf *time.Time, wm *string, flags aggregateStatusFlags) {
	if pv > m.ProjectionVersion {
		m.ProjectionVersion = pv
	}
	if asOf != nil && (m.DataAsOf == nil || asOf.After(*m.DataAsOf)) {
		t := *asOf
		m.DataAsOf = &t
	}
	if wm != nil && (m.SourceWatermark == nil || *wm > *m.SourceWatermark) {
		s := *wm
		m.SourceWatermark = &s
	}
	m.statusFlags.AnyDegraded = m.statusFlags.AnyDegraded || flags.AnyDegraded
	m.statusFlags.AnyRebuilding = m.statusFlags.AnyRebuilding || flags.AnyRebuilding
	m.statusFlags.AnyDelayed = m.statusFlags.AnyDelayed || flags.AnyDelayed
}
