package ledger

import (
	"time"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// -----------------------------------------------------------------------------
// Chart of Accounts DTOs
// -----------------------------------------------------------------------------

// CreateAccountRequest is the body of POST /ledger/accounts.
type CreateAccountRequest struct {
	Code        string     `json:"code"        validate:"required,min=1,max=40"`
	Name        string     `json:"name"        validate:"required,min=2,max=200"`
	AccountType string     `json:"account_type" validate:"required,oneof=asset liability equity revenue expense"`
	SubType     *string    `json:"sub_type"    validate:"omitempty,max=100"`
	ParentID    *uuid.UUID `json:"parent_id"  validate:"omitempty"`

	NormalSide string `json:"normal_side" validate:"required,oneof=debit credit"`

	IsPostable *bool `json:"is_postable" validate:"omitempty"`
	IsActive   *bool `json:"is_active"   validate:"omitempty"`

	Description *string `json:"description" validate:"omitempty,max=500"`
}

// UpdateAccountRequest is the body of PATCH /ledger/accounts/{id}.
type UpdateAccountRequest struct {
	ExpectedVersion int64      `json:"expected_version" validate:"required,gte=1"`
	Name            *string    `json:"name"        validate:"omitempty,min=2,max=200"`
	SubType         *string    `json:"sub_type"    validate:"omitempty,max=100"`
	ParentID        *uuid.UUID `json:"parent_id"  validate:"omitempty"`

	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
	IsPostable  *bool   `json:"is_postable" validate:"omitempty"`
	Description *string `json:"description" validate:"omitempty,max=500"`
}

func (r UpdateAccountRequest) IsEmpty() bool {
	return r.Name == nil && r.SubType == nil && r.ParentID == nil &&
		r.IsActive == nil && r.IsPostable == nil && r.Description == nil
}

// DeactivateAccountRequest is the body of POST /ledger/accounts/{id}/deactivate.
type DeactivateAccountRequest struct {
	ExpectedVersion int64  `json:"expected_version" validate:"required,gte=1"`
	Reason          string `json:"reason" validate:"required,min=3,max=500"`
}

// ListAccountsQuery binds the query string of GET /ledger/accounts.
type ListAccountsQuery struct {
	pagination.Offset

	IsActive    *bool  `form:"is_active"`
	AccountType string `form:"account_type"`
	Q           string `form:"q"`
}

// AccountResponse is the read projection for a single account.
type AccountResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	AccountType string     `json:"account_type"`
	SubType     *string    `json:"sub_type,omitempty"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	NormalSide  string     `json:"normal_side"`
	IsSystem    bool       `json:"is_system"`
	IsActive    bool       `json:"is_active"`
	IsPostable  bool       `json:"is_postable"`
	Version     int64      `json:"version"`
	Description *string    `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AccountBalanceResponse is the response of GET /ledger/accounts/{id}/balance.
type AccountBalanceResponse struct {
	AccountID          uuid.UUID `json:"account_id"`
	PeriodYear         int       `json:"period_year"`
	PeriodMonth        int       `json:"period_month"`
	OpeningDebitMinor  int64     `json:"opening_debit_minor"`
	OpeningCreditMinor int64     `json:"opening_credit_minor"`
	DebitMinor         int64     `json:"debit_minor"`
	CreditMinor        int64     `json:"credit_minor"`
	ClosingDebitMinor  int64     `json:"closing_debit_minor"`
	ClosingCreditMinor int64     `json:"closing_credit_minor"`
}

type AccountBalanceQuery struct {
	PeriodYear  int        `form:"period_year" validate:"required,gte=1,lte=9999"`
	PeriodMonth int        `form:"period_month" validate:"required,gte=1,lte=12"`
	BranchID    *uuid.UUID `form:"branch_id"`
}

// -----------------------------------------------------------------------------
// Fiscal Period DTOs
// -----------------------------------------------------------------------------

// CreatePeriodRequest is the body of POST /ledger/periods.
type CreatePeriodRequest struct {
	Name      string    `json:"name"       validate:"required,min=1,max=100"`
	StartDate time.Time `json:"start_date" validate:"required"`
	EndDate   time.Time `json:"end_date"   validate:"required"`
}

// ClosePeriodRequest is the body of POST /ledger/periods/{id}/close.
type ClosePeriodRequest struct {
	ExpectedVersion int64  `json:"expected_version" validate:"required,gte=1"`
	Reason          string `json:"reason"           validate:"required,min=3,max=500"`
}

// ReopenPeriodRequest is the body of POST /ledger/periods/{id}/reopen.
type ReopenPeriodRequest struct {
	ExpectedVersion int64  `json:"expected_version" validate:"required,gte=1"`
	Reason          string `json:"reason"           validate:"required,min=3,max=500"`
}

// PeriodResponse is the read projection for a fiscal period.
type PeriodResponse struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	StartDate      time.Time  `json:"start_date"`
	EndDate        time.Time  `json:"end_date"`
	Status         string     `json:"status"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	ClosedBy       *uuid.UUID `json:"closed_by,omitempty"`
	ClosedReason   *string    `json:"closed_reason,omitempty"`
	ReopenedAt     *time.Time `json:"reopened_at,omitempty"`
	ReopenedBy     *uuid.UUID `json:"reopened_by,omitempty"`
	ReopenedReason *string    `json:"reopened_reason,omitempty"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ListPeriodsQuery binds the query string of GET /ledger/periods.
type ListPeriodsQuery struct {
	pagination.Offset

	Status string `form:"status"`
}

// -----------------------------------------------------------------------------
// Journal DTOs
// -----------------------------------------------------------------------------

// JournalLineRequest is a single line in a journal create/update request.
type JournalLineRequest struct {
	AccountID   uuid.UUID  `json:"account_id"   validate:"required"`
	BranchID    *uuid.UUID `json:"branch_id"    validate:"omitempty"`
	DebitMinor  int64      `json:"debit_minor"  validate:"gte=0"`
	CreditMinor int64      `json:"credit_minor" validate:"gte=0"`
	Description *string    `json:"description"  validate:"omitempty,max=500"`
	LineOrder   int        `json:"line_order"   validate:"required,gte=1"`
}

// CreateJournalRequest is the body of POST /ledger/journals.
type CreateJournalRequest struct {
	JournalDate time.Time `json:"journal_date"  validate:"required"`
	Description string    `json:"description"   validate:"required,min=3,max=1000"`

	SourceModule string     `json:"source_module" validate:"required,oneof=manual sale purchase payment adjustment inventory"`
	SourceID     *uuid.UUID `json:"source_id"     validate:"omitempty"`

	BranchID *uuid.UUID `json:"branch_id" validate:"omitempty"`

	Lines []JournalLineRequest `json:"lines" validate:"required,min=2,max=1000,dive"`
}

// UpdateJournalRequest is the body of PATCH /ledger/journals/{id}.
type UpdateJournalRequest struct {
	ExpectedVersion int64                `json:"expected_version" validate:"required,gte=1"`
	JournalDate     *time.Time           `json:"journal_date"     validate:"omitempty"`
	Description     *string              `json:"description"      validate:"omitempty,min=3,max=1000"`
	Lines           []JournalLineRequest `json:"lines"            validate:"omitempty,min=2,max=1000,dive"`
}

func (r UpdateJournalRequest) IsEmpty() bool {
	return r.JournalDate == nil && r.Description == nil && len(r.Lines) == 0
}

// PostJournalRequest is the body of POST /ledger/journals/{id}/post.
type PostJournalRequest struct {
	ExpectedVersion int64 `json:"expected_version" validate:"required,gte=1"`
}

// ReverseJournalRequest is the body of POST /ledger/journals/{id}/reverse.
type ReverseJournalRequest struct {
	ExpectedVersion int64     `json:"expected_version" validate:"required,gte=1"`
	Reason          string    `json:"reason"           validate:"required,min=3,max=1000"`
	PostingDate     time.Time `json:"posting_date"     validate:"required"`
}

// ListJournalsQuery binds the query string of GET /ledger/journals.
type ListJournalsQuery struct {
	Limit    int        `form:"limit" validate:"omitempty,gte=1,lte=100"`
	CursorID *uuid.UUID `form:"cursor_id"`

	Status       string     `form:"status"`
	SourceModule string     `form:"source_module"`
	SourceID     *uuid.UUID `form:"source_id"`
	AccountID    *uuid.UUID `form:"account_id"`
	BranchID     *uuid.UUID `form:"branch_id"`
	FromDate     *time.Time `form:"from_date"`
	ToDate       *time.Time `form:"to_date"`
}

// JournalLineResponse is the read projection for a single journal line.
type JournalLineResponse struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	BranchID    *uuid.UUID `json:"branch_id,omitempty"`
	DebitMinor  int64      `json:"debit_minor"`
	CreditMinor int64      `json:"credit_minor"`
	Description *string    `json:"description,omitempty"`
	LineOrder   int        `json:"line_order"`
}

// JournalResponse is the read projection for a single journal.
type JournalResponse struct {
	ID                  uuid.UUID             `json:"id"`
	OrganizationID      uuid.UUID             `json:"organization_id"`
	BranchID            *uuid.UUID            `json:"branch_id,omitempty"`
	PeriodID            uuid.UUID             `json:"period_id"`
	CurrencyCode        string                `json:"currency_code"`
	JournalNo           string                `json:"journal_no"`
	JournalDate         time.Time             `json:"journal_date"`
	SourceModule        string                `json:"source_module"`
	SourceID            *uuid.UUID            `json:"source_id,omitempty"`
	Description         string                `json:"description"`
	TotalDebitMinor     int64                 `json:"total_debit_minor"`
	TotalCreditMinor    int64                 `json:"total_credit_minor"`
	Status              string                `json:"status"`
	PostedBy            *uuid.UUID            `json:"posted_by,omitempty"`
	PostedAt            *time.Time            `json:"posted_at,omitempty"`
	ReversedByJournalID *uuid.UUID            `json:"reversed_by_journal_id,omitempty"`
	ReversalOfJournalID *uuid.UUID            `json:"reversal_of_journal_id,omitempty"`
	Version             int64                 `json:"version"`
	Lines               []JournalLineResponse `json:"lines,omitempty"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

// -----------------------------------------------------------------------------
// Report DTOs
// -----------------------------------------------------------------------------

// TrialBalanceQuery binds the query string of GET /ledger/reports/trial-balance.
type TrialBalanceQuery struct {
	AsOfDate time.Time  `form:"as_of" validate:"required"`
	BranchID *uuid.UUID `form:"branch_id"`
}

// TrialBalanceLine is one row of the trial balance.
type TrialBalanceLine struct {
	AccountID    uuid.UUID `json:"account_id"`
	AccountCode  string    `json:"account_code"`
	AccountName  string    `json:"account_name"`
	AccountType  string    `json:"account_type"`
	DebitMinor   int64     `json:"debit_minor"`
	CreditMinor  int64     `json:"credit_minor"`
	BalanceMinor int64     `json:"balance_minor"`
}

// TrialBalanceResponse is the response of GET /ledger/reports/trial-balance.
type TrialBalanceResponse struct {
	AsOfDate    time.Time          `json:"as_of_date"`
	BranchID    *uuid.UUID         `json:"branch_id,omitempty"`
	TotalDebit  int64              `json:"total_debit_minor"`
	TotalCredit int64              `json:"total_credit_minor"`
	Lines       []TrialBalanceLine `json:"lines"`
}

// GeneralLedgerQuery binds the query string of GET /ledger/reports/general-ledger.
type GeneralLedgerQuery struct {
	pagination.Cursor

	AccountID uuid.UUID  `form:"account_id" validate:"required"`
	BranchID  *uuid.UUID `form:"branch_id"`
	FromDate  *time.Time `form:"from_date"`
	ToDate    *time.Time `form:"to_date"`
}

// GeneralLedgerLine is one row of the general ledger.
type GeneralLedgerLine struct {
	JournalID    uuid.UUID  `json:"journal_id"`
	JournalNo    string     `json:"journal_no"`
	JournalDate  time.Time  `json:"journal_date"`
	Description  string     `json:"description"`
	DebitMinor   int64      `json:"debit_minor"`
	CreditMinor  int64      `json:"credit_minor"`
	BalanceMinor int64      `json:"balance_minor"`
	BranchID     *uuid.UUID `json:"branch_id,omitempty"`
}

// -----------------------------------------------------------------------------
// Export DTOs
// -----------------------------------------------------------------------------

// CreateExportRequest is the body of POST /ledger/exports.
type CreateExportRequest struct {
	ReportType string     `json:"report_type" validate:"required,oneof=trial_balance general_ledger account_activity"`
	FromDate   *time.Time `json:"from_date"   validate:"omitempty"`
	ToDate     *time.Time `json:"to_date"     validate:"omitempty"`
	BranchID   *uuid.UUID `json:"branch_id"   validate:"omitempty"`
	Format     string     `json:"format"      validate:"required,oneof=csv xlsx pdf"`
}

// ExportJobResponse is the read projection for an export job.
type ExportJobResponse struct {
	ID          uuid.UUID  `json:"id"`
	ReportType  string     `json:"report_type"`
	Status      string     `json:"status"`
	Format      string     `json:"format"`
	RowCount    *int64     `json:"row_count,omitempty"`
	DownloadURL *string    `json:"download_url,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
