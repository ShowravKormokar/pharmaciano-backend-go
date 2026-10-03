package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// dosageFormColumns mirrors the declaration order of 000005_master_data.up.sql:32-41.
const dosageFormColumns = `id, code, name, route_id, description, is_active, ` +
	`created_at, updated_at, deleted_at`

// dosageFormTable describes dosage_forms.
//
// route_id is deliberately NOT the spec's parentCol. ListMasterDataQuery documents
// that ?parent_id= applies only to the three self-referencing hierarchies, and
// dosage_forms is not one of them; the column is indexed by 000020 for the foreign
// key's benefit, not offered as a filter.
var dosageFormTable = tableSpec{
	table:        "dosage_forms",
	columns:      dosageFormColumns,
	insertCols:   `code, name, route_id, description, is_active`,
	updateCols:   `name, route_id, description, is_active`,
	sortColumns:  dosageFormSortColumns,
	defaultOrder: defaultOrder,
}

// scanDosageForm maps one row into a DosageForm.
func scanDosageForm(row rowScanner) (*DosageForm, error) {
	var f DosageForm
	if err := row.Scan(
		&f.ID, &f.Code, &f.Name, &f.RouteID, &f.Description,
		&f.IsActive, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &f, nil
}

// dosageFormValues binds f as [id, code, then every mutable column].
func dosageFormValues(f *DosageForm) []any {
	return []any{f.ID, f.Code, f.Name, f.RouteID, f.Description, f.IsActive}
}

// CreateDosageForm inserts a dosage form and returns the stored row. The service
// resolves f.RouteID to a live route before calling; a stale one surfaces here as a
// foreign-key violation.
func (r *Repository) CreateDosageForm(ctx context.Context, f *DosageForm) (*DosageForm, error) {
	return createRow(ctx, r, dosageFormTable, f, dosageFormValues, scanDosageForm)
}

// GetDosageForm returns the live dosage form with the given id, or db.ErrNoRows.
func (r *Repository) GetDosageForm(ctx context.Context, id uuid.UUID) (*DosageForm, error) {
	return getRow(ctx, r, dosageFormTable, id, scanDosageForm, false)
}

// GetDosageFormForUpdate is GetDosageForm with a row lock.
func (r *Repository) GetDosageFormForUpdate(ctx context.Context, id uuid.UUID) (*DosageForm, error) {
	return getRow(ctx, r, dosageFormTable, id, scanDosageForm, true)
}

// ListDosageForms returns a filtered, paginated page plus the total count.
func (r *Repository) ListDosageForms(ctx context.Context, f ListFilter, o pagination.Offset) ([]DosageForm, int64, error) {
	return listRows(ctx, r, dosageFormTable, scanDosageForm, f, o)
}

// UpdateDosageForm writes the mutable columns of f and returns the refreshed row.
// code and id are never written.
func (r *Repository) UpdateDosageForm(ctx context.Context, f *DosageForm) (*DosageForm, error) {
	return updateRow(ctx, r, dosageFormTable, f, dosageFormValues, scanDosageForm)
}

// SoftDeleteDosageForm marks the dosage form deleted and reports whether a row was
// affected.
func (r *Repository) SoftDeleteDosageForm(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, dosageFormTable, id)
}

// IsDosageFormReferenced reports whether any live medicine still points at this
// dosage form via medicines.dosage_form_id (000006:12).
func (r *Repository) IsDosageFormReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "dosage_form_id", id)
}
