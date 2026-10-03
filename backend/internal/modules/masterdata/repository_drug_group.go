package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// drugGroupColumns mirrors the declaration order of 000005_master_data.up.sql:165-176.
const drugGroupColumns = `id, code, name, description, is_active, created_at, updated_at, deleted_at`

// drugGroupTable describes drug_groups — the root of the drug taxonomy and the only
// one of the four taxonomy tables with no parent.
var drugGroupTable = tableSpec{
	table:        "drug_groups",
	columns:      drugGroupColumns,
	insertCols:   `code, name, description, is_active`,
	updateCols:   `name, description, is_active`,
	sortColumns:  drugGroupSortColumns,
	defaultOrder: defaultOrder,
}

// scanDrugGroup maps one row into a DrugGroup.
func scanDrugGroup(row rowScanner) (*DrugGroup, error) {
	var g DrugGroup
	if err := row.Scan(
		&g.ID, &g.Code, &g.Name, &g.Description,
		&g.IsActive, &g.CreatedAt, &g.UpdatedAt, &g.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &g, nil
}

// drugGroupValues binds g as [id, code, then every mutable column].
func drugGroupValues(g *DrugGroup) []any {
	return []any{g.ID, g.Code, g.Name, g.Description, g.IsActive}
}

// CreateDrugGroup inserts a drug group and returns the stored row. A duplicate code
// surfaces as a unique violation from ux_drug_groups_code.
func (r *Repository) CreateDrugGroup(ctx context.Context, g *DrugGroup) (*DrugGroup, error) {
	return createRow(ctx, r, drugGroupTable, g, drugGroupValues, scanDrugGroup)
}

// GetDrugGroup returns the live drug group with the given id, or db.ErrNoRows.
func (r *Repository) GetDrugGroup(ctx context.Context, id uuid.UUID) (*DrugGroup, error) {
	return getRow(ctx, r, drugGroupTable, id, scanDrugGroup, false)
}

// GetDrugGroupForUpdate is GetDrugGroup with a row lock.
func (r *Repository) GetDrugGroupForUpdate(ctx context.Context, id uuid.UUID) (*DrugGroup, error) {
	return getRow(ctx, r, drugGroupTable, id, scanDrugGroup, true)
}

// ListDrugGroups returns a filtered, paginated page plus the total count.
func (r *Repository) ListDrugGroups(ctx context.Context, f ListFilter, o pagination.Offset) ([]DrugGroup, int64, error) {
	return listRows(ctx, r, drugGroupTable, scanDrugGroup, f, o)
}

// UpdateDrugGroup writes the mutable columns of g and returns the refreshed row. code
// and id are never written.
func (r *Repository) UpdateDrugGroup(ctx context.Context, g *DrugGroup) (*DrugGroup, error) {
	return updateRow(ctx, r, drugGroupTable, g, drugGroupValues, scanDrugGroup)
}

// SoftDeleteDrugGroup marks the drug group deleted and reports whether a row was
// affected.
//
// Unlike the eight resources that medicines points at, drug_groups has NO
// Is...Referenced probe: no downstream table references it (contract C-11). The only
// rows that depend on it are its own drug_classes children, whose foreign key is ON
// DELETE SET NULL — and that key does not fire for this soft delete either. The
// service must therefore check for child drug classes itself before calling this;
// see the C-11 note in the contract.
func (r *Repository) SoftDeleteDrugGroup(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, drugGroupTable, id)
}

// HasDrugGroupChildren reports whether any live drug class still names this group.
//
// This is the first link of the taxonomy chain required by contract §20 note 2:
// drug_groups → drug_classes → therapeutic_classes → generic_medicines. Only the
// last link is reachable by a medicines probe, so each upstream link needs its own
// check or a soft delete orphans the subtree beneath it.
func (r *Repository) HasDrugGroupChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByChild(ctx, r, "drug_classes", "drug_group_id", id)
}
