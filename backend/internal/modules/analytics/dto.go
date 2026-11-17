package analytics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultGroupBy = GroupByDay

	MaxDashboardRangeDays  = 31
	MaxSalesOverviewRangeDays = 366
)

type GroupBy string

const (
	GroupByDay   GroupBy = "day"
	GroupByWeek  GroupBy = "week"
	GroupByMonth GroupBy = "month"
)

func (g GroupBy) Valid() bool {
	switch g {
	case GroupByDay, GroupByWeek, GroupByMonth:
		return true
	default:
		return false
	}
}

// DashboardQuery requests the sales-only "today" dashboard snapshot.
// Tenant and effective branch scope are derived from authenticated context;
// BranchID is an optional selector intersected with that scope server-side.
type DashboardQuery struct {
	BranchID *uuid.UUID
	Date     *time.Time // business-date override; nil means server-resolved "today"
}

func (q *DashboardQuery) Normalize() {
	// no-op: reserved for future normalization of validated inputs
}

func (q *DashboardQuery) Validate() error {
	if q == nil {
		return errors.New("dashboard query is required")
	}
	if q.BranchID != nil && *q.BranchID == uuid.Nil {
		return errors.New("branch_id must be a valid non-nil UUID")
	}
	return nil
}

// SalesOverviewQuery requests aggregated sales over a bounded date range.
// From is inclusive, To is exclusive (from < to). Currency is BDT-only for the
// initial release and must not be silently converted.
type SalesOverviewQuery struct {
	BranchID *uuid.UUID
	From     time.Time
	To       time.Time
	GroupBy  GroupBy
	Currency string
}

func (q *SalesOverviewQuery) Normalize() {
	if q == nil {
		return
	}
	if strings.TrimSpace(string(q.GroupBy)) == "" {
		q.GroupBy = DefaultGroupBy
	}
	q.Currency = strings.ToUpper(strings.TrimSpace(q.Currency))
	if q.Currency == "" {
		q.Currency = "BDT"
	}
}

func (q *SalesOverviewQuery) Validate() error {
	if q == nil {
		return errors.New("sales overview query is required")
	}
	if q.BranchID != nil && *q.BranchID == uuid.Nil {
		return errors.New("branch_id must be a valid non-nil UUID")
	}
	if q.From.IsZero() || q.To.IsZero() {
		return errors.New("from and to are required")
	}
	if !q.From.Before(q.To) {
		return errors.New("from must be earlier than to")
	}
	if !q.GroupBy.Valid() {
		return fmt.Errorf("group_by must be one of: %s, %s, %s", GroupByDay, GroupByWeek, GroupByMonth)
	}
	if q.Currency != "BDT" {
		return errors.New("currency must be BDT for the initial release")
	}
	maxDays := MaxSalesOverviewRangeDays
	if int(q.To.Sub(q.From).Hours()/24) > maxDays {
		return fmt.Errorf("date range exceeds maximum of %d days", maxDays)
	}
	return nil
}

// FreshnessMetadata discloses projection freshness for a response.
// A missing projection must be reported as unavailable/degraded, never as zero.
type FreshnessMetadata struct {
	GeneratedAt        time.Time    `json:"generated_at"`
	DataAsOf           *time.Time   `json:"data_as_of,omitempty"`
	FreshnessLagSeconds *int64      `json:"freshness_lag_seconds,omitempty"`
	ProjectionVersion  int32        `json:"projection_version"`
	SourceStatus       SourceStatus `json:"source_status"`
	Timezone           string       `json:"timezone"`
	Currency           string       `json:"currency,omitempty"`
}

// MoneyValue represents an exact BDT amount in integer minor units.
type MoneyValue struct {
	Currency   string     `json:"currency"`
	MinorUnits MoneyMinor `json:"minor_units"`
}

// SalesTotals contains additive sales measures for a bucket. Derived ratios
// (e.g. AOV, margin ratio) must be computed explicitly from these values.
type SalesTotals struct {
	CompletedSaleCount int64      `json:"completed_sale_count"`
	GrossSales         MoneyValue `json:"gross_sales"`
	DiscountTotal      MoneyValue `json:"discount_total"`
	TaxTotal           MoneyValue `json:"tax_total"`
	NetSales           MoneyValue `json:"net_sales"`
	ReturnTotal        MoneyValue `json:"return_total"`
	UnitsSold          Quantity   `json:"units_sold"`
}

// DashboardResponse is the sales-only dashboard snapshot for an authorized scope.
type DashboardResponse struct {
	BranchID     *uuid.UUID        `json:"branch_id,omitempty"`
	BusinessDate string            `json:"business_date"`
	Totals       SalesTotals       `json:"totals"`
	Freshness    FreshnessMetadata `json:"freshness"`
}

// SalesOverviewBucket is one grouped sales bucket within the requested range.
type SalesOverviewBucket struct {
	PeriodStart string      `json:"period_start"`
	PeriodEnd   string      `json:"period_end"`
	Totals      SalesTotals `json:"totals"`
}

// SalesOverviewResponse is the aggregated sales overview for an authorized scope.
type SalesOverviewResponse struct {
	BranchID  *uuid.UUID            `json:"branch_id,omitempty"`
	From      string                `json:"from"`
	To        string                `json:"to"`
	GroupBy   GroupBy               `json:"group_by"`
	Buckets   []SalesOverviewBucket `json:"buckets"`
	Freshness FreshnessMetadata     `json:"freshness"`
}
