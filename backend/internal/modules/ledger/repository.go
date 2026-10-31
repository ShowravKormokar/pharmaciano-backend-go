package ledger

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
)

var errAccountNotPosting = errors.New("ledger account is inactive or non-postable")

// Repository is the ledger data-access layer. Every method is tenant-scoped.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// -----------------------------------------------------------------------------
// Chart of Accounts
// -----------------------------------------------------------------------------

const accountColumns = `id, organization_id, code, name, account_type, sub_type, ` +
	`parent_id, normal_side, is_system, is_active, is_postable, version, description, deactivation_reason, ` +
	`created_at, updated_at, deleted_at`

func scanAccount(row pgx.Row) (*ChartOfAccount, error) {
	var a ChartOfAccount
	if err := row.Scan(
		&a.ID, &a.OrganizationID, &a.Code, &a.Name, &a.AccountType, &a.SubType,
		&a.ParentID, &a.NormalSide, &a.IsSystem, &a.IsActive, &a.IsPostable, &a.Version, &a.Description, &a.DeactivationReason,
		&a.CreatedAt, &a.UpdatedAt, &a.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repository) FindAccountByID(ctx context.Context, orgID, id uuid.UUID) (*ChartOfAccount, error) {
	const q = `SELECT ` + accountColumns + ` FROM ledger_accounts
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanAccount(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindAccountByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*ChartOfAccount, error) {
	const q = `SELECT ` + accountColumns + ` FROM ledger_accounts
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR UPDATE`
	return scanAccount(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindPostingAccountForUpdate(ctx context.Context, orgID, id uuid.UUID) (*ChartOfAccount, error) {
	a, err := r.FindAccountByIDForUpdate(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, db.ErrNoRows
	}
	if !a.IsActive || !a.IsPostable {
		return nil, errAccountNotPosting
	}
	return a, nil
}

func (r *Repository) FindAccountByCode(ctx context.Context, orgID uuid.UUID, code string) (*ChartOfAccount, error) {
	const q = `SELECT ` + accountColumns + ` FROM ledger_accounts
		WHERE code = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanAccount(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, code, orgID))
}

func (r *Repository) OrganizationCurrency(ctx context.Context, orgID uuid.UUID) (string, error) {
	var currency string
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx,
		`SELECT currency FROM organizations WHERE id = $1 AND deleted_at IS NULL`, orgID,
	).Scan(&currency)
	return currency, err
}

func (r *Repository) ListAccounts(ctx context.Context, orgID uuid.UUID, isActive *bool, accountType, q string, limit, offset int) ([]*ChartOfAccount, error) {
	sb := `SELECT ` + accountColumns + ` FROM ledger_accounts
		WHERE organization_id = $1 AND deleted_at IS NULL`
	args := []any{orgID}
	n := 2
	if isActive != nil {
		sb += ` AND is_active = $` + itoa(n)
		args = append(args, *isActive)
		n++
	}
	if accountType != "" {
		sb += ` AND account_type = $` + itoa(n)
		args = append(args, accountType)
		n++
	}
	if q != "" {
		sb += ` AND (code ILIKE $` + itoa(n) + ` OR name ILIKE $` + itoa(n) + `)`
		args = append(args, "%"+q+"%")
		n++
	}
	sb += ` ORDER BY code ASC LIMIT $` + itoa(n) + ` OFFSET $` + itoa(n+1)
	args = append(args, limit, offset)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sb, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChartOfAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) InsertAccount(ctx context.Context, a *ChartOfAccount) (*ChartOfAccount, error) {
	const q = `INSERT INTO ledger_accounts (
		id, organization_id, code, name, account_type, sub_type, parent_id,
		normal_side, is_system, is_active, is_postable, description
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	RETURNING ` + accountColumns
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.UpdatedAt = a.CreatedAt
	return scanAccount(r.db.FromCtx(ctx).QueryRow(ctx, q,
		a.ID, a.OrganizationID, a.Code, a.Name, a.AccountType, a.SubType, a.ParentID,
		a.NormalSide, a.IsSystem, a.IsActive, a.IsPostable, a.Description,
	))
}

func (r *Repository) UpdateAccount(ctx context.Context, a *ChartOfAccount, expectedVersion int64) (*ChartOfAccount, error) {
	const q = `UPDATE ledger_accounts SET
		name = $3, sub_type = $4, parent_id = $5, is_active = $6,
		is_postable = $7, description = $8, version = version + 1, updated_at = $9
		WHERE id = $1 AND organization_id = $2 AND version = $10 AND deleted_at IS NULL
	RETURNING ` + accountColumns
	a.UpdatedAt = time.Now()
	return scanAccount(r.db.FromCtx(ctx).QueryRow(ctx, q,
		a.ID, a.OrganizationID, a.Name, a.SubType, a.ParentID, a.IsActive,
		a.IsPostable, a.Description, a.UpdatedAt, expectedVersion,
	))
}

func (r *Repository) DeactivateAccount(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, reason string) error {
	const q = `UPDATE ledger_accounts SET is_active = FALSE, deactivation_reason = $3,
		version = version + 1, updated_at = $4
		WHERE id = $1 AND organization_id = $2 AND version = $5 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID, reason, time.Now(), expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return db.ErrNoRows
	}
	return nil
}

// -----------------------------------------------------------------------------
// Fiscal Periods
// -----------------------------------------------------------------------------

const periodColumns = `id, organization_id, name, start_date, end_date, status, ` +
	`closed_at, closed_by, closed_reason, reopened_at, reopened_by, reopened_reason, version, created_at, updated_at, deleted_at`

func scanPeriod(row pgx.Row) (*LedgerPeriod, error) {
	var p LedgerPeriod
	if err := row.Scan(
		&p.ID, &p.OrganizationID, &p.Name, &p.StartDate, &p.EndDate, &p.Status,
		&p.ClosedAt, &p.ClosedBy, &p.ClosedReason, &p.ReopenedAt, &p.ReopenedBy,
		&p.ReopenedReason, &p.Version,
		&p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) FindPeriodByID(ctx context.Context, orgID, id uuid.UUID) (*LedgerPeriod, error) {
	const q = `SELECT ` + periodColumns + ` FROM ledger_periods
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanPeriod(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindPeriodByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*LedgerPeriod, error) {
	const q = `SELECT ` + periodColumns + ` FROM ledger_periods
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR UPDATE`
	return scanPeriod(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) ListPeriods(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]*LedgerPeriod, error) {
	sb := `SELECT ` + periodColumns + ` FROM ledger_periods WHERE organization_id = $1 AND deleted_at IS NULL`
	args := []any{orgID}
	n := 2
	if status != "" {
		sb += ` AND status = $` + itoa(n)
		args = append(args, status)
		n++
	}
	sb += ` ORDER BY start_date DESC LIMIT $` + itoa(n) + ` OFFSET $` + itoa(n+1)
	args = append(args, limit, offset)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sb, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LedgerPeriod
	for rows.Next() {
		p, err := scanPeriod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) InsertPeriod(ctx context.Context, p *LedgerPeriod) (*LedgerPeriod, error) {
	const q = `INSERT INTO ledger_periods (
		id, organization_id, name, start_date, end_date, status, version
	) VALUES ($1,$2,$3,$4,$5,$6,$7)
	RETURNING ` + periodColumns
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Status == "" {
		p.Status = "open"
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = p.CreatedAt
	return scanPeriod(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.ID, p.OrganizationID, p.Name, p.StartDate, p.EndDate, p.Status, p.Version,
	))
}

func (r *Repository) FindOpenPeriodForDate(ctx context.Context, orgID uuid.UUID, date time.Time, lock bool) (*LedgerPeriod, error) {
	q := `SELECT ` + periodColumns + ` FROM ledger_periods
		WHERE organization_id = $1 AND status = 'open' AND deleted_at IS NULL
		  AND $2::date BETWEEN start_date AND end_date`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanPeriod(r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, date))
}

func (r *Repository) ClosePeriod(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, closedBy uuid.UUID, reason string) (*LedgerPeriod, error) {
	const q = `UPDATE ledger_periods SET status = 'closed', closed_at = $4, closed_by = $5,
		closed_reason = $6, version = version + 1, updated_at = $4
		WHERE id = $1 AND organization_id = $2 AND version = $3 AND status = 'open'
		AND deleted_at IS NULL RETURNING ` + periodColumns
	now := time.Now()
	return scanPeriod(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, expectedVersion, now, closedBy, reason))
}

func (r *Repository) ReopenPeriod(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, reopenedBy uuid.UUID, reason string) (*LedgerPeriod, error) {
	const q = `UPDATE ledger_periods SET status = 'open', reopened_at = $4, reopened_by = $5,
		reopened_reason = $6, version = version + 1, updated_at = $4
		WHERE id = $1 AND organization_id = $2 AND version = $3 AND status = 'closed'
		AND deleted_at IS NULL RETURNING ` + periodColumns
	now := time.Now()
	return scanPeriod(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, expectedVersion, now, reopenedBy, reason))
}

func (r *Repository) CountDraftJournalsInPeriod(ctx context.Context, orgID, periodID uuid.UUID) (int64, error) {
	const q = `SELECT count(*) FROM ledger_journals
		WHERE organization_id = $1 AND period_id = $2
		  AND status = 'draft' AND deleted_at IS NULL`
	var count int64
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, periodID).Scan(&count)
	return count, err
}

// -----------------------------------------------------------------------------
// Journals
// -----------------------------------------------------------------------------

const journalColumns = `id, organization_id, branch_id, period_id, currency_code, journal_no, journal_date, source_module,
	source_id, idempotency_key, description, total_debit_minor, total_credit_minor, status,
	posted_by, posted_at, reversed_by_journal_id, reversal_of_journal_id, version,
	created_at, updated_at, deleted_at`

func scanJournal(row pgx.Row) (*Journal, error) {
	var j Journal
	if err := row.Scan(
		&j.ID, &j.OrganizationID, &j.BranchID, &j.PeriodID, &j.CurrencyCode, &j.JournalNo, &j.JournalDate, &j.SourceModule,
		&j.SourceID, &j.IdempotencyKey, &j.Description, &j.TotalDebitMinor, &j.TotalCreditMinor,
		&j.Status, &j.PostedBy, &j.PostedAt, &j.ReversedByJournalID, &j.ReversalOfJournalID,
		&j.Version, &j.CreatedAt, &j.UpdatedAt, &j.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *Repository) FindJournalByID(ctx context.Context, orgID, id uuid.UUID) (*Journal, error) {
	const q = `SELECT ` + journalColumns + ` FROM ledger_journals
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanJournal(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) FindJournalByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Journal, error) {
	const q = `SELECT ` + journalColumns + ` FROM ledger_journals
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR UPDATE`
	return scanJournal(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

func (r *Repository) ListJournals(ctx context.Context, orgID uuid.UUID, status, sourceModule string, sourceID, accountID, branchID *uuid.UUID, from, to *time.Time, limit int, cursor *uuid.UUID) ([]*Journal, error) {
	sb := `SELECT ` + journalColumns + ` FROM ledger_journals WHERE organization_id = $1 AND deleted_at IS NULL`
	args := []any{orgID}
	n := 2
	if status != "" {
		sb += ` AND status = $` + itoa(n)
		args = append(args, status)
		n++
	}
	if sourceModule != "" {
		sb += ` AND source_module = $` + itoa(n)
		args = append(args, sourceModule)
		n++
	}
	if sourceID != nil {
		sb += ` AND source_id = $` + itoa(n)
		args = append(args, *sourceID)
		n++
	}
	if branchID != nil {
		sb += ` AND branch_id = $` + itoa(n)
		args = append(args, *branchID)
		n++
	}
	if accountID != nil {
		sb += ` AND EXISTS (
			SELECT 1 FROM ledger_lines l
			WHERE l.organization_id = $1 AND l.journal_id = ledger_journals.id
			  AND l.account_id = $` + itoa(n) + ` AND l.deleted_at IS NULL
		)`
		args = append(args, *accountID)
		n++
	}
	if from != nil {
		sb += ` AND journal_date >= $` + itoa(n)
		args = append(args, *from)
		n++
	}
	if to != nil {
		sb += ` AND journal_date <= $` + itoa(n)
		args = append(args, *to)
		n++
	}
	if cursor != nil {
		sb += ` AND (journal_date, id) < (
			SELECT journal_date, id FROM ledger_journals
			WHERE organization_id = $` + itoa(n) + ` AND id = $` + itoa(n+1) + ` AND deleted_at IS NULL
		)`
		args = append(args, orgID, *cursor)
		n += 2
	}
	sb += ` ORDER BY journal_date DESC, id DESC LIMIT $` + itoa(n)
	args = append(args, limit)
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sb, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Journal
	for rows.Next() {
		j, err := scanJournal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *Repository) InsertJournal(ctx context.Context, j *Journal) (*Journal, error) {
	const q = `INSERT INTO ledger_journals (
		id, organization_id, branch_id, period_id, currency_code, journal_no, journal_date,
		source_module, source_id, idempotency_key, description, total_debit_minor,
		total_credit_minor, status, reversal_of_journal_id
	) VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6,''), 'L-' || lpad(nextval('ledger_journal_number_seq')::text, 10, '0')),
		$7,$8,$9,$10,$11,$12,$13,$14,$15)
	RETURNING ` + journalColumns
	if j.ID == uuid.Nil {
		j.ID = uuid.New()
	}
	if j.Status == "" {
		j.Status = JournalStatusDraft
	}
	if j.CurrencyCode == "" {
		j.CurrencyCode = "BDT"
	}
	if j.Version == 0 {
		j.Version = 1
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	j.UpdatedAt = j.CreatedAt
	return scanJournal(r.db.FromCtx(ctx).QueryRow(ctx, q,
		j.ID, j.OrganizationID, j.BranchID, j.PeriodID, j.CurrencyCode, j.JournalNo, j.JournalDate,
		j.SourceModule, j.SourceID, j.IdempotencyKey, j.Description, j.TotalDebitMinor,
		j.TotalCreditMinor, j.Status, j.ReversalOfJournalID,
	))
}

func (r *Repository) UpdateJournalDraft(ctx context.Context, j *Journal, expectedVersion int64) (*Journal, error) {
	const q = `UPDATE ledger_journals SET
		journal_date = $3, description = $4, total_debit_minor = $5, total_credit_minor = $6,
		period_id = $7, version = version + 1, updated_at = $8
		WHERE id = $1 AND organization_id = $2 AND status = 'draft' AND version = $9
		AND deleted_at IS NULL RETURNING ` + journalColumns
	now := time.Now()
	return scanJournal(r.db.FromCtx(ctx).QueryRow(ctx, q,
		j.ID, j.OrganizationID, j.JournalDate, j.Description, j.TotalDebitMinor, j.TotalCreditMinor,
		j.PeriodID, now, expectedVersion,
	))
}

func (r *Repository) PostJournal(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, postedBy uuid.UUID) (*Journal, error) {
	const q = `UPDATE ledger_journals SET status = 'posted', posted_by = $3, posted_at = $4,
		version = version + 1, updated_at = $4
		WHERE id = $1 AND organization_id = $2 AND status = 'draft' AND version = $5
		AND deleted_at IS NULL RETURNING ` + journalColumns
	now := time.Now()
	return scanJournal(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, postedBy, now, expectedVersion))
}

func (r *Repository) ReverseJournal(ctx context.Context, orgID, originalID, reversalID uuid.UUID) error {
	const q = `UPDATE ledger_journals SET reversed_by_journal_id = $3, updated_at = $4
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, originalID, orgID, reversalID, time.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return db.ErrNoRows
	}
	return nil
}

// -----------------------------------------------------------------------------
// Journal Lines
// -----------------------------------------------------------------------------

const lineColumns = `id, journal_id, account_id, branch_id, debit_minor, credit_minor, description, line_order, created_at, updated_at, deleted_at`

func scanLine(row pgx.Row) (*JournalLine, error) {
	var l JournalLine
	if err := row.Scan(
		&l.ID, &l.JournalID, &l.AccountID, &l.BranchID, &l.DebitMinor, &l.CreditMinor,
		&l.Description, &l.LineOrder, &l.CreatedAt, &l.UpdatedAt, &l.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &l, nil
}

func (r *Repository) ListLinesByJournal(ctx context.Context, orgID, journalID uuid.UUID) ([]*JournalLine, error) {
	const q = `SELECT ` + lineColumns + ` FROM ledger_lines
		WHERE journal_id = $1 AND organization_id = $2 AND deleted_at IS NULL
		ORDER BY line_order ASC`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, journalID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*JournalLine
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) InsertLines(ctx context.Context, orgID uuid.UUID, lines []*JournalLine) error {
	const q = `INSERT INTO ledger_lines (
		id, organization_id, journal_id, account_id, branch_id, debit_minor, credit_minor, description, line_order
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	for _, l := range lines {
		if l.ID == uuid.Nil {
			l.ID = uuid.New()
		}
		if l.CreatedAt.IsZero() {
			l.CreatedAt = time.Now()
		}
		l.UpdatedAt = l.CreatedAt
		if _, err := r.db.FromCtx(ctx).Exec(ctx, q,
			l.ID, orgID, l.JournalID, l.AccountID, l.BranchID, l.DebitMinor, l.CreditMinor, l.Description, l.LineOrder,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) DeleteLinesByJournal(ctx context.Context, orgID, journalID uuid.UUID) error {
	const q = `DELETE FROM ledger_lines WHERE journal_id = $1 AND organization_id = $2`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, journalID, orgID)
	return err
}

// -----------------------------------------------------------------------------
// Source postings
// -----------------------------------------------------------------------------

func (r *Repository) FindSourcePosting(ctx context.Context, orgID uuid.UUID, sourceType, postingKind string, sourceOpID uuid.UUID) (*LedgerSourcePosting, error) {
	const q = `SELECT id, organization_id, source_type, source_operation_id, posting_kind, journal_id, payload_hash, created_at
		FROM ledger_source_postings
		WHERE organization_id = $1 AND source_type = $2 AND source_operation_id = $3 AND posting_kind = $4`
	var sp LedgerSourcePosting
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, sourceType, sourceOpID, postingKind).Scan(
		&sp.ID, &sp.OrganizationID, &sp.SourceType, &sp.SourceOperationID, &sp.PostingKind,
		&sp.JournalID, &sp.PayloadHash, &sp.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &sp, nil
}

func (r *Repository) InsertSourcePosting(ctx context.Context, sp *LedgerSourcePosting) error {
	const q = `INSERT INTO ledger_source_postings (
		id, organization_id, source_type, source_operation_id, posting_kind, journal_id, payload_hash, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	if sp.ID == uuid.Nil {
		sp.ID = uuid.New()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now()
	}
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		sp.ID, sp.OrganizationID, sp.SourceType, sp.SourceOperationID, sp.PostingKind,
		sp.JournalID, sp.PayloadHash, sp.CreatedAt,
	)
	return err
}

// -----------------------------------------------------------------------------
// Subledger balance
// -----------------------------------------------------------------------------

func (r *Repository) LockSubledgerBalance(ctx context.Context, orgID, subledgerAccountID uuid.UUID) (*LedgerSubledgerBalance, error) {
	const q = `SELECT id, organization_id, subledger_account_id, outstanding_minor, reserved_minor, version, updated_at
		FROM ledger_subledger_balances
		WHERE organization_id = $1 AND subledger_account_id = $2 FOR UPDATE`
	var b LedgerSubledgerBalance
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, subledgerAccountID).Scan(
		&b.ID, &b.OrganizationID, &b.SubledgerAccountID, &b.OutstandingMinor, &b.ReservedMinor,
		&b.Version, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *Repository) UpsertSubledgerBalance(ctx context.Context, b *LedgerSubledgerBalance) error {
	const q = `INSERT INTO ledger_subledger_balances (
		id, organization_id, subledger_account_id, outstanding_minor, reserved_minor, version, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7)
	ON CONFLICT (organization_id, subledger_account_id) DO UPDATE SET
		outstanding_minor = EXCLUDED.outstanding_minor,
		reserved_minor = EXCLUDED.reserved_minor,
		version = ledger_subledger_balances.version + 1,
		updated_at = EXCLUDED.updated_at`
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	if b.Version == 0 {
		b.Version = 1
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = time.Now()
	}
	_, err := r.db.FromCtx(ctx).Exec(ctx, q,
		b.ID, b.OrganizationID, b.SubledgerAccountID, b.OutstandingMinor, b.ReservedMinor, b.Version, b.UpdatedAt,
	)
	return err
}

// -----------------------------------------------------------------------------
// Account balances
// -----------------------------------------------------------------------------

func (r *Repository) ReadAccountBalance(ctx context.Context, orgID, accountID uuid.UUID, year, month int, branchID *uuid.UUID) (*AccountBalance, error) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	q := `SELECT
		coalesce(sum(l.debit_minor - l.credit_minor) FILTER (WHERE j.journal_date < $3), 0),
		coalesce(sum(l.debit_minor) FILTER (WHERE j.journal_date >= $3 AND j.journal_date < $4), 0),
		coalesce(sum(l.credit_minor) FILTER (WHERE j.journal_date >= $3 AND j.journal_date < $4), 0)
		FROM ledger_lines l
		JOIN ledger_journals j
		  ON j.id = l.journal_id AND j.organization_id = l.organization_id
		WHERE l.organization_id = $1 AND l.account_id = $2
		  AND j.status = 'posted' AND j.deleted_at IS NULL
		  AND l.deleted_at IS NULL AND j.journal_date < $4`
	args := []any{orgID, accountID, start, end}
	if branchID != nil {
		q += ` AND l.branch_id = $5`
		args = append(args, *branchID)
	}

	var openingNet, debit, credit int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, args...).Scan(&openingNet, &debit, &credit); err != nil {
		return nil, err
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	const minInt64 = -maxInt64 - 1
	if openingNet == minInt64 ||
		(debit > 0 && openingNet > maxInt64-debit) {
		return nil, errors.New("account balance exceeds supported integer range")
	}
	afterDebits := openingNet + debit
	if credit > 0 && afterDebits < minInt64+credit {
		return nil, errors.New("account balance exceeds supported integer range")
	}
	closingNet := afterDebits - credit
	if closingNet == minInt64 {
		return nil, errors.New("account balance exceeds supported integer range")
	}
	b := &AccountBalance{
		OrganizationID:    orgID,
		AccountID:         accountID,
		BranchID:          branchID,
		PeriodYear:        year,
		PeriodMonth:       month,
		DebitAmountMinor:  debit,
		CreditAmountMinor: credit,
	}
	if openingNet > 0 {
		b.OpeningDebitMinor = openingNet
	} else if openingNet < 0 {
		b.OpeningCreditMinor = -openingNet
	}
	if closingNet > 0 {
		b.ClosingDebitMinor = closingNet
	} else if closingNet < 0 {
		b.ClosingCreditMinor = -closingNet
	}
	return b, nil
}

func (r *Repository) ReadTrialBalance(ctx context.Context, orgID uuid.UUID, asOf time.Time, branchID *uuid.UUID) ([]TrialBalanceLine, error) {
	q := `SELECT a.id, a.code, a.name, a.account_type,
		coalesce(sum(l.debit_minor - l.credit_minor), 0)::BIGINT AS net_minor
		FROM ledger_accounts a
		JOIN ledger_lines l
		  ON l.account_id = a.id AND l.organization_id = a.organization_id
		JOIN ledger_journals j
		  ON j.id = l.journal_id AND j.organization_id = l.organization_id
		WHERE a.organization_id = $1 AND a.deleted_at IS NULL
		  AND l.deleted_at IS NULL AND j.deleted_at IS NULL
		  AND j.status = 'posted' AND j.journal_date < ($2::date + INTERVAL '1 day')`
	args := []any{orgID, asOf}
	if branchID != nil {
		q += ` AND l.branch_id = $3`
		args = append(args, *branchID)
	}
	q += ` GROUP BY a.id, a.code, a.name, a.account_type
		HAVING sum(l.debit_minor - l.credit_minor) <> 0
		ORDER BY a.code, a.id`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := make([]TrialBalanceLine, 0)
	for rows.Next() {
		var line TrialBalanceLine
		var net int64
		if err := rows.Scan(&line.AccountID, &line.AccountCode, &line.AccountName, &line.AccountType, &net); err != nil {
			return nil, err
		}
		if net > 0 {
			line.DebitMinor = net
			line.BalanceMinor = net
		} else if net < 0 {
			line.CreditMinor = -net
			line.BalanceMinor = net
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func itoa(n int) string {
	return strconv.Itoa(n)
}
