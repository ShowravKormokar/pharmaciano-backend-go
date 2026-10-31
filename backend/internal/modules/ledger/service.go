package ledger

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
)

// Service encapsulates ledger business rules.
type Service struct {
	repo    *Repository
	db      *db.DB
	metrics *telemetry.Metrics
}

// NewService wires the service to its repository.
func NewService(repo *Repository, database *db.DB, metrics *telemetry.Metrics) *Service {
	return &Service{repo: repo, db: database, metrics: metrics}
}

// -----------------------------------------------------------------------------
// Accounts
// -----------------------------------------------------------------------------

func (s *Service) CreateAccount(ctx context.Context, orgID uuid.UUID, req CreateAccountRequest) (*AccountResponse, error) {
	if orgID == uuid.Nil {
		return nil, errs.Validation("organization context is required")
	}
	isActive := req.IsActive == nil || *req.IsActive
	isPostable := req.IsPostable == nil || *req.IsPostable
	a := &ChartOfAccount{
		OrganizationID: orgID,
		Code:           strings.TrimSpace(req.Code),
		Name:           strings.TrimSpace(req.Name),
		AccountType:    AccountType(req.AccountType),
		SubType:        req.SubType,
		ParentID:       req.ParentID,
		NormalSide:     NormalSide(req.NormalSide),
		IsPostable:     isPostable,
		IsActive:       isActive,
		Description:    req.Description,
	}
	var created *ChartOfAccount
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if a.ParentID != nil {
			parent, err := s.repo.FindAccountByIDForUpdate(ctx, orgID, *a.ParentID)
			if err != nil {
				if errors.Is(err, db.ErrNoRows) {
					return errs.NotFound("parent account")
				}
				return errs.DatabaseError(err)
			}
			if !parent.IsActive || parent.IsPostable {
				return errs.Validation("parent account must be active and non-postable")
			}
		}
		var err error
		created, err = s.repo.InsertAccount(ctx, a)
		return mapLedgerWriteError(err, "account code already exists")
	})
	if err != nil {
		return nil, err
	}
	return accountToResponse(created), nil
}

func (s *Service) ListAccounts(ctx context.Context, orgID uuid.UUID, q ListAccountsQuery) ([]*AccountResponse, error) {
	q.Offset.NormalizeWithMax(100)
	accs, err := s.repo.ListAccounts(ctx, orgID, q.IsActive, q.AccountType, q.Q, q.Offset.Limit, q.Offset.OffsetSQL())
	if err != nil {
		return nil, err
	}
	out := make([]*AccountResponse, 0, len(accs))
	for _, a := range accs {
		out = append(out, accountToResponse(a))
	}
	return out, nil
}

func (s *Service) GetAccount(ctx context.Context, orgID, id uuid.UUID) (*AccountResponse, error) {
	a, err := s.repo.FindAccountByID(ctx, orgID, id)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("ledger account")
		}
		return nil, errs.DatabaseError(err)
	}
	if a == nil {
		return nil, errs.NotFound("ledger account")
	}
	return accountToResponse(a), nil
}

func (s *Service) UpdateAccount(ctx context.Context, orgID, id uuid.UUID, req UpdateAccountRequest) (*AccountResponse, error) {
	var updated *ChartOfAccount
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		account, err := s.repo.FindAccountByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("ledger account")
			}
			return errs.DatabaseError(err)
		}
		if account.Version != req.ExpectedVersion {
			return errs.Conflict("ledger account was changed; reload and retry")
		}
		if req.IsEmpty() {
			updated = account
			return nil
		}
		if req.Name != nil {
			account.Name = strings.TrimSpace(*req.Name)
		}
		if req.SubType != nil {
			account.SubType = req.SubType
		}
		if req.ParentID != nil {
			if *req.ParentID == id {
				return errs.Validation("an account cannot be its own parent")
			}
			parent, err := s.repo.FindAccountByIDForUpdate(ctx, orgID, *req.ParentID)
			if err != nil {
				if errors.Is(err, db.ErrNoRows) {
					return errs.NotFound("parent account")
				}
				return errs.DatabaseError(err)
			}
			if !parent.IsActive || parent.IsPostable {
				return errs.Validation("parent account must be active and non-postable")
			}
			account.ParentID = req.ParentID
		}
		if req.IsActive != nil {
			account.IsActive = *req.IsActive
		}
		if req.IsPostable != nil {
			account.IsPostable = *req.IsPostable
		}
		if req.Description != nil {
			account.Description = req.Description
		}
		updated, err = s.repo.UpdateAccount(ctx, account, req.ExpectedVersion)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("ledger account was changed; reload and retry")
			}
			return mapLedgerWriteError(err, "ledger account update conflicts with existing data")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accountToResponse(updated), nil
}

func (s *Service) DeactivateAccount(ctx context.Context, orgID, id uuid.UUID, expectedVersion int64, reason string) error {
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		account, err := s.repo.FindAccountByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("ledger account")
			}
			return errs.DatabaseError(err)
		}
		if account.Version != expectedVersion {
			return errs.Conflict("ledger account was changed; reload and retry")
		}
		if err := s.repo.DeactivateAccount(ctx, orgID, id, expectedVersion, strings.TrimSpace(reason)); err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("ledger account was changed; reload and retry")
			}
			return errs.DatabaseError(err)
		}
		return nil
	})
}

func (s *Service) GetAccountBalance(ctx context.Context, orgID, accountID uuid.UUID, year, month int, branchID *uuid.UUID) (*AccountBalanceResponse, error) {
	if month < 1 || month > 12 || year < 1 {
		return nil, errs.Validation("period_year and period_month are invalid")
	}
	if _, err := s.repo.FindAccountByID(ctx, orgID, accountID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("ledger account")
		}
		return nil, errs.DatabaseError(err)
	}
	balance, err := s.repo.ReadAccountBalance(ctx, orgID, accountID, year, month, branchID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	return &AccountBalanceResponse{
		AccountID:          accountID,
		PeriodYear:         year,
		PeriodMonth:        month,
		OpeningDebitMinor:  balance.OpeningDebitMinor,
		OpeningCreditMinor: balance.OpeningCreditMinor,
		DebitMinor:         balance.DebitAmountMinor,
		CreditMinor:        balance.CreditAmountMinor,
		ClosingDebitMinor:  balance.ClosingDebitMinor,
		ClosingCreditMinor: balance.ClosingCreditMinor,
	}, nil
}

// -----------------------------------------------------------------------------
// Periods
// -----------------------------------------------------------------------------

func (s *Service) CreatePeriod(ctx context.Context, orgID uuid.UUID, req CreatePeriodRequest) (*PeriodResponse, error) {
	startDate := dateOnly(req.StartDate).UTC()
	endDate := dateOnly(req.EndDate).UTC()
	if endDate.Before(startDate) {
		return nil, errs.Validation("end_date must not be before start_date")
	}
	p := &LedgerPeriod{
		OrganizationID: orgID,
		Name:           strings.TrimSpace(req.Name),
		StartDate:      startDate,
		EndDate:        endDate,
	}
	var created *LedgerPeriod
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		var err error
		created, err = s.repo.InsertPeriod(ctx, p)
		return mapLedgerWriteError(err, "ledger period overlaps an existing period")
	})
	if err != nil {
		return nil, err
	}
	return periodToResponse(created), nil
}

func (s *Service) ListPeriods(ctx context.Context, orgID uuid.UUID, q ListPeriodsQuery) ([]*PeriodResponse, error) {
	q.Offset.NormalizeWithMax(100)
	ps, err := s.repo.ListPeriods(ctx, orgID, q.Status, q.Offset.Limit, q.Offset.OffsetSQL())
	if err != nil {
		return nil, err
	}
	out := make([]*PeriodResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, periodToResponse(p))
	}
	return out, nil
}

func (s *Service) ClosePeriod(ctx context.Context, orgID, id uuid.UUID, req ClosePeriodRequest, actorID uuid.UUID) (*PeriodResponse, error) {
	if actorID == uuid.Nil {
		return nil, errs.Validation("authenticated actor is required")
	}
	var p *LedgerPeriod
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.repo.FindPeriodByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("ledger period")
			}
			return errs.DatabaseError(err)
		}
		if locked.Version != req.ExpectedVersion || locked.Status != "open" {
			return errs.Conflict("ledger period changed or is not open")
		}
		if drafts, err := s.repo.CountDraftJournalsInPeriod(ctx, orgID, id); err != nil {
			return errs.DatabaseError(err)
		} else if drafts > 0 {
			return errs.Conflict("ledger period has unposted draft journals")
		}
		p, err = s.repo.ClosePeriod(ctx, orgID, id, req.ExpectedVersion, actorID, req.Reason)
		if err != nil {
			return mapLedgerWriteError(err, "ledger period changed or is not open")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return periodToResponse(p), nil
}

func (s *Service) ReopenPeriod(ctx context.Context, orgID, id uuid.UUID, req ReopenPeriodRequest, actorID uuid.UUID) (*PeriodResponse, error) {
	if actorID == uuid.Nil {
		return nil, errs.Validation("authenticated actor is required")
	}
	var p *LedgerPeriod
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.repo.FindPeriodByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("ledger period")
			}
			return errs.DatabaseError(err)
		}
		if locked.Version != req.ExpectedVersion || locked.Status != "closed" {
			return errs.Conflict("ledger period changed or is not closed")
		}
		p, err = s.repo.ReopenPeriod(ctx, orgID, id, req.ExpectedVersion, actorID, req.Reason)
		if err != nil {
			return mapLedgerWriteError(err, "ledger period changed or is not closed")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return periodToResponse(p), nil
}

// -----------------------------------------------------------------------------
// Journals
// -----------------------------------------------------------------------------

func (s *Service) CreateJournal(ctx context.Context, orgID uuid.UUID, req CreateJournalRequest) (*JournalResponse, error) {
	if req.SourceModule != string(JournalSourceManual) || req.SourceID != nil {
		return nil, errs.Validation("public journal creation only supports manual journals")
	}
	req.JournalDate = dateOnly(req.JournalDate).UTC()
	lines, totalDebit, totalCredit, err := validatedJournalLines(req.Lines)
	if err != nil {
		return nil, err
	}
	if err := validateJournalLineBranches(lines, req.BranchID); err != nil {
		return nil, err
	}
	req.Description = strings.TrimSpace(req.Description)
	if orgID == uuid.Nil {
		return nil, errs.Validation("organization context is required")
	}
	var response *JournalResponse
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		currency, err := s.repo.OrganizationCurrency(ctx, orgID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if currency != "BDT" {
			return errs.Validation("ledger currently supports BDT functional currency only")
		}
		period, err := s.repo.FindOpenPeriodForDate(ctx, orgID, req.JournalDate, true)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal date is not in an open ledger period")
			}
			return errs.DatabaseError(err)
		}
		if err := s.lockPostingAccounts(ctx, orgID, lines); err != nil {
			return err
		}
		journal := &Journal{
			OrganizationID:   orgID,
			BranchID:         req.BranchID,
			PeriodID:         period.ID,
			CurrencyCode:     currency,
			JournalDate:      req.JournalDate,
			SourceModule:     JournalSourceManual,
			Description:      req.Description,
			TotalDebitMinor:  totalDebit,
			TotalCreditMinor: totalCredit,
			Status:           JournalStatusDraft,
		}
		created, err := s.repo.InsertJournal(ctx, journal)
		if err != nil {
			return mapLedgerWriteError(err, "could not create journal")
		}
		for _, line := range lines {
			line.JournalID = created.ID
			if line.BranchID == nil {
				line.BranchID = req.BranchID
			}
		}
		if err := s.repo.InsertLines(ctx, orgID, lines); err != nil {
			return mapLedgerWriteError(err, "could not create journal lines")
		}
		loadedLines, err := s.repo.ListLinesByJournal(ctx, orgID, created.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		response = journalToResponse(created)
		response.Lines = journalLinesToResponse(loadedLines)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Service) GetJournal(ctx context.Context, orgID, id uuid.UUID) (*JournalResponse, error) {
	j, err := s.repo.FindJournalByID(ctx, orgID, id)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, errs.NotFound("journal")
		}
		return nil, errs.DatabaseError(err)
	}
	if j == nil {
		return nil, errs.NotFound("journal")
	}
	ls, err := s.repo.ListLinesByJournal(ctx, orgID, id)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	resp := journalToResponse(j)
	resp.Lines = journalLinesToResponse(ls)
	return resp, nil
}

func (s *Service) UpdateJournalDraft(ctx context.Context, orgID, id uuid.UUID, req UpdateJournalRequest) (*JournalResponse, error) {
	var response *JournalResponse
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		journal, err := s.repo.FindJournalByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("journal")
			}
			return errs.DatabaseError(err)
		}
		if journal.Status != JournalStatusDraft || journal.Version != req.ExpectedVersion {
			return errs.Conflict("journal changed or is not a draft")
		}

		date := journal.JournalDate
		if req.JournalDate != nil {
			date = dateOnly(*req.JournalDate).UTC()
		}
		period, err := s.repo.FindOpenPeriodForDate(ctx, orgID, date, true)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal date is not in an open ledger period")
			}
			return errs.DatabaseError(err)
		}

		var lines []*JournalLine
		if len(req.Lines) > 0 {
			var debit, credit int64
			lines, debit, credit, err = validatedJournalLines(req.Lines)
			if err != nil {
				return err
			}
			if err := s.lockPostingAccounts(ctx, orgID, lines); err != nil {
				return err
			}
			if err := validateJournalLineBranches(lines, journal.BranchID); err != nil {
				return err
			}
			journal.TotalDebitMinor = debit
			journal.TotalCreditMinor = credit
		}
		journal.JournalDate = date
		journal.PeriodID = period.ID
		if req.Description != nil {
			journal.Description = strings.TrimSpace(*req.Description)
		}
		updated, err := s.repo.UpdateJournalDraft(ctx, journal, req.ExpectedVersion)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal changed before it could be updated")
			}
			return mapLedgerWriteError(err, "could not update journal")
		}
		if lines != nil {
			if err := s.repo.DeleteLinesByJournal(ctx, orgID, id); err != nil {
				return errs.DatabaseError(err)
			}
			for _, line := range lines {
				line.JournalID = id
				if line.BranchID == nil {
					line.BranchID = journal.BranchID
				}
			}
			if err := s.repo.InsertLines(ctx, orgID, lines); err != nil {
				return mapLedgerWriteError(err, "could not update journal lines")
			}
		}
		loaded, err := s.repo.ListLinesByJournal(ctx, orgID, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		response = journalToResponse(updated)
		response.Lines = journalLinesToResponse(loaded)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Service) ListJournals(ctx context.Context, orgID uuid.UUID, q ListJournalsQuery) ([]*JournalResponse, error) {
	if q.FromDate != nil && q.ToDate != nil && q.ToDate.Before(*q.FromDate) {
		return nil, errs.Validation("to_date must be on or after from_date")
	}
	if q.Limit < 1 {
		q.Limit = 50
	} else if q.Limit > 100 {
		q.Limit = 100
	}
	js, err := s.repo.ListJournals(ctx, orgID, q.Status, q.SourceModule, q.SourceID, q.AccountID, q.BranchID, q.FromDate, q.ToDate, q.Limit, q.CursorID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	out := make([]*JournalResponse, 0, len(js))
	for _, j := range js {
		out = append(out, journalToResponse(j))
	}
	return out, nil
}

func (s *Service) PostJournal(ctx context.Context, orgID, id uuid.UUID, req PostJournalRequest, actorID uuid.UUID) (*JournalResponse, error) {
	if actorID == uuid.Nil {
		return nil, errs.Validation("authenticated actor is required")
	}
	var posted *Journal
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		journal, err := s.repo.FindJournalByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("journal")
			}
			return errs.DatabaseError(err)
		}
		if journal.Status != JournalStatusDraft || journal.Version != req.ExpectedVersion {
			return errs.Conflict("journal changed or is not a draft")
		}
		period, err := s.repo.FindPeriodByIDForUpdate(ctx, orgID, journal.PeriodID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("ledger period")
			}
			return errs.DatabaseError(err)
		}
		if period.Status != "open" || journal.JournalDate.Before(period.StartDate) || dateOnly(journal.JournalDate).After(dateOnly(period.EndDate)) {
			return errs.Conflict("journal period is closed or does not contain its date")
		}
		lines, err := s.repo.ListLinesByJournal(ctx, orgID, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		requestLines := journalLinesToRequests(lines)
		_, debit, credit, err := validatedJournalLines(requestLines)
		if err != nil {
			return err
		}
		if debit != journal.TotalDebitMinor || credit != journal.TotalCreditMinor {
			return errs.Validation("journal totals do not match its lines")
		}
		if err := s.lockPostingAccounts(ctx, orgID, lines); err != nil {
			return err
		}
		posted, err = s.repo.PostJournal(ctx, orgID, id, req.ExpectedVersion, actorID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal changed before it could be posted")
			}
			return mapLedgerWriteError(err, "could not post journal")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return journalToResponse(posted), nil
}

func (s *Service) ReverseJournal(ctx context.Context, orgID, id uuid.UUID, req ReverseJournalRequest, actorID uuid.UUID) (*JournalResponse, error) {
	if actorID == uuid.Nil {
		return nil, errs.Validation("authenticated actor is required")
	}
	var response *JournalResponse
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		original, err := s.repo.FindJournalByIDForUpdate(ctx, orgID, id)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("journal")
			}
			return errs.DatabaseError(err)
		}
		if original.Status != JournalStatusPosted || original.Version != req.ExpectedVersion {
			return errs.Conflict("journal changed or is not posted")
		}
		if original.ReversedByJournalID != nil {
			return errs.Conflict("journal has already been reversed")
		}
		period, err := s.repo.FindOpenPeriodForDate(ctx, orgID, dateOnly(req.PostingDate), true)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("reversal date is not in an open ledger period")
			}
			return errs.DatabaseError(err)
		}
		originalLines, err := s.repo.ListLinesByJournal(ctx, orgID, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		reversalLines := make([]*JournalLine, 0, len(originalLines))
		requestLines := make([]JournalLineRequest, 0, len(originalLines))
		for _, line := range originalLines {
			reversalLines = append(reversalLines, &JournalLine{
				AccountID:   line.AccountID,
				BranchID:    line.BranchID,
				DebitMinor:  line.CreditMinor,
				CreditMinor: line.DebitMinor,
				Description: line.Description,
				LineOrder:   line.LineOrder,
			})
			requestLines = append(requestLines, JournalLineRequest{
				AccountID:   line.AccountID,
				BranchID:    line.BranchID,
				DebitMinor:  line.CreditMinor,
				CreditMinor: line.DebitMinor,
				Description: line.Description,
				LineOrder:   line.LineOrder,
			})
		}
		_, debit, credit, err := validatedJournalLines(requestLines)
		if err != nil {
			return err
		}
		if err := s.lockPostingAccounts(ctx, orgID, reversalLines); err != nil {
			return err
		}
		now := time.Now()
		reversal, err := s.repo.InsertJournal(ctx, &Journal{
			OrganizationID:      orgID,
			BranchID:            original.BranchID,
			PeriodID:            period.ID,
			CurrencyCode:        original.CurrencyCode,
			JournalDate:         req.PostingDate,
			SourceModule:        original.SourceModule,
			SourceID:            original.SourceID,
			Description:         "Reversal: " + strings.TrimSpace(req.Reason),
			TotalDebitMinor:     debit,
			TotalCreditMinor:    credit,
			Status:              JournalStatusDraft,
			ReversalOfJournalID: &id,
		})
		if err != nil {
			return mapLedgerWriteError(err, "could not create reversal journal")
		}
		for _, line := range reversalLines {
			line.JournalID = reversal.ID
		}
		if err := s.repo.InsertLines(ctx, orgID, reversalLines); err != nil {
			return mapLedgerWriteError(err, "could not create reversal lines")
		}
		reversal, err = s.repo.PostJournal(ctx, orgID, reversal.ID, reversal.Version, actorID)
		if err != nil {
			return mapLedgerWriteError(err, "could not post reversal journal")
		}
		if err := s.repo.ReverseJournal(ctx, orgID, id, reversal.ID); err != nil {
			return mapLedgerWriteError(err, "journal has already been reversed")
		}
		reversal.PostedAt = &now
		loaded, err := s.repo.ListLinesByJournal(ctx, orgID, reversal.ID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		response = journalToResponse(reversal)
		response.Lines = journalLinesToResponse(loaded)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// -----------------------------------------------------------------------------
// Trial balance
// -----------------------------------------------------------------------------

func (s *Service) TrialBalance(ctx context.Context, orgID uuid.UUID, q TrialBalanceQuery) (*TrialBalanceResponse, error) {
	if q.AsOfDate.IsZero() {
		return nil, errs.Validation("as_of is required")
	}
	lines, err := s.repo.ReadTrialBalance(ctx, orgID, q.AsOfDate, q.BranchID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	response := &TrialBalanceResponse{
		AsOfDate: dateOnly(q.AsOfDate),
		BranchID: q.BranchID,
		Lines:    lines,
	}
	for _, line := range lines {
		if line.DebitMinor > math.MaxInt64-response.TotalDebit || line.CreditMinor > math.MaxInt64-response.TotalCredit {
			return nil, errs.Conflict("trial balance exceeds supported integer range")
		}
		response.TotalDebit += line.DebitMinor
		response.TotalCredit += line.CreditMinor
	}
	if response.TotalDebit != response.TotalCredit {
		return nil, errs.New(errs.CodeLedgerUnbalanced, "ledger trial balance is out of balance")
	}
	return response, nil
}

// -----------------------------------------------------------------------------
// mappers
// -----------------------------------------------------------------------------

func accountToResponse(a *ChartOfAccount) *AccountResponse {
	return &AccountResponse{
		ID:          a.ID,
		Code:        a.Code,
		Name:        a.Name,
		AccountType: string(a.AccountType),
		SubType:     a.SubType,
		ParentID:    a.ParentID,
		NormalSide:  string(a.NormalSide),
		IsSystem:    a.IsSystem,
		IsActive:    a.IsActive,
		IsPostable:  a.IsPostable,
		Version:     a.Version,
		Description: a.Description,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

func periodToResponse(p *LedgerPeriod) *PeriodResponse {
	return &PeriodResponse{
		ID:             p.ID,
		Name:           p.Name,
		StartDate:      p.StartDate,
		EndDate:        p.EndDate,
		Status:         p.Status,
		ClosedAt:       p.ClosedAt,
		ClosedBy:       p.ClosedBy,
		ClosedReason:   p.ClosedReason,
		ReopenedAt:     p.ReopenedAt,
		ReopenedBy:     p.ReopenedBy,
		ReopenedReason: p.ReopenedReason,
		Version:        p.Version,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
}

func journalToResponse(j *Journal) *JournalResponse {
	return &JournalResponse{
		ID:                  j.ID,
		OrganizationID:      j.OrganizationID,
		BranchID:            j.BranchID,
		PeriodID:            j.PeriodID,
		CurrencyCode:        j.CurrencyCode,
		JournalNo:           j.JournalNo,
		JournalDate:         j.JournalDate,
		SourceModule:        string(j.SourceModule),
		SourceID:            j.SourceID,
		Description:         j.Description,
		TotalDebitMinor:     j.TotalDebitMinor,
		TotalCreditMinor:    j.TotalCreditMinor,
		Status:              string(j.Status),
		PostedBy:            j.PostedBy,
		PostedAt:            j.PostedAt,
		ReversedByJournalID: j.ReversedByJournalID,
		ReversalOfJournalID: j.ReversalOfJournalID,
		CreatedAt:           j.CreatedAt,
		UpdatedAt:           j.UpdatedAt,
		Version:             j.Version,
	}
}

func validatedJournalLines(requests []JournalLineRequest) ([]*JournalLine, int64, int64, error) {
	if len(requests) < 2 || len(requests) > 1000 {
		return nil, 0, 0, errs.Validation("journal must contain between 2 and 1000 lines")
	}
	lines := make([]*JournalLine, 0, len(requests))
	orders := make(map[int]struct{}, len(requests))
	branches := make(map[uuid.UUID]struct{})
	var debitTotal, creditTotal int64
	for _, line := range requests {
		if line.AccountID == uuid.Nil || line.LineOrder < 1 ||
			line.DebitMinor < 0 || line.CreditMinor < 0 ||
			(line.DebitMinor == 0) == (line.CreditMinor == 0) {
			return nil, 0, 0, errs.Validation("each journal line needs an account, unique order, and exactly one positive amount")
		}
		if _, exists := orders[line.LineOrder]; exists {
			return nil, 0, 0, errs.Validation("journal line orders must be unique")
		}
		orders[line.LineOrder] = struct{}{}
		if line.BranchID != nil {
			branches[*line.BranchID] = struct{}{}
		}
		if line.DebitMinor > math.MaxInt64-debitTotal || line.CreditMinor > math.MaxInt64-creditTotal {
			return nil, 0, 0, errs.Validation("journal total exceeds the supported integer range")
		}
		debitTotal += line.DebitMinor
		creditTotal += line.CreditMinor
		lines = append(lines, &JournalLine{
			AccountID:   line.AccountID,
			BranchID:    line.BranchID,
			DebitMinor:  line.DebitMinor,
			CreditMinor: line.CreditMinor,
			Description: line.Description,
			LineOrder:   line.LineOrder,
		})
	}
	if debitTotal != creditTotal {
		return nil, 0, 0, errs.New(errs.CodeLedgerUnbalanced, "journal debits and credits must balance")
	}
	if len(branches) > 1 {
		return nil, 0, 0, errs.Validation("cross-branch journals are not supported")
	}
	return lines, debitTotal, creditTotal, nil
}

func (s *Service) lockPostingAccounts(ctx context.Context, orgID uuid.UUID, lines []*JournalLine) error {
	accountIDs := make([]uuid.UUID, 0, len(lines))
	seen := make(map[uuid.UUID]struct{}, len(lines))
	for _, line := range lines {
		if _, exists := seen[line.AccountID]; !exists {
			seen[line.AccountID] = struct{}{}
			accountIDs = append(accountIDs, line.AccountID)
		}
	}
	sort.Slice(accountIDs, func(i, j int) bool {
		return accountIDs[i].String() < accountIDs[j].String()
	})
	for _, id := range accountIDs {
		if _, err := s.repo.FindPostingAccountForUpdate(ctx, orgID, id); err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.NotFound("posting account")
			}
			if errors.Is(err, errAccountNotPosting) {
				return errs.Conflict("journal references an inactive or non-postable account")
			}
			return errs.DatabaseError(err)
		}
	}
	return nil
}

func validateJournalLineBranches(lines []*JournalLine, journalBranchID *uuid.UUID) error {
	for _, line := range lines {
		if line.BranchID == nil {
			continue
		}
		if journalBranchID == nil || *line.BranchID != *journalBranchID {
			return errs.Validation("journal line branch must match the journal branch")
		}
	}
	return nil
}

func journalLinesToResponse(lines []*JournalLine) []JournalLineResponse {
	out := make([]JournalLineResponse, 0, len(lines))
	for _, line := range lines {
		out = append(out, JournalLineResponse{
			ID:          line.ID,
			AccountID:   line.AccountID,
			BranchID:    line.BranchID,
			DebitMinor:  line.DebitMinor,
			CreditMinor: line.CreditMinor,
			Description: line.Description,
			LineOrder:   line.LineOrder,
		})
	}
	return out
}

func journalLinesToRequests(lines []*JournalLine) []JournalLineRequest {
	out := make([]JournalLineRequest, 0, len(lines))
	for _, line := range lines {
		out = append(out, JournalLineRequest{
			AccountID:   line.AccountID,
			BranchID:    line.BranchID,
			DebitMinor:  line.DebitMinor,
			CreditMinor: line.CreditMinor,
			Description: line.Description,
			LineOrder:   line.LineOrder,
		})
	}
	return out
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func mapLedgerWriteError(err error, conflictMessage string) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23P01":
			return errs.Conflict(conflictMessage)
		case "23503", "23514", "22P02":
			return errs.Validation("ledger data violates a required accounting constraint")
		}
	}
	return errs.DatabaseError(err)
}
