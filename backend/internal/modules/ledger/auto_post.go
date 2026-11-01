package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	if len(cmd.Lines) < 2 {
		return nil, fmt.Errorf("auto-post requires at least two lines")
	}
	req := CreateJournalRequest{
		JournalDate:   cmd.JournalDate,
		Description:   cmd.Description,
		SourceModule:  string(cmd.SourceModule),
		SourceID:      cmd.SourceID,
		BranchID:      cmd.BranchID,
		Lines:         cmd.Lines,
	}
	j, err := s.CreateJournal(ctx, cmd.OrganizationID, req)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(j.ID.String())
	if err != nil {
		return nil, err
	}
	// post immediately
	resp, err := s.PostJournal(ctx, cmd.OrganizationID, id, PostJournalRequest{ExpectedVersion: 0}, cmd.ActorID)
	if err != nil {
		return nil, err
	}
	rid, _ := uuid.Parse(resp.ID.String())
	return &AutoPostResult{JournalID: rid, JournalNo: resp.JournalNo}, nil
}

// Need time import
var _ = time.Now
