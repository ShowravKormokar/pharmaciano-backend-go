package analytics

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// Analytics-owned persistence models for the sales-first scope.
// Source business facts remain owned by their source modules; these models store
// derived projection state, contribution identity, and operational metadata only.
// Routes and consumers are gated until source event contracts and prerequisites
// are implemented and verified.

// MoneyMinor stores BDT monetary amounts in integer minor units (paisa).
// Never use float32/float64 for money.
type MoneyMinor = int64

// Quantity stores quantities in the canonical base-unit integer representation.
type Quantity = int64

type SourceStatus string

const (
	SourceStatusCurrent    SourceStatus = "current"
	SourceStatusDelayed    SourceStatus = "delayed"
	SourceStatusRebuilding SourceStatus = "rebuilding"
	SourceStatusDegraded   SourceStatus = "degraded"
)

func (s SourceStatus) Valid() bool {
	switch s {
	case SourceStatusCurrent, SourceStatusDelayed, SourceStatusRebuilding, SourceStatusDegraded:
		return true
	default:
		return false
	}
}

// AnalyticsSalesDaily is the additive daily sales projection grain:
// organization + branch + business date + currency.
// Derived ratios (AOV, margin ratio) are computed at query time.
type AnalyticsSalesDaily struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id"       json:"branch_id"`
	BusinessDate   time.Time `db:"business_date"   json:"business_date"`
	Currency       string    `db:"currency"        json:"currency"`

	CompletedSaleCount int64      `db:"completed_sale_count" json:"completed_sale_count"`
	GrossSales         MoneyMinor `db:"gross_sales"          json:"gross_sales"`
	DiscountTotal      MoneyMinor `db:"discount_total"       json:"discount_total"`
	TaxTotal           MoneyMinor `db:"tax_total"            json:"tax_total"`
	NetSales           MoneyMinor `db:"net_sales"            json:"net_sales"`
	ReturnTotal        MoneyMinor `db:"return_total"         json:"return_total"`
	UnitsSold          Quantity   `db:"units_sold"           json:"units_sold"`

	ProjectionVersion int32        `db:"projection_version" json:"projection_version"`
	SourceStatus      SourceStatus `db:"source_status"      json:"source_status"`
	DataAsOf          *time.Time   `db:"data_as_of"         json:"data_as_of,omitempty"`
	SourceWatermark   *string      `db:"source_watermark"   json:"source_watermark,omitempty"`
}

// AnalyticsSalesDailyContribution records an immutable, deduplicated source
// contribution applied to the daily sales projection. It supports idempotent
// replay/rebuild and prevents duplicate effects when the same event is redelivered.
type AnalyticsSalesDailyContribution struct {
	ID             uuid.UUID  `db:"id"               json:"id"`
	CreatedAt      time.Time  `db:"created_at"       json:"created_at"`
	OrganizationID uuid.UUID  `db:"organization_id"  json:"organization_id"`
	BranchID       uuid.UUID  `db:"branch_id"        json:"branch_id"`
	BusinessDate   time.Time  `db:"business_date"    json:"business_date"`
	Currency       string     `db:"currency"         json:"currency"`
	EventID        uuid.UUID  `db:"event_id"         json:"event_id"`
	SourceModule   string     `db:"source_module"    json:"source_module"`
	AggregateType  string     `db:"aggregate_type"   json:"aggregate_type"`
	AggregateID    uuid.UUID  `db:"aggregate_id"     json:"aggregate_id"`
	AggregateVersion int64    `db:"aggregate_version" json:"aggregate_version"`
	SourceOperationID *uuid.UUID `db:"source_operation_id" json:"source_operation_id,omitempty"`
	OccurredAt     time.Time  `db:"occurred_at"      json:"occurred_at"`

	CompletedSaleCountDelta int64      `db:"completed_sale_count_delta" json:"completed_sale_count_delta"`
	GrossSalesDelta         MoneyMinor `db:"gross_sales_delta"          json:"gross_sales_delta"`
	DiscountTotalDelta      MoneyMinor `db:"discount_total_delta"       json:"discount_total_delta"`
	TaxTotalDelta           MoneyMinor `db:"tax_total_delta"            json:"tax_total_delta"`
	NetSalesDelta           MoneyMinor `db:"net_sales_delta"            json:"net_sales_delta"`
	ReturnTotalDelta        MoneyMinor `db:"return_total_delta"         json:"return_total_delta"`
	UnitsSoldDelta          Quantity   `db:"units_sold_delta"           json:"units_sold_delta"`
}

// AnalyticsProjectionCheckpoint tracks consumer progress for a projection when
// source ordering semantics support checkpointing. It is an optimization only;
// it must never be used to skip an unprocessed event.
type AnalyticsProjectionCheckpoint struct {
	ConsumerName      string    `db:"consumer_name"       json:"consumer_name"`
	OrganizationID    uuid.UUID `db:"organization_id"     json:"organization_id"`
	ProjectionName    string    `db:"projection_name"     json:"projection_name"`
	SchemaVersion     int32     `db:"schema_version"      json:"schema_version"`
	SourceCursor      *string   `db:"source_cursor"       json:"source_cursor,omitempty"`
	SourceSequence    *int64    `db:"source_sequence"     json:"source_sequence,omitempty"`
	Watermark         *time.Time `db:"watermark"          json:"watermark,omitempty"`
	UpdatedAt         time.Time `db:"updated_at"          json:"updated_at"`
}

// AnalyticsProjectionFailure records sanitized projection handler failures for
// observability and operator resolution. Raw sensitive payloads must not be stored.
type AnalyticsProjectionFailure struct {
	ID             uuid.UUID  `db:"id"               json:"id"`
	CreatedAt      time.Time  `db:"created_at"       json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"       json:"updated_at"`
	OrganizationID *uuid.UUID `db:"organization_id"  json:"organization_id,omitempty"`
	EventID        uuid.UUID  `db:"event_id"         json:"event_id"`
	ConsumerName   string     `db:"consumer_name"    json:"consumer_name"`
	ProjectionName *string    `db:"projection_name"  json:"projection_name,omitempty"`
	ErrorClass     string     `db:"error_class"      json:"error_class"`
	AttemptCount   int32      `db:"attempt_count"    json:"attempt_count"`
	FirstFailedAt  time.Time  `db:"first_failed_at"  json:"first_failed_at"`
	LastFailedAt   time.Time  `db:"last_failed_at"   json:"last_failed_at"`
	NextRetryAt    *time.Time `db:"next_retry_at"    json:"next_retry_at,omitempty"`
	Diagnostic     *string    `db:"diagnostic"       json:"diagnostic,omitempty"`
}

// AnalyticsProjectionRun records a projection rebuild/backfill/switch operation.
type AnalyticsProjectionRun struct {
	common.BaseModel

	OrganizationID    *uuid.UUID     `db:"organization_id"     json:"organization_id,omitempty"`
	ProjectionName    string         `db:"projection_name"     json:"projection_name"`
	ProjectionVersion int32          `db:"projection_version"  json:"projection_version"`
	Status            string         `db:"status"              json:"status"`
	SourceWatermark   *string        `db:"source_watermark"    json:"source_watermark,omitempty"`
	StartedAt         time.Time      `db:"started_at"          json:"started_at"`
	CompletedAt       *time.Time     `db:"completed_at"        json:"completed_at,omitempty"`
	Reason            *string        `db:"reason"              json:"reason,omitempty"`
	Details           common.JSONMap `db:"details"             json:"details,omitempty"`
}

// AnalyticsReconciliationRun records comparison of projection output to an
// authoritative source snapshot/contract. Analytics must not repair source facts.
type AnalyticsReconciliationRun struct {
	common.BaseModel

	OrganizationID    *uuid.UUID     `db:"organization_id"     json:"organization_id,omitempty"`
	ProjectionName    string         `db:"projection_name"     json:"projection_name"`
	SourceName        string         `db:"source_name"         json:"source_name"`
	ProjectionVersion int32          `db:"projection_version"  json:"projection_version"`
	Status            string         `db:"status"              json:"status"`
	SourceAsOf        *time.Time     `db:"source_as_of"        json:"source_as_of,omitempty"`
	SourceWatermark   *string        `db:"source_watermark"    json:"source_watermark,omitempty"`
	StartedAt         time.Time      `db:"started_at"          json:"started_at"`
	CompletedAt       *time.Time     `db:"completed_at"        json:"completed_at,omitempty"`
	MismatchCount     int64          `db:"mismatch_count"      json:"mismatch_count"`
	Severity          *string        `db:"severity"            json:"severity,omitempty"`
	Details           common.JSONMap `db:"details"             json:"details,omitempty"`
}

// AnalyticsDimensionSnapshot stores historical, tenant-safe dimension labels
// needed to explain projection rows after source entities are renamed or deactivated.
type AnalyticsDimensionSnapshot struct {
	common.BaseModel

	OrganizationID uuid.UUID      `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID     `db:"branch_id"       json:"branch_id,omitempty"`
	DimensionType  string         `db:"dimension_type"  json:"dimension_type"`
	DimensionID    uuid.UUID      `db:"dimension_id"    json:"dimension_id"`
	EffectiveFrom  time.Time      `db:"effective_from"  json:"effective_from"`
	EffectiveTo    *time.Time     `db:"effective_to"    json:"effective_to,omitempty"`
	Label          string         `db:"label"           json:"label"`
	Attributes     common.JSONMap `db:"attributes"      json:"attributes,omitempty"`
	SourceVersion  *string        `db:"source_version"  json:"source_version,omitempty"`
}
