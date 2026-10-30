package ledger

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeRevenue   AccountType = "revenue"
	AccountTypeExpense   AccountType = "expense"
)

type NormalSide string

const (
	NormalSideDebit  NormalSide = "debit"
	NormalSideCredit NormalSide = "credit"
)

type JournalStatus string

const (
	JournalStatusDraft    JournalStatus = "draft"
	JournalStatusPosted   JournalStatus = "posted"
	JournalStatusReversed JournalStatus = "reversed"
)

type JournalSourceModule string

const (
	JournalSourceManual     JournalSourceModule = "manual"
	JournalSourceSale       JournalSourceModule = "sale"
	JournalSourcePurchase   JournalSourceModule = "purchase"
	JournalSourcePayment    JournalSourceModule = "payment"
	JournalSourceAdjustment JournalSourceModule = "adjustment"
	JournalSourceInventory  JournalSourceModule = "inventory"
)

type BalanceScope string

const (
	BalanceScopeOrganization BalanceScope = "organization"
	BalanceScopeBranch       BalanceScope = "branch"
)

// -----------------------------------------------------------------------------
// Chart of Accounts
// -----------------------------------------------------------------------------

// ChartOfAccount represents an account in an organization's chart of accounts.
//
// Account codes are unique within an organization.
// System accounts are protected from unauthorized modification or deletion.
// ParentID supports hierarchical account structures.
type ChartOfAccount struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`

	Code        string      `db:"code"         json:"code"`
	Name        string      `db:"name"         json:"name"`
	AccountType AccountType `db:"account_type" json:"account_type"`
	SubType     *string     `db:"sub_type"     json:"sub_type,omitempty"`

	ParentID *uuid.UUID `db:"parent_id" json:"parent_id,omitempty"`

	NormalSide NormalSide `db:"normal_side" json:"normal_side"`

	IsSystem   bool  `db:"is_system" json:"is_system"`
	IsActive   bool  `db:"is_active" json:"is_active"`
	IsPostable bool  `db:"is_postable" json:"is_postable"`
	Version    int64 `db:"version" json:"version"`

	Description        *string `db:"description" json:"description,omitempty"`
	DeactivationReason *string `db:"deactivation_reason" json:"-"`
}

// -----------------------------------------------------------------------------
// Journal
// -----------------------------------------------------------------------------

// Journal represents a double-entry accounting journal.
//
// Draft journals may be edited according to permission and validation rules.
// Posted journals are immutable. Corrections must be made through reversal
// journals rather than editing or deleting posted financial records.
//
// All monetary amounts are stored as integer minor units.
// For BDT: 100 minor units = 1 BDT.
type Journal struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID `db:"branch_id"       json:"branch_id,omitempty"`
	PeriodID       uuid.UUID  `db:"period_id"       json:"period_id"`
	CurrencyCode   string     `db:"currency_code"   json:"currency_code"`

	JournalNo   string    `db:"journal_no"   json:"journal_no"`
	JournalDate time.Time `db:"journal_date" json:"journal_date"`

	SourceModule JournalSourceModule `db:"source_module" json:"source_module"`
	SourceID     *uuid.UUID          `db:"source_id"     json:"source_id,omitempty"`

	// IdempotencyKey prevents duplicate journal creation when a client or
	// upstream module retries the same operation.
	// Enforce uniqueness within the organization at the database level.
	IdempotencyKey *string `db:"idempotency_key" json:"-"`

	Description string `db:"description" json:"description"`

	// TotalDebitMinor and TotalCreditMinor are stored in minor currency units.
	// They must be equal before a journal can be posted.
	TotalDebitMinor  int64 `db:"total_debit_minor"  json:"total_debit_minor"`
	TotalCreditMinor int64 `db:"total_credit_minor" json:"total_credit_minor"`

	Status JournalStatus `db:"status" json:"status"`

	// Posting metadata.
	PostedBy *uuid.UUID `db:"posted_by" json:"posted_by,omitempty"`
	PostedAt *time.Time `db:"posted_at" json:"posted_at,omitempty"`

	// Reversal relationship:
	// ReversedByJournalID is set on the original journal after reversal.
	// ReversalOfJournalID is set on the newly created reversing journal.
	ReversedByJournalID *uuid.UUID `db:"reversed_by_journal_id" json:"reversed_by_journal_id,omitempty"`
	ReversalOfJournalID *uuid.UUID `db:"reversal_of_journal_id" json:"reversal_of_journal_id,omitempty"`
	Version             int64      `db:"version"                 json:"version"`
}

// -----------------------------------------------------------------------------
// Journal Line
// -----------------------------------------------------------------------------

// JournalLine represents one debit or credit entry in a journal.
//
// Each line must have exactly one positive amount:
//   - DebitMinor > 0 and CreditMinor == 0, or
//   - CreditMinor > 0 and DebitMinor == 0.
//
// These constraints must also be enforced by PostgreSQL CHECK constraints.
type JournalLine struct {
	common.BaseModel

	JournalID uuid.UUID `db:"journal_id" json:"journal_id"`
	AccountID uuid.UUID `db:"account_id" json:"account_id"`

	// Optional branch-level cost center.
	BranchID *uuid.UUID `db:"branch_id" json:"branch_id,omitempty"`

	DebitMinor  int64 `db:"debit_minor"  json:"debit_minor"`
	CreditMinor int64 `db:"credit_minor" json:"credit_minor"`

	Description *string `db:"description" json:"description,omitempty"`

	LineOrder int `db:"line_order" json:"line_order"`
}

// LedgerPeriod represents a fiscal period.
type LedgerPeriod struct {
	common.BaseModel
	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	Name           string     `db:"name"             json:"name"`
	StartDate      time.Time  `db:"start_date"       json:"start_date"`
	EndDate        time.Time  `db:"end_date"         json:"end_date"`
	Status         string     `db:"status"           json:"status"`
	ClosedAt       *time.Time `db:"closed_at"        json:"closed_at,omitempty"`
	ClosedBy       *uuid.UUID `db:"closed_by"        json:"closed_by,omitempty"`
	ClosedReason   *string    `db:"closed_reason"    json:"closed_reason,omitempty"`
	ReopenedAt     *time.Time `db:"reopened_at"      json:"reopened_at,omitempty"`
	ReopenedBy     *uuid.UUID `db:"reopened_by"       json:"reopened_by,omitempty"`
	ReopenedReason *string    `db:"reopened_reason"  json:"reopened_reason,omitempty"`
	Version        int64      `db:"version"          json:"version"`
}

// LedgerSourcePosting is the durable deduplication record.
type LedgerSourcePosting struct {
	common.BaseModel
	OrganizationID    uuid.UUID `db:"organization_id"      json:"organization_id"`
	SourceType        string    `db:"source_type"          json:"source_type"`
	SourceOperationID uuid.UUID `db:"source_operation_id"  json:"source_operation_id"`
	PostingKind       string    `db:"posting_kind"         json:"posting_kind"`
	JournalID         uuid.UUID `db:"journal_id"           json:"journal_id"`
	PayloadHash       *string   `db:"payload_hash"         json:"payload_hash,omitempty"`
}

// LedgerSubledgerBalance is the transactional exposure projection.
type LedgerSubledgerBalance struct {
	common.BaseModel
	OrganizationID     uuid.UUID `db:"organization_id"      json:"organization_id"`
	SubledgerAccountID uuid.UUID `db:"subledger_account_id" json:"subledger_account_id"`
	OutstandingMinor   int64     `db:"outstanding_minor"    json:"outstanding_minor"`
	ReservedMinor      int64     `db:"reserved_minor"       json:"reserved_minor"`
	Version            int64     `db:"version"              json:"version"`
}

// AccountBalance stores a materialized balance projection for an account.
//
// The journal and journal lines remain the authoritative accounting records.
// This table is a rebuildable projection used to improve balance and report
// query performance.
//
// Balance updates must happen in the same database transaction as journal
// posting. Never use this table as the sole source of accounting truth.
type AccountBalance struct {
	common.BaseModel

	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID `db:"branch_id"       json:"branch_id,omitempty"`

	AccountID uuid.UUID `db:"account_id" json:"account_id"`

	PeriodYear  int `db:"period_year"  json:"period_year"`
	PeriodMonth int `db:"period_month" json:"period_month"`

	// Opening balances.
	OpeningDebitMinor  int64 `db:"opening_debit_minor"  json:"opening_debit_minor"`
	OpeningCreditMinor int64 `db:"opening_credit_minor" json:"opening_credit_minor"`

	// Period activity.
	DebitAmountMinor  int64 `db:"debit_amount_minor"  json:"debit_amount_minor"`
	CreditAmountMinor int64 `db:"credit_amount_minor" json:"credit_amount_minor"`

	// Closing balances.
	ClosingDebitMinor  int64 `db:"closing_debit_minor"  json:"closing_debit_minor"`
	ClosingCreditMinor int64 `db:"closing_credit_minor" json:"closing_credit_minor"`
}
