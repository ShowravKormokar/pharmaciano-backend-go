package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// AutoPostCommand describes a typed source-originated posting request.
// Source modules (Sale, Purchase, Inventory) build this and pass it to the
// ledger service; arbitrary account IDs are never accepted from callers.
type AutoPostCommand struct {
	OrganizationID uuid.UUID
	ActorID        uuid.UUID

	SourceModule JournalSourceModule
	SourceID     *uuid.UUID
	PostingKind  string

	JournalDate time.Time
	Description string
	BranchID    *uuid.UUID

	// Lines are pre-validated debit/credit facts supplied by the posting engine
	// after resolving the approved posting-rule version. Amounts are minor units.
	Lines []JournalLineRequest

	// ExpectedRuleVersion, when set, pins the posting-rule version applied.
	ExpectedRuleVersion *int64
}

// AutoPostResult is returned after a successful auto-post.
type AutoPostResult struct {
	JournalID uuid.UUID
	JournalNo string
}

// AutoPost creates and posts a journal from a typed source command in one
// transactional step. It validates balance, accounts and period eligibility.
// The caller owns the surrounding transaction context.
func (s *Service) AutoPost(ctx context.Context, cmd AutoPostCommand) (*AutoPostResult, error) {
	if cmd.OrganizationID == uuid.Nil {
		return nil, errs.Validation("organization context is required")
	}
	if cmd.SourceModule == "" {
		return nil, errs.Validation("source_module is required")
	}
	if cmd.SourceID == nil {
		return nil, errs.Validation("source_id is required")
	}
	if strings.TrimSpace(cmd.PostingKind) == "" {
		return nil, errs.Validation("posting_kind is required")
	}
	if len(cmd.Lines) < 2 {
		return nil, errs.Validation("auto-post requires at least two lines")
	}
	if cmd.ActorID == uuid.Nil {
		return nil, errs.Validation("authenticated actor is required")
	}

	payloadHash, err := sourcePostingHash(cmd)
	if err != nil {
		return nil, errs.Validation("source posting payload is invalid")
	}

	var result *AutoPostResult
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		existing, err := s.repo.FindSourcePosting(ctx, cmd.OrganizationID, string(cmd.SourceModule), cmd.PostingKind, *cmd.SourceID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if existing != nil {
			if existing.PayloadHash != nil && *existing.PayloadHash != payloadHash {
				return errs.Conflict("source posting payload conflicts with an existing ledger posting")
			}
			j, err := s.repo.FindJournalByID(ctx, cmd.OrganizationID, existing.JournalID)
			if err != nil {
				if errors.Is(err, db.ErrNoRows) {
					return errs.NotFound("journal for source posting")
				}
				return errs.DatabaseError(err)
			}
			result = &AutoPostResult{JournalID: j.ID, JournalNo: j.JournalNo}
			return nil
		}

		currency, err := s.repo.OrganizationCurrency(ctx, cmd.OrganizationID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if currency != "BDT" {
			return errs.Validation("ledger currently supports BDT functional currency only")
		}
		period, err := s.repo.FindOpenPeriodForDate(ctx, cmd.OrganizationID, dateOnly(cmd.JournalDate).UTC(), true)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal date is not in an open ledger period")
			}
			return errs.DatabaseError(err)
		}

		lines, totalDebit, totalCredit, err := validatedJournalLines(cmd.Lines)
		if err != nil {
			return err
		}
		if err := validateJournalLineBranches(lines, cmd.BranchID); err != nil {
			return err
		}
		if err := s.lockPostingAccounts(ctx, cmd.OrganizationID, lines); err != nil {
			return err
		}

		created, err := s.repo.InsertJournal(ctx, &Journal{
			OrganizationID:   cmd.OrganizationID,
			BranchID:         cmd.BranchID,
			PeriodID:         period.ID,
			CurrencyCode:     currency,
			JournalDate:      dateOnly(cmd.JournalDate).UTC(),
			SourceModule:     cmd.SourceModule,
			SourceID:         cmd.SourceID,
			Description:      strings.TrimSpace(cmd.Description),
			TotalDebitMinor:  totalDebit,
			TotalCreditMinor: totalCredit,
			Status:           JournalStatusDraft,
		})
		if err != nil {
			return mapLedgerWriteError(err, "could not create journal")
		}
		for _, line := range lines {
			line.JournalID = created.ID
			if line.BranchID == nil {
				line.BranchID = cmd.BranchID
			}
		}
		if err := s.repo.InsertLines(ctx, cmd.OrganizationID, lines); err != nil {
			return mapLedgerWriteError(err, "could not create journal lines")
		}
		posted, err := s.repo.PostJournal(ctx, cmd.OrganizationID, created.ID, created.Version, cmd.ActorID)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return errs.Conflict("journal changed before it could be posted")
			}
			return mapLedgerWriteError(err, "could not post journal")
		}
		if err := s.repo.InsertSourcePosting(ctx, &LedgerSourcePosting{
			OrganizationID:    cmd.OrganizationID,
			SourceType:        string(cmd.SourceModule),
			SourceOperationID: *cmd.SourceID,
			PostingKind:       cmd.PostingKind,
			JournalID:         posted.ID,
			PayloadHash:       &payloadHash,
		}); err != nil {
			return errs.DatabaseError(err)
		}
		result = &AutoPostResult{JournalID: posted.ID, JournalNo: posted.JournalNo}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func sourcePostingHash(cmd AutoPostCommand) (string, error) {
	payload := struct {
		OrganizationID    string               `json:"organization_id"`
		SourceType        string               `json:"source_type"`
		SourceOperationID string               `json:"source_operation_id"`
		PostingKind       string               `json:"posting_kind"`
		JournalDate       time.Time            `json:"journal_date"`
		BranchID          *uuid.UUID           `json:"branch_id,omitempty"`
		Description       string               `json:"description"`
		Lines             []JournalLineRequest `json:"lines"`
	}{
		OrganizationID:    cmd.OrganizationID.String(),
		SourceType:        string(cmd.SourceModule),
		SourceOperationID: cmd.SourceID.String(),
		PostingKind:       cmd.PostingKind,
		JournalDate:       dateOnly(cmd.JournalDate).UTC(),
		BranchID:          cmd.BranchID,
		Description:       strings.TrimSpace(cmd.Description),
		Lines:             cmd.Lines,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

var _ = time.Now
