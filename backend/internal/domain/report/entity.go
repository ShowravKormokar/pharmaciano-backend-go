package report

import (
	"context"
	"time"
)

// Report represents the core report domain entity
type Report struct {
	ID          string
	Title       string
	Type        string
	Description string
	Status      string
	Data        interface{}
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IReportRepository defines the interface for report data access
type IReportRepository interface {
	Create(ctx context.Context, report *Report) error
	FindByID(ctx context.Context, id string) (*Report, error)
	Update(ctx context.Context, report *Report) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Report, error)
	ListByType(ctx context.Context, reportType string, limit, offset int) ([]*Report, error)
}

// IReportService defines the interface for report business logic
type IReportService interface {
	GenerateSalesReport(ctx context.Context, startDate, endDate time.Time) (*Report, error)
	GenerateInventoryReport(ctx context.Context) (*Report, error)
	GenerateFinancialReport(ctx context.Context, month int, year int) (*Report, error)
	GetReport(ctx context.Context, id string) (*Report, error)
	ListReports(ctx context.Context, limit, offset int) ([]*Report, error)
	ScheduleReportGeneration(ctx context.Context, reportType string) error
}
