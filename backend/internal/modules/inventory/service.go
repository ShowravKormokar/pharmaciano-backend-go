package inventory

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/telemetry"
)

type Service struct {
	repo    *Repository
	metrics *telemetry.Metrics
}

func NewService(repo *Repository, metrics *telemetry.Metrics) *Service {
	return &Service{repo: repo, metrics: metrics}
}

// ListBatches returns a cursor page scoped to the caller's organization and
// effective branch assignments. The repository applies all filters in SQL.
func (s *Service) ListBatches(ctx context.Context, query *ListBatchesQuery) (result BatchListResponse, retErr error) {
	defer func() {
		if s == nil || s.metrics == nil {
			return
		}
		outcome := "success"
		if retErr != nil {
			outcome = "error"
		}
		s.metrics.ObserveInventory("batch_list", outcome)
	}()
	if query == nil {
		return BatchListResponse{}, errs.Validation("invalid inventory batch query")
	}
	if err := query.NormalizeAndValidate(); err != nil {
		return BatchListResponse{}, errs.Validation("invalid inventory batch query")
	}
	orgID := appctx.OrgID(ctx)
	if orgID == uuid.Nil {
		return BatchListResponse{}, errs.Validation("organization context is required")
	}
	if s == nil || s.repo == nil {
		return BatchListResponse{}, errs.DatabaseError(fmt.Errorf("inventory repository is not configured"))
	}

	branchScope, orgWide := appctx.BranchScope(ctx)
	if orgWide {
		branchScope = nil
	}
	filter := batchListFilter{
		BranchID:    query.BranchID,
		BranchScope: branchScope,
		WarehouseID: query.WarehouseID,
		VariantID:   query.VariantID,
		Status:      query.Status,
		Condition:   query.Condition,
		Query:       query.Query,
	}

	load := func(ctx context.Context) error {
		var err error
		result, err = s.repo.ListBatches(ctx, orgID, filter, query)
		return err
	}
	var err error
	if s.metrics != nil {
		err = s.metrics.TraceDBQuery(ctx, "inventory.ListBatches", load)
	} else {
		err = load(ctx)
	}
	if err != nil {
		if err == ErrInvalidBatchQuery {
			return BatchListResponse{}, errs.Validation("invalid inventory batch query")
		}
		return BatchListResponse{}, errs.DatabaseError(err)
	}
	return result, nil
}
